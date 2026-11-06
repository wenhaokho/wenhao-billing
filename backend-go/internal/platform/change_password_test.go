package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

type observedReadCloser struct {
	io.ReadCloser
	once    sync.Once
	reading chan struct{}
}

func (b *observedReadCloser) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.reading) })
	return b.ReadCloser.Read(p)
}

func TestChangePasswordHTTP(t *testing.T) {
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
	hash, err := auth.Hash("old-secret", auth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO users (user_id,email,password_hash,role,password_reset_token,password_reset_expires) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com',$1,'admin','token',now()),('00000000-0000-0000-0000-000000000002','user@example.com',$1,'user','token2',now())`, hash)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, hashParams: &auth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token := func(id string, exp time.Time) *http.Cookie {
		v, e := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": id, "exp": exp.Unix()}).SignedString(cfg.Secret)
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	admin := token("00000000-0000-0000-0000-000000000001", time.Now().Add(time.Hour))
	nonadmin := token("00000000-0000-0000-0000-000000000002", time.Now().Add(time.Hour))
	call := func(body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/auth/change-password", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("no-store missing")
		}
		for _, secret := range []string{"old-secret", "new-secret", hash, "token"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Error("secret leaked")
			}
		}
		return w
	}
	// Real TCP client stalls midway through a body after the handler starts reading.
	reading := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = &observedReadCloser{ReadCloser: r.Body, reading: reading}
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST /api/v1/auth/change-password HTTP/1.1\r\nHost: %s\r\nCookie: session=%s\r\nContent-Length: 100\r\n\r\n{", address, admin.Value)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-reading:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start reading request body")
	}
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	lockTx, err := pool.Begin(lockCtx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	_, err = lockTx.Exec(lockCtx, `SELECT user_id FROM users WHERE user_id='00000000-0000-0000-0000-000000000001' FOR UPDATE NOWAIT`)
	lockTx.Rollback(lockCtx)
	cancel()
	conn.Close()
	if err != nil {
		t.Fatalf("stalled body held user row lock: %v", err)
	}
	body := `{"current_password":"old-secret","new_password":"new-secret"}`
	cases := []struct {
		name, body string
		c          *http.Cookie
		code       int
		detail     string
	}{{"missing", body, nil, 401, "not authenticated"}, {"tampered", body, &http.Cookie{Name: "session", Value: admin.Value + "x"}, 401, "not authenticated"}, {"expired", body, token("00000000-0000-0000-0000-000000000001", time.Now().Add(-time.Hour)), 401, "session expired"}, {"deleted", body, token("00000000-0000-0000-0000-000000000003", time.Now().Add(time.Hour)), 403, "admin required"}, {"nonadmin", body, nonadmin, 403, "admin required"}, {"wrong", `{"current_password":"wrong","new_password":"new-secret"}`, admin, 400, "current password is incorrect"}, {"same", `{"current_password":"old-secret","new_password":"old-secret"}`, admin, 400, "new password must differ from current password"}, {"short", `{"current_password":"old-secret","new_password":"short"}`, admin, 422, "string_too_short"}, {"long", `{"current_password":"old-secret","new_password":"` + strings.Repeat("x", 129) + `"}`, admin, 422, "string_too_long"}, {"missing field", `{"current_password":"old-secret"}`, admin, 422, "missing"}}
	// FastAPI checks persisted authorization before field validation, but parses JSON first.
	deleted := token("00000000-0000-0000-0000-000000000003", time.Now().Add(time.Hour))
	for _, c := range []*http.Cookie{nonadmin, deleted} {
		for _, tc := range []struct {
			body   string
			status int
			detail string
		}{{`{}`, 403, "admin required"}, {``, 403, "admin required"}, {`{`, 422, "json_invalid"}} {
			w := call(tc.body, c)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.detail) {
				t.Fatalf("authorization precedence: %d %s", w.Code, w.Body.String())
			}
		}
	}
	for _, c := range []*http.Cookie{nil, {Name: "session", Value: admin.Value + "x"}} {
		if w := call(`{`, c); w.Code != 422 || !strings.Contains(w.Body.String(), "json_invalid") {
			t.Fatalf("malformed JSON before session: %d %s", w.Code, w.Body.String())
		}
		if w := call(``, c); w.Code != 401 {
			t.Fatalf("absent body after session: %d %s", w.Code, w.Body.String())
		}
	}
	if w := call(``, admin); w.Code != 422 || !strings.Contains(w.Body.String(), "missing") {
		t.Fatalf("admin absent body: %d %s", w.Code, w.Body.String())
	}
	if w := call(`{}`, admin); w.Code != 422 {
		t.Fatalf("admin field validation: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := call(tc.body, tc.c)
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.detail) {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		body, key string
		value     float64
	}{
		{`{"current_password":"old-secret","new_password":"short"}`, "min_length", 6},
		{`{"current_password":"old-secret","new_password":"` + strings.Repeat("x", 129) + `"}`, "max_length", 128},
	} {
		var response struct {
			Detail []struct {
				Ctx map[string]any `json:"ctx"`
			} `json:"detail"`
		}
		w := call(tc.body, admin)
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Detail) != 1 || response.Detail[0].Ctx[tc.key] != tc.value {
			t.Fatalf("numeric validation context: %s", w.Body.String())
		}
	}
	var before string
	var reset *string
	if err = pool.QueryRow(ctx, `SELECT password_hash,password_reset_token FROM users WHERE email='admin@example.com'`).Scan(&before, &reset); err != nil || before != hash || reset == nil {
		t.Fatal("failed request changed user", err)
	}
	// An AFTER UPDATE trigger raises after the row update; the transaction must roll back the entire row.
	var snapshotBefore, snapshotAfter string
	if err = pool.QueryRow(ctx, `SELECT row_to_json(users)::text FROM users WHERE email='admin@example.com'`).Scan(&snapshotBefore); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_password_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected database failure'; END $$; CREATE TRIGGER reject_password_update AFTER UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_password_update()`)
	if err != nil {
		t.Fatal(err)
	}
	failed := call(body, admin)
	if failed.Code != 500 || !strings.Contains(failed.Body.String(), "internal server error") || strings.Contains(failed.Body.String(), "injected database failure") {
		t.Fatalf("database failure: %d %s", failed.Code, failed.Body.String())
	}
	if err = pool.QueryRow(ctx, `SELECT row_to_json(users)::text FROM users WHERE email='admin@example.com'`).Scan(&snapshotAfter); err != nil || snapshotAfter != snapshotBefore {
		t.Fatal("failed update did not roll back", err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_password_update ON users; DROP FUNCTION reject_password_update()`); err != nil {
		t.Fatal(err)
	}
	w := call(body, admin)
	if w.Code != 200 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("success: %d %s", w.Code, w.Body.String())
	}
	meRequest := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	meRequest.AddCookie(admin)
	me := httptest.NewRecorder()
	h.ServeHTTP(me, meRequest)
	if me.Code != 200 {
		t.Fatal("existing session was invalidated")
	}
	var after string
	var expires *time.Time
	if err = pool.QueryRow(ctx, `SELECT password_hash,password_reset_token,password_reset_expires FROM users WHERE email='admin@example.com'`).Scan(&after, &reset, &expires); err != nil || reset != nil || expires != nil {
		t.Fatal("reset fields", err)
	}
	if ok, _ := auth.Verify("new-secret", after); !ok {
		t.Fatal("new password mismatch")
	}
	if ok, _ := auth.Verify("old-secret", after); ok {
		t.Fatal("old password still works")
	}
	// Competing changes with the same current password must serialize at the row lock.
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, next := range []string{"parallel-one", "parallel-two"} {
		go func(next string) {
			<-start
			results <- call(`{"current_password":"new-secret","new_password":"`+next+`"}`, admin).Code
		}(next)
	}
	close(start)
	a, b := <-results, <-results
	if !((a == 200 && b == 400) || (a == 400 && b == 200)) {
		t.Fatalf("concurrent changes: %d, %d", a, b)
	}
	var other string
	if err = pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email='user@example.com'`).Scan(&other); err != nil || other != hash {
		t.Fatal("other user changed", err)
	}
}

func TestChangePasswordOpenAPI(t *testing.T) {
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
	op := paths["/api/v1/auth/change-password"].(map[string]any)["post"].(map[string]any)
	if op["requestBody"] == nil || op["security"] == nil {
		t.Fatal("missing body or security")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	schema := schemas["ChangePasswordBody"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if props["current_password"] == nil {
		t.Fatal("current password missing")
	}
	ctxSchema := schemas["ValidationDetail"].(map[string]any)["properties"].(map[string]any)["ctx"].(map[string]any)
	if additional, ok := ctxSchema["additionalProperties"].(map[string]any); !ok || len(additional) != 0 {
		t.Fatalf("validation context must allow numeric wire values: %v", ctxSchema)
	}
	next := props["new_password"].(map[string]any)
	if next["minLength"] != float64(6) || next["maxLength"] != float64(128) {
		t.Fatalf("bounds: %v", next)
	}
}

// Expected structures below were captured from ChangePasswordRequest's Pydantic
// errors; inputs are deliberately redacted instead of echoing credentials.
func TestChangePasswordValidationPythonContract(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{}`, `{"detail":[{"type":"missing","loc":["body","current_password"],"msg":"Field required","input":"[REDACTED]"},{"type":"missing","loc":["body","new_password"],"msg":"Field required","input":"[REDACTED]"}]}`},
		{`{"current_password":"secret","new_password":"short"}`, `{"detail":[{"type":"string_too_short","loc":["body","new_password"],"msg":"String should have at least 6 characters","input":"[REDACTED]","ctx":{"min_length":6}}]}`},
		{`{"current_password":3,"new_password":null}`, `{"detail":[{"type":"string_type","loc":["body","current_password"],"msg":"Input should be a valid string","input":"[REDACTED]"},{"type":"string_type","loc":["body","new_password"],"msg":"Input should be a valid string","input":"[REDACTED]"}]}`},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		_, _, ok := changePasswordValidation(w, jsonTestRequest([]byte(tc.body)))
		if ok || strings.TrimSpace(w.Body.String()) != tc.want {
			t.Errorf("body %s: %s", tc.body, w.Body.String())
		}
	}
	for _, body := range []string{`{"current_password":{"nested":"secret-value"},"new_password":"sensitive-value"}`, `{"current_password":"secret-value","extra":{"password":"sensitive-value"}}`, `["secret-value","sensitive-value"]`, `{"current_password":"secret-value"`} {
		w := httptest.NewRecorder()
		changePasswordValidation(w, jsonTestRequest([]byte(body)))
		if strings.Contains(w.Body.String(), "secret-value") || strings.Contains(w.Body.String(), "sensitive-value") {
			t.Error("validation leaked credentials")
		}
	}
}

func changePasswordValidation(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	obj, ok := parseChangePasswordBody(w, r)
	if !ok {
		return "", "", false
	}
	return validateChangePasswordBody(w, obj)
}
