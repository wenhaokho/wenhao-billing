package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type whoami struct {
	Body struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Role   string `json:"role"`
	}
}

// sessionApp is the app on a fresh migrated database, with one admin-only
// operation that reports the principal.
type sessionApp struct {
	h        http.Handler
	pool     *pgxpool.Pool
	sessions Sessions
}

func testSessions(environment string) Sessions {
	return NewSessions(Config{Environment: environment, SessionSecret: testSecret, SessionMaxAge: 8 * time.Hour, SessionRememberMaxAge: 30 * 24 * time.Hour})
}

func newSessionApp(t *testing.T) sessionApp {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sessions := testSessions("test")
	h, api := NewServer(Config{Environment: "test"}, Deps{Pool: pool, Sessions: sessions})
	huma.Register(api, Admin(huma.Operation{OperationID: "whoami", Method: http.MethodGet, Path: "/whoami"}), func(ctx context.Context, _ *struct{}) (*whoami, error) {
		p := PrincipalFrom(ctx)
		out := &whoami{}
		out.Body.UserID, out.Body.Email, out.Body.Role = p.UserID.String(), p.Email, p.Role
		return out, nil
	})
	huma.Register(api, Admin(huma.Operation{OperationID: "panics", Method: http.MethodGet, Path: "/panics"}), func(context.Context, *struct{}) (*whoami, error) {
		var p *Principal
		_ = p.UserID // a nil pointer, as a real bug would be
		return nil, nil
	})
	// An operation that declared neither Admin nor public.
	huma.Get(api, "/undeclared", func(context.Context, *struct{}) (*whoami, error) { return &whoami{}, nil })
	return sessionApp{h: h, pool: pool, sessions: sessions}
}

// user inserts a user and returns its id.
func (a sessionApp) user(t *testing.T, email, role string, epoch int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (user_id, email, password_hash, role, session_epoch) VALUES ($1, $2, 'x', $3, $4)`,
		id, email, role, epoch)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (a sessionApp) cookie(t *testing.T, id uuid.UUID, epoch int) string {
	t.Helper()
	tok, _, err := a.sessions.Issue(id, epoch, false)
	if err != nil {
		t.Fatal(err)
	}
	return SessionCookie + "=" + tok
}

func detail(t *testing.T, body []byte) string {
	t.Helper()
	var b struct{ Detail string }
	_ = json.Unmarshal(body, &b)
	return b.Detail
}

func wantRefusal(t *testing.T, rec *httptest.ResponseRecorder, status int, sentence string) {
	t.Helper()
	if rec.Code != status || detail(t, rec.Body.Bytes()) != sentence {
		t.Errorf("got %d %s, want %d %q", rec.Code, rec.Body, status, sentence)
	}
}

func sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNoCookieIs401NotAuthenticated(t *testing.T) {
	wantRefusal(t, get(t, newSessionApp(t).h, "/whoami"), 401, "not authenticated")
}

func TestTamperedCookieIs401(t *testing.T) {
	a := newSessionApp(t)
	id := a.user(t, "a@example.com", "admin", 0)
	c := a.cookie(t, id, 0)
	wantRefusal(t, get(t, a.h, "/whoami", "Cookie", c+"x"), 401, "not authenticated")

	other, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": id.String(), "typ": "session", "epoch": 0,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("another-secret-at-least-32-bytes-long"))
	if err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, get(t, a.h, "/whoami", "Cookie", SessionCookie+"="+other), 401, "not authenticated")

	// Signed with the session secret but not a session (a reset or MCP token).
	notSession := sign(t, jwt.MapClaims{
		"sub": id.String(), "typ": "reset", "epoch": 0,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	wantRefusal(t, get(t, a.h, "/whoami", "Cookie", SessionCookie+"="+notSession), 401, "not authenticated")
}

func TestExpiredCookieIs401SessionExpired(t *testing.T) {
	a := newSessionApp(t)
	id := a.user(t, "a@example.com", "admin", 0)
	tok := sign(t, jwt.MapClaims{
		"sub": id.String(), "typ": "session", "epoch": 0,
		"iat": time.Now().Add(-9 * time.Hour).Unix(), "exp": time.Now().Add(-time.Hour).Unix(),
	})
	rec := get(t, a.h, "/whoami", "Cookie", SessionCookie+"="+tok)
	wantRefusal(t, rec, 401, "session expired")
	// Python cleared the session; the dead cookie is cleared too.
	if sc := rec.Header().Get("Set-Cookie"); !strings.HasPrefix(sc, SessionCookie+"=;") || !strings.Contains(sc, "Max-Age=0") {
		t.Errorf("Set-Cookie %q, want the session cookie cleared", sc)
	}
}

func TestOlderEpochIs401SessionExpired(t *testing.T) {
	a := newSessionApp(t)
	id := a.user(t, "a@example.com", "admin", 3)
	wantRefusal(t, get(t, a.h, "/whoami", "Cookie", a.cookie(t, id, 2)), 401, "session expired")
	if rec := get(t, a.h, "/whoami", "Cookie", a.cookie(t, id, 3)); rec.Code != 200 {
		t.Errorf("current epoch: %d %s", rec.Code, rec.Body)
	}
}

func TestNonAdminIs403AdminRequired(t *testing.T) {
	a := newSessionApp(t)
	id := a.user(t, "v@example.com", "viewer", 0)
	wantRefusal(t, get(t, a.h, "/whoami", "Cookie", a.cookie(t, id, 0)), 403, "admin required")
}

func TestDeletedUserIs403(t *testing.T) {
	a := newSessionApp(t)
	wantRefusal(t, get(t, a.h, "/whoami", "Cookie", a.cookie(t, uuid.New(), 0)), 403, "admin required")
}

func TestAdminReachesHandlerWithPrincipal(t *testing.T) {
	a := newSessionApp(t)
	id := a.user(t, "a@example.com", "admin", 0)
	rec := get(t, a.h, "/whoami", "Cookie", a.cookie(t, id, 0))
	var out struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Role   string `json:"role"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out.UserID != id.String() || out.Email != "a@example.com" || out.Role != "admin" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// Remember-me lasts RememberMaxAge, otherwise MaxAge; the token's exp agrees.
func TestIssueLifetimes(t *testing.T) {
	s := testSessions("test")
	for remember, want := range map[bool]time.Duration{false: 8 * time.Hour, true: 30 * 24 * time.Hour} {
		tok, maxAge, err := s.Issue(uuid.New(), 0, remember)
		if err != nil || maxAge != want {
			t.Fatalf("remember=%v: %v %v", remember, maxAge, err)
		}
		claims, err := s.Tokens.Parse(tok)
		if err != nil {
			t.Fatal(err)
		}
		exp, _ := claims.GetExpirationTime()
		if d := time.Until(exp.Time); d < want-time.Minute || d > want {
			t.Errorf("remember=%v: exp in %v, want %v", remember, d, want)
		}
		if claims["typ"] != "session" {
			t.Errorf("typ %v", claims["typ"])
		}
	}
}

func TestUndeclaredOperationIsRefused(t *testing.T) {
	a := newSessionApp(t)
	id := a.user(t, "a@example.com", "admin", 0)
	if rec := get(t, a.h, "/undeclared", "Cookie", a.cookie(t, id, 0)); rec.Code != 403 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// The server's own operations are each admin-only or public; the features'
// operations are walked by cmd/api's test of the same name.
func TestEveryOperationIsAdminOrPublic(t *testing.T) {
	_, api := NewServer(Config{Environment: "test"}, Deps{})
	checked := 0
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op == nil {
				continue
			}
			if !IsPublic(op) && !IsAdmin(op) {
				t.Errorf("%s %s is neither Admin nor public", op.Method, path)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Error("no operation was checked")
	}
	marked := Admin(huma.Operation{})
	if IsAdmin(&huma.Operation{}) || !IsAdmin(&marked) {
		t.Error("IsAdmin does not read Admin's mark")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	for env, secure := range map[string]bool{"production": true, "prod": true, "": true, "development": false, "test": false} {
		s := testSessions(env)
		rec := httptest.NewRecorder()
		s.SetCookie(rec, "tok", 8*time.Hour)
		resp := http.Response{Header: rec.Header()}
		cs := resp.Cookies()
		if len(cs) != 1 {
			t.Fatalf("%q: %d cookies", env, len(cs))
		}
		c := cs[0]
		if c.Name != SessionCookie || c.Value != "tok" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 28800 || c.Secure != secure {
			t.Errorf("%q: %+v", env, c)
		}

		rec = httptest.NewRecorder()
		s.ClearCookie(rec)
		resp = http.Response{Header: rec.Header()}
		if cs := resp.Cookies(); len(cs) != 1 || cs[0].Name != SessionCookie || cs[0].MaxAge >= 0 || cs[0].Path != "/" || cs[0].Secure != secure {
			t.Errorf("%q clear: %+v", env, cs)
		}
	}
}

func TestHealthStaysPublic(t *testing.T) {
	if rec := get(t, newSessionApp(t).h, "/health"); rec.Code != 200 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// A panic is a 500 problem body (the frontend can read it), never a bare
// body, and the server keeps serving.
func TestAPanicIsAProblem500(t *testing.T) {
	a := newSessionApp(t)
	id := a.user(t, "a@example.com", "admin", 0)
	rec := get(t, a.h, "/panics", "Cookie", a.cookie(t, id, 0))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "An unexpected error occurred") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if rec := get(t, a.h, "/health"); rec.Code != 200 {
		t.Errorf("after a panic: %d", rec.Code)
	}
}
