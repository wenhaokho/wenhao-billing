package migrations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func testScopedReceiptSend(t *testing.T, pool *pgxpool.Pool, owners []scopedDocumentOwner, ids []string, secret []byte, token func(string, string) string) {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	payments := make([]string, len(owners))
	for i, o := range owners {
		err := pool.QueryRow(ctx, `INSERT INTO payments(payment_id,business_id,invoice_id,customer_id,amount,currency,payer_name,payment_date,intake_source,status)
			VALUES(gen_random_uuid(),$1,$2,$3,23.45,'SGD',$4,'2026-01-02','MANUAL','PENDING_MANUAL_REVIEW') RETURNING payment_id::text`,
			o.id, ids[i], o.customer, o.name+" Payer").Scan(&payments[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, o := range owners {
		scope, err := business.Parse(o.id)
		if err != nil {
			t.Fatal(err)
		}
		var messages []email.Message
		var providerErr error
		cancelRequest := func() {}
		sender := scopedQuotationSender(func(_ context.Context, msg email.Message) error {
			messages = append(messages, msg)
			cancelRequest()
			return providerErr
		})
		newRouter := func(pool *pgxpool.Pool, sender email.Sender) http.Handler {
			t.Helper()
			router, err := platform.NewScopedProfileRouter(pool, platform.SessionConfig{
				Secret: secret, Path: "/", SameSite: http.SameSiteLaxMode, Sender: sender,
			}, scope)
			if err != nil {
				t.Fatal(err)
			}
			return router
		}
		router := newRouter(pool, sender)
		request := func(ctx context.Context, router http.Handler, invoice, payment, session, body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/invoices/"+invoice+"/payments/"+payment+"/send-receipt", strings.NewReader(body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			if session != "" {
				req.AddCookie(&http.Cookie{Name: "session", Value: session})
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			return w
		}
		call := func(invoice, payment, session string) *httptest.ResponseRecorder {
			return request(ctx, router, invoice, payment, session, "{}")
		}
		expect := func(w *httptest.ResponseRecorder, want int) {
			t.Helper()
			if w.Code != want {
				t.Fatalf("receipt response %d, want %d: %s", w.Code, want, w.Body)
			}
		}
		session := token(o.user, o.id)
		other := owners[1-i]
		missing := "ffffffff-ffff-ffff-ffff-ffffffffffff"
		for _, tc := range []struct {
			invoice, payment, session string
			status                    int
		}{
			{ids[i], payments[i], "", 401},
			{ids[i], payments[i], token(other.user, other.id), 401},
			{ids[i], payments[i], token(other.user, o.id), 403},
			{ids[i], "malformed", session, 422},
			{ids[1-i], payments[1-i], session, 404},
			{ids[i], payments[1-i], session, 404},
			{ids[i], missing, session, 404},
		} {
			w := call(tc.invoice, tc.payment, tc.session)
			if w.Code != tc.status || len(messages) != 0 {
				t.Fatalf("response %d, want %d, with %d deliveries: %s", w.Code, tc.status, len(messages), w.Body)
			}
		}
		if call(ids[i], payments[1-i], session).Body.String() != call(ids[i], missing, session).Body.String() ||
			call(ids[1-i], payments[1-i], session).Body.String() != call(missing, payments[1-i], session).Body.String() {
			t.Fatal("foreign and absent references differ")
		}
		exec("UPDATE payments SET amount='NaN' WHERE business_id=$1 AND payment_id=$2", o.id, payments[i])
		expect(call(ids[i], payments[i], session), 500)
		exec("UPDATE payments SET amount=23.45,payment_date='infinity' WHERE business_id=$1 AND payment_id=$2", o.id, payments[i])
		expect(call(ids[i], payments[i], session), 500)
		exec("UPDATE payments SET payment_date='2026-01-02' WHERE business_id=$1 AND payment_id=$2", o.id, payments[i])
		exec("UPDATE invoices SET balance_due='NaN' WHERE business_id=$1 AND invoice_id=$2", o.id, ids[i])
		expect(call(ids[i], payments[i], session), 500)
		exec("UPDATE invoices SET balance_due=amount WHERE business_id=$1 AND invoice_id=$2", o.id, ids[i])
		expect(request(ctx, newRouter(pool, nil), ids[i], payments[i], session, "{}"), 500)
		if len(messages) != 0 {
			t.Fatal("invalid receipt reached the provider")
		}
		providerErr = errors.New("fake timeout after possible acceptance")
		expect(call(ids[i], payments[i], session), 502)
		if len(messages) != 1 {
			t.Fatalf("provider received %d calls after ambiguous failure", len(messages))
		}
		messages = nil
		providerErr = nil
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		cancelRequest = cancel
		w := request(reqCtx, router, ids[i], payments[i], session, "{}")
		if w.Code != 200 || len(messages) != 1 {
			t.Fatalf("receipt response %d with %d deliveries: %s", w.Code, len(messages), w.Body)
		}
		msg := messages[0]
		wantTo := strings.ToLower(o.name) + "@example.test"
		if len(msg.To) != 1 || msg.To[0] != wantTo || !strings.Contains(w.Body.String(), wantTo) || len(msg.Attachments) != 1 {
			t.Fatalf("wrong receipt recipient or attachment: %v / %s", msg.To, w.Body)
		}
		content := scopedPDFText(t, msg.Attachments[0].Content)
		for _, expected := range []string{o.name + " Studio", o.name + " Customer", "23.45 SGD", []string{"123.45 USD", "678.90 USD"}[i]} {
			if !strings.Contains(content, expected) || strings.Contains(content, other.name) {
				t.Fatalf("unexpected PDF text, expected %q: %q", expected, content)
			}
		}
		var status string
		if err := pool.QueryRow(ctx, "SELECT status FROM payments WHERE business_id=$1 AND payment_id=$2", o.id, payments[i]).Scan(&status); err != nil || status != "PENDING_MANUAL_REVIEW" {
			t.Fatalf("payment status after receipt is %q: %v", status, err)
		}
		if i != 0 {
			continue
		}
		cancelRequest = func() {}
		t.Run("CorruptReferences", func(t *testing.T) {
			for _, check := range []struct {
				table, constraint, key, column, rowID, correct, foreign string
				status                                                  int
			}{
				{"payments", "payments_invoice_id_fkey", "payment_id", "invoice_id", payments[i], ids[i], ids[1-i], 404},
				{"payments", "payments_customer_id_fkey", "payment_id", "customer_id", payments[i], o.customer, other.customer, 404},
				{"invoices", "invoices_customer_id_fkey", "invoice_id", "customer_id", ids[i], o.customer, other.customer, 500},
			} {
				func() {
					var def string
					err := pool.QueryRow(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2", check.table, check.constraint).Scan(&def)
					if err != nil {
						t.Fatal(err)
					}
					update := "UPDATE " + check.table + " SET " + check.column + "=$3 WHERE business_id=$1 AND " + check.key + "=$2"
					exec("ALTER TABLE " + check.table + " DROP CONSTRAINT " + check.constraint)
					defer func() {
						exec(update, o.id, check.rowID, check.correct)
						exec("ALTER TABLE " + check.table + " ADD CONSTRAINT " + check.constraint + " " + def)
					}()
					exec(update, o.id, check.rowID, check.foreign)
					foreign := request(ctx, router, ids[i], payments[i], session, `{"to_email":"override@example.test"}`)
					expect(foreign, check.status)
					exec(update, o.id, check.rowID, missing)
					absent := request(ctx, router, ids[i], payments[i], session, `{"to_email":"override@example.test"}`)
					expect(absent, check.status)
					if foreign.Body.String() != absent.Body.String() || len(messages) != 1 {
						t.Fatal("corrupt references exposed ownership or reached the provider")
					}
				}()
			}
		})
		for _, race := range []struct {
			name, update, rowID, lockQuery string
			value, restore                 any
			status                         int
		}{
			{"ConcurrentPaymentUnlink", "UPDATE payments SET invoice_id=$3 WHERE business_id=$1 AND payment_id=$2", payments[i], "%LockInvoiceReceiptPayment :one%", nil, ids[i], 404},
			{"ConcurrentAdminDemotion", "UPDATE users SET role=$3 WHERE business_id=$1 AND user_id=$2", o.user, "%LockSessionUserByID :one%", "user", "admin", 403},
		} {
			t.Run(race.name, func(t *testing.T) {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err := tx.Exec(ctx, race.update, o.id, race.rowID, race.value); err != nil {
					t.Fatal(err)
				}
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- call(ids[i], payments[i], session) }()
				wait, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				for {
					var blocked bool
					err := pool.QueryRow(wait, `SELECT EXISTS (SELECT FROM pg_stat_activity WHERE datname=current_database()
					AND wait_event_type='Lock' AND query LIKE $1)`, race.lockQuery).Scan(&blocked)
					if err != nil {
						t.Fatalf("receipt did not wait on the row lock: %v", err)
					}
					if blocked {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				defer exec(race.update, o.id, race.rowID, race.restore)
				select {
				case w := <-done:
					expect(w, race.status)
				case <-ctx.Done():
					t.Fatal("receipt did not finish after concurrent update")
				}
				if len(messages) != 1 {
					t.Fatal("rejected receipt reached the provider")
				}
			})
		}
		t.Run("SettingsSnapshot", func(t *testing.T) {
			gate := &invoiceLineGate{marker: "-- name: GetBusinessProfile", reached: make(chan struct{}), release: make(chan struct{})}
			config := pool.Config()
			config.ConnConfig.Tracer = gate
			sendPool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer sendPool.Close()
			defer func() {
				select {
				case <-gate.release:
				default:
					close(gate.release)
				}
			}()
			gatedRouter := newRouter(sendPool, sender)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- request(ctx, gatedRouter, ids[i], payments[i], session, "{}") }()
			select {
			case <-gate.reached:
			case w := <-done:
				t.Fatalf("receipt finished before profile query: %d %s", w.Code, w.Body)
			case <-ctx.Done():
				t.Fatal("receipt never reached profile query")
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "UPDATE customers SET contact_email='new@example.test' WHERE business_id=$1 AND customer_id=$2", o.id, o.customer); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "UPDATE business_profile SET name='New Studio' WHERE business_id=$1", o.id); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			defer exec("UPDATE business_profile SET name=$2 WHERE business_id=$1", o.id, o.name+" Studio")
			defer exec("UPDATE customers SET contact_email=$3 WHERE business_id=$1 AND customer_id=$2", o.id, o.customer, wantTo)
			close(gate.release)
			select {
			case w := <-done:
				expect(w, 200)
			case <-ctx.Done():
				t.Fatal("receipt did not finish after settings update")
			}
			expect(call(ids[i], payments[i], session), 200)
			if len(messages) != 3 {
				t.Fatalf("expected both snapshot deliveries, got %d messages total", len(messages))
			}
			for n, want := range []struct{ address, studio string }{{wantTo, o.name + " Studio"}, {"new@example.test", "New Studio"}} {
				msg := messages[n+1]
				content := scopedPDFText(t, msg.Attachments[0].Content)
				if len(msg.To) != 1 || msg.To[0] != want.address || !strings.Contains(content, want.studio) {
					t.Fatalf("mixed receipt settings: %v / %q", msg.To, content)
				}
			}
		})
	}
}
