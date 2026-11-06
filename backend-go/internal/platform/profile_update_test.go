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
	"sync"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestProfileUpdateSingleton(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	d := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), d.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(context.Background(), `DELETE FROM business_profile; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`)
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
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	unauth := httptest.NewRequest("PUT", "/api/v1/business-profile", strings.NewReader(`{}`))
	unauth.Header.Set("Content-Type", "application/json")
	uw := httptest.NewRecorder()
	h.ServeHTTP(uw, unauth)
	if uw.Code != 401 {
		t.Fatalf("unauthenticated: %d", uw.Code)
	}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"name":42}`, 422}, {`[]`, 422}, {``, 422},
	} {
		if w := call(tc.body); w.Code != tc.code {
			t.Fatalf("validation %q: %d %s", tc.body, w.Code, w.Body)
		}
	}
	var n int
	if err := p.QueryRow(context.Background(), `SELECT count(*) FROM business_profile`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("invalid request created profile: %d %v", n, err)
	}
	if w := call(`{"name":"Agency","address":"A"}`); w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := call(`{"name":null}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"address":"A"`) || !strings.Contains(w.Body.String(), `"name":null`) {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	if _, err = p.Exec(context.Background(), `INSERT INTO payment_account(business_profile_id,currency,instructions,is_default) VALUES (1,'USD','keep me',true)`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body, media string
		code        int
	}{{strings.Repeat("x", (1<<20)+1), "application/json", 413}, {"private-secret", "text/plain", 422}, {`{"name":`, "application/json", 422}} {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.media)
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "private-secret") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("boundary %s: %d %s", tc.media, w.Code, w.Body)
		}
	}
	var instructions string
	if err = p.QueryRow(context.Background(), `SELECT instructions FROM payment_account WHERE currency='USD'`).Scan(&instructions); err != nil || instructions != "keep me" {
		t.Fatalf("account changed: %q %v", instructions, err)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	op := paths["/api/v1/business-profile"].(map[string]any)["put"].(map[string]any)
	if op["requestBody"] == nil || op["security"] == nil {
		t.Fatalf("missing update contract: %#v", op)
	}
	if _, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`); err != nil {
		t.Fatal(err)
	}
	token2, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	ur := httptest.NewRequest("PUT", "/api/v1/business-profile", strings.NewReader(`{"name":"forbidden"}`))
	ur.Header.Set("Content-Type", "application/json")
	ur.AddCookie(&http.Cookie{Name: "session", Value: token2})
	uw2 := httptest.NewRecorder()
	h.ServeHTTP(uw2, ur)
	if uw2.Code != 403 {
		t.Fatalf("non-admin: %d", uw2.Code)
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	held, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.Background())
	if _, err = held.Exec(context.Background(), `SELECT id FROM business_profile WHERE id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan int, 2)
	for _, tc := range []struct{ body, token string }{{`{"contact_email":"parallel"}`, token}, {`{"contact_phone":"parallel"}`, token2}} {
		wg.Add(1)
		go func(body, tok string) {
			defer wg.Done()
			r := httptest.NewRequest("PUT", "/api/v1/business-profile", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "session", Value: tok})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				failures <- w.Code
			}
		}(tc.body, tc.token)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%ApplyBusinessProfileUpdate%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("updates did not both block: %d", waiting)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = held.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(failures)
	for code := range failures {
		t.Fatalf("concurrent update: %d", code)
	}
	var email, phone string
	if err = p.QueryRow(context.Background(), `SELECT contact_email,contact_phone FROM business_profile WHERE id=1`).Scan(&email, &phone); err != nil || email != "parallel" || phone != "parallel" {
		t.Fatalf("lost update: %q %q %v", email, phone, err)
	}

	trace := &customerLockTracer{reached: make(chan uint32, 1), release: make(chan struct{})}
	defer trace.unblock()
	pc, err := pgxpool.ParseConfig(d.URL())
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.Tracer = trace
	traced, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	th, err := NewHandlerWithSessions(traced, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile", strings.NewReader(`{"name":"forbidden"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		th.ServeHTTP(w, r)
		done <- w
	}()
	select {
	case <-trace.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("admin lock not reached")
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	trace.unblock()
	select {
	case w := <-done:
		if w.Code != 403 {
			t.Fatalf("demotion: %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("demotion request stuck")
	}
	var name *string
	if err = p.QueryRow(context.Background(), `SELECT name FROM business_profile WHERE id=1`).Scan(&name); err != nil || name != nil {
		t.Fatalf("demotion changed profile: %v %v", name, err)
	}

}

func TestProfileUpdatePythonDifferential(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	d := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pd := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), d.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pp, err := pgxpool.New(context.Background(), pd.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pp.Close()
	for _, pool := range []*pgxpool.Pool{p, pp} {
		_, err = pool.Exec(context.Background(), `DELETE FROM business_profile; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`)
		if err != nil {
			t.Fatal(err)
		}
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
	bodies := []string{`{}`, `{"name":"N","address":"A","contact_email":"E","contact_phone":"P","invoice_title":"T","invoice_summary":"S","logo_url":"L","default_notes":"D","npwp":"W","npwp_name":"WN"}`, `{"name":null,"npwp_name":null,"ignored":"x"}`, `{"name":42,"address":false}`, `[]`, `null`, `{"address":""}`}
	b, _ := json.Marshal(bodies)
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.business_profile import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
 print(json.dumps([[r.status_code,r.json()] for body in json.loads(os.environ['BODIES']) for r in [c.put('/api/v1/business-profile',content=body,headers={'content-type':'application/json'})]]))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pd.URL(), "BODIES="+string(b))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want [][]any
	if err = json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, body := range bodies {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code == 200 {
			delete(got.(map[string]any), "updated_at")
			delete(want[i][1].(map[string]any), "updated_at")
		}
		if w.Code != int(want[i][0].(float64)) || !reflect.DeepEqual(got, want[i][1]) {
			t.Fatalf("%s: got %d %#v want %#v", body, w.Code, got, want[i])
		}
	}
}
