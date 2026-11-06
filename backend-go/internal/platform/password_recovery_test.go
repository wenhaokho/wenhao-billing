package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type recoverySender struct {
	messages []email.Message
	fail     bool
}

func (s *recoverySender) Send(_ context.Context, m email.Message) error {
	s.messages = append(s.messages, m)
	if s.fail {
		return email.ErrDelivery
	}
	return nil
}
func TestPasswordRecoveryHTTP(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','one@example.com','old','admin'),('00000000-0000-0000-0000-000000000002','two@example.com','other','user')`)
	if err != nil {
		t.Fatal(err)
	}
	sender := &recoverySender{}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, Sender: sender, BaseURL: "https://example.com/a b/", hashParams: &auth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, withJSONContentType(httptest.NewRequest("POST", path, strings.NewReader(body))))
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("missing no-store")
		}
		return w
	}
	f := "/api/v1/auth/forgot-password"
	reset := "/api/v1/auth/reset-password"
	unknown := call(f, `{"email":"missing@example.com"}`)
	known := call(f, `{"email":"one@EXAMPLE.COM"}`)
	if known.Code != 200 || known.Body.String() != unknown.Body.String() || len(sender.messages) != 1 {
		t.Fatalf("forgot response %d %s", known.Code, known.Body.String())
	}
	var token string
	var expiry time.Time
	if err = pool.QueryRow(ctx, `SELECT password_reset_token,password_reset_expires FROM users WHERE email='one@example.com'`).Scan(&token, &expiry); err != nil || len(token) < 40 || time.Until(expiry) < 59*time.Minute || time.Until(expiry) > time.Hour {
		t.Fatalf("bad token or expiry: %v", err)
	}
	if !strings.Contains(sender.messages[0].Text, "/a%20b/reset-password?token="+url.QueryEscape(token)) {
		t.Fatal("link invalid")
	}
	payload := fmt.Sprintf(`{"token":%q,"new_password":"new-secret"}`, token)
	const n = 2
	var wg sync.WaitGroup
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := call(reset, payload)
			if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "new-secret") {
				t.Error("secret leaked")
			}
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	count := map[int]int{}
	for c := range codes {
		count[c]++
	}
	if count[200] != 1 || count[400] != 1 {
		t.Fatalf("concurrent codes %v", count)
	}
	var hash string
	var cleared *string
	if err = pool.QueryRow(ctx, `SELECT password_hash,password_reset_token FROM users WHERE email='one@example.com'`).Scan(&hash, &cleared); err != nil || cleared != nil {
		t.Fatal("token not cleared")
	}
	valid, err := auth.Verify("new-secret", hash)
	if err != nil || !valid {
		t.Fatal("password invalid")
	}
	var other string
	pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email='two@example.com'`).Scan(&other)
	if other != "other" {
		t.Fatal("other user changed")
	}
	_, err = pool.Exec(ctx, `UPDATE users SET password_reset_token='expired',password_reset_expires=now()-interval '1 second' WHERE email='one@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(reset, `{"token":"expired","new_password":"secret"}`); w.Code != 400 {
		t.Fatal(w.Body.String())
	}
	pool.QueryRow(ctx, `SELECT password_reset_token FROM users WHERE email='one@example.com'`).Scan(&cleared)
	if cleared != nil {
		t.Fatal("expired token not cleared")
	}
	for _, body := range []string{`{}`, `null`, `{"token":"sensitive-token","new_password":"pword"}`, `{"token":"sensitive-token","new_password":null}`, `{`, strings.Repeat("x", 1<<20+1)} {
		w := call(reset, body)
		if w.Code != 422 || (strings.Contains(w.Body.String(), "sensitive-token") || strings.Contains(w.Body.String(), "pword")) {
			t.Fatalf("invalid status %d body %s", w.Code, w.Body.String())
		}
	}
	// The reset must wait for a transaction already holding the user row.
	_, err = pool.Exec(ctx, `UPDATE users SET password_reset_token='lock-token',password_reset_expires=now()+interval '1 hour' WHERE email='one@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lockTx.Exec(ctx, `SELECT user_id FROM users WHERE email='one@example.com' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan int, 1)
	go func() { blocked <- call(reset, `{"token":"lock-token","new_password":"new-secret"}`).Code }()
	select {
	case <-blocked:
		t.Fatal("reset bypassed row lock")
	case <-time.After(150 * time.Millisecond):
	}
	if _, err = lockTx.Exec(ctx, `UPDATE users SET password_reset_token=NULL,password_reset_expires=NULL WHERE email='one@example.com'`); err != nil {
		t.Fatal(err)
	}
	if err = lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if <-blocked != 400 {
		t.Fatal("reset failed after lock released")
	}
	if err = pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email='one@example.com'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	// A second request replaces the token with fresh randomness.
	call(f, `{"email":"one@example.com"}`)
	var first string
	if err = pool.QueryRow(ctx, `SELECT password_reset_token FROM users WHERE email='one@example.com'`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	call(f, `{"email":"one@example.com"}`)
	var second string
	if err = pool.QueryRow(ctx, `SELECT password_reset_token FROM users WHERE email='one@example.com'`).Scan(&second); err != nil || first == second {
		t.Fatal("token did not rotate")
	}
	if w := call(reset, fmt.Sprintf(`{"token":%q,"new_password":"secret"}`, first)); w.Code != 400 {
		t.Fatal("old token remains valid")
	}
	// Failed writes roll back both password and token changes.
	_, err = pool.Exec(ctx, `CREATE FUNCTION fail_recovery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$; CREATE TRIGGER fail_recovery BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION fail_recovery()`)
	if err != nil {
		t.Fatal(err)
	}
	w := call(reset, fmt.Sprintf(`{"token":%q,"new_password":"new-secret"}`, second))
	if w.Code != 500 || strings.Contains(w.Body.String(), second) {
		t.Fatal("failed write response")
	}
	var afterToken, afterHash string
	if err = pool.QueryRow(ctx, `SELECT password_reset_token,password_hash FROM users WHERE email='one@example.com'`).Scan(&afterToken, &afterHash); err != nil || afterToken != second || afterHash != hash {
		t.Fatal("failed reset changed state")
	}
	if w = call(f, `{"email":"one@example.com"}`); w.Code != 500 {
		t.Fatal("failed issue response")
	}
	if err = pool.QueryRow(ctx, `SELECT password_reset_token FROM users WHERE email='one@example.com'`).Scan(&afterToken); err != nil || afterToken != second {
		t.Fatal("failed issue changed token")
	}
	_, err = pool.Exec(ctx, `DROP TRIGGER fail_recovery ON users; DROP FUNCTION fail_recovery()`)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	oldLog := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(oldLog)
	sender.fail = true
	if w := call(f, `{"email":"one@example.com"}`); w.Code != 200 || w.Body.String() != unknown.Body.String() {
		t.Fatal("mail failure leaked")
	}
	if strings.Contains(logs.String(), "one@example.com") || strings.Contains(logs.String(), second) || !strings.Contains(logs.String(), "delivery unavailable") {
		t.Fatal("unsafe or missing delivery log")
	}
	var doc map[string]any
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	if json.Unmarshal(w.Body.Bytes(), &doc) != nil {
		t.Fatal("openapi invalid")
	}
	paths := doc["paths"].(map[string]any)
	if paths[f] == nil || paths[reset] == nil {
		t.Fatal("missing routes")
	}
}

func TestRecoveryPythonHTTPDifferential(t *testing.T) {
	cases := []struct{ Path, Body string }{
		{"forgot-password", ``}, {"reset-password", ``}, {"forgot-password", `{}`}, {"forgot-password", `{"email":"invalid"}`}, {"forgot-password", `{"email":null}`},
		{"reset-password", `{}`}, {"reset-password", `{"token":"x","new_password":"12345"}`}, {"reset-password", `{"token":1,"new_password":"abcdef"}`}, {"reset-password", `{"token":"x","new_password":"abcdef"}`},
	}
	script := `import json,sys
from fastapi import FastAPI
from fastapi.testclient import TestClient
from app.schemas.auth import ForgotPasswordRequest, ResetPasswordRequest
app=FastAPI()
@app.post('/forgot-password')
def forgot(body: ForgotPasswordRequest): return {}
@app.post('/reset-password')
def reset(body: ResetPasswordRequest): return {}
c=TestClient(app)
for item in json.load(sys.stdin):
 r=c.post('/'+item['Path'],content=item['Body'],headers={'content-type':'application/json'})
 print(json.dumps({"status":r.status_code,"detail":[{"type":e["type"],"loc":e["loc"],"msg":e["msg"]} for e in r.json().get("detail",[])]}))
`
	data, _ := json.Marshal(cases)
	cmd := exec.Command("python", "-c", script)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(cases) {
		t.Fatal("python differential output")
	}
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, withJSONContentType(httptest.NewRequest("POST", "/api/v1/auth/"+tc.Path, strings.NewReader(tc.Body))))
		got := w.Code
		if got == 500 {
			got = 200
		}
		var python struct {
			Status int `json:"status"`
			Detail []struct {
				Type string `json:"type"`
				Loc  []any  `json:"loc"`
				Msg  string `json:"msg"`
			} `json:"detail"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &python); err != nil {
			t.Fatal(err)
		}
		if got != python.Status {
			t.Errorf("%s %s: go %d python %d", tc.Path, tc.Body, w.Code, python.Status)
			continue
		}
		if got == 422 {
			var result struct {
				Detail []struct {
					Type string `json:"type"`
					Loc  []any  `json:"loc"`
					Msg  string `json:"msg"`
				} `json:"detail"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Detail, python.Detail) {
				t.Errorf("%s %s: go %+v python %+v", tc.Path, tc.Body, result.Detail, python.Detail)
			}
		}
	}
}

func TestRecoveryOpenAPIContract(t *testing.T) {
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
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for _, route := range []struct {
		path     string
		statuses []string
		body     string
		fields   []string
	}{{"/api/v1/auth/forgot-password", []string{"200", "422", "500"}, "ForgotPasswordBody", []string{"email"}}, {"/api/v1/auth/reset-password", []string{"200", "400", "422", "500"}, "ResetPasswordBody", []string{"token", "new_password"}}} {
		op := paths[route.path].(map[string]any)["post"].(map[string]any)
		if sec, ok := op["security"]; ok && len(sec.([]any)) > 0 {
			t.Fatalf("%s is not public", route.path)
		}
		req := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		if req["$ref"] != "#/components/schemas/"+route.body {
			t.Fatalf("unexpected request %v", req)
		}
		responses := op["responses"].(map[string]any)
		for _, status := range route.statuses {
			content := responses[status].(map[string]any)["content"].(map[string]any)
			if content["application/json"] == nil {
				t.Fatalf("missing JSON response %s %s", route.path, status)
			}
		}
		props := schemas[route.body].(map[string]any)["properties"].(map[string]any)
		for _, field := range route.fields {
			if props[field] == nil {
				t.Fatalf("missing %s", field)
			}
		}
	}
	for _, tc := range []struct{ path, code, ref string }{{"/api/v1/auth/reset-password", "200", "#/components/schemas/SessionUser"}, {"/api/v1/auth/reset-password", "400", "#/components/schemas/AuthErrorBody"}, {"/api/v1/auth/forgot-password", "422", "#/components/schemas/ValidationErrorBody"}} {
		op := paths[tc.path].(map[string]any)["post"].(map[string]any)
		schema := op["responses"].(map[string]any)[tc.code].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		if schema["$ref"] != tc.ref {
			t.Fatalf("incorrect response schema: %v", schema)
		}
	}
	forgotOp := paths["/api/v1/auth/forgot-password"].(map[string]any)["post"].(map[string]any)
	forgotSchema := forgotOp["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	ref, ok := forgotSchema["$ref"].(string)
	if !ok {
		t.Fatal("forgot response schema missing ref")
	}
	forgotProps := schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)["properties"].(map[string]any)
	for _, f := range []string{"message", "reset_token", "reset_link"} {
		if forgotProps[f] == nil {
			t.Fatalf("missing response field %s", f)
		}
	}
	props := schemas["ResetPasswordBody"].(map[string]any)["properties"].(map[string]any)
	password := props["new_password"].(map[string]any)
	if password["minLength"] != float64(6) || password["maxLength"] != float64(128) || password["format"] != "password" {
		t.Fatalf("password schema %v", password)
	}
}
func TestRecoveryProductionArgon2Defaults(t *testing.T) {
	p := auth.DefaultHashParams
	if p.Memory < 65536 || p.Iterations < 3 || p.Parallelism < 4 || p.SaltLen < 16 || p.KeyLen < 32 {
		t.Fatalf("weak defaults: %+v", p)
	}
	hash, err := auth.Hash("production-test-password", p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatal("unexpected production hash parameters")
	}
	valid, err := auth.Verify("production-test-password", hash)
	if err != nil || !valid {
		t.Fatal("production hash verification failed")
	}
}

func TestRecoveryMissingBodyAndRedaction(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/auth/forgot-password", "/api/v1/auth/reset-password"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader("")))
		var body struct {
			Detail []struct {
				Type  string `json:"type"`
				Loc   []any  `json:"loc"`
				Input any    `json:"input"`
			} `json:"detail"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 422 || len(body.Detail) != 1 || body.Detail[0].Type != "missing" || !reflect.DeepEqual(body.Detail[0].Loc, []any{"body"}) || body.Detail[0].Input != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		for _, payload := range []string{`{"token":"secret-token","new_password":"abc","email":"bad"}`, `["secret-token","secret-password"]`, `{"token":{"nested":"secret-token"},"new_password":{"nested":"secret-password"}}`} {
			w = httptest.NewRecorder()
			h.ServeHTTP(w, withJSONContentType(httptest.NewRequest("POST", path, strings.NewReader(payload))))
			if w.Code != 422 || strings.Contains(w.Body.String(), "secret-token") || strings.Contains(w.Body.String(), "secret-password") {
				t.Fatalf("unsafe %s: %d %s", path, w.Code, w.Body.String())
			}
		}
	}
}
