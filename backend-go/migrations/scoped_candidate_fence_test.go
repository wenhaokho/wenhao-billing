package migrations

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func testScopedCandidateFence(t *testing.T, pool *pgxpool.Pool, cfg platform.SessionConfig, a, b, aUser, bUser string, token func(string, string) string) {
	t.Helper()
	scopeA, _ := business.Parse(a)
	scopeB, _ := business.Parse(b)
	routerA, err := platform.NewScopedProfileRouter(pool, cfg, scopeA)
	if err != nil {
		t.Fatal(err)
	}
	routerB, err := platform.NewScopedProfileRouter(pool, cfg, scopeB)
	if err != nil {
		t.Fatal(err)
	}
	const missing = "99999999-1111-4444-8888-999999999999"
	const inv = "/api/v1/invoices/"
	const customer = "/api/v1/customers"
	const lines = `"line_items":[{"description":"Service","quantity":1,"unit_price":1}]`
	const templateBody = `{"currency":"IDR","payment_terms":"14","schedule":{"frequency":"DAILY","start_date":"2030-01-01","end_mode":"NEVER"},` + lines + `}`
	now := time.Date(2030, 1, 31, 0, 0, 0, 0, time.UTC)
	for _, scope := range []string{a, b} {
		if _, err := pool.Exec(context.Background(), `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref)
			VALUES ($1,gen_random_uuid(),'RECURRING','IDR',1,1,'DRAFT',true,'{"frequency":"DAILY","start_date":"2031-01-01","end_mode":"NEVER"}'),
			($1,gen_random_uuid(),'USAGE','IDR',1,1,'DRAFT',false,'{"cut_off_day":31}')`, scope); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ name, method, path, body string }{
		{"invoice create", "POST", inv[:len(inv)-1], `{"currency":"IDR",` + lines + `}`},
		{"template create", "POST", inv + "recurring-template", templateBody},
		{"milestone create", "POST", inv + "milestone", `{"customer_id":"` + missing + `","currency":"IDR","amount":1,"milestone_ref":"Service"}`},
		{"invoice update", "PATCH", inv + missing, `{"notes":"fence"}`},
		{"template update", "PUT", inv + "recurring-templates/" + missing, templateBody},
		{"invoice delete", "DELETE", inv + missing, ""},
		{"invoice finalize", "POST", inv + missing + "/finalize", "{}"},
		{"invoice mark paid", "POST", inv + missing + "/mark-paid", "{}"},
		{"invoice void", "POST", inv + missing + "/void", `{"reason":"fence"}`},
		{"recurring action", "PATCH", inv + "recurring-templates/" + missing, `{"action":"pause"}`},
		{"recurring trigger", "POST", inv + "recurring/" + missing + "/trigger", `{"cycle_key":"2030-01-01"}`},
		{"usage lock", "POST", inv + "usage/" + missing + "/lock", `{"accrued_amount":10,"cutoff_date":"2026-10-31"}`},
		{"invoice payment", "POST", inv + missing + "/record-payment", `{"amount":1,"payment_date":"2026-10-31"}`},
		{"manual approval", "POST", "/api/v1/payments/" + missing + "/manual-review", `{"invoice_id":"` + missing + `"}`},
		{"manual reversal", "POST", "/api/v1/payments/" + missing + "/reverse", `{"reason":"fence"}`},
		{"invoice send", "POST", inv + missing + "/send", "{}"},
		{"quotation conversion", "POST", "/api/v1/quotations/" + missing + "/convert-to-invoice", "{}"},
		{"customer create", "POST", customer, `{"name":"Fence test customer"}`},
		{"customer update", "PATCH", customer + "/" + missing, `{"matching_aliases":["new payer"]}`},
		{"customer deactivate", "DELETE", customer + "/" + missing, ""},
		{"recurring worker", "", "", ""},
		{"usage worker", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			hold, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback(context.Background())
			var blocker int32
			if err = hold.QueryRow(ctx, `SELECT pg_backend_pid() FROM businesses WHERE business_id=$1 FOR NO KEY UPDATE`, a).Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			request := func(h http.Handler, user, scope string) *httptest.ResponseRecorder {
				switch tc.name {
				case "recurring worker", "usage worker":
					id, _ := business.Parse(scope)
					var err error
					if tc.name == "recurring worker" {
						_, err = platform.ScanScopedRecurring(ctx, pool, id, now)
					} else {
						meter := platform.ScopedUsageMeterFunc(func(context.Context, business.ID, pgtype.UUID) (decimal.Decimal, error) {
							return decimal.NewFromInt(1), nil
						})
						_, err = platform.ScanScopedUsageCutoff(ctx, pool, id, now, meter)
					}
					w := httptest.NewRecorder()
					if err != nil {
						w.WriteHeader(500)
						w.Body.WriteString(err.Error())
					}
					return w
				}
				r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body)).WithContext(ctx)
				r.Header.Set("Content-Type", "application/json")
				r.AddCookie(&http.Cookie{Name: "session", Value: token(user, scope)})
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			waiter := make(chan *httptest.ResponseRecorder, 1)
			go func() { waiter <- request(routerA, aUser, a) }()
			for {
				select {
				case w := <-waiter:
					t.Fatalf("writer bypassed business fence: %d %s", w.Code, w.Body)
				default:
				}
				var waiting bool
				err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
					WHERE $1=ANY(pg_blocking_pids(pid)) AND wait_event_type='Lock'
					AND query LIKE '%FROM businesses%FOR NO KEY UPDATE%')`, blocker).Scan(&waiting)
				if err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			// A waiter must leave its downstream actor row available to another transaction.
			if _, err = hold.Exec(ctx, `SELECT user_id FROM users WHERE business_id=$1 AND user_id=$2 FOR UPDATE NOWAIT`, a, aUser); err != nil {
				t.Fatalf("writer took actor lock before business fence: %v", err)
			}
			for _, query := range []string{
				`SELECT invoice_id FROM invoices WHERE business_id=$1 FOR UPDATE NOWAIT`,
				`SELECT customer_id FROM customers WHERE business_id=$1 FOR UPDATE NOWAIT`,
				`SELECT quotation_id FROM quotations WHERE business_id=$1 FOR UPDATE NOWAIT`,
				`SELECT payment_id FROM payments WHERE business_id=$1 FOR UPDATE NOWAIT`,
			} {
				if _, err = hold.Exec(ctx, query, a); err != nil {
					t.Fatalf("writer took downstream lock before business fence (%s): %v", query, err)
				}
			}
			if w := request(routerB, bUser, b); w.Code >= 500 || w.Code == 401 || w.Code == 403 || ctx.Err() != nil {
				t.Fatalf("other business did not proceed: %d %s", w.Code, w.Body)
			}
			if tc.name == "customer update" {
				if err := hold.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				if w := <-waiter; w.Code != 404 {
					t.Fatalf("writer did not resume after fence release: %d %s", w.Code, w.Body)
				}
				return
			}
			cancel()
			if w := <-waiter; w.Code != 500 {
				t.Fatalf("canceled waiter: %d %s", w.Code, w.Body)
			}
		})
	}
	t.Run("unfenced writer changes candidate membership", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var payer, selected string
		if err := pool.QueryRow(ctx, `INSERT INTO customers(business_id,customer_id,name)
			VALUES ($1,gen_random_uuid(),'Fence control payer') RETURNING customer_id::text`, a).Scan(&payer); err != nil {
			t.Fatal(err)
		}
		const insert = `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,issue_date)
			VALUES ($1,gen_random_uuid(),$2,'MILESTONE','IDR',1,1,'OPEN','2026-10-31')`
		if err := pool.QueryRow(ctx, insert+` RETURNING invoice_id::text`, a, payer).Scan(&selected); err != nil {
			t.Fatal(err)
		}
		hold, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer hold.Rollback(context.Background())
		if _, err = hold.Exec(ctx, `SELECT business_id FROM businesses WHERE business_id=$1 FOR NO KEY UPDATE`, a); err != nil {
			t.Fatal(err)
		}
		// Simulate a writer omitting the fence. The FK can still take KEY SHARE
		// while the matcher holds the fence and its currently selected invoice.
		if _, err = hold.Exec(ctx, `SELECT invoice_id FROM invoices WHERE business_id=$1 AND invoice_id=$2 FOR UPDATE`, a, selected); err != nil {
			t.Fatal(err)
		}
		count := func() int {
			t.Helper()
			var n int
			if err := hold.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE business_id=$1 AND customer_id=$2
				AND status='OPEN' AND currency='IDR' AND balance_due=1 AND issue_date='2026-10-31'`, a, payer).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if n := count(); n != 1 {
			t.Fatalf("expected a single candidate before competing write: %d", n)
		}
		if _, err = pool.Exec(ctx, insert, a, payer); err != nil {
			t.Fatalf("unfenced candidate insert blocked by existing invoice or business FK: %v", err)
		}
		if n := count(); n != 2 {
			t.Fatalf("unfenced candidate did not enter the set: %d", n)
		}
	})
}
