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

func TestAccountReads(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(context.Background(), `DELETE FROM chart_of_accounts; INSERT INTO chart_of_accounts(account_id,code,name,type,active) VALUES (101,'200','Second','EXPENSE',false),(102,'100','First','ASSET',true); INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user')`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
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
	deleted := token("00000000-0000-0000-0000-000000000003")
	call := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("missing no-store")
		}
		return w
	}
	for _, path := range []string{"/api/v1/accounting/chart-of-accounts", "/api/v1/accounting/chart-of-accounts/101"} {
		for _, tc := range []struct {
			c    *http.Cookie
			code int
		}{{nil, 401}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}, {deleted, 403}, {user, 403}} {
			w := call(path, tc.c)
			if w.Code != tc.code {
				t.Errorf("%s auth: %d %s", path, w.Code, w.Body.String())
			}
		}
	}
	paths := []string{"/api/v1/accounting/chart-of-accounts", "/api/v1/accounting/chart-of-accounts?active=true", "/api/v1/accounting/chart-of-accounts?active=false", "/api/v1/accounting/chart-of-accounts?active=YES", "/api/v1/accounting/chart-of-accounts?active=true&active=off", "/api/v1/accounting/chart-of-accounts?active=maybe", "/api/v1/accounting/chart-of-accounts/101", "/api/v1/accounting/chart-of-accounts/101.0", "/api/v1/accounting/chart-of-accounts/1_01", "/api/v1/accounting/chart-of-accounts/101.00", "/api/v1/accounting/chart-of-accounts/1_01.0", "/api/v1/accounting/chart-of-accounts/101.", "/api/v1/accounting/chart-of-accounts/101.0_0", "/api/v1/accounting/chart-of-accounts/1e2", "/api/v1/accounting/chart-of-accounts/101.1", "/api/v1/accounting/chart-of-accounts/1__01", "/api/v1/accounting/chart-of-accounts/999", "/api/v1/accounting/chart-of-accounts/nope", "/api/v1/accounting/chart-of-accounts/2147483648"}
	py := exec.Command("python", "-c", `import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.accounting import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['ACCOUNT_TEST_DATABASE_URL'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def db_session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=db_session
with TestClient(app,raise_server_exceptions=False) as client:
    print(json.dumps([[r.status_code, r.json() if r.headers.get('content-type','').startswith('application/json') else r.text] for r in (client.get(p) for p in json.loads(os.environ['ACCOUNT_TEST_PATHS']))]))`)
	pathJSON, _ := json.Marshal(paths)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "ACCOUNT_TEST_DATABASE_URL="+db.URL(), "ACCOUNT_TEST_PATHS="+string(pathJSON))
	output, err := py.Output()
	if err != nil {
		t.Fatalf("python: %v %s", err, output)
	}
	var expected [][]any
	if err = json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		w := call(path, admin)
		var body any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			body = w.Body.String()
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(body, expected[i][1]) {
			t.Errorf("%s Python=%v Go=%d %s", path, expected[i], w.Code, w.Body.String())
		}
	}
}
func TestAccountReadOpenAPI(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	for _, p := range []string{"/api/v1/accounting/chart-of-accounts", "/api/v1/accounting/chart-of-accounts/{account_id}"} {
		path, ok := paths[p].(map[string]any)
		if !ok {
			t.Errorf("missing %s", p)
			continue
		}
		op, ok := path["get"].(map[string]any)
		if !ok || op["security"] == nil {
			t.Errorf("missing secured GET %s", p)
			continue
		}
		responses := op["responses"].(map[string]any)
		for _, status := range []string{"401", "403", "422", "500"} {
			response, ok := responses[status].(map[string]any)
			if !ok {
				t.Errorf("%s missing %s", p, status)
				continue
			}
			content := response["content"].(map[string]any)
			jsonContent, ok := content["application/json"].(map[string]any)
			if !ok {
				t.Errorf("%s %s missing JSON", p, status)
				continue
			}
			schema := jsonContent["schema"].(map[string]any)
			want := "#/components/schemas/AuthErrorBody"
			if status == "422" {
				want = "#/components/schemas/ValidationErrorBody"
			}
			if schema["$ref"] != want {
				t.Errorf("%s %s schema %v", p, status, schema)
			}
			if status == "500" {
				if _, ok := content["text/plain"]; !ok && strings.Contains(p, "{account_id}") {
					t.Errorf("missing text/plain 500")
				}
			}
		}
		if strings.Contains(p, "{account_id}") {
			if response, ok := responses["404"].(map[string]any); !ok {
				t.Errorf("missing 404")
			} else if schema := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any); schema["$ref"] != "#/components/schemas/AuthErrorBody" {
				t.Errorf("404 schema %v", schema)
			}
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	a, ok := schemas["AccountOutput"].(map[string]any)
	if !ok {
		t.Fatal("account schema missing")
	}
	props := a["properties"].(map[string]any)
	for _, k := range []string{"account_id", "code", "name", "type", "active"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
}
