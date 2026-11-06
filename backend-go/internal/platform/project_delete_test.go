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

func TestProjectDeletePostgres(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','C'); INSERT INTO projects(project_id,customer_id,code,name,currency) VALUES ('11000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','ONE','One','USD'),('11000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','TWO','Two','USD'),('11000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001','FIVE','Five','USD'); INSERT INTO invoices(invoice_id,customer_id,project_id,invoice_type,currency,amount,balance_due,status,is_template) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN',false)`
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
		r := httptest.NewRequest("DELETE", "/api/v1/projects/"+id, nil)
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
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.projects import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1));app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db:yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
 print(json.dumps([[r.status_code,r.json() if r.content else None] for p in ['11000000-0000-0000-0000-000000000001','bad','11000000-0000-0000-0000-000000000099','11000000-0000-0000-0000-000000000005'] for r in [c.delete('/api/v1/projects/'+p)]]))`)
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
	for i, id := range []string{"11000000-0000-0000-0000-000000000001", "bad", "11000000-0000-0000-0000-000000000099", "11000000-0000-0000-0000-000000000005"} {
		w := call(id, true)
		var got any
		if w.Body.Len() > 0 {
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

	if w := call("11000000-0000-0000-0000-000000000001", true); w.Code != 400 || !strings.Contains(w.Body.String(), "referenced by 1") {
		t.Fatalf("referenced: %d %s", w.Code, w.Body)
	}
	_, e = pool.Exec(context.Background(), `INSERT INTO vendors(vendor_id,name) VALUES ('40000000-0000-0000-0000-000000000001','V'); INSERT INTO bills(bill_id,vendor_id,project_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','USD',2,2,'OPEN')`)
	if e != nil {
		t.Fatal(e)
	}
	if w := call("11000000-0000-0000-0000-000000000001", true); w.Code != 400 || !strings.Contains(w.Body.String(), "referenced by 2") {
		t.Fatalf("combined: %d %s", w.Code, w.Body)
	}
	_, e = pool.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000001'`)
	if e != nil {
		t.Fatal(e)
	}
	if w := call("11000000-0000-0000-0000-000000000001", true); w.Code != 400 || !strings.Contains(w.Body.String(), "referenced by 1") {
		t.Fatalf("bill: %d %s", w.Code, w.Body)
	}
	if w := call("11000000-0000-0000-0000-000000000002", false); w.Code != 401 {
		t.Fatalf("auth: %d", w.Code)
	}
	if w := call("11000000-0000-0000-0000-000000000002", true); w.Code != 204 || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := call("11000000-0000-0000-0000-000000000002", true); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
	// Hold an uncommitted FK key-share lock: deletion must wait and then count the committed invoice.
	_, e = pool.Exec(context.Background(), `INSERT INTO projects(project_id,customer_id,code,name,currency) VALUES ('11000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','THREE','Three','USD')`)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	_, e = tx.Exec(context.Background(), `INSERT INTO invoices(invoice_id,customer_id,project_id,invoice_type,currency,amount,balance_due,status,is_template) VALUES ('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000003','MILESTONE','USD',10,10,'OPEN',false)`)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call("11000000-0000-0000-0000-000000000003", true) }()
	select {
	case w := <-done:
		t.Fatalf("delete did not wait: %d %s", w.Code, w.Body)
	case <-time.After(150 * time.Millisecond):
	}
	if e = tx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case w := <-done:
		if w.Code != 400 {
			t.Fatalf("concurrent reference: %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete stalled")
	}
	_, e = pool.Exec(context.Background(), `INSERT INTO projects(project_id,customer_id,code,name,currency) VALUES ('11000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001','FOUR','Four','USD')`)
	if e != nil {
		t.Fatal(e)
	}
	demote, e := pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer demote.Rollback(context.Background())
	if _, e = demote.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	dch := make(chan *httptest.ResponseRecorder, 1)
	go func() { dch <- call("11000000-0000-0000-0000-000000000004", true) }()
	select {
	case w := <-dch:
		t.Fatalf("demotion did not wait: %d", w.Code)
	case <-time.After(150 * time.Millisecond):
	}
	if e = demote.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case w := <-dch:
		if w.Code != 403 {
			t.Fatalf("demotion: %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("demotion stalled")
	}
	var exists bool
	if e = pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM projects WHERE code='FOUR')`).Scan(&exists); e != nil || !exists {
		t.Fatalf("demotion deleted project: %v", e)
	}
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	var spec map[string]any
	if e = json.Unmarshal(out.Body.Bytes(), &spec); e != nil {
		t.Fatal(e)
	}
	paths := spec["paths"].(map[string]any)
	op := paths["/api/v1/projects/{project_id}"].(map[string]any)["delete"].(map[string]any)
	responses := op["responses"].(map[string]any)
	success, ok := responses["204"].(map[string]any)
	if out.Code != 200 || !ok || success["content"] != nil || op["operationId"] != "delete-project" {
		t.Fatalf("openapi: %d", out.Code)
	}

}
