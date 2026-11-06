package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestSessionHTTP(t *testing.T) {
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
	hash, err := auth.Hash("secret-pass", auth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO users (user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com',$1,'admin'),('00000000-0000-0000-0000-000000000002','user@example.com',$1,'user'),('00000000-0000-0000-0000-000000000003','old@example.com','$2b$12$invalid','admin'),('00000000-0000-0000-0000-000000000004','user@bücher.de',$1,'admin')`, hash)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", Secure: true, SameSite: http.SameSiteStrictMode}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
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
		if strings.Contains(w.Body.String(), "secret-pass") || strings.Contains(w.Body.String(), string(cfg.Secret)) || strings.Contains(w.Body.String(), hash) {
			t.Error("secret leaked")
		}
		return w
	}
	login := func(body string) *httptest.ResponseRecorder { return call("POST", "/api/v1/auth/login", body, nil) }
	for _, body := range []string{`{"email":"unknown@example.com","password":"secret-pass"}`, `{"email":"admin@example.com","password":"wrong"}`, `{"email":"old@example.com","password":"secret-pass"}`} {
		if w := login(body); w.Code != 401 || !strings.Contains(w.Body.String(), "invalid credentials") {
			t.Fatalf("invalid login: %d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"email":"bad","password":"x"}`, `{"email":"admin@example.com"}`, `{"email":"admin@example.com","password":"x","remember":"bad"}`} {
		if w := login(body); w.Code != 422 {
			t.Fatalf("validation: %d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"email":" admin@EXAMPLE.COM ","password":"secret-pass"}`, `{"email":"Admin <admin@EXAMPLE.COM>","password":"secret-pass","remember":"yes"}`, `{"email":"user@xn--bcher-kva.de","password":"secret-pass"}`} {
		if result := login(body); result.Code != 200 {
			t.Fatalf("normalized email login: %d %s", result.Code, result.Body.String())
		}
	}
	w := login(`{"email":"admin@example.com","password":"secret-pass"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"user_id"`) {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != int((8*time.Hour).Seconds()) {
		t.Fatalf("cookie: %+v", c)
	}
	if w = call("GET", "/api/v1/auth/me", "", c); w.Code != 200 {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []*http.Cookie{nil, {Name: c.Name, Value: "old.invalid"}, {Name: c.Name, Value: c.Value + "x"}} {
		if w = call("GET", "/api/v1/auth/me", "", bad); w.Code != 401 {
			t.Fatalf("invalid cookie %d", w.Code)
		}
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001", "exp": time.Now().Add(-time.Hour).Unix()}).SignedString(cfg.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if w = call("GET", "/api/v1/auth/me", "", &http.Cookie{Name: "session", Value: expired}); w.Code != 401 || !strings.Contains(w.Body.String(), "session expired") {
		t.Fatal("expired cookie accepted")
	}
	claims, err := (auth.Tokens{Secret: cfg.Secret}).Parse(c.Value)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || time.Until(exp.Time) < 8*time.Hour-time.Minute || time.Until(exp.Time) > 8*time.Hour {
		t.Fatal("short effective TTL")
	}
	remembered := login(`{"email":"admin@example.com","password":"secret-pass","remember":true}`).Result().Cookies()[0]
	if remembered.MaxAge != int((30 * 24 * time.Hour).Seconds()) {
		t.Fatal("remember TTL")
	}
	claims, err = (auth.Tokens{Secret: cfg.Secret}).Parse(remembered.Value)
	if err != nil {
		t.Fatal(err)
	}
	exp, err = claims.GetExpirationTime()
	if err != nil || time.Until(exp.Time) < 30*24*time.Hour-time.Minute || time.Until(exp.Time) > 30*24*time.Hour {
		t.Fatal("remembered effective TTL")
	}
	user := login(`{"email":"user@example.com","password":"secret-pass"}`).Result().Cookies()[0]
	if w = call("GET", "/api/v1/auth/me", "", user); w.Code != 403 {
		t.Fatal("non-admin accepted")
	}
	if _, err = pool.Exec(context.Background(), `DELETE FROM users WHERE email='admin@example.com'`); err != nil {
		t.Fatal(err)
	}
	if w = call("GET", "/api/v1/auth/me", "", c); w.Code != 403 {
		t.Fatal("deleted user accepted")
	}
	w = call("POST", "/api/v1/auth/logout", "", c)
	if w.Code != 204 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatal("logout did not clear")
	}
}
