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

func TestAdminCreateAndReset(t *testing.T) {
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
	_, err = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role,password_reset_token,password_reset_expires) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com','old','admin','token',now())`)
	if err != nil {
		t.Fatal(err)
	}
	params := auth.DefaultHashParams
	params.Memory = 1024
	params.Iterations = 1
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, hashParams: &params}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// Compare real FastAPI HTTP validation, removing echoed inputs from both sides.
	cases := [][2]string{{"/api/v1/users/bad/reset-password", `{"password":"123"}`}, {"/api/v1/users/bad/reset-password", `{}`}, {"/api/v1/users/bad/reset-password", `null`}, {"/api/v1/users/bad/reset-password", `{"password":"abcdef"}`}, {"/api/v1/users", `{}`}, {"/api/v1/users", `{"email":"bad","password":"123"}`}, {"/api/v1/users", `{"email":"` + strings.Repeat("a", 250) + `@example.com","password":"abcdef"}`}, {"/api/v1/users/00000000-0000-0000-0000-000000000099/reset-password", `{"password":"abcdef"}`}}
	rawCases, _ := json.Marshal(cases)
	py := exec.Command("python", "-c", `import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session
from app.api.v1.routers.users import router
from app.api.deps import current_admin
from app.db.session import get_db
from app.models.user import User
engine=create_engine(os.environ['TASK17_DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.include_router(router,prefix='/api/v1')
def session():
 with Session(engine) as db: yield db
def admin():
 with Session(engine) as db: yield db.scalar(select(User).where(User.email=='admin@example.com'))
app.dependency_overrides[get_db]=session; app.dependency_overrides[current_admin]=admin
with TestClient(app) as c:
 result=[]
 for path,body in json.loads(os.environ['TASK17_CASES']):
  r=c.post(path,content=body,headers={'Content-Type':'application/json'}); data=r.json()
  if isinstance(data.get('detail'),list):
   for error in data['detail']: error.pop('input',None)
  result.append([r.status_code,data])
 print(json.dumps(result))`)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "TASK17_DB="+db.URL(), "TASK17_CASES="+string(rawCases))
	out, err := py.Output()
	if err != nil {
		t.Fatalf("Python HTTP: %v %s", err, out)
	}
	var expected [][]any
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		rec := call(tc[0], tc[1])
		var data map[string]any
		if err = json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if details, ok := data["detail"].([]any); ok {
			for _, v := range details {
				delete(v.(map[string]any), "input")
			}
		}
		if float64(rec.Code) != expected[i][0] || !reflect.DeepEqual(data, expected[i][1]) {
			t.Errorf("Python HTTP case %d: %d %v expected %v", i, rec.Code, data, expected[i])
		}
	}
	// Compare successful response shapes and duplicate responses against FastAPI.
	successCases := [][2]string{{"/api/v1/users", `{"email":"python-compare@example.com","password":"abcdef","display_name":"","role":"user"}`}, {"/api/v1/users/00000000-0000-0000-0000-000000000001/reset-password", `{"password":"abcdef"}`}, {"/api/v1/users", `{"email":"admin@example.com","password":"abcdef"}`}}
	successJSON, _ := json.Marshal(successCases)
	pySuccess := exec.Command("python", "-c", `import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select, delete
from sqlalchemy.orm import Session
from app.api.v1.routers.users import router
from app.api.deps import current_admin
from app.db.session import get_db
from app.models.user import User
engine=create_engine(os.environ['TASK17_DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI();app.include_router(router,prefix='/api/v1')
def session():
 with Session(engine) as db: yield db
def admin():
 with Session(engine) as db: yield db.scalar(select(User).where(User.email=='admin@example.com'))
app.dependency_overrides[get_db]=session;app.dependency_overrides[current_admin]=admin
with TestClient(app) as c:
 result=[]
 for path,body in json.loads(os.environ['TASK17_CASES']):
  r=c.post(path,content=body,headers={'Content-Type':'application/json'}); data=r.json();data.pop('user_id',None);data.pop('created_at',None);result.append([r.status_code,data])
  if path=='/api/v1/users' and r.status_code==201:
   with Session(engine) as db: db.execute(delete(User).where(User.email==data['email']));db.commit()
 print(json.dumps(result))`)
	pySuccess.Dir = "../../../backend"
	pySuccess.Env = append(os.Environ(), "TASK17_DB="+db.URL(), "TASK17_CASES="+string(successJSON))
	pyOut, err := pySuccess.Output()
	if err != nil {
		t.Fatalf("Python success HTTP: %v %s", err, pyOut)
	}
	var pyExpected [][]any
	if err = json.Unmarshal(pyOut, &pyExpected); err != nil {
		t.Fatal(err)
	}
	for i, tc := range successCases {
		rec := call(tc[0], tc[1])
		var data map[string]any
		if err = json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		delete(data, "user_id")
		delete(data, "created_at")
		if float64(rec.Code) != pyExpected[i][0] || !reflect.DeepEqual(data, pyExpected[i][1]) {
			t.Errorf("Python success case %d: %d %v expected %v", i, rec.Code, data, pyExpected[i])
		}
	}
	w := call("/api/v1/users", `{"email":"new@example.com","password":"secret123"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	w = call("/api/v1/users/00000000-0000-0000-0000-000000000001/reset-password", `{"password":"newsecret"}`)
	if w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	var hash string
	var token *string
	if err = pool.QueryRow(context.Background(), `SELECT password_hash,password_reset_token FROM users WHERE email='admin@example.com'`).Scan(&hash, &token); err != nil {
		t.Fatal(err)
	}
	ok, err := auth.Verify("newsecret", hash)
	if err != nil || !ok || token != nil {
		t.Fatal("reset not persisted")
	}
	for _, tc := range []struct {
		path, body string
		code       int
	}{
		{"/api/v1/users", `{}`, 422},
		{"/api/v1/users", `{"email":"bad","password":"SENSITIVE"}`, 422},
		{"/api/v1/users", `{"email":"new@example.com","password":"SENSITIVE"}`, 409},
		{"/api/v1/users", `{"email":"bad@example.com","password":"12345"}`, 422},
		{"/api/v1/users", `{"email":"bad@example.com","password":"` + strings.Repeat("界", 129) + `"}`, 422},
		{"/api/v1/users", `{"email":"bad@example.com","password":null}`, 422},
		{"/api/v1/users", `{"email":"bad@example.com","password":"abcdef","role":null}`, 422},
		{"/api/v1/users", `{"email":"bad@example.com","password":"abcdef","display_name":"` + strings.Repeat("x", 256) + `"}`, 422},
		{"/api/v1/users", strings.Repeat("x", 1<<20+1), 422},
		{"/api/v1/users", `{"email":"oversize@example.com","password":"abcdef","extra":"` + strings.Repeat("x", 1<<20) + `"}`, 422},
		{"/api/v1/users/bad/reset-password", `{"password":"abcdef"}`, 422},
		{"/api/v1/users/00000000-0000-0000-0000-000000000099/reset-password", `{"password":"abcdef"}`, 404},
	} {
		w := call(tc.path, tc.body)
		if w.Code != tc.code {
			t.Errorf("%s: got %d want %d: %s", tc.path, w.Code, tc.code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "SENSITIVE") {
			t.Fatal("secret in response")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("missing no-store")
		}
	}
	w = call("/api/v1/users", `{"email":"unicode@example.com","password":"界界界界界界","role":"user","display_name":""}`)
	if w.Code != 201 {
		t.Fatalf("unicode: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if json.Unmarshal(w.Body.Bytes(), &created) != nil || created["role"] != "user" || created["display_name"] != "" {
		t.Fatalf("created shape %s", w.Body.String())
	}
	var createdHash string
	if err = pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE email='unicode@example.com'`).Scan(&createdHash); err != nil {
		t.Fatal(err)
	}
	ok, err = auth.Verify("界界界界界界", createdHash)
	if err != nil || !ok {
		t.Fatal("unicode password not verified")
	}
	ok, _ = auth.Verify("wrong", createdHash)
	if ok {
		t.Fatal("wrong password verified")
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- call("/api/v1/users", `{"email":"race@example.com","password":"abcdef"}`).Code
		}()
	}
	wg.Wait()
	close(codes)
	seen := map[int]int{}
	for code := range codes {
		seen[code]++
	}
	if seen[201] != 1 || seen[409] != 1 {
		t.Errorf("race: %v", seen)
	}
	_, err = pool.Exec(context.Background(), `UPDATE users SET password_reset_token='rollback-token',password_reset_expires=now() WHERE email='admin@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `CREATE FUNCTION reject_admin_reset() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.password_reset_token IS NULL THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_admin_reset BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_admin_reset()`)
	if err != nil {
		t.Fatal(err)
	}
	w = call("/api/v1/users/00000000-0000-0000-0000-000000000001/reset-password", `{"password":"rollback"}`)
	if w.Code != 500 {
		t.Errorf("injected failure %d", w.Code)
	}
	var after string
	var rollbackToken *string
	var rollbackExpires *time.Time
	if err = pool.QueryRow(context.Background(), `SELECT password_hash,password_reset_token,password_reset_expires FROM users WHERE email='admin@example.com'`).Scan(&after, &rollbackToken, &rollbackExpires); err != nil || after != hash || rollbackToken == nil || *rollbackToken != "rollback-token" || rollbackExpires == nil {
		t.Fatal("rollback failed")
	}
	_, err = pool.Exec(context.Background(), `DROP TRIGGER reject_admin_reset ON users; DROP FUNCTION reject_admin_reset()`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cookie string
		code   int
	}{{"", 401}, {"tampered", 401}} {
		r := httptest.NewRequest("POST", "/api/v1/users", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: tc.cookie})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.code {
			t.Errorf("auth %d", rec.Code)
		}
	}
	nonadmin, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": created["user_id"]})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/users", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: nonadmin})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("nonadmin %d", rec.Code)
	}

	// Reset a different user and verify both reset fields are cleared, without changing the admin.
	_, err = pool.Exec(context.Background(), `UPDATE users SET password_reset_token='token',password_reset_expires=now() WHERE email='unicode@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	target := created["user_id"].(string)
	w = call("/api/v1/users/"+target+"/reset-password", `{"password":"changed"}`)
	if w.Code != 200 {
		t.Fatalf("target reset: %d %s", w.Code, w.Body.String())
	}
	var resetToken *string
	var expires *time.Time
	if err = pool.QueryRow(context.Background(), `SELECT password_hash,password_reset_token,password_reset_expires FROM users WHERE email='unicode@example.com'`).Scan(&after, &resetToken, &expires); err != nil || resetToken != nil || expires != nil {
		t.Fatal("reset fields were not cleared")
	}
	ok, err = auth.Verify("changed", after)
	if err != nil || !ok {
		t.Fatal("target hash invalid")
	}
	if err = pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE email='admin@example.com'`).Scan(&after); err != nil || after != hash {
		t.Fatal("other user changed")
	}
	// Reset must wait for a target row lock before replacing its password.
	targetTx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = targetTx.Exec(context.Background(), `SELECT user_id FROM users WHERE email='unicode@example.com' FOR UPDATE`)
	if err != nil {
		t.Fatal(err)
	}
	targetDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { targetDone <- call("/api/v1/users/"+target+"/reset-password", `{"password":"locked"}`) }()
	select {
	case <-targetDone:
		targetTx.Rollback(context.Background())
		t.Fatal("reset did not wait for target lock")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = targetTx.Exec(context.Background(), `UPDATE users SET display_name='locked name' WHERE email='unicode@example.com'`); err != nil {
		t.Fatal(err)
	}
	if err = targetTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-targetDone; got.Code != 200 || !strings.Contains(got.Body.String(), "locked name") {
		t.Errorf("target lock reset: %d %s", got.Code, got.Body.String())
	}
	// Reciprocal admin resets must both complete without a deadlock.
	_, err = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE email='unicode@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": target})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		start := make(chan struct{})
		result := make(chan int, 2)
		for _, tc := range []struct{ id, token string }{{target, tok}, {"00000000-0000-0000-0000-000000000001", otherToken}} {
			go func(id, token string) {
				<-start
				r := httptest.NewRequest("POST", "/api/v1/users/"+id+"/reset-password", strings.NewReader(`{"password":"reciprocal"}`))
				r.Header.Set("Content-Type", "application/json")
				r.AddCookie(&http.Cookie{Name: "session", Value: token})
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, r)
				result <- rec.Code
			}(tc.id, tc.token)
		}
		close(start)
		for j := 0; j < 2; j++ {
			select {
			case code := <-result:
				if code != 200 {
					t.Fatalf("reciprocal reset status: %d", code)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("reciprocal reset timed out")
			}
		}
	}
	// The admin row is locked and its role is checked again inside the transaction.
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(context.Background(), `SELECT user_id FROM users WHERE email='admin@example.com' FOR UPDATE`)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- call("/api/v1/users", `{"email":"blocked@example.com","password":"abcdef"}`).Code }()
	select {
	case <-done:
		tx.Rollback(context.Background())
		t.Fatal("create did not wait for admin lock")
	case <-time.After(100 * time.Millisecond):
	}
	_, err = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE email='admin@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 403 {
		t.Errorf("revoked admin: %d", code)
	}
	deleted, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000099"})
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "/api/v1/users", strings.NewReader(`{}`))
	r.AddCookie(&http.Cookie{Name: "session", Value: deleted})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("deleted admin %d", rec.Code)
	}

}

func TestAdminWriteOpenAPI(t *testing.T) {
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(nil, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/openapi.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var doc map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	for _, tc := range []struct {
		path  string
		codes []string
	}{{"/api/v1/users", []string{"201", "401", "403", "409", "422", "500"}}, {"/api/v1/users/{user_id}/reset-password", []string{"200", "401", "403", "404", "422", "500"}}} {
		op := paths[tc.path].(map[string]any)["post"].(map[string]any)
		responses := op["responses"].(map[string]any)
		for _, code := range tc.codes {
			if _, ok := responses[code]; !ok {
				t.Errorf("%s missing %s", tc.path, code)
			}
		}
		if _, ok := op["security"]; !ok {
			t.Errorf("%s missing security", tc.path)
		}
	}
}
