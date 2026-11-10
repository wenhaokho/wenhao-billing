package platform

import (
	"testing"

	"github.com/getsentry/sentry-go"
)

// TestNewServerDoesNotEnableSentry ports
// tests/unit/test_sentry_disabled_in_tests.py: building the app must not turn
// error reporting on, even when the configuration carries a Sentry DSN and a
// production environment (the dev container has a real SENTRY_DSN). Only
// serve calls observe.InitSentry, so an app a test builds - apitest.New goes
// through NewServer - never makes the deliberate failures of a failure-path
// test report to the live project.
func TestNewServerDoesNotEnableSentry(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Environment:       "production",
		SentryDSN:         "https://public-key@example.invalid/1",
		SentryEnvironment: "production",
	}
	// NewServer registers the health operation and the middleware; it never
	// reads the pool, so no database is needed.
	if handler, _ := NewServer(cfg, Deps{}); handler == nil {
		t.Fatal("NewServer returned no handler")
	}
	// CurrentHub carries a no-op client until observe.InitSentry binds one; a
	// bound client is active and its DSN is set.
	if dsn := sentry.CurrentHub().Client().Options().Dsn; dsn != "" {
		t.Errorf("Sentry client is active after NewServer: dsn %q", dsn)
	}
}
