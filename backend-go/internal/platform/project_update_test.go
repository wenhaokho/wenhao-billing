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
	"testing"
	"time"
)

func TestProjectUpdatePostgres(t *testing.T) {
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
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO projects(project_id,customer_id,code,name,currency,notes) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','A','Original','USD','note')`)
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
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "/api/v1/projects/20000000-0000-0000-0000-000000000001", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call(`{"name":"New","notes":null}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"New"`) || !strings.Contains(w.Body.String(), `"notes":null`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	w = call(`{"currency":null}`)
	if w.Code != 400 {
		t.Fatalf("null currency: %d %s", w.Code, w.Body)
	}
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	py, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer py.Close()
	seed := `INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'),('10000000-0000-0000-0000-000000000002','B'); INSERT INTO projects(project_id,customer_id,code,name,currency,notes,created_at) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','A','Original','USD','note','2020-01-01'),('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','OTHER','Other','USD',null,'2020-01-01')`
	_, e = py.Exec(context.Background(), seed)
	if e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(context.Background(), `DELETE FROM projects; DELETE FROM customers;`+seed)
	if e != nil {
		t.Fatal(e)
	}
	cases := []string{`{}`, `{"name":"  spaced  ","code":" new ","currency":"idr","notes":"abc","active":false}`, `{"customer_id":"10000000-0000-0000-0000-000000000002","contract_value":"1_0","status":"ON_HOLD","start_date":"2020-01-01","end_date":"2020-12-31"}`, `{"notes":null,"contract_value":null,"start_date":null,"end_date":null}`, `{"unknown":"ignored"}`, `{"code":"OTHER"}`, `{"customer_id":"10000000-0000-0000-0000-000000000099"}`, `{"active":"yes"}`, `{"customer_id":"bad","code":"","name":5,"currency":"US","status":"invalid","active":"maybe"}`, `{"currency":null}`, `{"contract_value":"NaN"}`, `{"contract_value":"0.00001"}`}
	bs, _ := json.Marshal(cases)
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
 print(json.dumps([[r.status_code,r.json()] for b in json.loads(os.environ['BODIES']) for r in [c.patch('/api/v1/projects/20000000-0000-0000-0000-000000000001',json=json.loads(b))]]))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pydb.URL(), "BODIES="+string(bs))
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	for i, body := range cases {
		w = call(body)
		var got any
		if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatal(e)
		}
		if expected[i][0] == float64(422) {
			for _, x := range expected[i][1].(map[string]any)["detail"].([]any) {
				x.(map[string]any)["input"] = "[REDACTED]"
			}
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(got, expected[i][1]) {
			t.Errorf("case %s got %d %#v expected %#v", body, w.Code, got, expected[i])
		}
	}

	// Non-JSON and oversized requests cannot mutate the row.
	for _, tc := range []struct {
		body, content string
		status        int
	}{{`{"name":"secret"}`, "text/plain", 422}, {strings.Repeat("x", (1<<20)+1), "application/json", 413}} {
		r := httptest.NewRequest("PATCH", "/api/v1/projects/20000000-0000-0000-0000-000000000001", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.content)
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("media/limit %d %s", w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("PATCH", "/api/v1/projects/20000000-0000-0000-0000-000000000001", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("auth %d", w.Code)
	}
	open := httptest.NewRecorder()
	h.ServeHTTP(open, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(open.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	op := doc["paths"].(map[string]any)["/api/v1/projects/{project_id}"].(map[string]any)["patch"].(map[string]any)
	if op["security"] == nil || op["responses"].(map[string]any)["200"] == nil {
		t.Fatal("missing schema/security")
	}

	for _, tc := range []struct {
		path, body string
		status     int
	}{{"/api/v1/projects/20000000-0000-0000-0000-000000000099", `{}`, 404}, {"/api/v1/projects/invalid", `{}`, 422}, {"/api/v1/projects/20000000-0000-0000-0000-000000000001", `{"name":"sensitive","status":"bad"}`, 422}} {
		r := httptest.NewRequest("PATCH", tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "sensitive") {
			t.Fatalf("path/status/cache/redaction: %d %s %q", w.Code, w.Body, w.Header().Get("Cache-Control"))
		}
	}
	var otherCode, otherName, otherCurrency string
	if e = pool.QueryRow(context.Background(), `SELECT code,name,currency FROM projects WHERE project_id='20000000-0000-0000-0000-000000000002'`).Scan(&otherCode, &otherName, &otherCurrency); e != nil || otherCode != "OTHER" || otherName != "Other" || otherCurrency != "USD" {
		t.Fatalf("other project changed: %q %q %q %v", otherCode, otherName, otherCurrency, e)
	}
	// Distinct admins update disjoint fields while a third transaction holds the project lock.
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`)
	if e != nil {
		t.Fatal(e)
	}
	token2, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	lock, e := pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Rollback(context.Background())
	if _, e = lock.Exec(context.Background(), `SELECT 1 FROM projects WHERE project_id='20000000-0000-0000-0000-000000000001' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	race := make(chan int, 2)
	for i, b := range []string{`{"name":"concurrent"}`, `{"notes":"retained"}`} {
		tok := token
		if i == 1 {
			tok = token2
		}
		go func(body, tok string) {
			r := httptest.NewRequest("PATCH", "/api/v1/projects/20000000-0000-0000-0000-000000000001", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "session", Value: tok})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			race <- w.Code
		}(b, tok)
	}
	select {
	case status := <-race:
		t.Fatalf("update bypassed project lock: %d", status)
	case <-time.After(150 * time.Millisecond):
	}
	if e = lock.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		select {
		case status := <-race:
			if status != 200 {
				t.Fatalf("concurrent %d", status)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent timeout")
		}
	}
	var gotName, gotNotes string
	if e = pool.QueryRow(context.Background(), `SELECT name,notes FROM projects WHERE project_id='20000000-0000-0000-0000-000000000001'`).Scan(&gotName, &gotNotes); e != nil || gotName != "concurrent" || gotNotes != "retained" {
		t.Fatalf("lost update %q %q %v", gotName, gotNotes, e)
	}
	tx, e := pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() { ch <- call(`{"name":"demoted"}`) }()
	time.Sleep(150 * time.Millisecond)
	if e = tx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case w := <-ch:
		if w.Code != 403 {
			t.Fatalf("demotion %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("demotion timeout")
	}
	var name string
	if e = pool.QueryRow(context.Background(), `SELECT name FROM projects WHERE project_id='20000000-0000-0000-0000-000000000001'`).Scan(&name); e != nil || name == "demoted" {
		t.Fatalf("demotion wrote %q %v", name, e)
	}

}
