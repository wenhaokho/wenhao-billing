package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// cliApp is a migrated database and the full API on it.
type cliApp struct {
	pool     *pgxpool.Pool
	sessions platform.Sessions
	h        http.Handler
}

func newCLIApp(t *testing.T) cliApp {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg := platform.Config{Environment: "test", SessionSecret: testSecret, SessionMaxAge: 8 * time.Hour, SessionRememberMaxAge: 30 * 24 * time.Hour}
	s := platform.NewSessions(cfg)
	h, api := platform.NewServer(cfg, platform.Deps{Pool: pool, Sessions: s})
	register(api, features{Pool: pool, Sessions: s, Mail: email.LogSender{}, Config: cfg, Clock: platform.SystemClock{}})
	return cliApp{pool: pool, sessions: s, h: h}
}

// user inserts a user with a Python-era bcrypt hash.
func (a cliApp) user(t *testing.T, addr, role string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (user_id, email, password_hash, role, password_reset_token, password_reset_expires)
		 VALUES ($1, $2, '$2b$12$KIXQJxQ0r6aQyJ8ZtV1c7eWlqfN7mJr0pQh7f4o0v2h5i6s9Yb5yW', $3, 'pending', '2030-01-01')`,
		id, addr, role); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a cliApp) epoch(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), `SELECT session_epoch FROM users WHERE user_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (a cliApp) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func TestSetPasswordThenLogin(t *testing.T) {
	t.Parallel()
	a := newCLIApp(t)
	a.user(t, "admin@example.com", "admin")
	if err := setPasswordCmd(context.Background(), a.pool, "admin@example.com", strings.NewReader("a-new-password\r\n")); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"a-new-password"}`))
	req.Header.Set("Content-Type", "application/json")
	if rec := a.serve(req); rec.Code != http.StatusOK {
		t.Errorf("login: %d %s", rec.Code, rec.Body)
	}
	if err := setPasswordCmd(context.Background(), a.pool, "nobody@example.com", strings.NewReader("a-new-password\n")); err == nil {
		t.Error("set-password for an unknown email succeeded")
	}
	if err := setPasswordCmd(context.Background(), a.pool, "admin@example.com", strings.NewReader("short\n")); err == nil {
		t.Error("set-password accepted a 5-character password")
	}
}

func TestSetPasswordBumpsEpoch(t *testing.T) {
	t.Parallel()
	a := newCLIApp(t)
	id := a.user(t, "admin@example.com", "admin")
	if err := setPasswordCmd(context.Background(), a.pool, "admin@example.com", strings.NewReader("a-new-password")); err != nil {
		t.Fatal(err)
	}
	if n := a.epoch(t, id); n != 1 {
		t.Errorf("epoch %d, want 1", n)
	}
	var token *string
	if err := a.pool.QueryRow(context.Background(), `SELECT password_reset_token FROM users WHERE user_id = $1`, id).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token != nil {
		t.Errorf("reset token %q kept", *token)
	}
}

func TestIssueSessionTokenOpensMe(t *testing.T) {
	t.Parallel()
	a := newCLIApp(t)
	id := a.user(t, "admin@example.com", "admin")
	var out bytes.Buffer
	if err := issueSessionCmd(context.Background(), a.pool, a.sessions, "admin@example.com", &out); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(out.String())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Cookie", platform.SessionCookie+"="+token)
	if rec := a.serve(req); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), id.String()) {
		t.Errorf("me: %d %s", rec.Code, rec.Body)
	}
	if n := a.epoch(t, id); n != 0 {
		t.Errorf("epoch %d: issue-session must write nothing", n)
	}
}

func TestIssueSessionRefusesNonAdmin(t *testing.T) {
	t.Parallel()
	a := newCLIApp(t)
	a.user(t, "viewer@example.com", "viewer")
	var out bytes.Buffer
	if err := issueSessionCmd(context.Background(), a.pool, a.sessions, "viewer@example.com", &out); err == nil {
		t.Error("issued a session for a viewer")
	}
	if err := issueSessionCmd(context.Background(), a.pool, a.sessions, "nobody@example.com", &out); err == nil {
		t.Error("issued a session for an unknown email")
	}
	if out.Len() != 0 {
		t.Errorf("printed %q", out.String())
	}
}

// serve shows email bodies (live reset links) in the log only for a local
// environment.
func TestMailSenderShowsBodiesOnlyLocally(t *testing.T) {
	t.Parallel()
	for env, want := range map[string]bool{"development": true, "test": true, "production": false, "staging": false, "prod": false} {
		if got := mailSender(platform.Config{Environment: env}, nil).ShowBodies; got != want {
			t.Errorf("%s: ShowBodies %v, want %v", env, got, want)
		}
	}
}

// With RESEND_API_KEY the API sends through Resend; without it, mail is logged.
func TestPickSenderResendOnlyWithAKey(t *testing.T) {
	t.Parallel()
	if _, ok := pickSender(platform.Config{}, nil).(email.LogSender); !ok {
		t.Errorf("no key: sender is not the LogSender")
	}
	if _, ok := pickSender(platform.Config{ResendAPIKey: "re_x"}, nil).(email.LogSender); ok {
		t.Errorf("with a key: sender is still the LogSender")
	}
}

// An empty RESEND_API_KEY outside development and test logs one Warn; a local
// environment or a key stays quiet.
func TestPickSenderWarnsOnceWithoutAKeyInProduction(t *testing.T) {
	t.Parallel()
	for env, want := range map[string]int{"production": 1, "staging": 1, "development": 0, "test": 0} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			pickSender(platform.Config{Environment: env}, slog.New(slog.NewTextHandler(&buf, nil)))
			if got := strings.Count(buf.String(), "mail is logged, not sent"); got != want {
				t.Errorf("%d warnings, want %d: %q", got, want, buf.String())
			}
		})
	}
	t.Run("with a key", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		pickSender(platform.Config{Environment: "production", ResendAPIKey: "re_x"}, slog.New(slog.NewTextHandler(&buf, nil)))
		if buf.Len() != 0 {
			t.Errorf("logged %q", buf.String())
		}
	})
}
