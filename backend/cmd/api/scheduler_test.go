package main

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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
	// The context is cancelled before the start: the scheduler's first tick
	// sees it and runs no job, so no real job (the hosting enforcement
	// among them) runs on the database.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, wait := startScheduler(ctx, schedulerPool(t), cfg, platform.SystemClock{}, logger, email.LogSender{})
	if wait == nil {
		t.Fatal("the scheduler started without a wait function")
	}
	defer wait()
	want := []string{jobs.RecurringScanID, jobs.UsageLockScanID, jobs.HostingEnforcementID, jobs.FXRatesSyncID}
	if len(got) != len(want) {
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
	// Cancelled before the start so that no real job runs on the database.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, wait := startScheduler(ctx, schedulerPool(t), cfg, platform.SystemClock{}, logger, email.LogSender{})
	if wait == nil {
		t.Fatal("the scheduler started without a wait function")
	}
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not return after the context was cancelled: serve must not close the pool before this")
	}
}

// TestTheHostingJobIsScheduled: the hosting enforcement is in the job list
// with a slot and work.
func TestTheHostingJobIsScheduled(t *testing.T) {
	t.Parallel()
	cfg := schedulerConfig(t, nil)
	var found *jobs.Job
	for _, job := range scheduledJobs(nil, cfg, slog.New(slog.NewTextHandler(testWriter{t}, nil)), email.LogSender{}) {
		if job.ID == jobs.HostingEnforcementID {
			found = &job
		}
	}
	if found == nil {
		t.Fatal("the job list has no hosting job")
	}
	if found.Due == nil || found.Run == nil {
		t.Error("the hosting job has no slot or no work")
	}
}

// TestTheHostingJobRunsEnforceWithAClientFromConfig runs the hosting job's
// work as the scheduler builds it: CLOUDFLARE_API_BASE_URL and
// CLOUDFLARE_API_TOKEN point its client at an httptest Cloudflare, and an
// overdue hosting item is suspended through it.
func TestTheHostingJobRunsEnforceWithAClientFromConfig(t *testing.T) {
	t.Parallel()
	type call struct{ method, path, auth string }
	var mu sync.Mutex
	var calls []call
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, call{r.Method, r.URL.Path, r.Header.Get("Authorization")})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success": true}`))
	}))
	t.Cleanup(cf.Close)
	cfg := schedulerConfig(t, map[string]string{
		"CLOUDFLARE_API_BASE_URL": cf.URL, "CLOUDFLARE_API_TOKEN": "cf-scheduler-token",
	})
	pool := schedulerPool(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	customer, item, invoice := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO customers (customer_id, name, matching_aliases) VALUES ($1, 'Hosted Client', '["Hosted Client"]'::jsonb)`, customer)
	exec(`INSERT INTO items (item_id, name, item_type, default_currency, default_unit_price, is_sold, is_purchased, active,
is_hosting, hosting_domain, hosting_grace_days, hosting_suspension_enabled, hosting_status)
VALUES ($1, 'Acme hosting', 'SERVICE', 'USD', 240, true, false, true, true, 'example.com', 3, true, 'ACTIVE')`, item)
	exec(`INSERT INTO cloudflare_targets (target_id, item_id, zone_id, record_id, record_name, record_type, live_content,
maintenance_content, proxied) VALUES ($1, $2, 'zone_123', 'record_123', 'example.com', 'CNAME', 'live.example.net',
'maintenance.example.net', true)`, uuid.New(), item)
	exec(`INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status,
issue_date, due_date, wht_type, wht_amount, wht_booked)
VALUES ($1, $2, 'RECURRING', 'USD', 240, 240, 'SENT', '2026-06-01', '2026-06-08', 'NONE', 0, 0)`, invoice, customer)
	exec(`INSERT INTO invoice_line_items (line_item_id, invoice_id, item_id, position, description, quantity, unit_price, amount)
VALUES ($1, $2, $3, 0, 'Acme hosting - example.com', 1, 240, 240)`, uuid.New(), invoice, item)

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	var run func(context.Context, time.Time) error
	for _, job := range scheduledJobs(pool, cfg, logger, email.LogSender{}) {
		if job.ID == jobs.HostingEnforcementID {
			run = job.Run
		}
	}
	if run == nil {
		t.Fatal("the job list has no hosting work")
	}
	if err := run(ctx, time.Date(2026, 6, 12, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := call{http.MethodPatch, "/zones/zone_123/dns_records/record_123", "Bearer cf-scheduler-token"}
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("Cloudflare calls = %+v, want one %+v", calls, want)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT hosting_status FROM items WHERE item_id = $1`, item).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "SUSPENDED" {
		t.Errorf("hosting_status = %q, want SUSPENDED", status)
	}
}

// schedulerConfig is a config with the scheduler on and the extra variables.
func schedulerConfig(t *testing.T, extra map[string]string) platform.Config {
	t.Helper()
	env := map[string]string{
		"DATABASE_URL": "postgresql://u:p@h:5432/d", "SESSION_SECRET": "scheduler-test-secret-32-bytes-long",
		"WEBHOOK_HMAC_SECRET": "a-real-webhook-secret", "EMAIL_WEBHOOK_TOKEN": "a-real-email-token",
		"SCHEDULER_ENABLED": "true",
	}
	maps.Copy(env, extra)
	cfg, err := platform.ParseConfig(env)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// testWriter writes the log lines into the test's output.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
