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

func TestBillCreateDifferential(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO vendors(vendor_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Vendor'); INSERT INTO customers(customer_id,name) VALUES ('20000000-0000-0000-0000-000000000001','Customer'); INSERT INTO projects(project_id,customer_id,code,name,currency,status) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','P','Project','IDR','ACTIVE'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000001','Item','SERVICE'); INSERT INTO chart_of_accounts(account_id,code,name,type) VALUES (98765,'999999','Expense','EXPENSE'); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','USD',3,3,'DRAFT'); INSERT INTO bill_line_items(line_item_id,bill_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'unrelated',1,3,3); INSERT INTO payments(payment_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('70000000-0000-0000-0000-000000000001',3,'USD','other','2026-01-01','manual','PENDING'); INSERT INTO journal_entries(entry_id,source_type,source_id) VALUES ('80000000-0000-0000-0000-000000000001','OTHER','70000000-0000-0000-0000-000000000001'); INSERT INTO journal_lines(line_id,entry_id,account_id,debit,currency) VALUES ('90000000-0000-0000-0000-000000000001','80000000-0000-0000-0000-000000000001',98765,3,'USD')`
	for _, p := range []*pgxpool.Pool{p, pp} {
		if _, e = p.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	snapshot := func() string {
		var v string
		err := p.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT to_jsonb(b) FROM bills b WHERE bill_id='50000000-0000-0000-0000-000000000001'),(SELECT to_jsonb(l) FROM bill_line_items l WHERE line_item_id='60000000-0000-0000-0000-000000000001'),(SELECT jsonb_agg(to_jsonb(p)) FROM payments p),(SELECT jsonb_agg(to_jsonb(j)) FROM journal_entries j),(SELECT jsonb_agg(to_jsonb(l)) FROM journal_lines l))::text`).Scan(&v)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	originalSnapshot := snapshot()
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
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"usd","bill_number":"B1","discount_type":"PERCENT","discount_value":"3.5","line_items":[{"description":"A","quantity":"1.2345","unit_price":"7.6543"},{"description":"B","quantity":"0.0001","unit_price":"0.5"}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"SGD","line_items":[]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"IDR","project_id":null,"po_number":"P","issue_date":"2026-01-01","due_date":"2026-02-02","payment_terms":"Net 30","notes":"Hello","discount_type":"AMOUNT","discount_value":"100","line_items":[{"description":"x","quantity":"1","unit_price":"2","position":0},{"description":"y","quantity":"0.0001","unit_price":"1.5","position":5}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","status":"PAID","wht_type":"PPH23","line_items":[]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"US","discount_type":"BAD","line_items":[{"description":"x","quantity":"-1","unit_price":"0.00001"}]}`,
		`{"vendor_id":null,"currency":"USD","line_items":null}`,
		`{"vendor_id":"bad","currency":"USD","line_items":[{"quantity":1}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":null}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","project_id":"30000000-0000-0000-0000-000000000001","line_items":[{"item_id":"40000000-0000-0000-0000-000000000001","expense_account_id":98765,"description":"Item","quantity":"1.5","unit_price":"2.5"}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"expense_account_id":1.5}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"bad"}]}`,
		``,
		`{`,
		`null`,
		`[]`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","currency":"SGD"}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":true,"unit_price":1}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","discount_value":"0.00001"}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000099","currency":"USD"}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"item_id":"40000000-0000-0000-0000-000000000099","description":"x","quantity":1,"unit_price":1}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","bill_number":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`,
		`{}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":" 1 ","expense_account_id":" 98765 "}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"\t+2\n","expense_account_id":" 98765 "}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":" 1/1 ","expense_account_id":" 98765 "}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":" 1e0 ","expense_account_id":" 98765 "}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":" 1.5 ","expense_account_id":" 98765 "}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"1/1"}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"1e0"}]}`,
		`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1,"position":2147483648}]}`,
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
  before=counts(); r=c.post('/api/v1/bills',content=b,headers={'content-type':'application/json'}); after=counts()

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
		var beforeInvoices, beforeLines int
		if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bills),(SELECT count(*) FROM bill_line_items)`).Scan(&beforeInvoices, &beforeLines); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("POST", "/api/v1/bills", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code >= 400 {
			var afterInvoices, afterLines int
			if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bills),(SELECT count(*) FROM bill_line_items)`).Scan(&afterInvoices, &afterLines); e != nil {
				t.Fatal(e)
			}
			if beforeInvoices != afterInvoices || beforeLines != afterLines || !reflect.DeepEqual(expected[i][2], expected[i][3]) {
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
	for _, media := range []string{"application/json; charset=utf-8", "application/problem+json", "text/plain", ""} {
		req := httptest.NewRequest("POST", "/api/v1/bills", strings.NewReader(`{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD"}`))
		if media != "" {
			req.Header.Set("Content-Type", media)
		}
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		want := 201
		if media == "text/plain" {
			want = 422
		}
		if w.Code != want {
			t.Errorf("media %q: got %d want %d: %s", media, w.Code, want, w.Body)
		}
	}
	oversized := httptest.NewRequest("POST", "/api/v1/bills", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	oversized.Header.Set("Content-Type", "application/json")
	oversized.AddCookie(&http.Cookie{Name: "session", Value: token})
	over := httptest.NewRecorder()
	h.ServeHTTP(over, oversized)
	if over.Code != 413 {
		t.Errorf("oversize: %d", over.Code)
	}
	if got := snapshot(); got != originalSnapshot {
		t.Error("unrelated bill, line, payment or ledger changed")
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var document map[string]any
	if err := json.Unmarshal(spec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	op := document["paths"].(map[string]any)["/api/v1/bills"].(map[string]any)["post"].(map[string]any)
	if op["security"] == nil || op["requestBody"] == nil {
		t.Error("missing OpenAPI security or request")
	}
	responses := op["responses"].(map[string]any)
	for _, code := range []string{"201", "401", "403", "413", "422", "500"} {
		if responses[code] == nil {
			t.Errorf("missing OpenAPI response %s", code)
		}
	}

	body := `{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":2}]}`
	request := func(cookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/bills", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := request(false); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	if w := request(true); w.Code != 201 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Errorf("cache or creation: %d %v", w.Code, w.Header())
	}
	count := func() int {
		var n int
		if e := p.QueryRow(context.Background(), `SELECT count(*) FROM bills`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := count()
	if _, e = p.Exec(context.Background(), `CREATE FUNCTION fail_bill_line() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced'; END $$; CREATE TRIGGER fail_bill_line BEFORE INSERT ON bill_line_items FOR EACH ROW EXECUTE FUNCTION fail_bill_line()`); e != nil {
		t.Fatal(e)
	}
	if w := request(true); w.Code != 500 {
		t.Errorf("forced line failure: %d %s", w.Code, w.Body)
	}
	if count() != before {
		t.Error("line failure committed bill")
	}
	if _, e = p.Exec(context.Background(), `DROP TRIGGER fail_bill_line ON bill_line_items; DROP FUNCTION fail_bill_line(); CREATE FUNCTION corrupt_bill_timestamp() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.created_at='infinity'; RETURN NEW; END $$; CREATE TRIGGER corrupt_bill_timestamp BEFORE INSERT ON bills FOR EACH ROW EXECUTE FUNCTION corrupt_bill_timestamp()`); e != nil {
		t.Fatal(e)
	}
	if w := request(true); w.Code != 500 {
		t.Errorf("serialization failure: %d %s", w.Code, w.Body)
	}
	if count() != before {
		t.Error("serialization failure committed bill")
	}
	if _, e = p.Exec(context.Background(), `DROP TRIGGER corrupt_bill_timestamp ON bills; DROP FUNCTION corrupt_bill_timestamp()`); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errors := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := request(true); w.Code != 201 {
				errors <- w.Code
			}
		}()
	}
	wg.Wait()
	close(errors)
	for code := range errors {
		t.Errorf("concurrent: %d", code)
	}
	if count() != before+8 {
		t.Error("concurrent creations lost")
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
	go func() { result <- request(true).Code }()
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
			t.Fatal("request did not wait for user lock")
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
	if count() != before+8 {
		t.Error("demotion race committed bill")
	}
	if _, e = p.Exec(context.Background(), `UPDATE users SET role='staff' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if w := request(true); w.Code != 403 {
		t.Errorf("demoted user: %d", w.Code)
	}

}
