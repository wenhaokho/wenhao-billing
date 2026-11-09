// Package apitest serves the API with a feature's routes on a fresh migrated
// database, signed in as an admin, for the features' tests.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// Start is the clock's time when New returns.
var Start = time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)

// AdminEmail is the signed-in admin's address.
const AdminEmail = "admin@example.com"

// secret is 32 bytes, the shortest Config accepts.
const secret = "apitest-session-secret-32-bytes!"

// Deps are what a feature's Register takes.
type Deps struct {
	Pool     *pgxpool.Pool
	Sessions platform.Sessions
	Mail     *Mail // records the messages sent
	Config   platform.Config
	Clock    *testkit.Clock
}

// App is the API under test.
type App struct {
	Deps
	Handler http.Handler
	// Cookie is the request cookie ("billing_session=…") of a signed-in admin.
	Cookie  string
	AdminID uuid.UUID
}

// New serves the operations register adds, on a fresh database, with the
// clock at Start and one admin signed in. The admin's password hash is a
// placeholder: the session is issued directly, never through /auth/login.
func New(t *testing.T, register func(api huma.API, d Deps)) *App {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	clock := testkit.NewClock(Start)
	cfg := platform.Config{
		Environment: "test", SessionSecret: secret,
		SessionMaxAge: 8 * time.Hour, SessionRememberMaxAge: 30 * 24 * time.Hour,
		AppBaseURL: "http://app.test/",
	}
	sessions := platform.NewSessions(cfg)
	sessions.Clock = clock
	handler, api := platform.NewServer(cfg, platform.Deps{Pool: pool, Clock: clock, Sessions: sessions})
	d := Deps{Pool: pool, Sessions: sessions, Mail: &Mail{}, Config: cfg, Clock: clock}
	register(api, d)

	// Python's test rates (tests/conftest.py _seed_test_fx_rates), so a
	// foreign-currency ledger test converts without building rates itself.
	if _, err := pool.Exec(ctx, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES ('USD', 'IDR', 16000, '2026-01-01', 'TEST'), ('SGD', 'IDR', 12000, '2026-01-01', 'TEST')`); err != nil {
		t.Fatal(err)
	}

	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, $2, 'placeholder: never signs in', 'admin')`,
		adminID, AdminEmail); err != nil {
		t.Fatal(err)
	}
	token, _, err := sessions.Issue(adminID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	cookie := platform.SessionCookie + "=" + token
	return &App{Deps: d, Handler: handler, Cookie: cookie, AdminID: adminID}
}

// Do sends a request as the admin. body is nil (no body), a string (sent as
// is) or any other value (sent as JSON).
func (a *App) Do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return a.send(t, method, path, body, a.Cookie)
}

// Anon sends a request with no session cookie.
func (a *App) Anon(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return a.send(t, method, path, body, "")
}

func (a *App) send(t *testing.T, method, path string, body any, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		j, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(j)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, req)
	return rec
}

// Decode reads rec's JSON body, failing the test (showing the body) when the
// status is not status.
func Decode[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	var v T
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body)
	}
	return v
}

// Mail records the messages the routes send.
type Mail struct {
	mu   sync.Mutex
	sent []email.Message
}

// Send records m.
func (m *Mail) Send(_ context.Context, msg email.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

// Messages are the messages sent so far, oldest first.
func (m *Mail) Messages() []email.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]email.Message(nil), m.sent...)
}
