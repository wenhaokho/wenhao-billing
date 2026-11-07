package platform

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
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

func TestQuotationConvertPostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','admin'); INSERT INTO customers(customer_id,name,default_wht_type,default_wht_rate,default_wht_gross_up) VALUES ('10000000-0000-0000-0000-000000000001','A','PPH23',2,true); INSERT INTO quotations(quotation_id,customer_id,currency,subtotal,amount,status,payment_terms) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','USD',100,100,'DRAFT','Net 30'); INSERT INTO quotation_line_items(line_item_id,quotation_id,description,quantity,unit_price,amount,position) VALUES ('50000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','item',1,100,100,0)`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/quotations/20000000-0000-0000-0000-000000000001/convert-to-invoice", strings.NewReader(`{"issue_date":"2025-01-01","due_in_days":"3.0"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// Compare actual FastAPI responses using the same isolated database, resetting
	// the generated invoice between requests so document numbering is identical.
	py := `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.quotations import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1));app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app,raise_server_exceptions=False) as c:
 r=c.post('/api/v1/quotations/20000000-0000-0000-0000-000000000001/convert-to-invoice',json={'issue_date':'2025-01-01','due_in_days':'3.0'});d=r.json();keys=['status','currency','subtotal','amount','wht_type','wht_rate','wht_gross_up','wht_base','wht_amount','issue_date','due_date','payment_terms'];print(json.dumps([r.status_code,{k:d.get(k) for k in keys}] if r.status_code==201 else [r.status_code,d]))`
	for _, tc := range []struct {
		status, currency, terms, kind string
		gross                         bool
	}{{"DRAFT", "USD", "Net 30", "PPH23", true}, {"SENT", "IDR", "on receipt", "PPH21", false}, {"ACCEPTED", "EUR", "", "NONE", false}, {"EXPIRED", "USD", "other", "PPH23", false}, {"DRAFT", "USD", "Net ٣٠", "PPH23", false}, {"SENT", "USD", "Net 30", "PPH23", false}, {"DRAFT", "USD", "On Receipt", "NONE", false}, {"DRAFT", "USD", "Due\ton receipt", "NONE", false}, {"DRAFT", "USD", "Net\x1c30", "NONE", false}, {"DRAFT", "USD", "On Receipt\x1f", "NONE", false}, {"VOID", "USD", "Net 7", "NONE", false}} {
		_, err = p.Exec(context.Background(), `UPDATE quotations SET status=$1,currency=$2,payment_terms=$3,converted_invoice_id=NULL`, tc.status, tc.currency, tc.terms)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Exec(context.Background(), `UPDATE customers SET default_wht_type=$1,default_wht_gross_up=$2`, tc.kind, tc.gross); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("python", "-c", py)
		cmd.Env = append(os.Environ(), "DB="+db.URL(), "PYTHONWARNINGS=ignore")
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("python: %v %s", err, raw)
		}
		var want []any
		if err = json.Unmarshal(raw, &want); err != nil {
			t.Fatalf("python output %s: %v", raw, err)
		}
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET status=$1,converted_invoice_id=NULL`, tc.status); err != nil {
			t.Fatal(err)
		}
		if _, err = p.Exec(context.Background(), `DELETE FROM invoices`); err != nil {
			t.Fatal(err)
		}
		w := call()
		var result map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var got []any
		if w.Code == 201 {
			fields := map[string]any{}
			for _, key := range []string{"status", "currency", "subtotal", "amount", "wht_type", "wht_rate", "wht_gross_up", "wht_base", "wht_amount", "issue_date", "due_date", "payment_terms"} {
				fields[key] = result[key]
			}
			got = []any{float64(w.Code), fields}
		} else {
			got = []any{float64(w.Code), result}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%+v python %v go %v", tc, want, got)
		}
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET converted_invoice_id=NULL; DELETE FROM invoices`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.Exec(context.Background(), `UPDATE customers SET default_wht_type='PPH23',default_wht_rate=2,default_wht_gross_up=true; UPDATE quotations SET currency='USD',payment_terms='Net 30'`); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"DECLINED", "VOID", "INVOICED"} {
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET status=$1`, status); err != nil {
			t.Fatal(err)
		}
		if w := call(); w.Code != 400 {
			t.Fatalf("%s: %d %s", status, w.Code, w.Body)
		}
	}

	for _, body := range []string{`null`, `[]`, `{"issue_date":"invalid"}`, `{"due_in_days":null}`, `{"due_in_days":1.5}`} {
		req := httptest.NewRequest("POST", "/api/v1/quotations/20000000-0000-0000-0000-000000000001/convert-to-invoice", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 422 {
			t.Fatalf("invalid body %s: %d %s", body, w.Code, w.Body)
		}
	}
	// Invalid or missing lines and invalid withholding must not create invoices.
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT'; DELETE FROM quotation_line_items`); err != nil {
		t.Fatal(err)
	}
	if w := call(); w.Code != 400 {
		t.Fatalf("empty quotation: %d %s", w.Code, w.Body)
	}
	if _, err = p.Exec(context.Background(), `INSERT INTO quotation_line_items(line_item_id,quotation_id,description,quantity,unit_price,amount,position) VALUES ('50000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','item',1,100,100,0); UPDATE customers SET default_wht_rate=0`); err != nil {
		t.Fatal(err)
	}
	if w := call(); w.Code != 400 {
		t.Fatalf("invalid withholding: %d %s", w.Code, w.Body)
	}
	if _, err = p.Exec(context.Background(), `UPDATE customers SET default_wht_rate=2; UPDATE quotations SET payment_terms='net 9999999999999999999999999'`); err != nil {
		t.Fatal(err)
	}
	if w := call(); w.Code != 500 {
		t.Fatalf("overflow: %d %s", w.Code, w.Body)
	}
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET payment_terms='Net 30'; UPDATE users SET role='staff'`); err != nil {
		t.Fatal(err)
	}
	if w := call(); w.Code != 403 {
		t.Fatalf("demoted user: %d %s", w.Code, w.Body)
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='admin'`); err != nil {
		t.Fatal(err)
	}
	var before int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&before); err != nil || before != 0 {
		t.Fatalf("unexpected invoice count: %d %v", before, err)
	}
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT'`); err != nil {
		t.Fatal(err)
	}

	// Python accepts underscore integers and very large integers when quotation
	// payment terms override them. Compare both HTTP implementations.
	for _, literal := range []string{`"1_000"`, `999999999999999999999999999999999999999999999999999999999999999999`, `1.00000000000000001`, `1e20`, `-9223372036854775808.0`} {
		script := strings.Replace(py, "'due_in_days':'3.0'", "'due_in_days':json.loads(os.environ['DUE'])", 1)
		cmd := exec.Command("python", "-c", script)
		cmd.Env = append(os.Environ(), "DB="+db.URL(), "DUE="+literal, "PYTHONWARNINGS=ignore")
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("python due %s: %v %s", literal, err, raw)
		}
		var want []any
		if err = json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT',converted_invoice_id=NULL; DELETE FROM invoices`); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/v1/quotations/20000000-0000-0000-0000-000000000001/convert-to-invoice", strings.NewReader(`{"issue_date":"2025-01-01","due_in_days":`+literal+`}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var got map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if float64(w.Code) != want[0] || got["due_date"] != want[1].(map[string]any)["due_date"] {
			t.Fatalf("due %s: Python %v Go %d %v", literal, want, w.Code, got)
		}
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT',converted_invoice_id=NULL; DELETE FROM invoices`); err != nil {
			t.Fatal(err)
		}
	}
	// A concurrent change to customer defaults must be observed after its lock is released.
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE customers SET default_wht_rate=4`); err != nil {
		t.Fatal(err)
	}
	waiting := make(chan *httptest.ResponseRecorder, 1)
	go func() { waiting <- call() }()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockMilestoneCustomer%'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		tx.Rollback(context.Background())
		t.Fatal("conversion did not wait for customer lock")
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	rw := <-waiting
	if rw.Code != 201 || !strings.Contains(rw.Body.String(), `"wht_rate":"4.0000"`) {
		t.Fatalf("customer lock: %d %s", rw.Code, rw.Body)
	}
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT',converted_invoice_id=NULL; DELETE FROM invoices; UPDATE customers SET default_wht_rate=2`); err != nil {
		t.Fatal(err)
	}

	// An invoice writer holding the allocation lock must be able to take FK locks
	// on the same references while conversion waits for that allocation lock.
	if _, err = p.Exec(context.Background(), `INSERT INTO projects(project_id,customer_id,code,name,currency) VALUES ('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P','Project','USD'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000001','Item','SERVICE'); UPDATE quotations SET project_id='30000000-0000-0000-0000-000000000001'; UPDATE quotation_line_items SET item_id='40000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}

	// Project deletion's FK check must not deadlock with the locked quotation.
	projectTx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer projectTx.Rollback(context.Background())
	if _, err = projectTx.Exec(context.Background(), `SELECT project_id FROM projects WHERE project_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	projectWait := make(chan *httptest.ResponseRecorder, 1)
	go func() { projectWait <- call() }()
	deadline = time.Now().Add(5 * time.Second)
	blocked = false
	for time.Now().Before(deadline) {
		var n int
		err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%SELECT project_id FROM projects WHERE project_id=$1 FOR KEY SHARE%'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("conversion did not wait for project lock")
	}
	if _, err = projectTx.Exec(context.Background(), `SAVEPOINT deletion`); err != nil {
		t.Fatal(err)
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dcancel()
	_, err = projectTx.Exec(dctx, `DELETE FROM projects WHERE project_id='30000000-0000-0000-0000-000000000001'`)
	var pgErr *pgconn.PgError
	if dctx.Err() != nil || !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("project delete should fail with foreign key violation: %v", err)
	}
	if _, err = projectTx.Exec(context.Background(), `ROLLBACK TO SAVEPOINT deletion`); err != nil {
		t.Fatal(err)
	}
	if err = projectTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w := <-projectWait; w.Code != 201 {
		t.Fatalf("conversion after project delete: %d %s", w.Code, w.Body)
	}
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT',converted_invoice_id=NULL; DELETE FROM invoices`); err != nil {
		t.Fatal(err)
	}
	writer, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	if _, err = writer.Exec(context.Background(), `SELECT pg_advisory_xact_lock(749221001)`); err != nil {
		t.Fatal(err)
	}
	blockedConversion := make(chan *httptest.ResponseRecorder, 1)
	go func() { blockedConversion <- call() }()
	deadline = time.Now().Add(5 * time.Second)
	blocked = false
	for time.Now().Before(deadline) {
		var n int
		err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockInvoiceNumberAllocation%'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("conversion did not wait for allocation lock")
	}
	cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = writer.Exec(cctx, `INSERT INTO invoices(invoice_id,customer_id,project_id,invoice_type,invoice_number,currency,amount,balance_due,status,is_template) VALUES ('60000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','MILESTONE','OTHER','USD',100,100,'DRAFT',false); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,description,quantity,unit_price,amount,position) VALUES ('70000000-0000-0000-0000-000000000001','60000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','Item',1,100,100,0)`)
	if err != nil {
		t.Fatalf("invoice FK lock blocked: %v", err)
	}
	if err = writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w := <-blockedConversion; w.Code != 201 {
		t.Fatalf("conversion after invoice writer: %d %s", w.Code, w.Body)
	}
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT',converted_invoice_id=NULL,project_id=NULL; UPDATE quotation_line_items SET item_id=NULL; DELETE FROM invoices`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 2)
	go func() { done <- call() }()
	token2, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		req := httptest.NewRequest("POST", "/api/v1/quotations/20000000-0000-0000-0000-000000000001/convert-to-invoice", strings.NewReader(`{"issue_date":"2025-01-01","due_in_days":"3.0"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token2})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		done <- w
	}()
	a, b := <-done, <-done
	if a.Code != 201 {
		a, b = b, a
	}
	if a.Code != 201 || b.Code != 400 {
		t.Fatalf("race: %d %s; %d %s", a.Code, a.Body, b.Code, b.Body)
	}
	var out map[string]any
	if err = json.Unmarshal(a.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["due_date"] != "2025-01-31" || out["wht_type"] != "PPH23" || out["amount"] != "102.0400" {
		t.Fatalf("response: %v", out)
	}
	var count int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE source_quote_id='20000000-0000-0000-0000-000000000001'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count %d %v", count, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT',converted_invoice_id=NULL; CREATE FUNCTION reject_conversion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'rejected'; END $$; CREATE TRIGGER reject_conversion BEFORE UPDATE ON quotations FOR EACH ROW EXECUTE FUNCTION reject_conversion()`)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(); w.Code != 500 {
		t.Fatalf("rollback %d %s", w.Code, w.Body)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback count %d %v", count, err)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(map[string]any)["/api/v1/quotations/{quotation_id}/convert-to-invoice"].(map[string]any)["post"].(map[string]any)
	if op["security"] == nil || op["responses"].(map[string]any)["201"] == nil {
		t.Fatal(op)
	}
}
