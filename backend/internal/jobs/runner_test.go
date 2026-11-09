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

	"github.com/wenhaokho/wenhao-billing/backend/internal/jobs/jobsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// newPool opens a pool on a fresh migrated database.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return newPoolWithMaxConns(t, 0)
}

// newPoolWithMaxConns opens a pool on a fresh migrated database, with at
// most maxConns connections (zero: the pool's own default).
func newPoolWithMaxConns(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{
		URL: testkit.PostgresURL(t, migrations.FS), MaxConns: maxConns,
	})
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

// TestATickHoldsOneConnectionForTheBookkeeping: the slot bookkeeping runs
// on the lock's own connection, so a tick does not take a second pool
// connection for it. The pool is small (max(4, NumCPU)) and every job
// ticks in the same minute, so a tick that held two connections could
// starve the API's requests.
//
// The test's pool has a single connection: a tick that took a second one
// for the bookkeeping would wait for the lock's connection, which is held
// for the whole tick, and would never get it. The job's own work may use
// the pool (it runs after the bookkeeping); the test's job does not.
func TestATickHoldsOneConnectionForTheBookkeeping(t *testing.T) {
	t.Parallel()
	pool := newPoolWithMaxConns(t, 1)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	job := Job{
		ID:  "recurring-scan-and-generate",
		Due: DailySlot(2),
		Run: func(context.Context, time.Time) error { return nil },
	}
	r := newRunner(pool, c, func(error) {})
	// The tick must finish: with a single connection, a second one for
	// the bookkeeping would block forever.
	done := make(chan error, 1)
	go func() { done <- r.tick(context.Background(), job) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tick waited for a second connection: the bookkeeping must use the lock's connection")
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
// error, and the error is reported once: the runner returns it, and sched
// (which calls the runner) reports it. The runner does not report it
// itself, or the error would be reported twice.
func TestJobErrorRecordedAndReported(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	failure := errors.New("the provider is down")
	j := &recordingJob{id: "usage-lock-cutoff-scan", err: failure}
	var mu sync.Mutex
	var reported []error
	r := newRunner(pool, c, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err)
	})

	err := r.tick(context.Background(), j.job(DailySlot(3)))
	if !errors.Is(err, failure) {
		t.Fatalf("tick error = %v, want %v", err, failure)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 0 {
		t.Errorf("reported = %v, want nothing: the returned error is the report", reported)
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

// TestAJobWithoutWorkIsRecordedAndDoesNothing: the hosting enforcement's
// work does not exist yet (the integrations plan brings it), so the job is
// recorded and does nothing rather than panicking.
func TestAJobWithoutWorkIsRecordedAndDoesNothing(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	job := Job{ID: "hosting-overdue-enforcement", Due: DailySlot(4)}
	r := newRunner(pool, c, func(error) {})
	if err := r.tick(context.Background(), job); err != nil {
		t.Fatalf("tick: %v", err)
	}
	got := slots(t, pool, job.ID)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is not set")
	}
}

// TestAnAbandonedSlotRunsAgain: a slot row with neither an end nor an
// error is a run that died (a killed process, a panic, a cancelled
// context). The advisory lock is held, so no live runner owns the slot: it
// runs again, and the row records the new attempt. Without this the slot
// would be lost for good and the catch-up after a deploy would never
// happen.
func TestAnAbandonedSlotRunsAgain(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	j := &recordingJob{id: "recurring-scan-and-generate"}
	slot := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
	// The abandoned row: started, but neither finished nor failed.
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO scheduled_job_runs (job_id, slot, started_at) VALUES ($1, $2, $3)`,
		j.id, slot, slot); err != nil {
		t.Fatalf("insert the abandoned row: %v", err)
	}

	r := newRunner(pool, c, func(error) {})
	if err := r.tick(context.Background(), j.job(DailySlot(2))); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := j.runCount(); got != 1 {
		t.Fatalf("runs = %d, want 1: an abandoned slot must run again", got)
	}
	got := slots(t, pool, j.id)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is not set: the slot is still abandoned")
	}
	if got[0].Error != nil {
		t.Errorf("error = %v, want none", *got[0].Error)
	}
	if !got[0].StartedAt.Equal(c.now) {
		t.Errorf("started_at = %s, want the new attempt's %s", got[0].StartedAt, c.now)
	}
}

// TestRestartSlotLeavesAFinishedRowAlone: the restart query says the row
// is abandoned (neither an end nor an error). A row that finished (or
// failed) is left alone: restarting it would wipe the end of a run that
// did finish, and the slot would run again for nothing.
func TestRestartSlotLeavesAFinishedRowAlone(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	const jobID = "recurring-scan-and-generate"
	slot := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
	// The finished row: it has an end and no error.
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO scheduled_job_runs (job_id, slot, started_at, finished_at) VALUES ($1, $2, $3, $3)`,
		jobID, slot, slot); err != nil {
		t.Fatalf("insert the finished row: %v", err)
	}
	q := jobsdb.New(pool)
	if err := q.RestartSlot(context.Background(), jobsdb.RestartSlotParams{
		JobID: jobID, Slot: slot, StartedAt: c.now,
	}); err != nil {
		t.Fatalf("restart the slot: %v", err)
	}
	got := slots(t, pool, jobID)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is gone: the restart wiped the end of a run that finished")
	}
	if !got[0].StartedAt.Equal(slot) {
		t.Errorf("started_at = %s, want the row's own %s", got[0].StartedAt, slot)
	}
}

// TestTheSlotBookkeepingSurvivesAShutdown: the slot bookkeeping runs on a
// context the shutdown does not cancel. A query that fails because its
// context was cancelled closes the pgx connection (the protocol's state is
// then unknown), and the slot row would be left half-written: the tick
// would report a spurious unlock failure and the slot would be abandoned.
//
// The job cancels the context (a shutdown) and the tick must still finish
// the slot and release the lock without an error.
func TestTheSlotBookkeepingSurvivesAShutdown(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	const jobID = "recurring-scan-and-generate"
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var reported []error
	job := Job{
		ID:  jobID,
		Due: DailySlot(2),
		Run: func(context.Context, time.Time) error {
			cancel()
			return nil
		},
	}
	r := newRunner(pool, c, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err)
	})
	if err := r.tick(ctx, job); err != nil {
		t.Fatalf("tick: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 0 {
		t.Errorf("reported %v, want nothing", reported)
	}
	got := slots(t, pool, jobID)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is not set: the slot is still abandoned")
	}
}

// TestATickWithoutAConnectionIsSkipped: the pool is exhausted (every
// connection is taken), so the tick cannot get the connection the advisory
// lock lives on. The wait is bounded: the tick is skipped and logged, and
// the next tick tries again. It must never wait forever: the pool is
// small and the API's requests need it too.
func TestATickWithoutAConnectionIsSkipped(t *testing.T) {
	t.Parallel()
	pool := newPoolWithMaxConns(t, 1)
	// The only connection is taken by somebody else.
	held, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	logs := &logCapture{}
	r := newRunner(pool, c, func(error) {}, logs.logger())
	r.lockTimeout = 100 * time.Millisecond
	job := Job{
		ID:  "recurring-scan-and-generate",
		Due: DailySlot(2),
		Run: func(context.Context, time.Time) error {
			t.Error("the job ran without a connection")
			return nil
		},
	}
	done := make(chan error, 1)
	go func() { done <- r.tick(context.Background(), job) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tick waited forever for a connection")
	}
	if !logs.contains("no database connection") {
		t.Errorf("the skipped tick is not logged: %v", logs.lines)
	}
}

// TestStartRunsEveryJobWithASmallPool: the scheduler runs the jobs one
// after another, so it never holds more than one connection for itself.
// The pool here has two connections and three due jobs: with a scheduler
// that ran the jobs in parallel (each on its own connection, plus one for
// the lock) the jobs would take turns skipping every tick and never all
// run.
//
// The test drives the scheduler through Start, not through a single tick:
// the whole scheduler is what has to fit in a small pool. Each job also
// watches the pool: the scheduler may hold one connection while a job
// runs, never two.
//
// The run count is counted inside the jobs themselves, so the test never
// queries the pool while the scheduler is running (that would be a second
// connection of the test's own).
func TestStartRunsEveryJobWithASmallPool(t *testing.T) {
	t.Parallel()
	pool := newPoolWithMaxConns(t, 2)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	var mu sync.Mutex
	var peak int32
	var ran int
	// job is a job that counts its runs and watches how many connections
	// the pool has while it runs.
	job := func(id string, due func(time.Time) time.Time) Job {
		return Job{
			ID:  id,
			Due: due,
			Run: func(context.Context, time.Time) error {
				mu.Lock()
				ran++
				if got := pool.Stat().TotalConns(); got > peak {
					peak = got
				}
				mu.Unlock()
				return nil
			},
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wait := start(ctx, pool, c, func(error) {}, 10*time.Millisecond,
		job("recurring-scan-and-generate", DailySlot(2)),
		job("usage-lock-cutoff-scan", DailySlot(3)),
		job("hosting-overdue-enforcement", DailySlot(4)))
	// Every job has run: all three.
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return ran >= 3
	})
	cancel()
	wait()
	mu.Lock()
	defer mu.Unlock()
	if peak > 1 {
		t.Errorf("the scheduler held %d connections while a job ran, want 1", peak)
	}
	// Each job ran exactly once: its slot is recorded, so a later tick
	// would have found it and done nothing.
	for _, id := range []string{"recurring-scan-and-generate", "usage-lock-cutoff-scan", "hosting-overdue-enforcement"} {
		got := slots(t, pool, id)
		if len(got) != 1 {
			t.Errorf("the job %q recorded %d runs, want 1", id, len(got))
		}
	}
}

// TestTheJobsNeverRunAtTheSameTime: the scheduler runs the jobs one after
// another, never in parallel: the pool is small and the API needs it, so
// two jobs at once would take two connections and could starve the API.
//
// The test drives the scheduler through Start.
func TestTheJobsNeverRunAtTheSameTime(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	var mu sync.Mutex
	running := 0
	overlap := false
	ran := 0
	job := func(id string) Job {
		return Job{
			ID:  id,
			Due: DailySlot(2),
			Run: func(context.Context, time.Time) error {
				mu.Lock()
				running++
				if running > 1 {
					overlap = true
				}
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				running--
				ran++
				mu.Unlock()
				return nil
			},
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wait := start(ctx, pool, c, func(error) {}, 10*time.Millisecond,
		job("recurring-scan-and-generate"), job("usage-lock-cutoff-scan"))
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return ran >= 2
	})
	cancel()
	wait()
	mu.Lock()
	defer mu.Unlock()
	if overlap {
		t.Error("two jobs ran at the same time: the scheduler must run them one after another")
	}
}

// TestAFailedUnlockDoesNotReturnTheConnectionToThePool: a connection that
// may still hold the advisory lock must not go back to the pool, or the
// next user of it would inherit the lock. It is taken out of the pool and
// closed instead.
func TestAFailedUnlockDoesNotReturnTheConnectionToThePool(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	r := newRunner(pool, c, func(error) {})
	// The unlock fails.
	r.unlock = func(context.Context, *pgxpool.Conn, int64) error {
		return errors.New("the connection is gone")
	}
	job := Job{
		ID:  "recurring-scan-and-generate",
		Due: DailySlot(2),
		Run: func(context.Context, time.Time) error { return nil },
	}
	if err := r.tick(context.Background(), job); err != nil {
		t.Fatalf("tick: %v", err)
	}
	// The connection is gone from the pool: the pool has none left.
	if got := pool.Stat().TotalConns(); got != 0 {
		t.Errorf("the pool holds %d connections, want 0: the locked one must be closed", got)
	}
}

// TestAShutdownStillFinishesTheSlot: the end of a run is written without
// the caller's cancellation. A shutdown cancels the context while the job
// runs; without this the slot row keeps neither an end nor an error, and
// the slot is abandoned (the next start would have to run it again).
func TestAShutdownStillFinishesTheSlot(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	const jobID = "recurring-scan-and-generate"
	ctx, cancel := context.WithCancel(context.Background())
	job := Job{
		ID:  jobID,
		Due: DailySlot(2),
		Run: func(context.Context, time.Time) error {
			// The shutdown: the context ends while the job runs.
			cancel()
			return nil
		},
	}
	r := newRunner(pool, c, func(error) {})
	if err := r.tick(ctx, job); err != nil {
		t.Fatalf("tick: %v", err)
	}
	got := slots(t, pool, jobID)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is not set: the slot is still abandoned")
	}
}

// TestAJobCutShortByTheShutdownRunsAgain: a job that the shutdown cuts
// short (it returns the context's error) must leave its slot row
// abandoned, with neither an end nor an error. A row with an error is not
// abandoned, so the slot would never run again: that is the deploy case
// the catch-up is for.
//
// The next tick (with a fresh context) runs the slot again.
func TestAJobCutShortByTheShutdownRunsAgain(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	const jobID = "recurring-scan-and-generate"
	ctx, cancel := context.WithCancel(context.Background())
	var runs int
	job := Job{
		ID:  jobID,
		Due: DailySlot(2),
		Run: func(ctx context.Context, _ time.Time) error {
			runs++
			if runs == 1 {
				// The shutdown cuts the job short.
				cancel()
				return ctx.Err()
			}
			return nil
		},
	}
	r := newRunner(pool, c, func(error) {})
	if err := r.tick(ctx, job); err == nil {
		t.Fatal("tick error = nil, want the context's error")
	}
	got := slots(t, pool, jobID)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt != nil {
		t.Errorf("finished_at = %s, want none: the slot must stay abandoned", got[0].FinishedAt)
	}
	if got[0].Error != nil {
		t.Errorf("error = %q, want none: the slot must stay abandoned", *got[0].Error)
	}

	// The next tick, with a fresh context, runs the slot again.
	if err := r.tick(context.Background(), job); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if runs != 2 {
		t.Errorf("the job ran %d times, want 2: the slot must run again", runs)
	}
	got = slots(t, pool, jobID)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is not set after the second run")
	}
}

// TestAPanickingJobRecordsItsSlot: a job that panics must still record
// its slot, with the panic as the error. Otherwise the panic leaves the
// slot abandoned and it is lost for good.
func TestAPanickingJobRecordsItsSlot(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	const jobID = "usage-lock-cutoff-scan"
	job := Job{
		ID:  jobID,
		Due: DailySlot(3),
		Run: func(context.Context, time.Time) error {
			panic("the job exploded")
		},
	}
	var reported []error
	r := newRunner(pool, c, func(err error) { reported = append(reported, err) })

	err := r.tick(context.Background(), job)
	if err == nil {
		t.Fatal("tick error = nil, want the panic")
	}
	if !strings.Contains(err.Error(), "the job exploded") {
		t.Errorf("tick error = %v, want the panic", err)
	}
	got := slots(t, pool, jobID)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is not set: the slot is still abandoned")
	}
	if got[0].Error == nil {
		t.Fatal("the panic is not recorded as the slot's error")
	}
	if !strings.Contains(*got[0].Error, "the job exploded") {
		t.Errorf("recorded error = %q, want the panic", *got[0].Error)
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
// job, so the API can log it (and Sentry sees the error). The scheduler
// reports it once, through sched, which is what returns the job's error.
func TestReportIsCalledForAFailedJob(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	failure := errors.New("boom")
	j := &recordingJob{id: "usage-lock-cutoff-scan", err: failure}
	var mu sync.Mutex
	var reported []error
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wait := start(ctx, pool, c, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err)
	}, 10*time.Millisecond, j.job(DailySlot(3)))
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reported) > 0
	})
	cancel()
	wait()
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 {
		t.Fatalf("reported %d errors, want 1: %v", len(reported), reported)
	}
	if !errors.Is(reported[0], failure) {
		t.Errorf("reported = %v, want the job's error", reported[0])
	}
}

// TestSchedulerOffByDefault: the scheduler is off unless SCHEDULER_ENABLED
// says otherwise (Go change 8; Python's default was true). The variable is
// read by the platform's config, the one the API uses.
func TestSchedulerOffByDefault(t *testing.T) {
	t.Parallel()
	cfg, err := platform.ParseConfig(schedulerVars(nil))
	if err != nil {
		t.Fatalf("parse the config: %v", err)
	}
	if cfg.SchedulerEnabled {
		t.Error("the scheduler is on with SCHEDULER_ENABLED unset, want off")
	}
	cfg, err = platform.ParseConfig(schedulerVars(map[string]string{"SCHEDULER_ENABLED": "false"}))
	if err != nil {
		t.Fatalf("parse the config: %v", err)
	}
	if cfg.SchedulerEnabled {
		t.Error("the scheduler is on with SCHEDULER_ENABLED=false, want off")
	}
	cfg, err = platform.ParseConfig(schedulerVars(map[string]string{"SCHEDULER_ENABLED": "true"}))
	if err != nil {
		t.Fatalf("parse the config: %v", err)
	}
	if !cfg.SchedulerEnabled {
		t.Error("the scheduler is off with SCHEDULER_ENABLED=true, want on")
	}
}

// schedulerVars is the environment the config test reads: the required
// variables, plus the extra ones.
func schedulerVars(extra map[string]string) map[string]string {
	vars := map[string]string{
		"DATABASE_URL":        "postgresql://u:p@h:5432/d",
		"SESSION_SECRET":      "0123456789abcdef0123456789abcdef",
		"WEBHOOK_HMAC_SECRET": "a-real-webhook-secret",
		"EMAIL_WEBHOOK_TOKEN": "a-real-email-token",
	}
	for k, v := range extra {
		vars[k] = v
	}
	return vars
}

// TestStartRunsTheJobs: Start runs the jobs on a tick; the jobs that are
// due run, and the ones that are not do not.
//
// The scheduler is stopped and waited for, so the assertions below see a
// settled state and no goroutine is left behind.
func TestStartRunsTheJobs(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	c := &clock{now: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)}
	due := &recordingJob{id: "recurring-scan-and-generate"}
	notDue := &recordingJob{id: "fx-rates-weekly-sync"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wait := start(ctx, pool, c, func(error) {}, 10*time.Millisecond,
		due.job(DailySlot(2)), notDue.job(WeeklySlot(time.Monday, 5)))
	// Wait for the first tick of both jobs: the due job runs, the weekly
	// job's slot is too old and is recorded as skipped.
	waitFor(t, func() bool { return due.runCount() > 0 && len(slots(t, pool, notDue.id)) == 1 })
	// Stop the scheduler and wait for its ticks: a tick that ran after
	// this would race with the assertions below.
	cancel()
	wait()
	// The due job ran exactly once: its slot is recorded, so every later
	// tick finds it and does nothing.
	if got := due.runCount(); got != 1 {
		t.Errorf("the due job ran %d times, want 1", got)
	}
	// The weekly job's slot is a week old, so it is skipped and recorded,
	// never run.
	if got := notDue.runCount(); got != 0 {
		t.Errorf("the weekly job ran %d times, want 0", got)
	}
	got := slots(t, pool, due.id)
	if len(got) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(got))
	}
	if got[0].FinishedAt == nil {
		t.Error("finished_at is not set after a run")
	}
	// The weekly job's slot is recorded as skipped, with its reason.
	weekly := slots(t, pool, notDue.id)
	if len(weekly) != 1 {
		t.Fatalf("the weekly job's recorded runs = %d, want 1", len(weekly))
	}
	if weekly[0].Error == nil {
		t.Error("the skipped weekly slot is not recorded with a reason")
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
