package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestCustomerSummaryPostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	config := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	db := pgtestdb.Custom(t, config, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pydb := pgtestdb.Custom(t, config, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	pp, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	seed1 := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,due_date) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN',false,'2025-01-01','2025-02-01')`
	for _, p := range []*pgxpool.Pool{pool, pp} {
		_, e = p.Exec(context.Background(), seed1)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e != nil {
		t.Fatal(e)
	}
	seed2 := `INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000002','Other');
 INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,due_date,wht_amount,wht_booked,created_at) VALUES
 ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','RECURRING','IDR',20,15,'DRAFT',true,'2025-02-01','2025-03-01',3,1,'2025-01-01'),
 ('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','USAGE','USD',30,0,'PAID',false,'2025-03-01','2025-04-01',0,0,'2025-01-01'),
 ('20000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000002','MILESTONE','USD',999,999,'OPEN',false,'2025-04-01','2025-05-01',0,0,'2025-01-01');
 INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,due_date,created_at) VALUES
 ('20000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001','USAGE','USD',5,5,'SENT',false,'2025-03-05','2025-04-05','2025-01-01'),
 ('20000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000001','MILESTONE','IDR',6,6,'PARTIAL',false,'2025-03-06','2025-04-06','2025-01-01'),
 ('20000000-0000-0000-0000-000000000007','10000000-0000-0000-0000-000000000001','MILESTONE','USD',7,7,'VOID',false,'2025-03-07','2025-04-07','2025-01-01');
 INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001',0,'Test',2,5,10);
 INSERT INTO payments(payment_id,customer_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('40000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001',7,'USD','A',CURRENT_DATE-365,'MANUAL','CLEARED'),('40000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001',999,'USD','A',CURRENT_DATE-366,'MANUAL','CLEARED'),('40000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000002',999,'USD','B',CURRENT_DATE,'MANUAL','CLEARED'),('40000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001',8,'IDR','A',CURRENT_DATE-364,'MANUAL','CLEARED'),('40000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001',999,'IDR','A',CURRENT_DATE,'MANUAL','PENDING_MANUAL_REVIEW'); UPDATE invoices SET created_at='2025-01-01', billing_cycle_ref='{"nested":{"large":9007199254740993,"small":1.25,"items":[-9007199254740993,0.5]}}'::jsonb WHERE invoice_id='20000000-0000-0000-0000-000000000001'`
	for _, p := range []*pgxpool.Pool{pool, pp} {
		_, e = p.Exec(context.Background(), seed2)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	tok, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	snapshot := func() string {
		var v string
		err := pool.QueryRow(context.Background(), `SELECT jsonb_build_object('invoices',(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i),'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM invoice_line_items l),'payments',(SELECT jsonb_agg(to_jsonb(p) ORDER BY payment_id) FROM payments p),'customers',(SELECT jsonb_agg(to_jsonb(c) ORDER BY customer_id) FROM customers c))::text`).Scan(&v)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := snapshot()
	py := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.customers import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
    print(json.dumps([c.get('/api/v1/customers/10000000-0000-0000-0000-000000000001'+s).json() for s in ['/invoices','/billing-summary']]))`)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "DB="+pydb.URL())
	python, e := py.Output()
	if e != nil {
		t.Fatalf("Python: %v %s", e, python)
	}
	var expected []any
	dec := json.NewDecoder(bytes.NewReader(python))
	dec.UseNumber()
	if e = dec.Decode(&expected); e != nil {
		t.Fatal(e)
	}
	for index, suffix := range []string{"/invoices", "/billing-summary"} {
		r := httptest.NewRequest("GET", "/api/v1/customers/10000000-0000-0000-0000-000000000001"+suffix, nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
		dec.UseNumber()
		if e = dec.Decode(&got); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, expected[index]) {
			t.Fatalf("%s mismatch Go %s Python %s", suffix, w.Body.String(), python)
		}
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	summary := expected[1].(map[string]any)
	for _, bucket := range []string{"milestone", "recurring", "usage"} {
		if len(summary[bucket].([]any)) == 0 {
			t.Fatalf("empty %s bucket", bucket)
		}
	}
	if snapshot() != before {
		t.Fatal("reads changed database")
	}
	call := func(path string, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	base := "/api/v1/customers/10000000-0000-0000-0000-000000000001/billing-summary"
	if w := call(base, ""); w.Code != 401 {
		t.Fatalf("missing auth %d", w.Code)
	}
	userToken, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	if w := call(base, userToken); w.Code != 403 {
		t.Fatalf("non-admin %d", w.Code)
	}
	if w := call(base, tok+"bad"); w.Code != 401 {
		t.Fatalf("invalid auth %d", w.Code)
	}
	if w := call("/api/v1/customers/not-a-uuid/invoices", tok); w.Code != 422 {
		t.Fatalf("invalid uuid %d", w.Code)
	}
	if w := call("/api/v1/customers/10000000-0000-0000-0000-000000000099/invoices", tok); w.Code != 404 {
		t.Fatalf("missing customer %d", w.Code)
	}
	if w := call(base, tok); w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	specResp := call("/openapi.json", "")
	var spec map[string]any
	if e = json.Unmarshal(specResp.Body.Bytes(), &spec); e != nil {
		t.Fatal(e)
	}
	paths := spec["paths"].(map[string]any)
	for _, p := range []string{"/api/v1/customers/{customer_id}/invoices", "/api/v1/customers/{customer_id}/billing-summary"} {
		op := paths[p].(map[string]any)["get"].(map[string]any)
		if op["security"] == nil {
			t.Fatal("security schema missing")
		}
		responses := op["responses"].(map[string]any)
		for _, code := range []string{"200", "401", "403", "404", "422", "500"} {
			if responses[code] == nil {
				t.Fatalf("%s missing %s", p, code)
			}
		}
	}
	components := spec["components"].(map[string]any)["schemas"].(map[string]any)
	invoice := components["InvoiceOutputSchema"].(map[string]any)["properties"].(map[string]any)
	for _, k := range []string{"invoice_id", "amount", "balance_due", "expected_cash", "line_items", "issue_date"} {
		if invoice[k] == nil {
			t.Fatalf("missing invoice schema %s", k)
		}
	}
	if _, e = pool.Exec(context.Background(), `UPDATE invoices SET amount='NaN' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if w := call(base, tok); w.Code != 500 || strings.Contains(w.Body.String(), "NaN") {
		t.Fatalf("invalid numeric %d %s", w.Code, w.Body.String())
	}
	if _, e = pool.Exec(context.Background(), `DELETE FROM invoice_line_items;DELETE FROM payments;DELETE FROM invoices`); e != nil {
		t.Fatal(e)
	}
	if w := call(base, tok); w.Code != 200 || !strings.Contains(w.Body.String(), `"unpaid_invoices":[]`) {
		t.Fatalf("empty summary %d %s", w.Code, w.Body.String())
	}
	if w := call("/api/v1/customers/10000000-0000-0000-0000-000000000001/invoices", tok); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty list %d %s", w.Code, w.Body.String())
	}

}
