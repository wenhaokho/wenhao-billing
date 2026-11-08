package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

const (
	testSecret = "test-secret-at-least-32-bytes-long!!"
	password   = "test-password"
	adminEmail = "session-admin@example.com"
)

var start = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// fakeMail records what the routes send.
type fakeMail struct {
	mu   sync.Mutex
	sent []email.Message
	err  error
}

func (f *fakeMail) Send(_ context.Context, m email.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return f.err
}

func (f *fakeMail) messages() []email.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]email.Message(nil), f.sent...)
}

// app is the API with the auth routes on a fresh migrated database; every
// time decision reads clock.
type app struct {
	h     http.Handler
	pool  *pgxpool.Pool
	clock *testkit.Clock
	mail  *fakeMail
	cfg   platform.Config
}

func newApp(t *testing.T) app {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	clock := testkit.NewClock(start)
	cfg := platform.Config{
		Environment: "test", SessionSecret: testSecret,
		SessionMaxAge: 8 * time.Hour, SessionRememberMaxAge: 30 * 24 * time.Hour,
		AppBaseURL: "http://app.test/",
	}
	s := platform.NewSessions(cfg)
	s.Clock = clock
	h, api := platform.NewServer(cfg, platform.Deps{Pool: pool, Clock: clock, Sessions: s})
	mail := &fakeMail{}
	Register(api, pool, s, mail, cfg, clock)
	return app{h: h, pool: pool, clock: clock, mail: mail, cfg: cfg}
}

// user inserts a user whose password is password.
func (a app) user(t *testing.T, addr, role string) uuid.UUID {
	t.Helper()
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, $2, $3, $4)`, id, addr, hash, role); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a app) do(t *testing.T, method, path, body string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func (a app) login(t *testing.T, addr, pw string, remember bool, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"email": addr, "password": pw, "remember": remember})
	return a.do(t, http.MethodPost, "/api/v1/auth/login", string(body), header...)
}

// mustLogin signs in and returns the "billing_session=…" request cookie.
func (a app) mustLogin(t *testing.T, addr string, remember bool) string {
	t.Helper()
	rec := a.login(t, addr, password, remember)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	return cookieHeader(t, rec)
}

func (a app) me(t *testing.T, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	return a.do(t, http.MethodGet, "/api/v1/auth/me", "", "Cookie", cookie)
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == platform.SessionCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", platform.SessionCookie, rec.Header().Values("Set-Cookie"))
	return nil
}

func cookieHeader(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return platform.SessionCookie + "=" + sessionCookie(t, rec).Value
}

type problem struct {
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

func problemOf(rec *httptest.ResponseRecorder) problem {
	var p problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return p
}

func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, detail string) {
	t.Helper()
	if rec.Code != status || problemOf(rec).Detail != detail {
		t.Fatalf("got %d %s, want %d %q", rec.Code, rec.Body, status, detail)
	}
}

func bodyOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return out
}

func TestLoginSetsCookieAndReturnsUser(t *testing.T) {
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	rec := a.login(t, adminEmail, password, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	out := bodyOf(t, rec)
	if out["user_id"] != id.String() || out["email"] != adminEmail || out["role"] != "admin" || out["display_name"] != nil {
		t.Errorf("body %v", out)
	}
	if _, ok := out["display_name"]; !ok {
		t.Error("display_name missing; Python sent null")
	}
	if s, _ := out["created_at"].(string); !strings.HasSuffix(s, "Z") {
		t.Errorf("created_at %q, want a UTC timestamp", out["created_at"])
	}
	c := sessionCookie(t, rec)
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie %+v", c)
	}
	if rec := a.me(t, cookieHeader(t, rec)); rec.Code != http.StatusOK || bodyOf(t, rec)["user_id"] != id.String() {
		t.Errorf("me: %d %s", rec.Code, rec.Body)
	}
}

func TestLoginRememberSetsThirtyDayCookie(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	rec := a.login(t, adminEmail, password, true)
	if c := sessionCookie(t, rec); c.MaxAge != 30*24*3600 {
		t.Errorf("Max-Age %d, want 30 days", c.MaxAge)
	}
}

// Ports test_login_defaults_to_no_remember: remember is optional.
func TestLoginDefaultsToEightHours(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	rec := a.do(t, http.MethodPost, "/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if c := sessionCookie(t, rec); c.MaxAge != 8*3600 {
		t.Errorf("Max-Age %d, want 8 hours", c.MaxAge)
	}
	if rec := a.me(t, cookieHeader(t, rec)); rec.Code != http.StatusOK {
		t.Errorf("me: %d %s", rec.Code, rec.Body)
	}
}

func TestShortSessionExpiresAfterDefaultTTL(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	c := a.mustLogin(t, adminEmail, false)
	if rec := a.me(t, c); rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	a.clock.Advance(8*time.Hour + time.Minute)
	wantError(t, a.me(t, c), http.StatusUnauthorized, "session expired")
}

func TestRememberSessionSurvivesShortTTL(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	c := a.mustLogin(t, adminEmail, true)
	a.clock.Advance(8*time.Hour + time.Hour)
	if rec := a.me(t, c); rec.Code != http.StatusOK {
		t.Errorf("me: %d %s", rec.Code, rec.Body)
	}
}

func TestRememberSessionExpiresAfterRememberTTL(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	c := a.mustLogin(t, adminEmail, true)
	a.clock.Advance(30*24*time.Hour + time.Minute)
	wantError(t, a.me(t, c), http.StatusUnauthorized, "session expired")
}

func noSessionCookie(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == platform.SessionCookie && c.Value != "" {
			t.Errorf("a session cookie was set: %v", c)
		}
	}
}

func TestLoginWrongPasswordIs401InvalidCredentials(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	rec := a.login(t, adminEmail, "wrong-password", false)
	wantError(t, rec, http.StatusUnauthorized, "invalid credentials")
	noSessionCookie(t, rec)
}

func TestLoginUnknownEmailIs401InvalidCredentials(t *testing.T) {
	a := newApp(t)
	rec := a.login(t, "nobody@example.com", password, false)
	wantError(t, rec, http.StatusUnauthorized, "invalid credentials")
	noSessionCookie(t, rec)
}

// Python stored bcrypt hashes; until set-password replaces one, signing in
// with it is refused like a wrong password, never a 500.
func TestLoginWithBcryptHashIsInvalidCredentials(t *testing.T) {
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	if _, err := a.pool.Exec(context.Background(), `UPDATE users SET password_hash = $2 WHERE user_id = $1`,
		id, "$2b$12$KIXQJxQ0r6aQyJ8ZtV1c7eWlqfN7mJr0pQh7f4o0v2h5i6s9Yb5yW"); err != nil {
		t.Fatal(err)
	}
	wantError(t, a.login(t, adminEmail, password, false), http.StatusUnauthorized, "invalid credentials")
}

func TestLogoutClearsCookieAnd204(t *testing.T) {
	a := newApp(t)
	rec := a.do(t, http.MethodPost, "/api/v1/auth/logout", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if c := sessionCookie(t, rec); c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("cookie %+v, want it cleared", c)
	}
}

func TestMeReturnsUser(t *testing.T) {
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	if _, err := a.pool.Exec(context.Background(), `UPDATE users SET display_name = 'Wen' WHERE user_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	rec := a.me(t, a.mustLogin(t, adminEmail, false))
	out := bodyOf(t, rec)
	if rec.Code != http.StatusOK || out["user_id"] != id.String() || out["display_name"] != "Wen" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	wantError(t, a.do(t, http.MethodGet, "/api/v1/auth/me", ""), http.StatusUnauthorized, "not authenticated")
}

func (a app) changePassword(t *testing.T, cookie, current, next string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	return a.do(t, http.MethodPost, "/api/v1/auth/change-password", string(body), "Cookie", cookie)
}

// Wrong current password is 400, not 401: the frontend signs out on any 401.
func TestChangePasswordWrongCurrentIs400(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	c := a.mustLogin(t, adminEmail, false)
	wantError(t, a.changePassword(t, c, "wrong-password", "brand-new-pw"), http.StatusBadRequest, "current password is incorrect")
}

func TestChangePasswordSameIs400(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	c := a.mustLogin(t, adminEmail, false)
	wantError(t, a.changePassword(t, c, password, password), http.StatusBadRequest, "new password must differ from current password")
}

func TestChangePasswordSignsOutOtherSessions(t *testing.T) {
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	mine := a.mustLogin(t, adminEmail, true)
	a.clock.Advance(time.Second)
	other := a.mustLogin(t, adminEmail, false)
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE users SET password_reset_token = 'x', password_reset_expires = '2030-01-01' WHERE user_id = $1`, id); err != nil {
		t.Fatal(err)
	}

	rec := a.changePassword(t, mine, password, "brand-new-pw")
	if rec.Code != http.StatusOK || bodyOf(t, rec)["user_id"] != id.String() {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// This browser keeps a remembered session.
	if c := sessionCookie(t, rec); c.MaxAge != 30*24*3600 {
		t.Errorf("re-issued cookie Max-Age %d, want the remembered 30 days", c.MaxAge)
	}
	renewed := cookieHeader(t, rec)

	wantError(t, a.me(t, other), http.StatusUnauthorized, "session expired")
	wantError(t, a.me(t, mine), http.StatusUnauthorized, "session expired")
	if rec := a.me(t, renewed); rec.Code != http.StatusOK {
		t.Errorf("renewed cookie: %d %s", rec.Code, rec.Body)
	}
	var token *string
	if err := a.pool.QueryRow(context.Background(), `SELECT password_reset_token FROM users WHERE user_id = $1`, id).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token != nil {
		t.Errorf("reset token %q survived the change", *token)
	}
	wantError(t, a.login(t, adminEmail, password, false), http.StatusUnauthorized, "invalid credentials")
	if rec := a.login(t, adminEmail, "brand-new-pw", false); rec.Code != http.StatusOK {
		t.Errorf("new password: %d %s", rec.Code, rec.Body)
	}
}

const genericForgot = `{"message":"If an account exists for that email, a reset link has been sent.","reset_token":null,"reset_link":null}`

func (a app) forgot(t *testing.T, addr string) *httptest.ResponseRecorder {
	t.Helper()
	return a.do(t, http.MethodPost, "/api/v1/auth/forgot-password", `{"email":"`+addr+`"}`)
}

func wantGeneric(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != genericForgot {
		t.Errorf("got %d %s, want 200 %s", rec.Code, rec.Body, genericForgot)
	}
}

func TestForgotPasswordIsGenericForUnknownEmail(t *testing.T) {
	a := newApp(t)
	wantGeneric(t, a.forgot(t, "nobody@example.com"))
	if n := len(a.mail.messages()); n != 0 {
		t.Errorf("%d emails sent for an unknown address", n)
	}
	// A known address answers the same, even when sending fails.
	a.user(t, adminEmail, "admin")
	a.mail.err = context.DeadlineExceeded
	wantGeneric(t, a.forgot(t, adminEmail))
}

var tokenInLink = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

// forgotToken requests a reset for adminEmail and returns the emailed token.
func (a app) forgotToken(t *testing.T) string {
	t.Helper()
	wantGeneric(t, a.forgot(t, adminEmail))
	sent := a.mail.messages()
	if len(sent) == 0 {
		t.Fatal("no email sent")
	}
	m := tokenInLink.FindStringSubmatch(sent[len(sent)-1].Text)
	if m == nil {
		t.Fatalf("no token in %q", sent[len(sent)-1].Text)
	}
	return m[1]
}

func TestForgotPasswordStoresHashNotToken(t *testing.T) {
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	token := a.forgotToken(t)
	if len(token) != 43 {
		t.Errorf("token %q: want token_urlsafe(32), 43 characters", token)
	}
	link := "http://app.test/reset-password?token=" + token
	m := a.mail.messages()[0]
	wantText := "Hi,\n\n" +
		"A password reset was requested for your wenhao-billing account.\n" +
		"Open the link below within the next hour to choose a new password:\n\n" +
		link + "\n\n" +
		"If you didn't request this, you can ignore this email — your current password will stay active.\n"
	wantHTML := "<p>Hi,</p>" +
		"<p>A password reset was requested for your wenhao-billing account.</p>" +
		`<p><a href="` + link + `">Reset your password</a> (link expires in 1 hour).</p>` +
		"<p>If you didn't request this, you can ignore this email.</p>"
	if m.To != adminEmail || m.Subject != "Reset your wenhao-billing password" || m.Text != wantText || m.HTML != wantHTML {
		t.Errorf("email %+v", m)
	}

	var stored string
	var expires time.Time
	if err := a.pool.QueryRow(context.Background(),
		`SELECT password_reset_token, password_reset_expires FROM users WHERE user_id = $1`, id).Scan(&stored, &expires); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	if stored != hex.EncodeToString(sum[:]) {
		t.Errorf("stored %q, want sha256(token)", stored)
	}
	if !expires.Equal(start.Add(time.Hour)) {
		t.Errorf("expires %v, want %v", expires, start.Add(time.Hour))
	}
}

func (a app) reset(t *testing.T, token, next string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": token, "new_password": next})
	return a.do(t, http.MethodPost, "/api/v1/auth/reset-password", string(body))
}

func TestResetPasswordConsumesToken(t *testing.T) {
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	before := a.mustLogin(t, adminEmail, false)
	token := a.forgotToken(t)

	rec := a.reset(t, token, "brand-new-pw")
	if rec.Code != http.StatusOK || bodyOf(t, rec)["user_id"] != id.String() {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := a.login(t, adminEmail, "brand-new-pw", false); rec.Code != http.StatusOK {
		t.Errorf("new password: %d %s", rec.Code, rec.Body)
	}
	wantError(t, a.me(t, before), http.StatusUnauthorized, "session expired")
	wantError(t, a.reset(t, token, "another-pw"), http.StatusBadRequest, "invalid or expired reset token")
	wantError(t, a.reset(t, "not-a-token", "another-pw"), http.StatusBadRequest, "invalid or expired reset token")
}

func TestResetPasswordExpiredTokenIs400AndCleared(t *testing.T) {
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	token := a.forgotToken(t)
	a.clock.Advance(time.Hour + time.Second)
	wantError(t, a.reset(t, token, "brand-new-pw"), http.StatusBadRequest, "invalid or expired reset token")
	var stored, expires any
	if err := a.pool.QueryRow(context.Background(),
		`SELECT password_reset_token, password_reset_expires FROM users WHERE user_id = $1`, id).Scan(&stored, &expires); err != nil {
		t.Fatal(err)
	}
	if stored != nil || expires != nil {
		t.Errorf("expired token kept: %v %v", stored, expires)
	}
	if rec := a.login(t, adminEmail, password, false); rec.Code != http.StatusOK {
		t.Errorf("old password: %d %s", rec.Code, rec.Body)
	}
}

func TestPasswordLengthIs422(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	c := a.mustLogin(t, adminEmail, false)
	for _, pw := range []string{"12345", strings.Repeat("x", 129)} {
		if rec := a.changePassword(t, c, password, pw); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("change-password %d chars: %d %s", len(pw), rec.Code, rec.Body)
		}
		if rec := a.reset(t, "some-token", pw); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("reset-password %d chars: %d %s", len(pw), rec.Code, rec.Body)
		}
	}
	// The bounds themselves are accepted.
	if rec := a.changePassword(t, c, password, "123456"); rec.Code != http.StatusOK {
		t.Errorf("6 characters: %d %s", rec.Code, rec.Body)
	}
}
