package platform

import (
	"context"
	"encoding/json"
	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBillUpdateDifferential(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	db := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pydb := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	pp, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO vendors(vendor_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Vendor'); INSERT INTO customers(customer_id,name) VALUES ('20000000-0000-0000-0000-000000000001','Customer'); INSERT INTO projects(project_id,customer_id,code,name,currency,status) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','P','Project','IDR','ACTIVE'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000001','Item','SERVICE'); INSERT INTO chart_of_accounts(account_id,code,name,type) VALUES (98765,'999999','Expense','EXPENSE'); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','USD',3,3,'DRAFT'); INSERT INTO bill_line_items(line_item_id,bill_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'unrelated',1,3,3); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','USD',8,8,'DRAFT'); INSERT INTO bill_line_items(line_item_id,bill_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002',0,'untouched',1,8,8); INSERT INTO payments(payment_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('70000000-0000-0000-0000-000000000001',3,'USD','other','2026-01-01','manual','PENDING'); INSERT INTO journal_entries(entry_id,source_type,source_id) VALUES ('80000000-0000-0000-0000-000000000001','OTHER','70000000-0000-0000-0000-000000000001'); INSERT INTO journal_lines(line_id,entry_id,account_id,debit,currency) VALUES ('90000000-0000-0000-0000-000000000001','80000000-0000-0000-0000-000000000001',98765,3,'USD')`
	for _, p := range []*pgxpool.Pool{p, pp} {
		if _, e = p.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	snapshot := func() string {
		var v string
		err := p.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT to_jsonb(b) FROM bills b WHERE bill_id='50000000-0000-0000-0000-000000000001'),(SELECT jsonb_agg(to_jsonb(l) ORDER BY l.line_item_id) FROM bill_line_items l WHERE bill_id='50000000-0000-0000-0000-000000000001'),(SELECT jsonb_agg(to_jsonb(p)) FROM payments p),(SELECT jsonb_agg(to_jsonb(j)) FROM journal_entries j),(SELECT jsonb_agg(to_jsonb(l)) FROM journal_lines l))::text`).Scan(&v)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	unrelated := func() string {
		var v string
		if e := p.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT to_jsonb(b) FROM bills b WHERE bill_id='50000000-0000-0000-0000-000000000002'),(SELECT to_jsonb(l) FROM bill_line_items l WHERE line_item_id='60000000-0000-0000-0000-000000000002'),(SELECT jsonb_agg(to_jsonb(p)) FROM payments p),(SELECT jsonb_agg(to_jsonb(j)) FROM journal_entries j),(SELECT jsonb_agg(to_jsonb(l)) FROM journal_lines l))::text`).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	originalUnrelated := unrelated()
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	bodies := []string{
		`{}`, `{"notes":"hello","bill_number":"B","po_number":"P","issue_date":"2026-01-01","due_date":"2026-02-01","payment_terms":"net 30","project_id":"30000000-0000-0000-0000-000000000001"}`,
		`{"currency":"sgd","discount_type":"PERCENT","discount_value":"3.5","line_items":[{"description":"x","quantity":"0.0001","unit_price":"0.5"},{"description":"y","quantity":"0.0001","unit_price":"1.5"}]}`,
		`{"notes":null,"bill_number":null,"po_number":null,"issue_date":null,"due_date":null,"payment_terms":null,"project_id":null,"discount_type":null,"discount_value":null,"line_items":null}`,
		`{"currency":null}`, `{"vendor_id":null}`, `{"status":null}`, `{"currency":"US"}`, `{"status":"BAD"}`, `{"line_items":[{"description":"x","quantity":-1,"unit_price":1}]}`, `{`, `null`, `[]`,
		`{"line_items":[{"description":"a","quantity":1,"unit_price":2,"position":true},{"description":"b","quantity":"0.0001","unit_price":"1.5","position":"2","item_id":"40000000-0000-0000-0000-000000000001","expense_account_id":98765}]}`,
		`{"discount_type":"AMOUNT","discount_value":"10","line_items":[{"description":"a","quantity":1,"unit_price":2}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000099"}`, `{"line_items":[{"description":"a","quantity":1,"unit_price":2,"item_id":"40000000-0000-0000-0000-000000000099"}]}`,
		`{"currency":"IDR","unknown":"ignored"}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":" 1 ","expense_account_id":" 98765 "}]}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"\t+2\n","expense_account_id":" 98765 "}]}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":" 1/1 ","expense_account_id":" 98765 "}]}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":" 1e0 ","expense_account_id":" 98765 "}]}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":" 1.5 ","expense_account_id":" 98765 "}]}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"1/1"}]}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"1e0"}]}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":2147483648}]}`,
		`{"status":"OPEN","line_items":[]}`, `{"status":"PAID"}`, `{}`,
	}

	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.bills import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
def counts():
 with engine.connect() as conn: return list(conn.execute(text("SELECT (SELECT count(*) FROM bills), (SELECT count(*) FROM bill_line_items)")).one())
results=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for b in json.loads(os.environ['BODIES']):
  before=counts(); r=c.patch('/api/v1/bills/50000000-0000-0000-0000-000000000001',content=b,headers={'content-type':'application/json'}); after=counts()

  try: payload=r.json()
  except ValueError: payload=r.text
  results.append([r.status_code,payload,before,after])
print(json.dumps(results))`)
	cmd.Dir = "../../../backend"
	b, _ := json.Marshal(bodies)
	cmd.Env = append(os.Environ(), "DB="+pydb.URL(), "BODIES="+string(b))
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, out)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatalf("python output: %v %s", e, out)
	}
	for i, body := range bodies {
		beforeState := snapshot()
		var beforeInvoices, beforeLines int
		if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bills),(SELECT count(*) FROM bill_line_items)`).Scan(&beforeInvoices, &beforeLines); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("PATCH", "/api/v1/bills/50000000-0000-0000-0000-000000000001", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code >= 400 {
			var afterInvoices, afterLines int
			if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bills),(SELECT count(*) FROM bill_line_items)`).Scan(&afterInvoices, &afterLines); e != nil {
				t.Fatal(e)
			}
			if beforeState != snapshot() || beforeInvoices != afterInvoices || beforeLines != afterLines || !reflect.DeepEqual(expected[i][2], expected[i][3]) {
				t.Errorf("case %d invalid input wrote data", i)
			}
		}
		var actual any
		if e = json.Unmarshal(w.Body.Bytes(), &actual); e != nil {
			actual = w.Body.String()
		}
		for _, v := range []any{actual, expected[i][1]} {
			if m, ok := v.(map[string]any); ok {
				if details, ok := m["detail"].([]any); ok {
					for _, detail := range details {
						if d, ok := detail.(map[string]any); ok {
							d["input"] = "[REDACTED]"
						}
					}
				}
			}
			if m, ok := v.(map[string]any); ok {
				delete(m, "bill_id")
				delete(m, "created_at")
				if lines, ok := m["line_items"].([]any); ok {
					for _, l := range lines {
						delete(l.(map[string]any), "line_item_id")
					}
				}
			}
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(actual, expected[i][1]) {
			t.Errorf("case %d: go=%d %#v python=%#v", i, w.Code, actual, expected[i])
		}
	}

	// The Python payment endpoint changes balance and status; reset both databases
	// to its resulting OPEN balance before comparing an edit with replacement lines.
	for _, pool := range []*pgxpool.Pool{p, pp} {
		if _, e = pool.Exec(context.Background(), `UPDATE bills SET status='OPEN',amount=10,balance_due=4 WHERE bill_id='50000000-0000-0000-0000-000000000001'`); e != nil {
			t.Fatal(e)
		}
	}
	paymentCmd := exec.Command("python", "-c", `import os,json
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.bills import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1));app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app,raise_server_exceptions=False) as c:
 r=c.patch('/api/v1/bills/50000000-0000-0000-0000-000000000001',json={'line_items':[{'description':'changed','quantity':1,'unit_price':8}]});print(json.dumps([r.status_code,r.json()]))`)
	paymentCmd.Dir = "../../../backend"
	paymentCmd.Env = append(os.Environ(), "DB="+pydb.URL())
	pyout, e := paymentCmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var expectedPayment []any
	if e = json.Unmarshal(pyout, &expectedPayment); e != nil {
		t.Fatal(e)
	}
	reqPayment := httptest.NewRequest("PATCH", "/api/v1/bills/50000000-0000-0000-0000-000000000001", strings.NewReader(`{"line_items":[{"description":"changed","quantity":1,"unit_price":8}]}`))
	reqPayment.Header.Set("Content-Type", "application/json")
	reqPayment.AddCookie(&http.Cookie{Name: "session", Value: token})
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, reqPayment)
	var actualPayment map[string]any
	if e = json.Unmarshal(rw.Body.Bytes(), &actualPayment); e != nil {
		t.Fatal(e)
	}
	ep := expectedPayment[1].(map[string]any)
	if rw.Code != int(expectedPayment[0].(float64)) || actualPayment["balance_due"] != ep["balance_due"] || actualPayment["amount"] != ep["amount"] || actualPayment["status"] != ep["status"] {
		t.Errorf("payment balance differential: %s vs %s", rw.Body, pyout)
	}
	// Exercise rollback and concurrent disjoint writes on the actual Go endpoint.
	if _, e = p.Exec(context.Background(), `UPDATE bills SET status='DRAFT' WHERE bill_id='50000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	call := func(body string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/api/v1/bills/50000000-0000-0000-0000-000000000001", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := call(`{}`, false); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	if w := call(`{}`, true); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("cache: %d %v", w.Code, w.Header())
	}

	if w := call(`{"currency":"private-secret"}`, true); w.Code != 422 || strings.Contains(w.Body.String(), "private-secret") {
		t.Errorf("validation redaction: %d %s", w.Code, w.Body)
	}
	for _, media := range []string{"application/json; charset=utf-8", "application/problem+json", "text/plain", ""} {
		req := httptest.NewRequest("PATCH", "/api/v1/bills/50000000-0000-0000-0000-000000000001", strings.NewReader(`{}`))
		if media != "" {
			req.Header.Set("Content-Type", media)
		}
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		want := 200
		if media == "text/plain" {
			want = 422
		}
		if w.Code != want {
			t.Errorf("media %q: %d", media, w.Code)
		}
	}
	req := httptest.NewRequest("PATCH", "/api/v1/bills/50000000-0000-0000-0000-000000000001", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	over := httptest.NewRecorder()
	h.ServeHTTP(over, req)
	if over.Code != 413 || over.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("oversize: %d", over.Code)
	}

	for _, status := range []string{"PARTIAL", "PAID", "VOID"} {
		if _, e = p.Exec(context.Background(), `UPDATE bills SET status=$1 WHERE bill_id='50000000-0000-0000-0000-000000000001'`, status); e != nil {
			t.Fatal(e)
		}
		before := snapshot()
		w := call(`{"notes":"must not change"}`, true)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "bill is "+status+" and cannot be edited") || snapshot() != before {
			t.Errorf("status %s: %d %s", status, w.Code, w.Body)
		}
	}
	if _, e = p.Exec(context.Background(), `UPDATE bills SET status='DRAFT' WHERE bill_id='50000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(context.Background(), `CREATE FUNCTION fail_update_line() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced'; END $$; CREATE TRIGGER fail_update_line BEFORE INSERT ON bill_line_items FOR EACH ROW EXECUTE FUNCTION fail_update_line()`); e != nil {
		t.Fatal(e)
	}
	before := snapshot()
	if w := call(`{"notes":"should rollback","line_items":[{"description":"x","quantity":1,"unit_price":2}]}`, true); w.Code != 500 {
		t.Errorf("rollback response: %d", w.Code)
	}
	if snapshot() != before {
		t.Error("line failure changed persisted state")
	}
	if _, e = p.Exec(context.Background(), `DROP TRIGGER fail_update_line ON bill_line_items; DROP FUNCTION fail_update_line()`); e != nil {
		t.Fatal(e)
	}

	if _, e = p.Exec(context.Background(), `CREATE FUNCTION corrupt_update_time() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.created_at='infinity'; RETURN NEW; END $$; CREATE TRIGGER corrupt_update_time BEFORE UPDATE ON bills FOR EACH ROW EXECUTE FUNCTION corrupt_update_time()`); e != nil {
		t.Fatal(e)
	}
	before = snapshot()
	if w := call(`{"notes":"bad output"}`, true); w.Code != 500 {
		t.Errorf("output validation: %d", w.Code)
	}
	if snapshot() != before {
		t.Error("output failure committed")
	}
	if _, e = p.Exec(context.Background(), `DROP TRIGGER corrupt_update_time ON bills; DROP FUNCTION corrupt_update_time()`); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, body := range []string{`{"notes":"concurrent notes"}`, `{"payment_terms":"concurrent terms"}`} {
		wg.Add(1)
		go func(body string) { defer wg.Done(); results <- call(body, true).Code }(body)
	}
	wg.Wait()
	close(results)
	for code := range results {
		if code != 200 {
			t.Errorf("concurrent: %d", code)
		}
	}
	var notes, terms string
	if e = p.QueryRow(context.Background(), `SELECT notes,payment_terms FROM bills WHERE bill_id='50000000-0000-0000-0000-000000000001'`).Scan(&notes, &terms); e != nil || notes != "concurrent notes" || terms != "concurrent terms" {
		t.Errorf("lost update: %q %q %v", notes, terms, e)
	}

	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT 1 FROM users WHERE user_id='00000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() { result <- call(`{"notes":"demoted"}`, true).Code }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM users%' AND pid <> pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("did not wait on admin")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='staff' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 403 {
		t.Errorf("demotion race: %d", code)
	}
	var after string
	if err = p.QueryRow(context.Background(), `SELECT notes FROM bills WHERE bill_id='50000000-0000-0000-0000-000000000001'`).Scan(&after); err != nil || after != "concurrent notes" {
		t.Errorf("demotion wrote: %q %v", after, err)
	}

	if _, e = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	lock, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(context.Background(), `SELECT 1 FROM bills WHERE bill_id='50000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	result = make(chan int, 1)
	go func() { result <- call(`{"notes":"stale"}`, true).Code }()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM bills WHERE bill_id%FOR UPDATE%' AND pid <> pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("did not wait on bill")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = lock.Exec(context.Background(), `UPDATE bills SET status='VOID' WHERE bill_id='50000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err = lock.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 400 {
		t.Errorf("status race: %d", code)
	}

	if unrelated() != originalUnrelated {
		t.Error("unrelated bill, line, payment or ledger changed")
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var document map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &document); e != nil {
		t.Fatal(e)
	}
	op := document["paths"].(map[string]any)["/api/v1/bills/{bill_id}"].(map[string]any)["patch"].(map[string]any)
	if op["security"] == nil || op["requestBody"] == nil {
		t.Error("OpenAPI missing security or request")
	}
	responses := op["responses"].(map[string]any)
	for _, code := range []string{"200", "400", "401", "403", "404", "413", "422", "500"} {
		if responses[code] == nil {
			t.Errorf("OpenAPI missing %s", code)
		}
	}

}
