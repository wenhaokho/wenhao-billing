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
	got := startScheduler(t.Context(), schedulerPool(t), cfg, platform.SystemClock{}, logger, email.LogSender{})
	if got != nil {
		t.Errorf("the scheduler started %d jobs with SCHEDULER_ENABLED unset, want none", len(got))
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
	defer cancel()
	got := startScheduler(ctx, schedulerPool(t), cfg, platform.SystemClock{}, logger, email.LogSender{})
	want := []string{"recurring-scan-and-generate", "usage-lock-cutoff-scan", "fx-rates-weekly-sync"}
	if len(got) != len(want) {
		t.Fatalf("the scheduler started %d jobs, want %d", len(got), len(want))
	}
	for i, job := range got {
		if job.ID != want[i] {
			t.Errorf("job %d is %q, want %q", i, job.ID, want[i])
		}
	}
}

// The hosting enforcement is the job the integrations plan adds; its id and
// slot are here already.
func TestTheHostingJobIsReadyForTheIntegrationsPlan(t *testing.T) {
	t.Parallel()
	job := jobs.HostingEnforcement(func(context.Context, time.Time) error { return nil })
	if job.ID != "hosting-overdue-enforcement" {
		t.Errorf("the hosting job's id = %q", job.ID)
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
