package platform

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestFXWritePostgres(t *testing.T) {
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
	call := func(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/api/v1/accounting/fx-rates"
	body := `{"from_currency":"usd","to_currency":"idr","rate":"15321.12345678","as_of_date":"2025-01-01"}`
	for _, tc := range []struct {
		c    *http.Cookie
		code int
	}{{nil, 401}, {user, 403}, {admin, 201}, {admin, 409}} {
		w := call("POST", path, body, tc.c)
		if w.Code != tc.code {
			t.Fatalf("POST got %d %s want %d", w.Code, w.Body, tc.code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache")
		}
	}
	w := call("GET", path+"?from_currency=USD", "", admin)
	var rows []fxOutput
	if json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].Rate != "15321.12345678" || rows[0].FromCurrency != "USD" || rows[0].Source != "MANUAL" {
		t.Fatalf("read %d %s", w.Code, w.Body)
	}
	for _, b := range []string{`{}`, `{"from_currency":"USD","to_currency":"IDR","rate":"NaN","as_of_date":"2025-01-02"}`, `{"from_currency":"USD","to_currency":"IDR","rate":"0.000000001","as_of_date":"2025-01-02"}`} {
		if w = call("POST", path, b, admin); w.Code != 422 {
			t.Fatalf("invalid %s: %d %s", b, w.Code, w.Body)
		}
	}
	// Media type, malformed input and oversized bodies cannot cause a write or echo secrets.
	for _, tc := range []struct {
		content, body string
		status        int
	}{{"text/plain", `{"secret":"top-secret"}`, 422}, {"application/json", `{"secret":"top-secret"`, 422}, {"application/json", strings.Repeat("x", (1<<20)+1), 413}} {
		r := httptest.NewRequest("POST", path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.content)
		r.AddCookie(admin)
		result := httptest.NewRecorder()
		h.ServeHTTP(result, r)
		if result.Code != tc.status || strings.Contains(result.Body.String(), "top-secret") {
			t.Fatalf("request guard %d %s", result.Code, result.Body)
		}
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if json.Unmarshal(spec.Body.Bytes(), &doc) != nil {
		t.Fatal("invalid OpenAPI")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemas["FxCreateSchema"].(map[string]any)["properties"].(map[string]any)
	rate := props["rate"].(map[string]any)
	variants, ok := rate["anyOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("invalid rate schema: %v", rate)
	}
	number := variants[0].(map[string]any)
	str := variants[1].(map[string]any)
	if number["type"] != "number" || number["exclusiveMinimum"] != float64(0) || str["type"] != "string" {
		t.Fatalf("invalid rate variants: %v", variants)
	}
	paths := doc["paths"].(map[string]any)
	responses := paths[path+"/{rate_id}"].(map[string]any)["delete"].(map[string]any)["responses"].(map[string]any)
	content := responses["500"].(map[string]any)["content"].(map[string]any)
	if content["application/json"] == nil || content["text/plain"] == nil {
		t.Fatalf("invalid delete 500 schema %v", content)
	}
	for _, route := range []struct{ path, method string }{{path, "post"}, {path + "/{rate_id}", "delete"}} {
		entry := paths[route.path].(map[string]any)[route.method].(map[string]any)
		if entry["security"] == nil || entry["responses"] == nil {
			t.Fatalf("incomplete OpenAPI %s", route.path)
		}
	}
	// A demotion committed while the write waits on the persisted-user lock must prevent the insert.
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call("POST", path, strings.Replace(body, "2025-01-01", "2025-01-02", 1), admin) }()
	select {
	case <-done:
		t.Fatal("write did not wait for admin lock")
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Code != 403 {
			t.Fatalf("demotion %d %s", result.Code, result.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write stayed blocked")
	}
	if _, err = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM fx_rates WHERE as_of_date='2025-01-02'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("demotion wrote %d %v", count, err)
	}
	for _, suffix := range []string{"+999", "999.0", "9_9_9"} {
		if w = call("DELETE", path+"/"+suffix, "", admin); w.Code != 404 {
			t.Fatalf("valid integer %s: %d %s", suffix, w.Code, w.Body)
		}
	}
	id := rows[0].RateID
	pathID := path + "/" + fmt.Sprint(id)
	if w = call("DELETE", pathID, "", admin); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	if w = call("DELETE", pathID, "", admin); w.Code != 404 {
		t.Fatalf("missing %d %s", w.Code, w.Body)
	}
	if w = call("DELETE", path+"/nope", "", admin); w.Code != 422 {
		t.Fatalf("invalid id %d %s", w.Code, w.Body)
	}
}

func TestFXCreateValidationDifferential(t *testing.T) {
	bodies := []string{`{}`, `{"from_currency":"USD","to_currency":"IDR","rate":0,"as_of_date":"2020-01-01"}`, `{"from_currency":"USD","to_currency":"IDR","rate":"0.000000001","as_of_date":"2020-01-01"}`, `{"from_currency":"US","to_currency":"IDR","rate":"1","as_of_date":"bad"}`, `{"from_currency":null,"to_currency":"IDR","rate":true,"as_of_date":null}`, `{"from_currency":"USD","to_currency":"IDR","rate":"100000000000","as_of_date":"2020-01-01"}`, `{"from_currency":"USD","to_currency":"IDR","rate":"NaN","as_of_date":"2020-01-01"}`}
	b, _ := json.Marshal(bodies)
	cmd := exec.Command("python", "-c", `import json,os
from app.schemas.fx import FxRateCreate
from pydantic import ValidationError
out=[]
for body in json.loads(os.environ['FX_BODIES']):
 try: FxRateCreate.model_validate_json(body);out.append([])
 except ValidationError as e: out.append([[x['type'],x['loc'][0]] for x in e.errors()])
print(json.dumps(out))`)
	cmd.Env = append(os.Environ(), "FX_BODIES="+string(b))
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var expected [][][]string
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	for i, body := range bodies {
		_, errs := parseFXCreate([]byte(body))
		got := [][]string{}
		for _, e := range errs {
			got = append(got, []string{e.Type, e.Loc[1].(string)})
		}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("%s Go %v Python %v", body, got, expected[i])
		}
	}
}

func TestFXWriteHTTPDifferential(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	conf := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	goDB := pgtestdb.Custom(t, conf, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pyDB := pgtestdb.Custom(t, conf, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), goDB.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	other, e := pgxpool.New(context.Background(), pyDB.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	for _, p := range []*pgxpool.Pool{pool, other} {
		_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`)
		if e != nil {
			t.Fatal(e)
		}
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
	bodies := []string{`{}`, `{"from_currency":"usd","to_currency":"idr","rate":"1.2500","as_of_date":"2020-01-01"}`, `{"from_currency":"usd","to_currency":"idr","rate":"1.2500","as_of_date":"2020-01-01"}`, `{"from_currency":"USD","to_currency":"IDR","rate":0,"as_of_date":"2020-01-01"}`, `{"from_currency":"US","to_currency":"IDR","rate":"NaN","as_of_date":"bad"}`, `{"from_currency":"GBP","to_currency":"IDR","rate":"1","as_of_date":253402300800000}`, `{"from_currency":"GBP","to_currency":"IDR","rate":"1","as_of_date":-62135683200000}`, `{"from_currency":"GBP","to_currency":"IDR","rate":"1","as_of_date":-62198755200000}`}
	b, _ := json.Marshal(bodies)
	cmd := exec.Command("python", "-c", `import json,os,base64,time
from itsdangerous import TimestampSigner
from starlette.middleware.sessions import SessionMiddleware
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.accounting import router
from app.db.session import get_db
engine=create_engine(os.environ['FX_DATABASE'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI();app.add_middleware(SessionMiddleware,secret_key='secret');app.include_router(router,prefix='/api/v1')
def db():
 with Session(engine) as s: yield s
app.dependency_overrides[get_db]=db
session=base64.b64encode(json.dumps({'user_id':'00000000-0000-0000-0000-000000000001','exp':int(time.time())+3600}).encode())
cookie=TimestampSigner('secret').sign(session).decode()
with TestClient(app,raise_server_exceptions=False) as c:
 c.cookies.set('session',cookie);out=[]
 for body in json.loads(os.environ['FX_BODIES']):
  r=c.post('/api/v1/accounting/fx-rates',content=body,headers={'content-type':'application/json'});out.append([r.status_code,r.json()])
 for value in ['999.0','9_9_9','999999999999999999999999999999999','2','2']:
  r=c.delete('/api/v1/accounting/fx-rates/'+value);out.append([r.status_code,(r.json() if r.headers.get('content-type','').startswith('application/json') else r.text) if r.content else None])
 print(json.dumps(out))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "FX_DATABASE="+pyDB.URL(), "FX_BODIES="+string(b))
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Python %v %s %s", e, out, e.(*exec.ExitError).Stderr)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	for i, body := range bodies {
		r := httptest.NewRequest("POST", "/api/v1/accounting/fx-rates", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: v})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		want := expected[i][1]
		for _, x := range []any{got, want} {
			if obj, ok := x.(map[string]any); ok {
				delete(obj, "created_at")
				if details, ok := obj["detail"].([]any); ok {
					for _, d := range details {
						d.(map[string]any)["input"] = "[REDACTED]"
					}
				}
			}
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(got, want) {
			t.Errorf("%s Go %d %v Python %v", body, w.Code, got, expected[i])
		}
	}
	for _, p := range []*pgxpool.Pool{pool, other} {
		var count int
		if e = p.QueryRow(context.Background(), `SELECT count(*) FROM fx_rates WHERE from_currency='GBP'`).Scan(&count); e != nil || count != 0 {
			t.Fatalf("invalid date wrote %d %v", count, e)
		}
	}
	for j, id := range []string{"999.0", "9_9_9", "999999999999999999999999999999999", "2", "2"} {
		r := httptest.NewRequest("DELETE", "/api/v1/accounting/fx-rates/"+id, nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: v})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		if w.Body.Len() > 0 {
			if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
				got = w.Body.String()
			}
		}
		want := expected[len(bodies)+j]
		if float64(w.Code) != want[0] || !reflect.DeepEqual(got, want[1]) {
			t.Errorf("DELETE %s Go %d %v Python %v", id, w.Code, got, want)
		}
	}

}
