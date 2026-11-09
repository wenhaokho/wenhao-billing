package jobs

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// newPool opens a pool on a fresh migrated database.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// clock is a fixed clock for the tests.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

// recordingJob is a job that counts its runs and can fail.
type recordingJob struct {
	mu    sync.Mutex
	id    string
	runs  int
	err   error
	slots []time.Time
}

// job is the Job of the recording job.
func (j *recordingJob) job(due func(time.Time) time.Time) Job {
	return Job{
		ID:  j.id,
		Due: due,
		Run: func(_ context.Context, slot time.Time) error {
			j.mu.Lock()
			defer j.mu.Unlock()
			j.runs++
			j.slots = append(j.slots, slot)
			return j.err
		},
	}
}

// runCount is how many times the job ran.
func (j *recordingJob) runCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.runs
}

// slotRow is a recorded slot.
type slotRow struct {
	JobID      string
	Slot       time.Time
	StartedAt  time.Time
	FinishedAt *time.Time
	Error      *string
}

// slots reads the recorded runs of a job.
func slots(t *testing.T, pool *pgxpool.Pool, jobID string) []slotRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT job_id, slot, started_at, finished_at, error FROM scheduled_job_runs WHERE job_id = $1 ORDER BY slot`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []slotRow
	for rows.Next() {
		var r slotRow
		if err := rows.Scan(&r.JobID, &r.Slot, &r.StartedAt, &r.FinishedAt, &r.Error); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCatchUpOneSlotOnce: the latest due slot runs once; the next tick finds
// the slot recorded and runs nothing.
func TestCatchUpOneSlotOnce(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	j := &recordingJob{id: "recurring-scan-and-generate"}
	r := newRunner(pool, c, func(error) {})

	if err := r.tick(context.Background(), j.job(DailySlot(2))); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if j.runCount() != 1 {
		t.Fatalf("runs = %d, want 1", j.runCount())
	}
	want := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
	if !j.slots[0].Equal(want) {
		t.Errorf("slot = %s, want %s", j.slots[0], want)
	}

	// The same slot again: recorded, so nothing runs.
	if err := r.tick(context.Background(), j.job(DailySlot(2))); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if j.runCount() != 1 {
		t.Errorf("runs = %d, want 1: a recorded slot must not run again", j.runCount())
	}

	got := slots(t, pool, j.id)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if !got[0].Slot.Equal(want) {
		t.Errorf("recorded slot = %s, want %s", got[0].Slot, want)
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is not set after a run")
	}
	if got[0].Error != nil {
		t.Errorf("error = %v, want none", *got[0].Error)
	}
}

// TestCatchUpAfterARestart: a slot that passed while the process was down
// is run once when it starts (a change from Python, whose job store was in
// memory).
func TestCatchUpAfterARestart(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	// 03:30 on the 10th: the 02:00 slot of the 10th is due.
	c := &clock{now: time.Date(2026, 11, 10, 3, 30, 0, 0, time.UTC)}
	j := &recordingJob{id: "recurring-scan-and-generate"}
	r := newRunner(pool, c, func(error) {})

	if err := r.tick(context.Background(), j.job(DailySlot(2))); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if j.runCount() != 1 {
		t.Fatalf("runs = %d, want 1", j.runCount())
	}
	want := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
	if !j.slots[0].Equal(want) {
		t.Errorf("slot = %s, want %s", j.slots[0], want)
	}
}

// TestSlotOlderThan24hSkippedAndLogged: a slot more than 24 hours old is
// recorded as skipped and logged, never run. The weekly job's slot is the
// one that can be that old: the Monday's, on a Tuesday.
func TestSlotOlderThan24hSkippedAndLogged(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	// Tuesday 09:30: the weekly slot is the Monday's 05:00, 28 hours old.
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	j := &recordingJob{id: "fx-rates-weekly-sync"}
	logs := &logCapture{}
	r := newRunner(pool, c, func(error) {}, logs.logger())

	if err := r.tick(context.Background(), j.job(WeeklySlot(time.Monday, 5))); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if j.runCount() != 0 {
		t.Errorf("runs = %d, want 0: a stale slot must not run", j.runCount())
	}
	got := slots(t, pool, j.id)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].Error == nil {
		t.Error("the skipped slot is not recorded with a reason")
	}
	if !logs.contains("skip") {
		t.Errorf("the skipped slot is not logged: %v", logs.lines)
	}
}

// TestJobErrorRecordedAndReported: a job that fails is recorded with its
// error and the error is reported.
func TestJobErrorRecordedAndReported(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	failure := errors.New("the provider is down")
	j := &recordingJob{id: "usage-lock-cutoff-scan", err: failure}
	var reported []error
	r := newRunner(pool, c, func(err error) { reported = append(reported, err) })

	err := r.tick(context.Background(), j.job(DailySlot(3)))
	if !errors.Is(err, failure) {
		t.Fatalf("tick error = %v, want %v", err, failure)
	}
	if len(reported) != 1 || !errors.Is(reported[0], failure) {
		t.Errorf("reported = %v, want the job's error", reported)
	}
	got := slots(t, pool, j.id)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].Error == nil {
		t.Fatal("the error is not recorded")
	}
	if *got[0].Error != failure.Error() {
		t.Errorf("recorded error = %q, want %q", *got[0].Error, failure.Error())
	}
}

// TestPythonHoldingLockSkipsGo: a lock the Python backend holds (the same
// crc32 key) stops the Go runner: the job must not run.
func TestPythonHoldingLockSkipsGo(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	// Another connection takes the advisory lock of the recurring job.
	held, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	var locked bool
	if err := held.QueryRow(context.Background(), "SELECT pg_try_advisory_lock($1)", int64(3587987709)).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("the test could not take the advisory lock")
	}

	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	j := &recordingJob{id: "recurring-scan-and-generate"}
	r := newRunner(pool, c, func(error) {})

	if err := r.tick(context.Background(), j.job(DailySlot(2))); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if j.runCount() != 0 {
		t.Errorf("runs = %d, want 0: the lock is held elsewhere", j.runCount())
	}
}

// TestSlotRecordedBeforeTheJobRuns: the slot row is written before the job
// runs, so a second runner finds it and does nothing.
func TestSlotRecordedBeforeTheJobRuns(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	// The job reads the recorded rows while it runs: it must see its own
	// slot, written before the run.
	var seen int
	const jobID = "recurring-scan-and-generate"
	job := Job{
		ID:  jobID,
		Due: DailySlot(2),
		Run: func(_ context.Context, _ time.Time) error {
			seen = len(slots(t, pool, jobID))
			return nil
		},
	}
	r := newRunner(pool, c, func(error) {})
	if err := r.tick(context.Background(), job); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if seen != 1 {
		t.Errorf("the slot was recorded %d times while the job ran, want 1", seen)
	}
}

// logCapture collects the log lines the runner writes.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

// logger is the slog logger that writes into the capture.
func (l *logCapture) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(l, nil))
}

// Write is the sink of the log handler.
func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, string(p))
	return len(p), nil
}

// contains reports whether a line holds the text.
func (l *logCapture) contains(text string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, text) {
			return true
		}
	}
	return false
}

// TestReportIsCalledForAFailedJob: the report function hears about a failed
// job, so the API can log it (and Sentry sees the error).
func TestReportIsCalledForAFailedJob(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	j := &recordingJob{id: "usage-lock-cutoff-scan", err: errors.New("boom")}
	var reported []error
	r := newRunner(pool, c, func(err error) { reported = append(reported, err) })
	if err := r.tick(context.Background(), j.job(DailySlot(3))); err == nil {
		t.Fatal("tick error = nil, want the job's error")
	}
	if len(reported) != 1 {
		t.Errorf("reported %d errors, want 1", len(reported))
	}
}

// TestSchedulerOffByDefault: the scheduler is off unless SCHEDULER_ENABLED
// says otherwise (Go change 8; Python's default was true).
func TestSchedulerOffByDefault(t *testing.T) {
	t.Parallel()
	if got := SchedulerEnabled(map[string]string{}); got {
		t.Error("the scheduler is on with SCHEDULER_ENABLED unset, want off")
	}
	if got := SchedulerEnabled(map[string]string{"SCHEDULER_ENABLED": "false"}); got {
		t.Error("the scheduler is on with SCHEDULER_ENABLED=false, want off")
	}
	if got := SchedulerEnabled(map[string]string{"SCHEDULER_ENABLED": "true"}); !got {
		t.Error("the scheduler is off with SCHEDULER_ENABLED=true, want on")
	}
}

// TestStartRunsTheJobs: Start runs the jobs on a tick; the jobs that are
// due run, and the ones that are not do not.
func TestStartRunsTheJobs(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	due := &recordingJob{id: "recurring-scan-and-generate"}
	notDue := &recordingJob{id: "fx-rates-weekly-sync"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start(ctx, pool, c, func(error) {}, 10*time.Millisecond,
		due.job(DailySlot(2)), notDue.job(WeeklySlot(time.Monday, 5)))
	// Wait for the first tick.
	waitFor(t, func() bool { return due.runCount() > 0 })
	if notDue.runCount() != 0 {
		t.Errorf("the weekly job ran %d times before its slot", notDue.runCount())
	}
}

// waitFor waits for a condition, up to a few seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the condition was not met in time")
}
