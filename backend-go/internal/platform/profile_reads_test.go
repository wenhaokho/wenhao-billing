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

func TestProfileReadsPostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	d := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pd := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, e := pgxpool.New(context.Background(), d.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	pp, e := pgxpool.New(context.Background(), pd.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	for _, pool := range []*pgxpool.Pool{p, pp} {
		_, e = pool.Exec(context.Background(), `DELETE FROM business_profile; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','user')`)
		if e != nil {
			t.Fatal(e)
		}
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	cookie := func(id string) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	call := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("no-store")
		}
		return w
	}
	admin := cookie("00000000-0000-0000-0000-000000000001")
	for _, tc := range []struct {
		c    *http.Cookie
		code int
	}{{nil, 401}, {cookie("00000000-0000-0000-0000-000000000002"), 403}, {cookie("00000000-0000-0000-0000-000000000003"), 403}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}} {
		if w := call("/api/v1/business-profile", tc.c); w.Code != tc.code {
			t.Fatalf("auth %d %s", w.Code, w.Body)
		}
	}
	paths := []string{"/api/v1/business-profile", "/api/v1/business-profile/payment-accounts"}
	compare := func(missing bool) {
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
 print(json.dumps([[r.status_code,r.json()] for p in json.loads(os.environ['PATHS']) for r in [c.get(p)]]))`)
		cmd.Dir = "../../../backend"
		b, _ := json.Marshal(paths)
		cmd.Env = append(os.Environ(), "DB="+pd.URL(), "PATHS="+string(b))
		out, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		var expected [][]any
		if e = json.Unmarshal(out, &expected); e != nil {
			t.Fatal(e)
		}
		for i, path := range paths {
			w := call(path, admin)
			var got any
			if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
				t.Fatalf("%s %d %s", path, w.Code, w.Body)
			}
			if missing && i == 0 {
				delete(got.(map[string]any), "updated_at")
				delete(expected[i][1].(map[string]any), "updated_at")
			}
			if w.Code != int(expected[i][0].(float64)) || !reflect.DeepEqual(got, expected[i][1]) {
				t.Fatalf("%s got %d %#v want %#v", path, w.Code, got, expected[i])
			}
		}
	}
	compare(true)
	for _, pool := range []*pgxpool.Pool{p, pp} {
		var n int
		if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM business_profile`).Scan(&n); e != nil || n != 0 {
			t.Fatalf("GET wrote profile %d %v", n, e)
		}
		_, e = pool.Exec(context.Background(), `INSERT INTO business_profile(id,name,address,contact_email,contact_phone,invoice_title,invoice_summary,logo_url,default_notes,npwp,npwp_name,updated_at) VALUES(1,'Name','Address','x@example.com','123','Title','Summary','logo','notes','npwp','taxpayer','2024-01-01'); INSERT INTO payment_account(id,business_profile_id,currency,instructions,is_default,updated_at) VALUES(1,1,'USD','US',false,'2024-01-01'),(2,1,'IDR','ID',true,'2024-01-02')`)
		if e != nil {
			t.Fatal(e)
		}
	}
	snapshot := func(pool *pgxpool.Pool) string {
		var v string
		if e := pool.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(b) ORDER BY id) FROM business_profile b),(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM payment_account a))::text`).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	before, beforePy := snapshot(p), snapshot(pp)
	compare(false)
	if snapshot(p) != before || snapshot(pp) != beforePy {
		t.Fatal("GET mutated data")
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if json.Unmarshal(spec.Body.Bytes(), &doc) != nil {
		t.Fatal("invalid OpenAPI")
	}
	pathsSchema := doc["paths"].(map[string]any)
	for _, path := range paths {
		operation := pathsSchema[path].(map[string]any)["get"].(map[string]any)
		if len(operation["security"].([]any)) == 0 {
			t.Fatal("missing security")
		}
		if operation["responses"].(map[string]any)["200"] == nil {
			t.Fatal("missing response")
		}
	}
	for _, pool := range []*pgxpool.Pool{p, pp} {
		var n int
		if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_account`).Scan(&n); e != nil || n != 2 {
			t.Fatalf("accounts changed %d %v", n, e)
		}
	}
	for _, value := range []string{"infinity", "-infinity", "10000-01-01", "0001-01-01 BC"} {
		if _, e = p.Exec(context.Background(), `UPDATE business_profile SET updated_at=$1::timestamp`, value); e != nil {
			t.Fatal(e)
		}
		w := call("/api/v1/business-profile", admin)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
			t.Fatalf("timestamp %s returned %d %s", value, w.Code, w.Body)
		}
		if _, e = p.Exec(context.Background(), `UPDATE payment_account SET updated_at=$1::timestamp WHERE id=1`, value); e != nil {
			t.Fatal(e)
		}
		w = call("/api/v1/business-profile/payment-accounts", admin)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
			t.Fatalf("account timestamp %s returned %d %s", value, w.Code, w.Body)
		}
		if _, e = p.Exec(context.Background(), `UPDATE payment_account SET updated_at='2024-01-01'`); e != nil {
			t.Fatal(e)
		}
	}

	// A database trigger simulates an invalid returned timestamp on write. The
	// response must fail before commit, preserving the original profile.
	if _, e = p.Exec(context.Background(), `UPDATE business_profile SET updated_at='2024-01-01'; CREATE FUNCTION invalid_profile_stamp() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.updated_at := 'infinity'; RETURN NEW; END $$; CREATE TRIGGER invalid_profile_stamp BEFORE UPDATE ON business_profile FOR EACH ROW EXECUTE FUNCTION invalid_profile_stamp()`); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("PUT", "/api/v1/business-profile", strings.NewReader(`{"name":"Changed"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(admin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
		t.Fatalf("profile write timestamp: %d %s", w.Code, w.Body)
	}
	var name string
	if e = p.QueryRow(context.Background(), `SELECT name FROM business_profile WHERE id=1`).Scan(&name); e != nil || name != "Name" {
		t.Fatalf("profile write committed: %s %v", name, e)
	}

}
