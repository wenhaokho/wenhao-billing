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

func TestVendorReadPostgres(t *testing.T) {
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
	_, err = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','user'); INSERT INTO vendors(vendor_id,name,default_currency,payment_terms_days,active,created_at) VALUES ('10000000-0000-0000-0000-000000000001','Zebra','USD',10,true,'2025-01-02 03:04:05.1234'),('10000000-0000-0000-0000-000000000002','Alpha','IDR',30,false,'2025-01-02 03:04:05'); UPDATE vendors SET contact_email='vendor@example.com',contact_name='Contact',tax_id='TAX',notes='Notes' WHERE name='Zebra'`)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err = pool.QueryRow(context.Background(), `SELECT json_agg(row_to_json(v) ORDER BY vendor_id)::text FROM vendors v`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
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
	call := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("no-store %s", path)
		}
		return w
	}
	for _, path := range []string{"/api/v1/vendors", "/api/v1/vendors/10000000-0000-0000-0000-000000000001"} {
		for _, tc := range []struct {
			c    *http.Cookie
			code int
		}{{nil, 401}, {user, 403}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}, {token("00000000-0000-0000-0000-000000000001", -time.Hour), 401}, {token("00000000-0000-0000-0000-000000000003", time.Hour), 403}} {
			w := call(path, tc.c)
			if w.Code != tc.code {
				t.Fatalf("%s got %d %s want %d", path, w.Code, w.Body, tc.code)
			}
		}
	}
	for _, tc := range []struct {
		path        string
		code, count int
		first       string
	}{{"/api/v1/vendors", 200, 2, "Alpha"}, {"/api/v1/vendors?active=true", 200, 1, "Zebra"}, {"/api/v1/vendors?active=true&active=off", 200, 1, "Alpha"}, {"/api/v1/vendors?active=bad", 422, 0, ""}} {
		w := call(tc.path, admin)
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body)
		}
		if tc.code == 200 {
			var rows []map[string]any
			if json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != tc.count || rows[0]["name"] != tc.first {
				t.Fatalf("%s: %s", tc.path, w.Body)
			}
		}
	}
	for _, tc := range []struct {
		id   string
		code int
	}{{"10000000-0000-0000-0000-000000000001", 200}, {"10000000000000000000000000000001", 200}, {"%7B10000000-0000-0000-0000-000000000001%7D", 200}, {"urn:uuid:10000000-0000-0000-0000-000000000001", 200}, {"10000000_0000-0000-0000-000000000001", 422}, {"10000000-0000-0000-0000-000000000003", 404}} {
		w := call("/api/v1/vendors/"+tc.id, admin)
		if w.Code != tc.code {
			t.Errorf("%s: %d %s", tc.id, w.Code, w.Body)
		}
	}
	// Compare the actual FastAPI router against identical rows in another disposable database.
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pyPool, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pyPool.Close()
	_, e = pyPool.Exec(context.Background(), `INSERT INTO vendors(vendor_id,name,default_currency,payment_terms_days,active,created_at) VALUES ('10000000-0000-0000-0000-000000000001','Zebra','USD',10,true,'2025-01-02 03:04:05.1234'),('10000000-0000-0000-0000-000000000002','Alpha','IDR',30,false,'2025-01-02 03:04:05'); UPDATE vendors SET contact_email='vendor@example.com',contact_name='Contact',tax_id='TAX',notes='Notes' WHERE name='Zebra'`)
	if e != nil {
		t.Fatal(e)
	}
	paths := []string{"/api/v1/vendors", "/api/v1/vendors?active=YES", "/api/v1/vendors?active=1&active=off", "/api/v1/vendors?active=bad", "/api/v1/vendors/10000000-0000-0000-0000-000000000001", "/api/v1/vendors/10000000-0000-0000-0000-000000000003", "/api/v1/vendors/10000000_0000-0000-0000-000000000001"}
	command := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.vendors import router
from app.api.deps import current_admin
from app.db.session import get_db
engine = create_engine(os.environ["VENDOR_DB"].replace("postgres://", "postgresql+psycopg://", 1))
app = FastAPI(); app.include_router(router, prefix="/api/v1")
app.dependency_overrides[current_admin] = lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db] = session
with TestClient(app) as client:
    print(json.dumps([[r.status_code, r.json()] for r in (client.get(p) for p in json.loads(os.environ["VENDOR_PATHS"]))]))
`)
	encoded, _ := json.Marshal(paths)
	command.Dir = "../../../backend"
	command.Env = append(os.Environ(), "VENDOR_DB="+pydb.URL(), "VENDOR_PATHS="+string(encoded))
	output, e := command.Output()
	if e != nil {
		t.Fatalf("Python HTTP: %v %s", e, output)
	}
	var expected [][]any
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e)
	}
	for i, path := range paths {
		w := call(path, admin)
		var body any
		if e = json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(body, expected[i][1]) {
			t.Errorf("Python parity %s: Go %d %v Python %v", path, w.Code, body, expected[i])
		}
	}
	// Shared email parsing must reject the same values through account write callers.
	for _, email := range []string{"foo@example.com (Comment)", "foo@example.com\n"} {
		body, _ := json.Marshal(map[string]any{"email": email, "password": "password"})
		for _, endpoint := range []struct{ method, path string }{{"POST", "/api/v1/users"}, {"PATCH", "/api/v1/users/me"}} {
			request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(admin)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != 422 {
				t.Errorf("%s accepted invalid email: %d %s", endpoint.path, response.Code, response.Body)
			}
		}
	}
	profileBody, _ := json.Marshal(map[string]any{"email": `"John \"Doe\"" <a@example.com>`})
	profileRequest := httptest.NewRequest("PATCH", "/api/v1/users/me", strings.NewReader(string(profileBody)))
	profileRequest.Header.Set("Content-Type", "application/json")
	profileRequest.AddCookie(admin)
	profileResponse := httptest.NewRecorder()
	h.ServeHTTP(profileResponse, profileRequest)
	if profileResponse.Code != 200 || !strings.Contains(profileResponse.Body.String(), `"email":"a@example.com"`) {
		t.Errorf("profile display name: %d %s", profileResponse.Code, profileResponse.Body)
	}
	// Legacy rows can predate request validation. Compare FastAPI's response validation.
	legacySQL := []string{
		`UPDATE vendors SET contact_email=' Test@EXAMPLE.COM ' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='Name <Test@EXAMPLE.COM>' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='"John \"Doe\"" <a@example.com>' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='"Name <a@example.com>' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='"John \"Doe\"" <a@example.com> (Comment)' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='foo@example.com (Comment)' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email=E'foo@example.com\n' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email=E'foo@example.com\r' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='Name (Comment) <foo@example.com>' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='foo(comment)@example.com' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email=E'\tfoo@example.com\t' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='private-invalid-email' WHERE name='Zebra'`,
		`UPDATE vendors SET contact_email='vendor@example.com', name='' WHERE name='Zebra'`,
		`UPDATE vendors SET name='Zebra', default_currency='US' WHERE name=''`,
		`UPDATE vendors SET default_currency='USD', payment_terms_days=-1 WHERE name='Zebra'`,
		`UPDATE vendors SET payment_terms_days=366 WHERE name='Zebra'`,
	}
	encodedLegacy, _ := json.Marshal(legacySQL)
	legacy := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.vendors import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['VENDOR_DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
results=[]
with TestClient(app,raise_server_exceptions=False) as client:
    for sql in json.loads(os.environ['VENDOR_SQL']):
        with engine.begin() as conn: conn.execute(text(sql))
        result=[]
        for path in ['/api/v1/vendors/10000000-0000-0000-0000-000000000001','/api/v1/vendors']:
            r=client.get(path); result.append([r.status_code,r.json() if r.status_code==200 else None])
        results.append(result)
print(json.dumps(results))
`)
	legacy.Dir = "../../../backend"
	legacy.Env = append(os.Environ(), "VENDOR_DB="+pydb.URL(), "VENDOR_SQL="+string(encodedLegacy))
	output, e = legacy.Output()
	if e != nil {
		t.Fatalf("Python legacy HTTP: %v", e)
	}
	var legacyExpected [][][]any
	if e = json.Unmarshal(output, &legacyExpected); e != nil {
		t.Fatal(e)
	}
	for i, sql := range legacySQL {
		if _, e = pool.Exec(context.Background(), sql); e != nil {
			t.Fatal(e)
		}
		for j, path := range []string{"/api/v1/vendors/10000000-0000-0000-0000-000000000001", "/api/v1/vendors"} {
			w := call(path, admin)
			if float64(w.Code) != legacyExpected[i][j][0] {
				t.Errorf("legacy %d %s: status %d vs Python %v", i, path, w.Code, legacyExpected[i][j][0])
				continue
			}
			if w.Code == 200 {
				var body any
				if e = json.Unmarshal(w.Body.Bytes(), &body); e != nil || !reflect.DeepEqual(body, legacyExpected[i][j][1]) {
					t.Errorf("legacy %d %s: body %s vs Python %v", i, path, w.Body, legacyExpected[i][j][1])
				}
			} else if strings.TrimSpace(w.Body.String()) != `{"detail":"internal server error"}` {
				t.Errorf("unexpected unsanitized error: %s", w.Body)
			}
		}
	}
	if _, e = pool.Exec(context.Background(), `UPDATE vendors SET payment_terms_days=10 WHERE name='Zebra'`); e != nil {
		t.Fatal(e)
	}
	var after string
	if err = pool.QueryRow(context.Background(), `SELECT json_agg(row_to_json(v) ORDER BY vendor_id)::text FROM vendors v`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("vendor rows changed: %s -> %s", before, after)
	}
	if _, err = pool.Exec(context.Background(), `UPDATE vendors SET created_at='infinity' WHERE name='Zebra'`); err != nil {
		t.Fatal(err)
	}
	if w := call("/api/v1/vendors/10000000-0000-0000-0000-000000000001", admin); w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
		t.Fatalf("vendor timestamp: %d %s", w.Code, w.Body)
	}
	_, err = pool.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	if w := call("/api/v1/vendors", admin); w.Code != 403 {
		t.Fatalf("demoted: %d", w.Code)
	}
}

func TestVendorReadOpenAPI(t *testing.T) {
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
	for _, path := range []string{"/api/v1/vendors", "/api/v1/vendors/{vendor_id}"} {
		op := paths[path].(map[string]any)["get"].(map[string]any)
		if op["security"] == nil {
			t.Error("missing security")
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
	listOp := paths["/api/v1/vendors"].(map[string]any)["get"].(map[string]any)
	listSchema := listOp["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if listSchema["type"] != "array" || listSchema["nullable"] == true {
		t.Errorf("list schema must be a non-null array: %v", listSchema)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemas["VendorOutputSchema"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"vendor_id", "name", "contact_email", "contact_name", "default_currency", "payment_terms_days", "tax_id", "notes", "active", "created_at"} {
		if props[name] == nil {
			t.Errorf("missing %s", name)
		}
	}
}
