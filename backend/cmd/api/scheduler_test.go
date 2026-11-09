package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/jobs"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// schedulerPool opens a pool on a migrated database.
func schedulerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestSchedulerDoesNotStartWithoutTheVariable: the scheduler is off unless
// SCHEDULER_ENABLED says otherwise (Go change 8), so the API does not run
// the jobs beside the Python backend.
func TestSchedulerDoesNotStartWithoutTheVariable(t *testing.T) {
	t.Parallel()
	cfg, err := platform.ParseConfig(map[string]string{
		"DATABASE_URL": "postgresql://u:p@h:5432/d", "SESSION_SECRET": "scheduler-test-secret-32-bytes-long",
		"WEBHOOK_HMAC_SECRET": "a-real-webhook-secret", "EMAIL_WEBHOOK_TOKEN": "a-real-email-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchedulerEnabled {
		t.Fatal("the config turns the scheduler on with SCHEDULER_ENABLED unset")
	}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	got, wait := startScheduler(t.Context(), schedulerPool(t), cfg, platform.SystemClock{}, logger, email.LogSender{})
	if got != nil {
		t.Errorf("the scheduler started %d jobs with SCHEDULER_ENABLED unset, want none", len(got))
	}
	if wait != nil {
		t.Error("the scheduler returned a wait function with SCHEDULER_ENABLED unset, want nil")
	}
}

// TestSchedulerStartsWithTheVariable: SCHEDULER_ENABLED=true starts the
// jobs, with Python's ids.
func TestSchedulerStartsWithTheVariable(t *testing.T) {
	t.Parallel()
	cfg, err := platform.ParseConfig(map[string]string{
		"DATABASE_URL": "postgresql://u:p@h:5432/d", "SESSION_SECRET": "scheduler-test-secret-32-bytes-long",
		"WEBHOOK_HMAC_SECRET": "a-real-webhook-secret", "EMAIL_WEBHOOK_TOKEN": "a-real-email-token",
		"SCHEDULER_ENABLED": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SchedulerEnabled {
		t.Fatal("the config leaves the scheduler off with SCHEDULER_ENABLED=true")
	}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	ctx, cancel := context.WithCancel(t.Context())
	got, wait := startScheduler(ctx, schedulerPool(t), cfg, platform.SystemClock{}, logger, email.LogSender{})
	if wait == nil {
		t.Fatal("the scheduler started without a wait function")
	}
	want := []string{jobs.RecurringScanID, jobs.UsageLockScanID, jobs.HostingEnforcementID, jobs.FXRatesSyncID}
	if len(got) != len(want) {
		cancel()
		wait()
		t.Fatalf("the scheduler started %d jobs, want %d", len(got), len(want))
	}
	for i, job := range got {
		if job.ID != want[i] {
			t.Errorf("job %d is %q, want %q", i, job.ID, want[i])
		}
	}
	// The hosting job is the integrations job: its id, its daily 04:00 UTC
	// slot and its Python lock key.
	hosting := got[2]
	if hosting.ID != jobs.HostingEnforcementID {
		t.Errorf("hosting job id = %q, want %q", hosting.ID, jobs.HostingEnforcementID)
	}
	if got := jobs.LockKey(hosting.ID); got != 1601569364 {
		t.Errorf("LockKey(%q) = %d, want Python's 1601569364", hosting.ID, got)
	}
	now := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	if due := hosting.Due(now); !due.Equal(time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)) {
		t.Errorf("hosting job due at %s = %s, want 2026-11-10 04:00 UTC", now, due)
	}
	if hosting.Run == nil {
		t.Error("the hosting job has no work")
	}
	cancel()
	wait()
}

// TestSchedulerWaitJoinsTheScheduler: serve waits for the scheduler's
// goroutine to stop before it closes the database pool. The wait function
// must therefore block until the context is cancelled and then return.
func TestSchedulerWaitJoinsTheScheduler(t *testing.T) {
	t.Parallel()
	cfg, err := platform.ParseConfig(map[string]string{
		"DATABASE_URL": "postgresql://u:p@h:5432/d", "SESSION_SECRET": "scheduler-test-secret-32-bytes-long",
		"WEBHOOK_HMAC_SECRET": "a-real-webhook-secret", "EMAIL_WEBHOOK_TOKEN": "a-real-email-token",
		"SCHEDULER_ENABLED": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	ctx, cancel := context.WithCancel(t.Context())
	_, wait := startScheduler(ctx, schedulerPool(t), cfg, platform.SystemClock{}, logger, email.LogSender{})
	if wait == nil {
		t.Fatal("the scheduler started without a wait function")
	}
	cancel()
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not return after the context was cancelled: serve must not close the pool before this")
	}
}

// The hosting enforcement is wired into the scheduler now: its id is
// Python's, its lock key is Python's, and its slot is daily 04:00 UTC.
func TestTheHostingJobIsReadyForTheIntegrationsPlan(t *testing.T) {
	t.Parallel()
	job := jobs.HostingEnforcement(func(context.Context, time.Time) error { return nil })
	if job.ID != jobs.HostingEnforcementID {
		t.Errorf("the hosting job's id = %q, want %q", job.ID, jobs.HostingEnforcementID)
	}
	if job.ID != "hosting-overdue-enforcement" {
		t.Errorf("the hosting job's id = %q", job.ID)
	}
	if got := jobs.LockKey(job.ID); got != 1601569364 {
		t.Errorf("LockKey(%q) = %d, want Python's 1601569364", job.ID, got)
	}
	now := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	if due := job.Due(now); !due.Equal(time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)) {
		t.Errorf("hosting job due at %s = %s, want 2026-11-10 04:00 UTC", now, due)
	}
	if job.Due == nil || job.Run == nil {
		t.Error("the hosting job has no slot or no work")
	}
}

// testWriter writes the log lines into the test's output.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
