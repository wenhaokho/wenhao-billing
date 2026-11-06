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

func TestCustomerReadRequiresSession(t *testing.T) {
	h, e := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"/api/v1/customers", "/api/v1/customers/10000000-0000-0000-0000-000000000001"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 401 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestCustomerReadPostgres(t *testing.T) {
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
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com','secret-hash','admin'),('00000000-0000-0000-0000-000000000002','user@example.com','secret-hash','user'); INSERT INTO customers(customer_id,name,active,matching_aliases,quotation_cc_emails,default_wht_rate) VALUES ('10000000-0000-0000-0000-000000000001','Zebra',true,'["alias"]','["cc@example.com"]',2.5000),('10000000-0000-0000-0000-000000000002','Alpha',false,'[]','[]',NULL)`)
	if e != nil {
		t.Fatal(e)
	}
	var initial string
	if e = pool.QueryRow(context.Background(), `SELECT jsonb_build_object('customers',(SELECT jsonb_agg(to_jsonb(c) ORDER BY customer_id) FROM customers c),'users',(SELECT jsonb_agg(to_jsonb(u) ORDER BY user_id) FROM users u))::text`).Scan(&initial); e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token := func(id string, ttl time.Duration) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: ttl}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	admin := token("00000000-0000-0000-0000-000000000001", time.Hour)
	user := token("00000000-0000-0000-0000-000000000002", time.Hour)
	expired := token("00000000-0000-0000-0000-000000000001", -time.Hour)
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
	for _, path := range []string{"/api/v1/customers", "/api/v1/customers/10000000-0000-0000-0000-000000000001"} {
		for _, tc := range []struct {
			c      *http.Cookie
			status int
		}{{nil, 401}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}, {expired, 401}, {user, 403}} {
			w := call(path, tc.c)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "secret-hash") || strings.Contains(w.Body.String(), "Zebra") {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
		}
	}
	for _, tc := range []struct{ path, names string }{{"/api/v1/customers", "Alpha,Zebra"}, {"/api/v1/customers?active=yes", "Zebra"}, {"/api/v1/customers?active=false", "Alpha"}, {"/api/v1/customers?active=true&active=0", "Alpha"}} {
		w := call(tc.path, admin)
		var rows []map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		names := []string{}
		for _, r := range rows {
			names = append(names, r["name"].(string))
		}
		if strings.Join(names, ",") != tc.names {
			t.Errorf("%s: %v", tc.path, names)
		}
	}
	w := call("/api/v1/customers/10000000-0000-0000-0000-000000000001", admin)
	var row map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &row) != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if row["default_wht_rate"] != "2.5000" || row["contact_email"] != nil || len(row["matching_aliases"].([]any)) != 1 || len(row["billing_cc_emails"].([]any)) != 0 {
		t.Errorf("%s", w.Body.String())
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"/api/v1/customers/bad", 422}, {"/api/v1/customers/10000000-0000-0000-0000-000000000099", 404}, {"/api/v1/customers?active=maybe", 422}} {
		w := call(tc.path, admin)
		if w.Code != tc.status {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	paths := []string{"/api/v1/customers", "/api/v1/customers?active=true&active=0", "/api/v1/customers?active=maybe", "/api/v1/customers/bad", "/api/v1/customers/10000000-0000-0000-0000-000000000099", "/api/v1/customers/10000000-0000-0000-0000-000000000001"}
	py := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.customers import router
from app.api.deps import current_admin
from app.db.session import get_db
engine = create_engine(os.environ["CUSTOMER_TEST_DATABASE_URL"].replace("postgres://", "postgresql+psycopg://", 1))
app = FastAPI()
app.include_router(router, prefix="/api/v1")
app.dependency_overrides[current_admin] = lambda: object()
def db_session():
    with Session(engine) as db:
        yield db
app.dependency_overrides[get_db] = db_session
with TestClient(app) as client:
    print(json.dumps([[r.status_code, r.json()] for r in (client.get(p) for p in json.loads(os.environ["CUSTOMER_TEST_PATHS"]))]))
`)
	py.Dir = "../../../backend"
	pathJSON, _ := json.Marshal(paths)
	py.Env = append(os.Environ(), "CUSTOMER_TEST_DATABASE_URL="+db.URL(), "CUSTOMER_TEST_PATHS="+string(pathJSON))
	output, e := py.Output()
	if e != nil {
		t.Fatalf("Python HTTP: %v %s", e, output)
	}
	var expected [][]any
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("%v: %s", e, output)
	}
	for i, path := range paths {
		w := call(path, admin)
		var body any
		if e = json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(body, expected[i][1]) {
			t.Errorf("Python parity %s: Go %d %s Python %v", path, w.Code, w.Body.String(), expected[i])
		}
	}
	// Compare the public UUID paths against FastAPI using a missing ID so write
	// routes cannot change either database. Reset-password redacts validation input.
	forms := []string{"{xyz}", "urn:uuid:xyz", "10000000x0000x0000x0000x000000000099", "{10000000-0000-0000-0000-000000000099}", "urn:uuid:10000000-0000-0000-0000-000000000099", "10000000000000000000000000000099"}
	type request struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Body   string `json:"body"`
	}
	requests := []request{}
	for _, id := range forms {
		requests = append(requests, request{"GET", "/api/v1/customers/" + id, ""}, request{"GET", "/api/v1/items/" + id, ""}, request{"PATCH", "/api/v1/items/" + id, `{}`}, request{"DELETE", "/api/v1/items/" + id, ""}, request{"POST", "/api/v1/users/" + id + "/reset-password", `{"password":"long-enough"}`})
	}
	requests = append(requests, request{"PATCH", "/api/v1/items/bad", `{`}, request{"PATCH", "/api/v1/items/bad", `{"name":123,"is_sold":"maybe"}`}, request{"PATCH", "/api/v1/items/{xyz}", `{}`})
	pyPaths, _ := json.Marshal(requests)
	pyUUID := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.customers import router as customers
from app.api.v1.routers.items import router as items
from app.api.v1.routers.users import router as users
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ["CUSTOMER_TEST_DATABASE_URL"].replace("postgres://","postgresql+psycopg://",1))
app=FastAPI()
for router in (customers,items,users): app.include_router(router,prefix="/api/v1")
app.dependency_overrides[current_admin]=lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as client:
    results=[]
    for req in json.loads(os.environ["CUSTOMER_TEST_PATHS"]):
        r=client.request(req["method"],req["path"],content=req["body"],headers={"content-type":"application/json"})
        results.append([r.status_code,r.json()])
    print(json.dumps(results))
`)
	pyUUID.Dir = "../../../backend"
	pyUUID.Env = append(os.Environ(), "CUSTOMER_TEST_DATABASE_URL="+db.URL(), "CUSTOMER_TEST_PATHS="+string(pyPaths))
	uuidOut, e := pyUUID.Output()
	if e != nil {
		t.Fatalf("Python UUID HTTP: %v", e)
	}
	var uuidExpected [][]any
	if e = json.Unmarshal(uuidOut, &uuidExpected); e != nil {
		t.Fatal(e)
	}
	for i, req := range requests {
		r := httptest.NewRequest(req.Method, req.Path, strings.NewReader(req.Body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(admin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatalf("%s %s: %d %s", req.Method, req.Path, w.Code, w.Body.String())
		}
		want := uuidExpected[i][1]
		if req.Method == "POST" && uuidExpected[i][0] == float64(422) {
			want.(map[string]any)["detail"].([]any)[0].(map[string]any)["input"] = "[REDACTED]"
		}
		if float64(w.Code) != uuidExpected[i][0] || !reflect.DeepEqual(got, want) {
			t.Errorf("%s %s: Go %d %s Python %v", req.Method, req.Path, w.Code, w.Body.String(), uuidExpected[i])
		}
	}
	// Authorization remains ahead of body decoding, and the body limit applies
	// even when the path UUID is malformed.
	for _, tc := range []struct {
		cookie *http.Cookie
		body   string
		status int
	}{{nil, "{", 401}, {user, "{", 403}, {admin, strings.Repeat("x", (1<<20)+1), 413}} {
		r := httptest.NewRequest("PATCH", "/api/v1/items/bad", strings.NewReader(tc.body))
		if tc.cookie != nil {
			r.AddCookie(tc.cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("PATCH guard: got %d want %d", w.Code, tc.status)
		}
	}
	var final string
	if e = pool.QueryRow(context.Background(), `SELECT jsonb_build_object('customers',(SELECT jsonb_agg(to_jsonb(c) ORDER BY customer_id) FROM customers c),'users',(SELECT jsonb_agg(to_jsonb(u) ORDER BY user_id) FROM users u))::text`).Scan(&final); e != nil || final != initial {
		t.Fatalf("database changed: %v", e)
	}
	var count int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM customers WHERE (name='Alpha' AND active=false) OR (name='Zebra' AND active=true AND default_wht_rate=2.5000)`).Scan(&count); e != nil || count != 2 {
		t.Fatalf("reads changed database: %d %v", count, e)
	}
	_, e = pool.Exec(context.Background(), `DELETE FROM users WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/customers", admin); w.Code != 403 {
		t.Errorf("deleted user: %d", w.Code)
	}
}

func TestCustomerReadOpenAPI(t *testing.T) {
	h, e := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	paths := doc["paths"].(map[string]any)
	for _, path := range []string{"/api/v1/customers", "/api/v1/customers/{customer_id}"} {
		op := paths[path].(map[string]any)["get"].(map[string]any)
		if op["security"] == nil {
			t.Errorf("%s security missing", path)
		}
		responses := op["responses"].(map[string]any)
		for _, status := range []string{"200", "401", "403", "422", "500"} {
			if responses[status] == nil {
				t.Errorf("%s missing %s", path, status)
			}
		}
		if strings.Contains(path, "{") && responses["404"] == nil {
			t.Error("missing 404")
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemas["CustomerOutputSchema"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"customer_id", "matching_aliases", "quotation_cc_emails", "billing_cc_emails", "default_wht_rate"} {
		if props[name] == nil {
			t.Errorf("missing %s", name)
		}
	}
}

func TestCustomerArrayOpenAPINonNullable(t *testing.T) {
	h, e := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	props := doc["components"].(map[string]any)["schemas"].(map[string]any)["CustomerOutputSchema"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"matching_aliases", "quotation_cc_emails", "billing_cc_emails"} {
		p := props[name].(map[string]any)
		if p["type"] != "array" {
			t.Errorf("%s: %v", name, p)
		}
	}
}
