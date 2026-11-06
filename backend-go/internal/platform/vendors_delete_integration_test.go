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

func TestVendorDeletePostgres(t *testing.T) {
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
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','user')`)
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token := func(id string) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	admin := token("00000000-0000-0000-0000-000000000001")
	user := token("00000000-0000-0000-0000-000000000002")
	call := func(id string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("DELETE", "/api/v1/vendors/"+id, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	_, e = pool.Exec(context.Background(), `INSERT INTO vendors(vendor_id,name,default_currency,payment_terms_days,active) VALUES ('10000000-0000-0000-0000-000000000001','One','IDR',30,true)`)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		c      *http.Cookie
		status int
	}{{nil, 401}, {user, 403}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}, {admin, 200}, {admin, 200}} {
		w := call("10000000-0000-0000-0000-000000000001", tc.c)
		if w.Code != tc.status {
			t.Fatalf("got %d %s want %d", w.Code, w.Body, tc.status)
		}
		if w.Code == 200 {
			var v map[string]any
			if json.Unmarshal(w.Body.Bytes(), &v) != nil || v["active"] != false {
				t.Fatal(w.Body)
			}
		}
	}
	for _, tc := range []struct {
		id     string
		status int
	}{{"bad", 422}, {"10000000-0000-0000-0000-000000000099", 404}, {"{10000000-0000-0000-0000-000000000001}", 200}} {
		w := call(tc.id, admin)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", tc.id, w.Code, w.Body)
		}
	}
	_, e = pool.Exec(context.Background(), `UPDATE vendors SET active=true, contact_email='invalid'`)
	if e != nil {
		t.Fatal(e)
	}
	if w := call("10000000-0000-0000-0000-000000000001", admin); w.Code != 500 {
		t.Fatalf("invalid response: %d %s", w.Code, w.Body)
	}
	var active bool
	if e = pool.QueryRow(context.Background(), `SELECT active FROM vendors`).Scan(&active); e != nil || !active {
		t.Fatalf("validation rollback: %v", e)
	}
	_, e = pool.Exec(context.Background(), `UPDATE vendors SET contact_email=null, payment_terms_days=366`)
	if e != nil {
		t.Fatal(e)
	}
	if w := call("10000000-0000-0000-0000-000000000001", admin); w.Code != 500 {
		t.Fatalf("invalid schema: %d %s", w.Code, w.Body)
	}
	if e = pool.QueryRow(context.Background(), `SELECT active FROM vendors`).Scan(&active); e != nil || !active {
		t.Fatalf("schema rollback: %v", e)
	}
	_, e = pool.Exec(context.Background(), `UPDATE vendors SET contact_email='Test@EXAMPLE.COM', payment_terms_days=30; CREATE FUNCTION reject_vendor_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private commit detail'; END $$; CREATE CONSTRAINT TRIGGER reject_vendor_commit AFTER UPDATE ON vendors DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_vendor_commit()`)
	if e != nil {
		t.Fatal(e)
	}
	if w := call("10000000-0000-0000-0000-000000000001", admin); w.Code != 500 || strings.Contains(w.Body.String(), "private commit detail") {
		t.Fatalf("commit: %d %s", w.Code, w.Body)
	}
	if e = pool.QueryRow(context.Background(), `SELECT active FROM vendors`).Scan(&active); e != nil || !active {
		t.Fatalf("commit rollback: %v", e)
	}

	_, e = pool.Exec(context.Background(), `DROP TRIGGER reject_vendor_commit ON vendors; DROP FUNCTION reject_vendor_commit(); INSERT INTO vendors(vendor_id,name,default_currency,payment_terms_days,active) VALUES ('10000000-0000-0000-0000-000000000002','Other','USD',10,true); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','IDR',10,10)`)
	if e != nil {
		t.Fatal(e)
	}
	var otherBefore string
	if e = pool.QueryRow(context.Background(), `SELECT row_to_json(v)::text FROM vendors v WHERE name='Other'`).Scan(&otherBefore); e != nil {
		t.Fatal(e)
	}
	var before string
	if e = pool.QueryRow(context.Background(), `SELECT row_to_json(b)::text FROM bills b`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	trace := &customerLockTracer{reached: make(chan uint32, 1), release: make(chan struct{})}
	tracedConfig, e := pgxpool.ParseConfig(db.URL())
	if e != nil {
		t.Fatal(e)
	}
	tracedConfig.ConnConfig.Tracer = trace
	tracedPool, e := pgxpool.NewWithConfig(context.Background(), tracedConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer tracedPool.Close()
	defer trace.unblock()
	tracedHandler, e := NewHandlerWithSessions(tracedPool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("DELETE", "/api/v1/vendors/10000000-0000-0000-0000-000000000001", nil)
		r.AddCookie(admin)
		w := httptest.NewRecorder()
		tracedHandler.ServeHTTP(w, r)
		done <- w
	}()
	var pid uint32
	select {
	case pid = <-trace.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("admin lock not reached")
	}
	tx, e := pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE email='a@example.com'`); e != nil {
		t.Fatal(e)
	}
	trace.unblock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if e = pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')`, pid).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recheck did not wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = tx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if w := <-done; w.Code != 403 {
		t.Fatalf("demotion: %d %s", w.Code, w.Body)
	}
	if _, e = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE email='a@example.com'`); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.vendors import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ["VENDOR_DB"].replace("postgres://","postgresql+psycopg://",1))
app=FastAPI();app.include_router(router,prefix="/api/v1");app.dependency_overrides[current_admin]=lambda:object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as client:
    r=client.delete("/api/v1/vendors/10000000-0000-0000-0000-000000000001");print(json.dumps([r.status_code,r.json()]))
`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "VENDOR_DB="+db.URL())
	py, e := cmd.Output()
	if e != nil {
		t.Fatalf("Python: %v", e)
	}
	var expected []any
	if e = json.Unmarshal(py, &expected); e != nil {
		t.Fatal(e)
	}
	w := call("10000000-0000-0000-0000-000000000001", admin)
	var got any
	if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
		t.Fatal(e)
	}
	if float64(w.Code) != expected[0] || !reflect.DeepEqual(got, expected[1]) {
		t.Fatalf("parity Go %d %v Python %v", w.Code, got, expected)
	}
	var after string
	if e = pool.QueryRow(context.Background(), `SELECT row_to_json(b)::text FROM bills b`).Scan(&after); e != nil || before != after {
		t.Fatalf("bill changed: %s %s %v", before, after, e)
	}
	if e = pool.QueryRow(context.Background(), `SELECT active FROM vendors WHERE name='Other'`).Scan(&active); e != nil || !active {
		t.Fatalf("other vendor changed: %v", e)
	}

	var otherAfter string
	if e = pool.QueryRow(context.Background(), `SELECT row_to_json(v)::text FROM vendors v WHERE name='Other'`).Scan(&otherAfter); e != nil || otherAfter != otherBefore {
		t.Fatalf("other vendor snapshot changed: %v", e)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if spec.Code != 200 || json.Unmarshal(spec.Body.Bytes(), &doc) != nil {
		t.Fatalf("openapi: %d", spec.Code)
	}
	paths := doc["paths"].(map[string]any)
	item := paths["/api/v1/vendors/{vendor_id}"].(map[string]any)
	operation, ok := item["delete"].(map[string]any)
	if !ok {
		t.Fatal("missing delete operation")
	}
	for _, status := range []string{"200", "401", "403", "404", "422", "500"} {
		if _, ok := operation["responses"].(map[string]any)[status]; !ok {
			t.Fatalf("missing response %s", status)
		}
	}
	if operation["security"] == nil {
		t.Fatal("missing security")
	}

}
