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

func TestVendorUpdatePostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	_, e = pool.Exec(ctx, `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','user'),('00000000-0000-0000-0000-000000000003','c@example.com','x','admin'); INSERT INTO vendors(vendor_id,name,default_currency,payment_terms_days,active) VALUES ('10000000-0000-0000-0000-000000000001','One','IDR',30,true),('10000000-0000-0000-0000-000000000002','Other','USD',10,true); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','IDR',10,10)`)
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
	admin2 := token("00000000-0000-0000-0000-000000000003")
	call := func(id, body, media string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "/api/v1/vendors/"+id, strings.NewReader(body))
		r.Header.Set("Content-Type", media)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("no-store missing")
		}
		return w
	}
	id := "10000000-0000-0000-0000-000000000001"
	snapshot := func(query string) string {
		var s string
		if e := pool.QueryRow(ctx, query).Scan(&s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	other := snapshot(`SELECT row_to_json(v)::text FROM vendors v WHERE name='Other'`)
	bill := snapshot(`SELECT row_to_json(b)::text FROM bills b`)
	for _, tc := range []struct {
		id, body, media string
		c               *http.Cookie
		status          int
	}{
		{id, `{}`, "application/json", nil, 401}, {id, `{}`, "application/json", user, 403}, {id, `{}`, "application/json", &http.Cookie{Name: "session", Value: admin.Value + "x"}, 401},
		{"bad", `{"active":"bad"}`, "application/json", admin, 422}, {"10000000-0000-0000-0000-000000000099", `{}`, "application/json", admin, 404},
		{id, `{`, "application/json", nil, 422}, {id, `{}`, "text/plain", admin, 422}, {id, `{"default_currency":"US","payment_terms_days":366}`, "application/json", admin, 422},
		{id, `{"name":"Updated","contact_email":"Test@EXAMPLE.COM","contact_name":"C","default_currency":"USD","payment_terms_days":"5","tax_id":"T","notes":"N","active":"false"}`, "application/json", admin, 200},
		{id, `{"contact_name":null,"notes":null}`, "application/json", admin, 200},
		{id, `{"name":""}`, "application/json", admin, 500}, {id, `{"active":null}`, "application/json", admin, 500},
	} {
		w := call(tc.id, tc.body, tc.media, tc.c)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d %s want %d", tc.body, w.Code, w.Body, tc.status)
		}
		if tc.status == 200 {
			var v map[string]any
			if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil || v["name"] != "Updated" || v["contact_email"] != "Test@example.com" || v["payment_terms_days"] != float64(5) || v["active"] != false {
				t.Fatalf("output: %s", w.Body)
			}
		}
	}
	// Invalid decoded values and URL paths must never be reflected to clients.
	beforeInvalid := snapshot(`SELECT row_to_json(v)::text FROM vendors v WHERE vendor_id='10000000-0000-0000-0000-000000000001'`)
	for _, tc := range []struct{ path, body, typ, root, field, msg string }{
		{id, `{"notes":{"secret":"sensitive-body"}}`, "string_type", "body", "notes", "Input should be a valid string"},
		{id, `{"notes":["escaped-\u0073ecret"]}`, "string_type", "body", "notes", "Input should be a valid string"},
		{"sensitive-path%5Cescaped", `{}`, "uuid_parsing", "path", "vendor_id", "Input should be a valid UUID, invalid character: found `s` at 0"},
	} {
		w := call(tc.path, tc.body, "application/json", admin)
		if w.Code != 422 || strings.Contains(w.Body.String(), "sensitive") || strings.Contains(w.Body.String(), "escaped") {
			t.Fatalf("invalid input leaked: %d %s", w.Code, w.Body)
		}
		var response struct {
			Detail []struct {
				Type  string `json:"type"`
				Loc   []any  `json:"loc"`
				Msg   string `json:"msg"`
				Input any    `json:"input"`
			} `json:"detail"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil || len(response.Detail) != 1 {
			t.Fatalf("invalid error: %s %v", w.Body, e)
		}
		detail := response.Detail[0]
		if detail.Type != tc.typ || len(detail.Loc) != 2 || detail.Loc[0] != tc.root || detail.Loc[1] != tc.field || detail.Msg != tc.msg || detail.Input != "[REDACTED]" {
			t.Fatalf("invalid detail: %+v", detail)
		}
	}
	if got := snapshot(`SELECT row_to_json(v)::text FROM vendors v WHERE vendor_id='10000000-0000-0000-0000-000000000001'`); got != beforeInvalid {
		t.Fatal("invalid request changed vendor")
	}

	if got := snapshot(`SELECT row_to_json(v)::text FROM vendors v WHERE vendor_id='10000000-0000-0000-0000-000000000002'`); got != other {
		t.Fatal("other vendor changed")
	}
	if got := snapshot(`SELECT row_to_json(b)::text FROM bills b`); got != bill {
		t.Fatal("bill changed")
	}
	if got := snapshot(`SELECT name FROM vendors WHERE vendor_id='10000000-0000-0000-0000-000000000001'`); got != "Updated" {
		t.Fatalf("response rollback: %s", got)
	}
	if w := call(id, strings.Repeat("x", (1<<20)+1), "application/json", admin); w.Code != 413 {
		t.Fatalf("body limit: %d", w.Code)
	}
	deleted := token("00000000-0000-0000-0000-000000000099")
	if w := call(id, `{}`, "application/json", deleted); w.Code != 403 {
		t.Fatalf("deleted user: %d", w.Code)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if spec.Code != 200 || json.Unmarshal(spec.Body.Bytes(), &doc) != nil {
		t.Fatal("openapi")
	}
	op := doc["paths"].(map[string]any)["/api/v1/vendors/{vendor_id}"].(map[string]any)["patch"].(map[string]any)
	if op["security"] == nil || op["requestBody"] == nil {
		t.Fatal("missing schema or security")
	}

	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	detailSchema := schemas["VendorUpdateValidationDetail"].(map[string]any)
	inputSchema := detailSchema["properties"].(map[string]any)["input"].(map[string]any)
	if inputSchema["type"] != "string" || !reflect.DeepEqual(inputSchema["enum"], []any{"[REDACTED]"}) {
		t.Fatalf("incorrect redaction schema: %v", inputSchema)
	}
	response422 := op["responses"].(map[string]any)["422"].(map[string]any)
	schema422 := response422["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema422["$ref"] != "#/components/schemas/VendorUpdateValidationErrorBody" {
		t.Fatalf("incorrect 422 schema: %v", schema422)
	}
	for _, status := range []string{"200", "401", "403", "404", "413", "422", "500"} {
		if _, ok := op["responses"].(map[string]any)[status]; !ok {
			t.Fatalf("missing status %s", status)
		}
	}

	// Hold the vendor row while two independent updates queue. Each must read
	// the latest committed row after the lock, preserving the other change.
	lock, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Rollback(ctx)
	if _, e = lock.Exec(ctx, `SELECT vendor_id FROM vendors WHERE vendor_id=$1 FOR UPDATE`, id); e != nil {
		t.Fatal(e)
	}
	done := make(chan *httptest.ResponseRecorder, 2)
	go func() { done <- call(id, `{"notes":"concurrent"}`, "application/json", admin) }()
	go func() { done <- call(id, `{"tax_id":"concurrent"}`, "application/json", admin2) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if e = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("vendor update did not wait for lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = lock.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if w := <-done; w.Code != 200 {
			t.Fatalf("concurrent update: %d %s", w.Code, w.Body)
		}
	}
	if got := snapshot(`SELECT notes || ':' || tax_id FROM vendors WHERE vendor_id='10000000-0000-0000-0000-000000000001'`); got != "concurrent:concurrent" {
		t.Fatalf("lost update: %s", got)
	}
	// Pause immediately before the user row lock, commit a demotion, then resume.
	trace := &customerLockTracer{reached: make(chan uint32, 1), release: make(chan struct{})}
	tracedConfig, e := pgxpool.ParseConfig(db.URL())
	if e != nil {
		t.Fatal(e)
	}
	tracedConfig.ConnConfig.Tracer = trace
	tracedPool, e := pgxpool.NewWithConfig(ctx, tracedConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer tracedPool.Close()
	defer trace.unblock()
	tracedHandler, e := NewHandlerWithSessions(tracedPool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	raceDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("PATCH", "/api/v1/vendors/"+id, strings.NewReader(`{"name":"should-not-write"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(admin)
		w := httptest.NewRecorder()
		tracedHandler.ServeHTTP(w, r)
		raceDone <- w
	}()
	var pid uint32
	select {
	case pid = <-trace.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("admin lock not reached")
	}
	demote, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer demote.Rollback(ctx)
	if _, e = demote.Exec(ctx, `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	trace.unblock()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if e = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')`, pid).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("admin lock did not wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = demote.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if w := <-raceDone; w.Code != 403 {
		t.Fatalf("demotion: %d %s", w.Code, w.Body)
	}
	if got := snapshot(`SELECT name FROM vendors WHERE vendor_id='10000000-0000-0000-0000-000000000001'`); got != "Updated" {
		t.Fatalf("demotion wrote %s", got)
	}
	if _, e = pool.Exec(ctx, `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}

	// Compare full responses with FastAPI using a separate disposable PostgreSQL database.
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	parityCases := []string{`{}`, `{"name":"Parity","contact_email":"Test@EXAMPLE.COM","payment_terms_days":"12","active":"no"}`, `{"notes":null,"contact_name":"Alice"}`, `{"default_currency":"US","payment_terms_days":366,"active":"bad"}`, `{"contact_email":"bad"}`, `{"payment_terms_days":1.5}`, `{"unknown":42}`}
	for _, body := range parityCases {
		seed := `UPDATE vendors SET name='One',contact_email=null,contact_name=null,default_currency='IDR',payment_terms_days=30,tax_id=null,notes=null,active=true,created_at='2020-01-01' WHERE vendor_id='10000000-0000-0000-0000-000000000001'`
		if _, e = pool.Exec(ctx, seed); e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.vendors import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['VENDOR_DB'].replace('postgres://','postgresql+psycopg://',1))
with engine.begin() as c:
 c.execute(text("INSERT INTO vendors(vendor_id,name,default_currency,payment_terms_days,active,created_at) VALUES ('10000000-0000-0000-0000-000000000001','One','IDR',30,true,'2020-01-01') ON CONFLICT (vendor_id) DO UPDATE SET name='One',contact_email=null,contact_name=null,default_currency='IDR',payment_terms_days=30,tax_id=null,notes=null,active=true,created_at='2020-01-01'"))
app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as client:
 r=client.patch('/api/v1/vendors/10000000-0000-0000-0000-000000000001',content=os.environ['CASE_BODY'],headers={'content-type':'application/json'}); print(json.dumps([r.status_code,r.json()]))
`)
		cmd.Dir = "../../../backend"
		cmd.Env = append(os.Environ(), "VENDOR_DB="+pydb.URL(), "CASE_BODY="+body)
		py, e := cmd.Output()
		if e != nil {
			t.Fatalf("Python: %v %s", e, py)
		}
		var expected []any
		if e = json.Unmarshal(py, &expected); e != nil {
			t.Fatal(e)
		}
		w := call(id, body, "application/json", admin)
		var got any
		if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatal(e)
		}
		// Python echoes invalid input; the Go endpoint deliberately redacts it.
		if w.Code == 422 {
			for _, d := range expected[1].(map[string]any)["detail"].([]any) {
				d.(map[string]any)["input"] = "[REDACTED]"
			}
		}
		if float64(w.Code) != expected[0] || !reflect.DeepEqual(got, expected[1]) {
			t.Fatalf("body %s Go %d %v Python %v", body, w.Code, got, expected)
		}
	}
	if _, e = pool.Exec(ctx, `UPDATE vendors SET name='Updated' WHERE vendor_id='10000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}

	_, e = pool.Exec(ctx, `CREATE FUNCTION reject_vendor_update_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private commit detail'; END $$; CREATE CONSTRAINT TRIGGER reject_vendor_update_commit AFTER UPDATE ON vendors DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_vendor_update_commit()`)
	if e != nil {
		t.Fatal(e)
	}
	w := call(id, `{"name":"Changed"}`, "application/json", admin)
	if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("commit: %d %s", w.Code, w.Body)
	}
	if got := snapshot(`SELECT name FROM vendors WHERE vendor_id='10000000-0000-0000-0000-000000000001'`); got != "Updated" {
		t.Fatal("commit rollback")
	}
}
