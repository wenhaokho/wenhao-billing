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

func TestUsersHTTP(t *testing.T) {
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
	_, err = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role,display_name,password_reset_token) VALUES ('00000000-0000-0000-0000-000000000001','z@example.com','secret','admin','Old','reset'),('00000000-0000-0000-0000-000000000002','a@example.com','secret','user',NULL,'reset2')`)
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
	call := func(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
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
	for _, tc := range []struct {
		method, path string
		c            *http.Cookie
		code         int
	}{{"GET", "/api/v1/users", nil, 401}, {"GET", "/api/v1/users", &http.Cookie{Name: "session", Value: "invalid"}, 401}, {"GET", "/api/v1/users", user, 403}, {"GET", "/api/v1/users", deleted, 403}, {"PATCH", "/api/v1/users/me", nil, 401}, {"PATCH", "/api/v1/users/me", &http.Cookie{Name: "session", Value: "invalid"}, 401}, {"PATCH", "/api/v1/users/me", deleted, 403}, {"PATCH", "/api/v1/users/me", user, 403}} {
		w := call(tc.method, tc.path, `{}`, tc.c)
		if w.Code != tc.code {
			t.Errorf("auth: got %d %s", w.Code, w.Body.String())
		}
	}
	w := call("GET", "/api/v1/users", "", admin)
	if w.Code != 200 || strings.Index(w.Body.String(), "a@example.com") > strings.Index(w.Body.String(), "z@example.com") || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "reset") {
		t.Errorf("list: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		body     string
		code     int
		contains string
	}{{`{"email":"a@example.com"}`, 409, "email already in use"}, {`{"email":"bad"}`, 422, "valid email"}, {`{"display_name":""}`, 200, `"display_name":null`}, {`{"email":" New@EXAMPLE.COM ","display_name":"New"}`, 200, `New@example.com`}, {`{"email":null,"display_name":null}`, 200, `"display_name":"New"`}, {`{}`, 200, `"display_name":"New"`}} {
		w := call("PATCH", "/api/v1/users/me", tc.body, admin)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.contains) {
			t.Errorf("patch %s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
	w = call("PATCH", "/api/v1/users/me", `{"display_name":"`+strings.Repeat("x", 256)+`"}`, admin)
	if w.Code != 422 {
		t.Errorf("long name: %d %s", w.Code, w.Body.String())
	}

	// Compare representative validation and duplicate responses with Python HTTP.
	cases := []string{`{"email":"bad"}`, `{"display_name":12}`, `{"email":"a@example.com"}`, `{`, `{"email":`, `{"email": }`, `[]`, `null`, ``}
	py := exec.Command("python", "-c", `import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session
from app.api.v1.routers.users import router
from app.api.deps import current_admin
from app.db.session import get_db
from app.models.user import User
engine=create_engine(os.environ['TASK16_DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.include_router(router,prefix='/api/v1')
def session():
 with Session(engine) as db: yield db
def admin():
 with Session(engine) as db: yield db.scalar(select(User).where(User.email=='New@example.com'))
app.dependency_overrides[get_db]=session; app.dependency_overrides[current_admin]=admin
with TestClient(app,raise_server_exceptions=True) as c:
 print(json.dumps([[r.status_code,r.json()] for r in (c.patch('/api/v1/users/me',content=x,headers={'Content-Type':'application/json'}) for x in json.loads(os.environ['TASK16_CASES']))]))`)
	caseJSON, _ := json.Marshal(cases)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "TASK16_DB="+db.URL(), "TASK16_CASES="+string(caseJSON))
	output, err := py.Output()
	if err != nil {
		t.Fatalf("python parity: %v %s", err, output)
	}
	var expected [][]any
	if err = json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, body := range cases {
		w := call("PATCH", "/api/v1/users/me", body, admin)
		var got any
		if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(got, expected[i][1]) {
			t.Errorf("Python parity %s: %d %v expected %v", body, w.Code, got, expected[i])
		}
	}

	// Email length is checked on original, normalized and IDNA forms in bytes.
	for _, addr := range []string{strings.Repeat("a", 243) + "@example.com", strings.Repeat("é", 122) + "@example.com", strings.Repeat("a", 240) + "@bücher.de"} {
		body, _ := json.Marshal(map[string]any{"email": addr})
		for _, endpoint := range []struct{ method, path, extra string }{{"PATCH", "/api/v1/users/me", ""}, {"POST", "/api/v1/users", ` ,"password":"secret12"`}, {"POST", "/api/v1/auth/login", ` ,"password":"secret12"`}} {
			raw := string(body)
			if endpoint.extra != "" {
				raw = raw[:len(raw)-1] + endpoint.extra + "}"
			}
			w := call(endpoint.method, endpoint.path, raw, admin)
			if w.Code != 422 {
				t.Errorf("overlong %s: %d %s", endpoint.path, w.Code, w.Body.String())
			}
		}
	}
	// The profile body limit deliberately rejects input over 1 MiB.
	w = call("PATCH", "/api/v1/users/me", `{"ignored":"`+strings.Repeat("x", 1<<20)+`"}`, admin)
	if w.Code != 422 {
		t.Errorf("oversized profile body: %d", w.Code)
	}
	// Compare real Python and Go HTTP validation and normalized success values across all callers.
	addresses := []string{strings.Repeat("a", 242) + "@example.com", strings.Repeat("a", 243) + "@example.com", strings.Repeat("é", 121) + "@example.com", strings.Repeat("é", 122) + "@example.com", strings.Repeat("a", 240) + "@bücher.de", " SUPPORT@EXAMPLE.COM ", strings.Repeat("x", 2032) + " <x@example.com>", strings.Repeat("x", 2033) + " <x@example.com>", strings.Repeat("é", 2032) + " <x@example.com>", strings.Repeat("é", 2033) + " <x@example.com>", strings.Repeat(" ", 2049) + "x@example.com"}
	py = exec.Command("python", "-c", `import json,os
from fastapi import FastAPI, Depends
from fastapi.testclient import TestClient
from sqlalchemy import create_engine,select,text
from sqlalchemy.orm import Session
from app.api.v1.routers.users import router as users
from app.api.v1.routers.auth import router as auth
from app.api.deps import current_admin
from app.db.session import get_db
from app.models.user import User
engine=create_engine(os.environ['TASK16_DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI();app.include_router(users,prefix='/api/v1');app.include_router(auth,prefix='/api/v1')
def db():
 with Session(engine) as s: yield s
def admin(s=Depends(get_db)):
 return s.scalar(select(User).where(User.user_id=='00000000-0000-0000-0000-000000000001'))
app.dependency_overrides[get_db]=db;app.dependency_overrides[current_admin]=admin
out=[]
with TestClient(app,raise_server_exceptions=True) as c:
 for address in json.loads(os.environ['TASK16_CASES']):
  for method,path in [('PATCH','/api/v1/users/me'),('POST','/api/v1/users'),('POST','/api/v1/auth/login')]:
   payload={'email':address}
   if method=='POST': payload['password']='secret12'
   r=c.request(method,path,json=payload);body=r.json();out.append([r.status_code,body.get('email') if r.status_code in (200,201) else body])
   with engine.begin() as conn:
    conn.execute(text("DELETE FROM users WHERE user_id NOT IN ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002')"))
    conn.execute(text("UPDATE users SET email='New@example.com' WHERE user_id='00000000-0000-0000-0000-000000000001'"))
print(json.dumps(out))`)
	addressJSON, _ := json.Marshal(addresses)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "TASK16_DB="+db.URL(), "TASK16_CASES="+string(addressJSON))
	output, err = py.Output()
	if err != nil {
		t.Fatalf("email Python HTTP: %v %s", err, output)
	}
	if err = json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	idx := 0
	for _, address := range addresses {
		for _, endpoint := range []struct{ method, path string }{{"PATCH", "/api/v1/users/me"}, {"POST", "/api/v1/users"}, {"POST", "/api/v1/auth/login"}} {
			payload := map[string]any{"email": address}
			if endpoint.method == "POST" {
				payload["password"] = "secret12"
			}
			data, _ := json.Marshal(payload)
			w := call(endpoint.method, endpoint.path, string(data), admin)
			if strings.Contains(w.Body.String(), "secret12") {
				t.Errorf("password leaked by %s", endpoint.path)
			}
			var got map[string]any
			if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var value any = got
			if w.Code == 200 || w.Code == 201 {
				value = got["email"]
			}
			// Admin-create intentionally redacts all validation inputs, including email.
			if endpoint.path == "/api/v1/users" && w.Code == 422 {
				details := expected[idx][1].(map[string]any)["detail"].([]any)
				for _, d := range details {
					d.(map[string]any)["input"] = "[REDACTED]"
				}
			}
			if float64(w.Code) != expected[idx][0] || !reflect.DeepEqual(value, expected[idx][1]) {
				t.Errorf("email parity %s %s: %d %v expected %v", endpoint.path, address, w.Code, value, expected[idx])
			}
			idx++
			if _, err = pool.Exec(context.Background(), `DELETE FROM users WHERE user_id NOT IN ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002'); UPDATE users SET email='New@example.com' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
				t.Fatal(err)
			}
		}
	}

	var wg sync.WaitGroup
	for _, body := range []string{`{"email":"concurrent@example.com"}`, `{"display_name":"Concurrent"}`} {
		wg.Add(1)
		go func(body string) {
			defer wg.Done()
			w := call("PATCH", "/api/v1/users/me", body, admin)
			if w.Code != 200 {
				t.Errorf("concurrent: %d %s", w.Code, w.Body.String())
			}
		}(body)
	}
	wg.Wait()
	var email, name string
	if err := pool.QueryRow(context.Background(), `SELECT email,display_name FROM users WHERE user_id='00000000-0000-0000-0000-000000000001'`).Scan(&email, &name); err != nil || email != "concurrent@example.com" || name != "Concurrent" {
		t.Errorf("lost update: %q %q %v", email, name, err)
	}
	var other string
	if err := pool.QueryRow(context.Background(), `SELECT email FROM users WHERE user_id='00000000-0000-0000-0000-000000000002'`).Scan(&other); err != nil || other != "a@example.com" {
		t.Errorf("other user changed: %q %v", other, err)
	}

	// A database failure rolls the whole profile change back.
	if _, err := pool.Exec(context.Background(), `CREATE FUNCTION reject_profile() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$; CREATE TRIGGER reject_profile BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_profile()`); err != nil {
		t.Fatal(err)
	}
	w = call("PATCH", "/api/v1/users/me", `{"email":"rollback@example.com","display_name":"rollback"}`, admin)
	if w.Code != 500 {
		t.Errorf("injected failure: %d %s", w.Code, w.Body.String())
	}
	if err := pool.QueryRow(context.Background(), `SELECT email,display_name FROM users WHERE user_id='00000000-0000-0000-0000-000000000001'`).Scan(&email, &name); err != nil || email != "concurrent@example.com" || name != "Concurrent" {
		t.Errorf("rollback: %q %q %v", email, name, err)
	}
	if _, err := pool.Exec(context.Background(), `DROP TRIGGER reject_profile ON users; DROP FUNCTION reject_profile()`); err != nil {
		t.Fatal(err)
	}

	// A competing profile update must wait until the existing row lock is released.
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `SELECT user_id FROM users WHERE user_id='00000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- call("PATCH", "/api/v1/users/me", `{"display_name":"After lock"}`, admin).Code }()
	select {
	case code := <-done:
		t.Errorf("update bypassed row lock: %d", code)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := tx.Exec(context.Background(), `UPDATE users SET email='locked@example.com' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 200 {
			t.Errorf("locked update: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Error("update did not resume")
	}
	if err := pool.QueryRow(context.Background(), `SELECT email FROM users WHERE user_id='00000000-0000-0000-0000-000000000001'`).Scan(&email); err != nil || email != "locked@example.com" {
		t.Errorf("locked update lost email: %q %v", email, err)
	}

	// Re-check admin status after acquiring the row lock.
	tx, err = pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `SELECT user_id FROM users WHERE user_id='00000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done = make(chan int, 1)
	go func() { done <- call("PATCH", "/api/v1/users/me", `{"display_name":"Should not change"}`, admin).Code }()
	select {
	case code := <-done:
		t.Errorf("admin recheck bypassed lock: %d", code)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 403 {
			t.Errorf("demoted while waiting: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("demoted update did not resume")
	}
	if _, err = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}

	// List must recheck the persisted role after a concurrent demotion.
	tx, err = pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `SELECT user_id FROM users WHERE user_id='00000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done = make(chan int, 1)
	go func() { done <- call("GET", "/api/v1/users", "", admin).Code }()
	select {
	case code := <-done:
		t.Errorf("list bypassed role lock: %d", code)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 403 {
			t.Errorf("list after demotion: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("list did not resume")
	}
	if _, err = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	// Two administrators racing for the same email cannot both succeed.
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	codes := make(chan int, 2)
	start := make(chan struct{})
	for _, c := range []*http.Cookie{admin, user} {
		go func(c *http.Cookie) {
			<-start
			codes <- call("PATCH", "/api/v1/users/me", `{"email":"shared@example.com"}`, c).Code
		}(c)
	}
	close(start)
	a, b := <-codes, <-codes
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Errorf("uniqueness race: %d %d", a, b)
	}

}

func TestUsersOpenAPI(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	for _, tc := range []struct {
		path, method string
		codes        []string
	}{{"/api/v1/users", "get", []string{"200", "401", "403", "500"}}, {"/api/v1/users/me", "patch", []string{"200", "401", "403", "409", "422", "500"}}} {
		op := paths[tc.path].(map[string]any)[tc.method].(map[string]any)
		if op["security"] == nil {
			t.Errorf("%s security missing", tc.path)
		}
		responses := op["responses"].(map[string]any)
		for _, code := range tc.codes {
			if responses[code] == nil {
				t.Errorf("%s missing %s", tc.path, code)
			}
		}
		if tc.method == "patch" && op["requestBody"] == nil {
			t.Error("profile request body missing")
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	profile := schemas["ProfileBody"].(map[string]any)
	props := profile["properties"].(map[string]any)
	if profile["additionalProperties"] != true || props["email"] == nil || props["display_name"] == nil {
		t.Errorf("profile schema: %v", profile)
	}
	name := props["display_name"].(map[string]any)
	if name["maxLength"] != float64(255) {
		t.Errorf("display_name maxLength: %v", name)
	}
	user := schemas["SessionUser"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"user_id", "email", "role", "display_name", "created_at"} {
		if user[field] == nil {
			t.Errorf("user field %s missing", field)
		}
	}
	for _, field := range []string{"password_hash", "password_reset_token", "password_reset_expires"} {
		if user[field] != nil {
			t.Errorf("secret field %s exposed", field)
		}
	}
	get := paths["/api/v1/users"].(map[string]any)["get"].(map[string]any)
	patch := paths["/api/v1/users/me"].(map[string]any)["patch"].(map[string]any)
	schemaFor := func(op map[string]any, code string) map[string]any {
		return op["responses"].(map[string]any)[code].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	}
	list := schemaFor(get, "200")
	if list["type"] != "array" || list["items"].(map[string]any)["$ref"] != "#/components/schemas/SessionUser" {
		t.Errorf("list schema: %v", list)
	}
	for _, tc := range []struct {
		op        map[string]any
		code, ref string
	}{{patch, "200", "#/components/schemas/SessionUser"}, {patch, "409", "#/components/schemas/AuthErrorBody"}, {patch, "422", "#/components/schemas/ValidationErrorBody"}, {get, "403", "#/components/schemas/AuthErrorBody"}} {
		if got := schemaFor(tc.op, tc.code)["$ref"]; got != tc.ref {
			t.Errorf("%s schema: %v", tc.code, got)
		}
	}
	body := patch["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if body["$ref"] != "#/components/schemas/ProfileBody" {
		t.Errorf("request schema: %v", body)
	}

}
