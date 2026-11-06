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

func TestProjectCreatePostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A')`)
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: v})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call(`{"customer_id":"10000000-0000-0000-0000-000000000001","code":" ab ","name":" Test ","currency":"usd","contract_value":"123.4500"}`)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"code":"AB"`) || !strings.Contains(w.Body.String(), `"contract_value":"123.4500"`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	py, err := pgxpool.New(context.Background(), pydb.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer py.Close()
	if _, err = py.Exec(context.Background(), `INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A')`); err != nil {
		t.Fatal(err)
	}
	cases := []string{`{"customer_id":"10000000-0000-0000-0000-000000000001","code":"new","name":"Example","currency":"idr","contract_value":"42.1234","status":"ON_HOLD","start_date":"2020-01-01","end_date":"2021-02-03","notes":"hi"}`, `{"customer_id":"10000000-0000-0000-0000-000000000001","code":"new","name":"X","currency":"idr"}`, `{"customer_id":"10000000-0000-0000-0000-000000000099","code":"missing","name":"X","currency":"idr"}`, `{}`, `{"customer_id":"bad","code":"","name":12,"currency":"US","status":"foo"}`, `{"customer_id":"10000000-0000-0000-0000-000000000001","code":"Z","name":"X","currency":"USD","contract_value":"0.00001"}`, `{"customer_id":"10000000-0000-0000-0000-000000000001","code":"Z","name":"X","currency":"USD","status":null}`, `{"customer_id":"10000000-0000-0000-0000-000000000001","code":"UNDERSCORE","name":"X","currency":"USD","contract_value":"1_0"}`, `{"customer_id":"10000000-0000-0000-0000-000000000001","code":"NONFINITE","name":"X","currency":"USD","contract_value":"NaN"}`}
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
 print(json.dumps([[r.status_code,r.json()] for b in json.loads(os.environ['BODIES']) for r in [c.post('/api/v1/projects',json=json.loads(b))]]))`)
	cmd.Dir = "../../../backend"
	bs, _ := json.Marshal(cases)
	cmd.Env = append(os.Environ(), "DB="+pydb.URL(), "BODIES="+string(bs))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var expected [][]any
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err)
	}
	for i, body := range cases {
		w := call(body)
		var got any
		if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code == 201 {
			g := got.(map[string]any)
			e := expected[i][1].(map[string]any)
			delete(g, "project_id")
			delete(e, "project_id")
			delete(g, "created_at")
			delete(e, "created_at")
		}
		if expected[i][0] == float64(422) {
			for _, x := range expected[i][1].(map[string]any)["detail"].([]any) {
				x.(map[string]any)["input"] = "[REDACTED]"
			}
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(got, expected[i][1]) {
			t.Errorf("case %s got %d %#v python %#v", body, w.Code, got, expected[i])
		}
	}
	for _, body := range []string{`{"customer_id":"10000000-0000-0000-0000-000000000001","code":"X","name":"N","currency":"USD","contract_value":"9999999999999999"}`, `{"secret":"token"`} {
		w := call(body)
		if w.Code != 422 || strings.Contains(w.Body.String(), "token") {
			t.Fatalf("validation: %d %s", w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(`{"secret":"token"}`))
	r.Header.Set("Content-Type", "text/plain")
	r.AddCookie(&http.Cookie{Name: "session", Value: v})
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, r)
	if bad.Code != 422 || strings.Contains(bad.Body.String(), "token") {
		t.Fatalf("media: %d %s", bad.Code, bad.Body)
	}

	for _, tc := range []struct {
		cookie *http.Cookie
		status int
	}{{nil, 401}, {&http.Cookie{Name: "session", Value: v + "x"}, 401}} {
		r := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(cases[0]))
		r.Header.Set("Content-Type", "application/json")
		if tc.cookie != nil {
			r.AddCookie(tc.cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("auth %d %s", w.Code, w.Body)
		}
	}
	r = httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: v})
	bad = httptest.NewRecorder()
	h.ServeHTTP(bad, r)
	if bad.Code != 413 {
		t.Fatalf("body limit %d", bad.Code)
	}
	open := httptest.NewRecorder()
	h.ServeHTTP(open, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(open.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	operation := doc["paths"].(map[string]any)["/api/v1/projects"].(map[string]any)["post"].(map[string]any)
	if operation["security"] == nil || operation["responses"].(map[string]any)["201"] == nil {
		t.Fatal("missing schema/security")
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- call(`{"customer_id":"10000000-0000-0000-0000-000000000001","code":"RACE","name":"X","currency":"USD"}`)
	}()
	time.Sleep(150 * time.Millisecond)
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-result:
		if w.Code != 403 {
			t.Fatalf("demotion %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("demotion wait")
	}
	var count int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM projects WHERE code='RACE'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("demotion wrote %d %v", count, err)
	}

}
