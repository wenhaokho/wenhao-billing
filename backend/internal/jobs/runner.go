package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/sched"

	"github.com/wenhaokho/wenhao-billing/backend/internal/jobs/jobsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// TickInterval is how often the runner looks for due slots. A minute is
// finer than the jobs' slots (they are on the hour), so a job starts within
// a minute of its time.
const TickInterval = time.Minute

// MaxSlotAge is how old a slot may be to still run. A slot older than this
// is skipped and logged, not run: it is a slot the process missed for longer
// than a day (the app was down, or the job was broken), and running it now
// would do a day's work out of time.
const MaxSlotAge = 24 * time.Hour

// LockAcquireTimeout bounds the wait for the connection that takes the
// advisory lock. The pool is small (max(4, NumCPU) connections) and every
// job ticks in the same minute, so a tick must never wait for a connection
// forever: a tick that cannot get one in time is skipped and logged, and
// the next tick tries again.
const LockAcquireTimeout = 10 * time.Second

// Job is a scheduled job: it has an id (its advisory lock key and its name
// in the logs), a slot function (the latest slot that has passed) and the
// work of the slot.
type Job struct {
	// ID is the job id; it is Python's job id, so both backends lock the
	// same key.
	ID string
	// Due is the slot function: it returns the latest slot that has passed.
	Due func(now time.Time) time.Time
	// Run does the work of the slot.
	Run func(ctx context.Context, slot time.Time) error
}

// Start runs the jobs until ctx ends. Every job ticks every TickInterval: a
// tick computes the latest due slot, runs it if it is not recorded yet, and
// records the run.
//
// The jobs run under a Postgres advisory lock keyed on their id (Python's
// lock keys, scheduler.py:40-52), so the Python and the Go backends (and
// several Go processes) never run the same job at once: a tick that cannot
// take the lock is skipped silently, as in Python.
//
// report hears about a job that failed, so the API can log it (and Sentry
// sees it); a failed job is recorded with its error and the slot is not run
// again.
func Start(ctx context.Context, pool *pgxpool.Pool, clock platform.Clock, report func(error), jobs ...Job) {
	start(ctx, pool, clock, report, TickInterval, jobs...)
}

// start is Start with the tick interval the caller chooses; the tests use a
// short one.
//
// It returns a wait function: it blocks until the scheduler's goroutines
// have stopped (the context is done and no tick is in flight). The API does
// not need it (the process ends with the context), but a test does: it must
// not assert while a tick is still running.
func start(ctx context.Context, pool *pgxpool.Pool, clock platform.Clock, report func(error), interval time.Duration, jobs ...Job) func() {
	r := newRunner(pool, clock, report)
	sched.Start(ctx, pool, func(job string, err error) {
		if report != nil {
			report(fmt.Errorf("job %s: %w", job, err))
		}
	}, r.schedJobs(interval, jobs...)...)
	return r.wait
}

// schedJobs builds the jobs gokit/sched runs.
//
// sched takes a lock of its own, on its own connection, for the whole tick.
// It must not be the job's Python key: the runner takes that key on
// another connection, and a session-level advisory lock cannot be taken
// twice. It is a negative key instead, outside the range of Python's
// unsigned crc32 keys, and it is per job: a key of zero would make every
// job contend with every other one, and a slow job would starve the rest.
//
// The real lock (the job's Python key) is taken by the runner itself: it
// covers the slot bookkeeping too, so sched's own key is not used.
func (r *runner) schedJobs(interval time.Duration, jobs ...Job) []sched.Job {
	schedJobs := make([]sched.Job, 0, len(jobs))
	for _, j := range jobs {
		schedJobs = append(schedJobs, sched.Job{
			Name:    j.ID,
			Every:   interval,
			LockKey: schedLockKey(j.ID),
			Run: func(ctx context.Context) error {
				return r.tick(ctx, j)
			},
		})
	}
	return schedJobs
}

// runner holds what a tick needs.
type runner struct {
	pool   *pgxpool.Pool
	clock  platform.Clock
	report func(error)
	logger *slog.Logger
	// mu guards the tick bookkeeping below.
	mu sync.Mutex
	// inflight counts the ticks that are running, and idle is signalled
	// when the last one ends.
	inflight int
	idle     *sync.Cond
	// stopped says the scheduler is shutting down: no tick starts any
	// more.
	stopped bool
	// unlock releases the advisory lock. It is the statement, in a field
	// so a test can make it fail.
	unlock func(ctx context.Context, conn *pgxpool.Conn, key int64) error
	// lockTimeout is how long a tick waits for the lock's connection.
	// LockAcquireTimeout in production; a test shortens it.
	lockTimeout time.Duration
}

// newRunner is the runner the tests drive.
func newRunner(pool *pgxpool.Pool, clock platform.Clock, report func(error), loggers ...*slog.Logger) *runner {
	r := &runner{pool: pool, clock: clock, report: report, unlock: releaseLock, lockTimeout: LockAcquireTimeout}
	r.idle = sync.NewCond(&r.mu)
	if len(loggers) > 0 {
		r.logger = loggers[0]
	}
	return r
}

// releaseLock releases the advisory lock of a key.
func releaseLock(ctx context.Context, conn *pgxpool.Conn, key int64) error {
	_, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", key)
	return err
}

// beginTick registers a tick that is about to run. It says false when the
// scheduler is stopping: no tick starts any more.
func (r *runner) beginTick() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	r.inflight++
	return true
}

// endTick registers that a tick has ended.
func (r *runner) endTick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight--
	if r.inflight == 0 {
		r.idle.Broadcast()
	}
}

// wait blocks until no tick is in flight and no tick starts any more.
//
// The scheduler's goroutines stop on the context, so the caller must have
// cancelled it first. A tick that starts after this does nothing.
func (r *runner) wait() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	for r.inflight > 0 {
		r.idle.Wait()
	}
}

// log returns the runner's logger, slog's default when there is none.
func (r *runner) log() *slog.Logger {
	if r.logger != nil {
		return r.logger
	}
	return slog.Default()
}

// tick runs a job's latest due slot, if it is due to run.
//
// The order is the one of the brief: the advisory lock is taken, then the
// slot row is written (its primary key makes a second runner of the same
// slot a no-op), then the job runs, then the row records the finish or the
// error. A slot that is recorded already is not run again, and a slot older
// than MaxSlotAge is recorded as skipped and logged, never run.
//
// A lock another process holds is not waited for: the tick is skipped
// silently, as in Python ("job %s skipped: already running in another
// process").
//
// The lock connection carries the whole tick: the slot bookkeeping runs on
// it, so a tick holds at most one pool connection while the job itself
// runs (the job opens its own connections while it works).
func (r *runner) tick(ctx context.Context, job Job) error {
	if !r.beginTick() {
		// The scheduler is stopping: this tick does nothing.
		return nil
	}
	defer r.endTick()
	if ctx.Err() != nil {
		// The scheduler is stopping: this tick does nothing. Without
		// this, a tick that starts just after the cancel would take a
		// connection and write, while the caller waits for the ticks to
		// finish.
		return nil
	}
	now := r.clock.Now().UTC()
	slot := job.Due(now)
	if slot.IsZero() {
		return nil
	}
	// The advisory lock is held on a connection of its own, like Python's
	// (scheduler.py:42-52): the lock belongs to the session, not to the
	// transaction.
	conn, err := r.acquireLockConn(ctx, job)
	if err != nil {
		return err
	}
	if conn == nil {
		// No connection in time: the tick is skipped and logged.
		return nil
	}
	// A connection that still holds the lock must not go back to the pool:
	// the next user of it would inherit the lock. It is closed instead.
	released := false
	defer func() {
		if !released {
			// The lock may still be held (the unlock failed, or the tick
			// ended without one): the session must die with the tick, so
			// the connection is taken out of the pool and closed.
			raw := conn.Hijack()
			_ = raw.Close(context.WithoutCancel(ctx))
		}
	}()
	key := LockKey(job.ID)
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
		return fmt.Errorf("job %s: lock: %w", job.ID, err)
	}
	if !locked {
		r.log().InfoContext(ctx, "job skipped: already running in another process", "job", job.ID)
		released = true
		conn.Release()
		return nil
	}
	defer func() {
		if err := r.unlock(context.WithoutCancel(ctx), conn, key); err != nil {
			r.reportErr(fmt.Errorf("job %s: unlock: %w", job.ID, err))
			// The lock may still be held: the connection is closed, not
			// released, so the lock dies with the session.
			return
		}
		released = true
		conn.Release()
	}()
	return r.runSlot(ctx, job, slot, now, conn)
}

// acquireLockConn takes the connection the advisory lock lives on. The wait
// is bounded: the pool is small and every job ticks in the same minute, so a
// tick that waits forever for a connection would starve the API's requests
// as well. A tick that cannot get one in time is skipped and logged.
func (r *runner) acquireLockConn(ctx context.Context, job Job) (*pgxpool.Conn, error) {
	timeout := r.lockTimeout
	if timeout <= 0 {
		timeout = LockAcquireTimeout
	}
	acquireCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := r.pool.Acquire(acquireCtx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("job %s: %w", job.ID, err)
		}
		r.log().WarnContext(ctx, "job skipped: no database connection for the lock in time",
			"job", job.ID, "timeout", timeout)
		return nil, nil
	}
	return conn, nil
}

// runSlot is tick's work, under the advisory lock. The bookkeeping runs on
// the lock's own connection, so a tick never holds two pool connections.
//
// The bookkeeping runs on a context the shutdown does not cancel: a query
// that fails because the context was cancelled closes the pgx connection
// (the protocol's state is then unknown), and the slot row would be left
// half-written. The job's own work keeps the caller's context: it should
// stop when the process stops.
func (r *runner) runSlot(ctx context.Context, job Job, slot, now time.Time, conn *pgxpool.Conn) error {
	bookCtx := context.WithoutCancel(ctx)
	q := jobsdb.New(conn)
	recorded, err := q.GetSlot(bookCtx, jobsdb.GetSlotParams{JobID: job.ID, Slot: slot})
	switch {
	case err == nil && !abandoned(recorded):
		// The slot is recorded and it finished: it ran already.
		return nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("job %s: read slot %s: %w", job.ID, slot, err)
	}
	// Either the slot is not recorded at all, or its row is abandoned
	// (neither an end nor an error): the run that wrote it died (a killed
	// process, a panic, a cancelled context).
	if now.Sub(slot) > MaxSlotAge {
		// The slot is too old to run: it is recorded as skipped, so it is
		// never considered again.
		reason := fmt.Sprintf("slot %s is older than %s: skipped", slot.Format(time.RFC3339), MaxSlotAge)
		if err == nil {
			// The row is abandoned: it is finished off with the reason.
			if err := q.FinishSlot(bookCtx, jobsdb.FinishSlotParams{
				FinishedAt: pgxTimestamptz(now), Error: &reason, JobID: job.ID, Slot: slot,
			}); err != nil {
				return fmt.Errorf("job %s: record the skipped slot %s: %w", job.ID, slot, err)
			}
		} else if err := q.SkipSlot(bookCtx, jobsdb.SkipSlotParams{
			JobID: job.ID, Slot: slot, StartedAt: now, Error: &reason,
		}); err != nil {
			return fmt.Errorf("job %s: record the skipped slot %s: %w", job.ID, slot, err)
		}
		r.log().InfoContext(ctx, "job skipped: the slot is too old",
			"job", job.ID, "slot", slot, "now", now)
		return nil
	}
	if err == nil {
		// The abandoned slot runs again: the advisory lock is held, so no
		// live runner owns it. The row records the new attempt.
		r.log().InfoContext(ctx, "job runs an abandoned slot again", "job", job.ID, "slot", slot)
		if err := q.RestartSlot(bookCtx, jobsdb.RestartSlotParams{
			JobID: job.ID, Slot: slot, StartedAt: now,
		}); err != nil {
			return fmt.Errorf("job %s: restart the abandoned slot %s: %w", job.ID, slot, err)
		}
	} else {
		// The slot row is written before the job runs: a second runner of
		// the same slot finds it and does nothing.
		if err := q.InsertSlot(bookCtx, jobsdb.InsertSlotParams{JobID: job.ID, Slot: slot, StartedAt: now}); err != nil {
			if isUniqueViolation(err) {
				// Another runner recorded the slot first.
				return nil
			}
			return fmt.Errorf("job %s: record the slot %s: %w", job.ID, slot, err)
		}
	}
	// A job without work (the hosting enforcement, whose work the
	// integrations plan brings) is recorded and does nothing.
	var runErr error
	if job.Run != nil {
		runErr = r.runJob(ctx, job, slot)
	} else {
		r.log().InfoContext(ctx, "job has no work yet", "job", job.ID, "slot", slot)
	}
	finished := r.clock.Now().UTC()
	var errText *string
	if runErr != nil {
		text := runErr.Error()
		errText = &text
	}
	// The end of the run is written without the caller's cancellation: a
	// shutdown must not leave the slot without an end, or the next start
	// would find an abandoned row.
	if err := q.FinishSlot(bookCtx, jobsdb.FinishSlotParams{
		FinishedAt: pgxTimestamptz(finished), Error: errText, JobID: job.ID, Slot: slot,
	}); err != nil {
		// The run itself is what matters: a failure to write the end of it
		// is reported, but the slot stays recorded, so it never runs twice.
		r.reportErr(fmt.Errorf("job %s: record the end of the slot %s: %w", job.ID, slot, err))
	}
	if runErr != nil {
		// The error is returned to sched, which reports it once. Reporting
		// it here as well would report it twice.
		return runErr
	}
	r.log().InfoContext(ctx, "job finished", "job", job.ID, "slot", slot)
	return nil
}

// runJob runs the job and turns a panic into an error: a panicking job
// must still record the slot, or the slot is lost for good.
func (r *runner) runJob(ctx context.Context, job Job, slot time.Time) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("job %s: panic: %v", job.ID, p)
		}
	}()
	return job.Run(ctx, slot)
}

// abandoned reports whether a recorded slot is an abandoned row: it has
// neither an end nor an error, so the run that wrote it never finished.
func abandoned(run jobsdb.ScheduledJobRun) bool {
	return !run.FinishedAt.Valid && run.Error == nil
}

// reportErr hands an error to the reporter, if there is one.
func (r *runner) reportErr(err error) {
	if r.report != nil {
		r.report(err)
	}
}

// isUniqueViolation reports whether err is Postgres's unique violation
// (SQLSTATE 23505): the slot's primary key, so another runner recorded it.
func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// pgxTimestamptz is the timestamptz of a time.
func pgxTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
