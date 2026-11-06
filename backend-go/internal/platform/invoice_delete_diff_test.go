package platform

import (
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

func TestInvoiceDeleteDifferential(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','C'); INSERT INTO projects(project_id,customer_id,code,name,currency) VALUES ('11000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','ONE','One','USD'),('11000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','TWO','Two','USD'),('11000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001','FIVE','Five','USD'); INSERT INTO invoices(invoice_id,customer_id,project_id,invoice_type,currency,amount,balance_due,status,is_template) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN',false),('20000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001',NULL,'MILESTONE','USD',10,10,'DRAFT',false),('20000000-0000-0000-0000-000000000006',NULL,NULL,'MILESTONE','USD',10,10,'PARTIAL',false),('20000000-0000-0000-0000-000000000007',NULL,NULL,'MILESTONE','USD',10,10,'PAID',false),('20000000-0000-0000-0000-000000000008',NULL,NULL,'MILESTONE','USD',10,10,'VOID',false),('20000000-0000-0000-0000-000000000009',NULL,NULL,'MILESTONE','USD',10,10,'SENT',false),('20000000-0000-0000-0000-000000000010',NULL,NULL,'MILESTONE','USD',10,10,'DRAFT',false); INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000005',0,'Delete',1,10,10),('30000000-0000-0000-0000-000000000002','20000000-0000-0000-0000-000000000010',0,'Keep',1,10,10); INSERT INTO payments(payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('40000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000010',10,'USD','P','2026-01-01','MANUAL','PENDING_MANUAL_REVIEW')`
	_, e = pool.Exec(context.Background(), seed)
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	call := func(id string, authorized bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("DELETE", "/api/v1/invoices/"+id, nil)
		if authorized {
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pp, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	if _, e = pp.Exec(context.Background(), seed); e != nil {
		t.Fatal(e)
	}
	// Populate independent rows and preserve their complete contents across both implementations.
	unrelated := `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,created_at) VALUES ('90000000-0000-0000-0000-000000000001','MILESTONE','USD',5,5,'OPEN','2026-01-01'); INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount,created_at) VALUES ('90000000-0000-0000-0000-000000000002','90000000-0000-0000-0000-000000000001',0,'unrelated',1,5,5,'2026-01-01'); INSERT INTO payments(payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status,created_at) VALUES ('90000000-0000-0000-0000-000000000003','90000000-0000-0000-0000-000000000001',5,'USD','other','2026-01-01','MANUAL','PENDING_MANUAL_REVIEW','2026-01-01'); INSERT INTO journal_entries(entry_id,source_type,source_id,memo,posted_at) VALUES ('90000000-0000-0000-0000-000000000004','INVOICE','90000000-0000-0000-0000-000000000001','unrelated','2026-01-01')`
	snapshot := func(db *pgxpool.Pool) string {
		var result string
		if e := db.QueryRow(context.Background(), `SELECT json_build_array((SELECT row_to_json(i) FROM invoices i WHERE invoice_id='90000000-0000-0000-0000-000000000001'),(SELECT row_to_json(l) FROM invoice_line_items l WHERE line_item_id='90000000-0000-0000-0000-000000000002'),(SELECT row_to_json(p) FROM payments p WHERE payment_id='90000000-0000-0000-0000-000000000003'),(SELECT row_to_json(j) FROM journal_entries j WHERE entry_id='90000000-0000-0000-0000-000000000004'))::text`).Scan(&result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	for _, db := range []*pgxpool.Pool{pool, pp} {
		if _, e = db.Exec(context.Background(), unrelated); e != nil {
			t.Fatal(e)
		}
	}
	beforeGo, beforePython := snapshot(pool), snapshot(pp)
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1));app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db:yield db
app.dependency_overrides[get_db]=session
with TestClient(app,raise_server_exceptions=False) as c:
 print(json.dumps([[r.status_code,r.json() if r.content and r.status_code != 500 else None] for p in ['20000000-0000-0000-0000-000000000001','bad','20000000-0000-0000-0000-000000000099','20000000-0000-0000-0000-000000000005','20000000-0000-0000-0000-000000000006','20000000-0000-0000-0000-000000000007','20000000-0000-0000-0000-000000000008','20000000-0000-0000-0000-000000000009','20000000-0000-0000-0000-000000000010'] for r in [c.delete('/api/v1/invoices/'+p)]]))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pydb.URL())
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var expected [][]any
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e)
	}
	for i, id := range []string{"20000000-0000-0000-0000-000000000001", "bad", "20000000-0000-0000-0000-000000000099", "20000000-0000-0000-0000-000000000005", "20000000-0000-0000-0000-000000000006", "20000000-0000-0000-0000-000000000007", "20000000-0000-0000-0000-000000000008", "20000000-0000-0000-0000-000000000009", "20000000-0000-0000-0000-000000000010"} {
		w := call(id, true)
		var got any
		if w.Body.Len() > 0 && w.Code != 500 {
			if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
				t.Fatal(e)
			}
		}
		if w.Code == 204 && (w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store") {
			t.Fatalf("204 headers/body: %#v %s", w.Header(), w.Body)
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(got, expected[i][1]) {
			t.Fatalf("parity %s: %d %#v vs %#v", id, w.Code, got, expected[i])
		}
	}
	for _, db := range []*pgxpool.Pool{pool, pp} {
		var deleted, retained, lines, payments int
		if e := db.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000005'), (SELECT count(*) FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000010'), (SELECT count(*) FROM invoice_line_items), (SELECT count(*) FROM payments)`).Scan(&deleted, &retained, &lines, &payments); e != nil {
			t.Fatal(e)
		}
		if deleted != 0 || retained != 1 || lines != 2 || payments != 2 {
			t.Fatalf("cascade/rollback: %d %d %d %d", deleted, retained, lines, payments)
		}
	}
	if snapshot(pool) != beforeGo || snapshot(pp) != beforePython {
		t.Fatal("unrelated invoice, line, payment or ledger entry changed")
	}
	if w := call("20000000-0000-0000-0000-000000000010", false); w.Code != 401 {
		t.Fatalf("auth: %d", w.Code)
	}
	// A status change holding the invoice row must be observed before deletion.
	ctx := context.Background()
	if _, e = pool.Exec(ctx, `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status) VALUES ('20000000-0000-0000-0000-000000000011','MILESTONE','USD',10,10,'DRAFT'),('20000000-0000-0000-0000-000000000012','MILESTONE','USD',10,10,'DRAFT'),('20000000-0000-0000-0000-000000000013','MILESTONE','USD',10,10,'DRAFT')`); e != nil {
		t.Fatal(e)
	}
	waitLock := func(query string) {
		deadline := time.Now().Add(5 * time.Second)
		for {
			var n int
			if e := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE $1 AND pid <> pg_backend_pid()`, query).Scan(&n); e != nil {
				t.Fatal(e)
			}
			if n > 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no lock wait for %s", query)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `UPDATE invoices SET status='OPEN' WHERE invoice_id='20000000-0000-0000-0000-000000000011'`); e != nil {
		t.Fatal(e)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call("20000000-0000-0000-0000-000000000011", true) }()
	waitLock("%FROM invoices%FOR UPDATE%")
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if w := <-done; w.Code != 400 {
		t.Fatalf("status race: %d %s", w.Code, w.Body)
	}
	// A reference inserted before deletion commits prevents the delete and rolls it back.
	tx, e = pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `INSERT INTO payments(payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('40000000-0000-0000-0000-000000000002','20000000-0000-0000-0000-000000000012',10,'USD','P','2026-01-01','MANUAL','PENDING_MANUAL_REVIEW')`); e != nil {
		t.Fatal(e)
	}
	go func() { done <- call("20000000-0000-0000-0000-000000000012", true) }()
	waitLock("%FROM invoices%FOR UPDATE%")
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if w := <-done; w.Code != 500 {
		t.Fatalf("reference race: %d %s", w.Code, w.Body)
	}
	// Queue actual finalize before delete behind an invoice lock, using distinct admins.
	if _, e = pool.Exec(ctx, `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status) VALUES ('20000000-0000-0000-0000-000000000014','MILESTONE','USD',10,10,'DRAFT')`); e != nil {
		t.Fatal(e)
	}
	token2, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	tx, e = pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT 1 FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000014' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	finalized := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("POST", "/api/v1/invoices/20000000-0000-0000-0000-000000000014/finalize", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token2})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		finalized <- w
	}()
	waitLock("%FROM invoices%FOR UPDATE%")
	go func() { done <- call("20000000-0000-0000-0000-000000000014", true) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if e = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%FOR UPDATE%' AND pid <> pg_backend_pid()`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finalize and delete did not both wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if w := <-finalized; w.Code != 200 {
		t.Fatalf("finalize race: %d %s", w.Code, w.Body)
	}
	if w := <-done; w.Code != 400 {
		t.Fatalf("delete after finalize: %d %s", w.Code, w.Body)
	}
	// Admin role is rechecked under lock in the transaction.
	tx, e = pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	go func() { done <- call("20000000-0000-0000-0000-000000000013", true) }()
	waitLock("%FROM users%FOR UPDATE%")
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if w := <-done; w.Code != 403 {
		t.Fatalf("demotion race: %d %s", w.Code, w.Body)
	}
	var remaining int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE invoice_id IN ('20000000-0000-0000-0000-000000000011','20000000-0000-0000-0000-000000000012','20000000-0000-0000-0000-000000000013')`).Scan(&remaining); e != nil || remaining != 3 {
		t.Fatalf("race rollback: %d %v", remaining, e)
	}
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	var spec map[string]any
	if e = json.Unmarshal(out.Body.Bytes(), &spec); e != nil {
		t.Fatal(e)
	}
	op := spec["paths"].(map[string]any)["/api/v1/invoices/{invoice_id}"].(map[string]any)["delete"].(map[string]any)
	success := op["responses"].(map[string]any)["204"].(map[string]any)
	if out.Code != 200 || success["content"] != nil || op["operationId"] != "delete-invoice" {
		t.Fatalf("openapi: %d %v", out.Code, op)
	}

}
