package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkersLifecycle(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	job := func(ctx context.Context) error { calls.Add(1); entered <- struct{}{}; <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	w := startWorkers(ctx, false, time.Millisecond, time.Second, log.New(&bytes.Buffer{}, "", 0), job)
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 {
		cancel()
		w.Wait()
		t.Fatal("disabled worker ran")
	}
	w.Wait()
	w = startWorkers(ctx, true, time.Millisecond, time.Hour, log.New(&bytes.Buffer{}, "", 0), job)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("overlapping worker runs")
	}
	cancel()
	done := make(chan struct{})
	go func() { w.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}
func TestWorkerErrorsAndPanicsAreIsolatedAndRedacted(t *testing.T) {
	var logs bytes.Buffer
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	job := func(context.Context) error {
		if calls.Add(1) == 1 {
			panic("secret panic")
		}
		return errors.New("secret error")
	}
	w := startWorkers(ctx, true, 10*time.Millisecond, time.Second, log.New(&logs, "", 0), job)
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	w.Wait()
	if calls.Load() < 2 {
		t.Fatal("worker did not recover")
	}
	if strings.Contains(logs.String(), "secret") || !strings.Contains(logs.String(), "failed") {
		t.Fatalf("unsafe log: %s", logs.String())
	}
}
func TestSchedulerConfiguration(t *testing.T) {
	t.Setenv("SCHEDULER_ENABLED", "false")
	enabled, err := schedulerEnabledFromEnv()
	if err != nil || enabled {
		t.Fatalf("disabled: %v %v", enabled, err)
	}
	t.Setenv("SCHEDULER_ENABLED", "invalid")
	if _, err = schedulerEnabledFromEnv(); err == nil {
		t.Fatal("invalid flag accepted")
	}
}

func TestProviderFailuresAreLoggedWithoutDetails(t *testing.T) {
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	sender := loggedSender{sender: failingSender{}, logger: logger}
	if err := sender.Send(context.Background(), email.Message{}); err == nil {
		t.Fatal("send error lost")
	}
	provider := loggedHostingProvider{provider: failingHostingProvider{}, logger: logger}
	if err := provider.Restore(context.Background(), platform.HostingRestoreTarget{}); err == nil {
		t.Fatal("restore error lost")
	}
	if strings.Contains(logs.String(), "secret") || strings.Count(logs.String(), "failed") != 2 {
		t.Fatalf("unsafe logs: %s", logs.String())
	}
}

type failingSender struct{}

func (failingSender) Send(context.Context, email.Message) error { return errors.New("secret") }

type failingHostingProvider struct{}

func (failingHostingProvider) Restore(context.Context, platform.HostingRestoreTarget) error {
	return errors.New("secret")
}

func TestNextRecurringScanIsAtTwoUTC(t *testing.T) {
	for _, tc := range []struct{ now, want string }{
		{"2026-11-06T01:59:59Z", "2026-11-06T02:00:00Z"},
		{"2026-11-06T02:00:00Z", "2026-11-07T02:00:00Z"},
		{"2026-11-06T22:00:00-07:00", "2026-11-08T02:00:00Z"},
	} {
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := nextRecurringScan(now).Format(time.RFC3339); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.now, got, tc.want)
		}
	}
}

func TestDailyWorkerWaitsForScheduleAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	durations := make(chan time.Duration, 2)
	calls := make(chan struct{}, 1)
	now := time.Date(2026, 11, 6, 1, 59, 0, 0, time.UTC)
	wait := func(d time.Duration) (<-chan time.Time, func()) { durations <- d; return ticks, func() {} }
	w := startDailyWorker(ctx, true, func() time.Time { return now }, wait, log.New(&bytes.Buffer{}, "", 0), func(context.Context) error { calls <- struct{}{}; return nil })
	if d := <-durations; d != time.Minute {
		t.Fatalf("delay %v", d)
	}
	select {
	case <-calls:
		t.Fatal("ran before 02:00")
	default:
	}
	now = now.Add(time.Minute)
	ticks <- now
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("did not run")
	}
	if d := <-durations; d != 24*time.Hour {
		t.Fatalf("next delay %v", d)
	}
	cancel()
	done := make(chan struct{})
	go func() { w.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not stop")
	}
}

func TestDailyWorkerDisabled(t *testing.T) {
	called := false
	w := startDailyWorker(context.Background(), false, time.Now, func(time.Duration) (<-chan time.Time, func()) { called = true; return nil, func() {} }, log.New(&bytes.Buffer{}, "", 0), func(context.Context) error { called = true; return nil })
	w.Wait()
	if called {
		t.Fatal("disabled daily worker ran")
	}
}

func TestNextUsageScanIsAtThreeUTC(t *testing.T) {
	for _, tc := range []struct{ now, want string }{{"2026-11-06T02:59:59Z", "2026-11-06T03:00:00Z"}, {"2026-11-06T03:00:00Z", "2026-11-07T03:00:00Z"}, {"2026-11-06T22:00:00-07:00", "2026-11-08T03:00:00Z"}} {
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := nextUsageScan(now).Format(time.RFC3339); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.now, got, tc.want)
		}
	}
}

func TestUsageDailyWorkerDisabled(t *testing.T) {
	called := false
	w := startScheduledDailyWorker(context.Background(), false, time.Now, func(time.Duration) (<-chan time.Time, func()) { called = true; return nil, func() {} }, log.New(&bytes.Buffer{}, "", 0), nextUsageScan, "usage cutoff scan", func(context.Context) error { called = true; return nil })
	w.Wait()
	if called {
		t.Fatal("disabled usage worker ran")
	}
}

func TestUsageWorkerLogsSanitizedFailures(t *testing.T) {
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	err := &platform.UsageScanError{Locked: 1, Failures: []platform.UsageInvoiceFailure{{InvoiceID: "30000000-0000-0000-0000-000000000001", Category: platform.UsageMeterFailed}, {InvoiceID: "30000000-0000-0000-0000-000000000002", Category: platform.UsageInvalidBillingRef}}}
	job := usageScanJob(logger, func(context.Context) (int, error) { return 1, err })
	if got := job(context.Background()); got != err {
		t.Fatalf("error lost: %v", got)
	}
	for _, part := range []string{"locked=1", "failed=2", "30000000-0000-0000-0000-000000000001", "meter_failed", "30000000-0000-0000-0000-000000000002", "invalid_billing_ref"} {
		if !strings.Contains(logs.String(), part) {
			t.Fatalf("missing %q in %s", part, logs.String())
		}
	}
	logs.Reset()
	job = usageScanJob(logger, func(context.Context) (int, error) { return 0, errors.New("database-secret-456") })
	_ = job(context.Background())
	if strings.Contains(logs.String(), "database-secret-456") {
		t.Fatalf("leaked: %s", logs.String())
	}
}

func TestNextFXSyncUTCWeekBoundary(t *testing.T) {
	for _, tc := range []struct{ now, want string }{
		{"2026-11-02T04:59:59Z", "2026-11-02T05:00:00Z"},
		{"2026-11-02T05:00:00Z", "2026-11-09T05:00:00Z"},
		{"2026-11-08T23:00:00-05:00", "2026-11-09T05:00:00Z"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := nextFXSync(now).Format(time.RFC3339); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.now, got, tc.want)
		}
	}
}

func TestNextHostingScan(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"2026-01-01T03:59:59Z", "2026-01-01T04:00:00Z"},
		{"2026-01-01T04:00:00Z", "2026-01-02T04:00:00Z"},
		{"2026-01-01T12:00:00+07:00", "2026-01-02T04:00:00Z"},
	} {
		in, _ := time.Parse(time.RFC3339, tc.in)
		want, _ := time.Parse(time.RFC3339, tc.want)
		if got := nextHostingScan(in); !got.Equal(want) {
			t.Errorf("nextHostingScan(%s) = %s; want %s", tc.in, got, want)
		}
	}
}

func TestHostingDailyWorkerDisabled(t *testing.T) {
	called := false
	w := startScheduledDailyWorker(context.Background(), false, time.Now, func(time.Duration) (<-chan time.Time, func()) { called = true; return nil, func() {} }, log.New(&bytes.Buffer{}, "", 0), nextHostingScan, "hosting enforcement", func(context.Context) error { called = true; return nil })
	w.Wait()
	if called {
		t.Fatal("disabled hosting worker ran")
	}
}

func TestWeeklyFXJobStatusAndDeadlineRedacted(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context) error
		want string
	}{
		{"skipped", func(context.Context) error { return platform.ErrFXJobAlreadyRunning }, "status=skipped"},
		{"failure", func(context.Context) error { return errors.New("provider-secret-token") }, "status=failed"},
		{"timeout", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, "status=timeout"},
		{"finished", func(context.Context) error { return nil }, "status=finished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			job := weeklyFXJob(log.New(&b, "", 0), 20*time.Millisecond, tc.run)
			if err := job(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(b.String(), tc.want) || strings.Contains(b.String(), "secret") {
				t.Fatalf("unsafe status %q", b.String())
			}
		})
	}
}
