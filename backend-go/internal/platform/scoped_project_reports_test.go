package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
)

func testScopedProjectReports(t *testing.T, url string, pool *pgxpool.Pool, cfg SessionConfig, base string, expected []any, snapshot func() string) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const admin = "00000000-0000-0000-0000-000000000001"
	const project = "11000000-0000-0000-0000-000000000001"
	const customer = "10000000-0000-0000-0000-000000000001"
	const invoice = "20000000-0000-0000-0000-000000000001"
	const bill = "50000000-0000-0000-0000-000000000001"
	const foreign = "60000000-0000-0000-0000-000000000001"
	const foreignCustomer = "60000000-0000-0000-0000-000000000002"
	const foreignProject = "60000000-0000-0000-0000-000000000003"
	const foreignVendor = "60000000-0000-0000-0000-000000000004"
	const foreignQuote = "60000000-0000-0000-0000-000000000005"
	const foreignItem = "60000000-0000-0000-0000-000000000006"
	const foreignAccount = 90001
	exec(`UPDATE invoices SET amount=10 WHERE invoice_id=$1`, invoice)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var a string
	if err = pool.QueryRow(ctx, `SELECT business_id::text FROM businesses`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	scope, err := business.Parse(a)
	if err != nil {
		t.Fatal(err)
	}
	newRouter := func(pool *pgxpool.Pool) http.Handler {
		t.Helper()
		router, err := NewScopedProfileRouter(pool, cfg, scope)
		if err != nil {
			t.Fatal(err)
		}
		return router
	}
	token := func(user, company string) string {
		t.Helper()
		signed, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": user, "business_id": company})
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	signed := token(admin, a)
	request := func(handler http.Handler, path, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	exec(`INSERT INTO businesses(business_id) VALUES ($1)`, foreign)
	exec(`INSERT INTO customers(business_id,customer_id,name) VALUES ($1,$2,'Foreign customer')`, foreign, foreignCustomer)
	exec(`INSERT INTO vendors(business_id,vendor_id,name) VALUES ($1,$2,'Foreign vendor')`, foreign, foreignVendor)
	exec(`INSERT INTO projects(business_id,project_id,customer_id,code,name,currency) VALUES ($1,$2,$3,'P1','Foreign project','USD')`, foreign, foreignProject, foreignCustomer)
	router := newRouter(pool)
	read := func(suffix string, index int) string {
		t.Helper()
		w := request(router, base+suffix, signed)
		dec := json.NewDecoder(strings.NewReader(w.Body.String()))
		dec.UseNumber()
		var got any
		if w.Code != 200 || dec.Decode(&got) != nil || !reflect.DeepEqual(got, expected[index]) {
			t.Fatalf("staged %s differs from v7/Python: %d %s", suffix, w.Code, w.Body)
		}
		return w.Body.String()
	}
	before := snapshot()
	summary := read("summary", 0)
	invoices := read("invoices?status=PAID&limit=0", 1)
	if !strings.Contains(summary, `"invoiced_total":"100.0000"`) || !strings.Contains(summary, `"bill_total":"9.0000"`) {
		t.Fatalf("legacy mixed USD/IDR totals changed: %s", summary)
	}
	for _, suffix := range []string{"summary", "invoices"} {
		w := request(router, "/api/v1/projects/"+foreignProject+"/"+suffix, signed)
		missing := request(router, "/api/v1/projects/11000000-0000-0000-0000-000000000099/"+suffix, signed)
		if w.Code != 404 || missing.Code != 404 || w.Body.String() != missing.Body.String() {
			t.Fatalf("foreign/missing %s: %d %s / %d %s", suffix, w.Code, w.Body, missing.Code, missing.Body)
		}
		if invalid := request(router, "/api/v1/projects/bad/"+suffix, signed); invalid.Code != 422 || !strings.Contains(invalid.Body.String(), `"loc":["path","project_id"]`) {
			t.Fatalf("malformed project ID on %s: %d %s", suffix, invalid.Code, invalid.Body)
		}
		for cookie, code := range map[string]int{
			"": 401, token(admin, foreign): 401, token("00000000-0000-0000-0000-000000000002", a): 403,
		} {
			if denied := request(router, base+suffix, cookie); denied.Code != code {
				t.Fatalf("access %s: %d %s, expected %d", suffix, denied.Code, denied.Body, code)
			}
		}
	}
	empty := "/api/v1/projects/11000000-0000-0000-0000-000000000003/"
	if w := request(router, empty+"invoices", signed); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty invoices: %d %s", w.Code, w.Body)
	}
	if w := request(router, empty+"summary", signed); w.Code != 200 || !strings.Contains(w.Body.String(), `"invoiced_total":"0"`) || !strings.Contains(w.Body.String(), `"contract_remaining":null`) {
		t.Fatalf("empty summary: %d %s", w.Code, w.Body)
	}
	if before != snapshot() {
		t.Fatal("scoped reads changed project records")
	}

	// Exercise visibility even if an old import bypassed composite foreign keys.
	var restores []string
	dropFK := func(table, name string) {
		t.Helper()
		var definition string
		if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2`, table, name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		exec("ALTER TABLE " + table + " DROP CONSTRAINT " + name)
		restores = append(restores, "ALTER TABLE "+table+" ADD CONSTRAINT "+name+" "+definition)
	}
	dropFK("invoices", "invoices_project_id_fkey")
	dropFK("invoices", "invoices_customer_id_fkey")
	dropFK("invoice_line_items", "invoice_line_items_invoice_id_fkey")
	dropFK("bills", "bills_project_id_fkey")
	dropFK("bills", "bills_vendor_id_fkey")
	dropFK("projects", "projects_customer_id_fkey")
	dropFK("invoices", "invoices_source_quote_id_fkey")
	dropFK("invoice_line_items", "invoice_line_items_item_id_fkey")
	dropFK("bill_line_items", "bill_line_items_expense_account_id_fkey")
	dropFK("bill_line_items", "bill_line_items_item_id_fkey")
	exec(`INSERT INTO invoices(business_id,invoice_id,project_id,customer_id,invoice_type,currency,amount,balance_due,status)
VALUES ($1,gen_random_uuid(),$2,$3,'MILESTONE','USD',900,900,'OPEN')`, foreign, project, foreignCustomer)
	exec(`INSERT INTO invoice_line_items(business_id,line_item_id,invoice_id,position,description,quantity,unit_price,amount)
VALUES ($1,gen_random_uuid(),$2,0,'foreign line',1,900,900)`, foreign, invoice)
	exec(`INSERT INTO bills(business_id,bill_id,project_id,vendor_id,currency,amount,balance_due,status)
VALUES ($1,gen_random_uuid(),$2,$3,'USD',900,900,'OPEN')`, foreign, project, foreignVendor)
	before = snapshot()
	read("summary", 0)
	read("invoices", 1)
	if before != snapshot() {
		t.Fatal("report changed corrupt foreign records")
	}

	exec(`UPDATE projects SET customer_id=$2 WHERE project_id=$1`, project, foreignCustomer)
	for _, suffix := range []string{"summary", "invoices"} {
		if w := request(router, base+suffix, signed); w.Code != 500 || w.Body.String() != "{\"detail\":\"internal server error\"}\n" {
			t.Fatalf("damaged project customer on %s: %d %s", suffix, w.Code, w.Body)
		}
	}
	exec(`UPDATE projects SET customer_id=$2 WHERE project_id=$1`, project, customer)
	exec(`UPDATE invoices SET customer_id=$2 WHERE invoice_id=$1`, invoice, foreignCustomer)
	for _, suffix := range []string{"summary", "invoices"} {
		if w := request(router, base+suffix, signed); w.Code != 500 || w.Body.String() != "{\"detail\":\"internal server error\"}\n" {
			t.Fatalf("damaged invoice customer on %s: %d %s", suffix, w.Code, w.Body)
		}
	}
	exec(`UPDATE invoices SET customer_id=$2 WHERE invoice_id=$1`, invoice, customer)
	exec(`UPDATE bills SET vendor_id=$2 WHERE bill_id=$1`, bill, foreignVendor)
	if w := request(router, base+"summary", signed); w.Code != 500 || w.Body.String() != "{\"detail\":\"internal server error\"}\n" {
		t.Fatalf("damaged bill vendor: %d %s", w.Code, w.Body)
	}
	read("invoices", 1)
	exec(`UPDATE bills SET vendor_id='40000000-0000-0000-0000-000000000001' WHERE bill_id=$1`, bill)

	exec(`INSERT INTO quotations(business_id,quotation_id,customer_id,currency,amount) VALUES ($1,$2,$3,'USD',10)`, foreign, foreignQuote, foreignCustomer)
	exec(`INSERT INTO items(business_id,item_id,name,item_type) VALUES ($1,$2,'Foreign item','SERVICE')`, foreign, foreignItem)
	exec(`INSERT INTO chart_of_accounts(business_id,account_id,code,name,type) VALUES ($1,$2,'FEXP','Foreign expense','EXPENSE')`, foreign, foreignAccount)
	exec(`INSERT INTO bill_line_items(business_id,line_item_id,bill_id,position,description,quantity,unit_price,amount)
VALUES ($1,gen_random_uuid(),$2,0,'Bill line',1,2,2)`, a, bill)
	for _, tc := range []struct {
		ref, damage, restore string
		id, refID            any
		bothReports          bool
	}{
		{"source quotation", `UPDATE invoices SET source_quote_id=$2 WHERE invoice_id=$1`, `UPDATE invoices SET source_quote_id=NULL WHERE invoice_id=$1`, invoice, foreignQuote, true},
		{"invoice line item", `UPDATE invoice_line_items SET item_id=$2 WHERE invoice_id=$1 AND position=0 AND description='Line'`, `UPDATE invoice_line_items SET item_id=NULL WHERE invoice_id=$1 AND description='Line'`, invoice, foreignItem, true},
		{"bill line item", `UPDATE bill_line_items SET item_id=$2 WHERE bill_id=$1`, `UPDATE bill_line_items SET item_id=NULL WHERE bill_id=$1`, bill, foreignItem, false},
		{"bill expense account", `UPDATE bill_line_items SET expense_account_id=$2 WHERE bill_id=$1`, `UPDATE bill_line_items SET expense_account_id=NULL WHERE bill_id=$1`, bill, foreignAccount, false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			exec(tc.damage, tc.id, tc.refID)
			defer exec(tc.restore, tc.id)
			suffixes := []string{"summary"}
			if tc.bothReports {
				suffixes = append(suffixes, "invoices")
			} else {
				read("invoices", 1)
			}
			for _, suffix := range suffixes {
				if w := request(router, base+suffix, signed); w.Code != 500 || w.Body.String() != "{\"detail\":\"internal server error\"}\n" {
					t.Fatalf("damaged %s on %s: %d %s", tc.ref, suffix, w.Code, w.Body)
				}
			}
		})
	}

	exec(`DELETE FROM invoice_line_items WHERE business_id=$1 AND invoice_id=$2`, foreign, invoice)
	exec(`DELETE FROM invoices WHERE business_id=$1 AND project_id=$2`, foreign, project)
	exec(`DELETE FROM bills WHERE business_id=$1 AND project_id=$2`, foreign, project)
	for _, restore := range restores {
		exec(restore)
	}

	for _, tc := range []struct {
		suffix, marker, original string
	}{
		{"invoices", "-- name: ProjectInvoiceLines", invoices},
		{"summary", "-- name: ProjectBillStats", summary},
	} {
		t.Run(tc.suffix+" snapshot", func(t *testing.T) {
			gate := &projectReportGate{marker: tc.marker, reached: make(chan struct{}), release: make(chan struct{})}
			config, err := pgxpool.ParseConfig(url)
			if err != nil {
				t.Fatal(err)
			}
			config.ConnConfig.Tracer = gate
			traced, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer traced.Close()
			var release sync.Once
			unblock := func() { release.Do(func() { close(gate.release) }) }
			defer unblock()
			tracedRouter := newRouter(traced)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- request(tracedRouter, base+tc.suffix, signed) }()
			select {
			case <-gate.reached:
			case <-time.After(5 * time.Second):
				t.Fatal("report did not reach the query gate")
			}
			exec(`BEGIN;
UPDATE projects SET contract_value=999 WHERE code='P1' AND name='Project';
UPDATE invoices SET amount=999 WHERE invoice_id='20000000-0000-0000-0000-000000000001';
UPDATE invoice_line_items SET description='new line' WHERE line_item_id='30000000-0000-0000-0000-000000000001';
UPDATE bills SET amount=999 WHERE bill_id='50000000-0000-0000-0000-000000000001';
COMMIT;`)
			unblock()
			select {
			case w := <-done:
				if w.Code != 200 || w.Body.String() != tc.original {
					t.Fatalf("report mixed snapshots: %d %s", w.Code, w.Body)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("report did not finish")
			}
			exec(`UPDATE projects SET contract_value=100 WHERE project_id='11000000-0000-0000-0000-000000000001';
UPDATE invoices SET amount=10 WHERE invoice_id='20000000-0000-0000-0000-000000000001';
UPDATE invoice_line_items SET description='Line' WHERE line_item_id='30000000-0000-0000-0000-000000000001';
UPDATE bills SET amount=2 WHERE bill_id='50000000-0000-0000-0000-000000000001';`)
		})
	}
}

type projectReportGate struct {
	marker  string
	reached chan struct{}
	release chan struct{}
}

func (g *projectReportGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, g.marker) {
		close(g.reached)
		<-g.release
	}
	return ctx
}

func (*projectReportGate) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
