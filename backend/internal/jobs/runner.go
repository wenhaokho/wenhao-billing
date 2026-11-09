package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

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

// LockAcquireTimeout bounds the wait for the connection a job run takes. The
// pool is small (max(4, NumCPU) connections) and the API needs it too, so a
// job run must never wait for a connection forever: a run that cannot get
// one in time is skipped and logged, and the next tick tries again.
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

// Start runs the jobs until ctx ends.
//
// One goroutine runs them: on every tick (once at the start, then every
// TickInterval) it runs the due jobs one after another. The jobs never run
// at the same time inside one process, so a tick holds one pool connection
// at a time, not one per job.
//
// Each job runs under a Postgres advisory lock keyed on its id (Python's
// lock keys, scheduler.py:40-52), so the Python and the Go backends (and
// several Go processes) never run the same job at once: a run that cannot
// take the lock is skipped silently, as in Python.
//
// report hears about a job that failed, so the API can log it (and Sentry
// sees it); a failed job is recorded with its error and the slot is not run
// again.
//
// Start returns at once; the jobs run until ctx ends. The returned wait
// function blocks until the scheduler's goroutine has stopped. The caller
// (serve) uses it to wait for the scheduler before it closes the database
// pool.
func Start(ctx context.Context, pool *pgxpool.Pool, clock platform.Clock, report func(error), jobs ...Job) func() {
	return start(ctx, pool, clock, report, TickInterval, jobs...)
}

// start is Start with the tick interval the caller chooses; the tests use a
// short one.
//
// It returns a wait function: it blocks until the scheduler's goroutine has
// stopped. The API waits for it before it closes the database pool, and a
// test needs it too: it must not assert while a tick is still running.
func start(ctx context.Context, pool *pgxpool.Pool, clock platform.Clock, report func(error), interval time.Duration, jobs ...Job) func() {
	r := newRunner(pool, clock, report)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		r.loop(ctx, interval, jobs)
	}()
	return func() { <-stopped }
}

// runner holds what a tick needs.
type runner struct {
	pool   *pgxpool.Pool
	clock  platform.Clock
	report func(error)
	logger *slog.Logger
	// unlock releases the advisory lock. It is the statement, in a field
	// so a test can make it fail.
	unlock func(ctx context.Context, conn *pgxpool.Conn, key int64) error
	// lockTimeout is how long a job run waits for its connection.
	// LockAcquireTimeout in production; a test shortens it.
	lockTimeout time.Duration
}

// newRunner is the runner the tests drive.
func newRunner(pool *pgxpool.Pool, clock platform.Clock, report func(error), loggers ...*slog.Logger) *runner {
	r := &runner{pool: pool, clock: clock, report: report, unlock: releaseLock, lockTimeout: LockAcquireTimeout}
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

// log returns the runner's logger, slog's default when there is none.
func (r *runner) log() *slog.Logger {
	if r.logger != nil {
		return r.logger
	}
	return slog.Default()
}

// loop is the scheduler's goroutine: it ticks once at the start and then
// every interval, until the context ends.
//
// A tick runs the due jobs one after another, never in parallel: the pool
// is small and the API needs it, so the scheduler never takes more than one
// connection for itself at a time.
func (r *runner) loop(ctx context.Context, interval time.Duration, jobs []Job) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		r.tickAll(ctx, jobs)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tickAll runs every job once, one after another. It is the only place that
// adds the "job <id>:" prefix to a tick's error: the tick and the job work
// return errors without it, so the prefix never appears twice.
func (r *runner) tickAll(ctx context.Context, jobs []Job) {
	for _, job := range jobs {
		if ctx.Err() != nil {
			// The scheduler is stopping: the rest of the jobs wait for
			// the next start.
			return
		}
		err := r.tick(ctx, job)
		if err == nil {
			continue
		}
		if errors.Is(err, ctx.Err()) {
			// The shutdown cut the run short: not a job failure. The
			// slot is left abandoned and runs again after the next
			// start.
			continue
		}
		r.reportErr(fmt.Errorf("job %s: %w", job.ID, err))
	}
}

// tick runs a job's latest due slot, if it is due to run.
//
// The order is the one of the brief: the advisory lock is taken, then the
// slot row is written (its primary key makes a second runner of the same
// slot a no-op), then the job runs, then the row records the finish or the
// error. A slot that is recorded already is not run again, and a slot older
// than MaxSlotAge is recorded as skipped and logged, never run.
//
// A lock another process holds is not waited for: the run is skipped
// silently, as in Python ("job %s skipped: already running in another
// process").
//
// The whole run has one pool connection of the scheduler's own: the
// advisory lock and the slot bookkeeping run on it. The job's work runs
// after the bookkeeping and may open connections of its own (its queries
// do), but the scheduler never holds two connections for itself, and the
// jobs never run at the same time.
func (r *runner) tick(ctx context.Context, job Job) error {
	if ctx.Err() != nil {
		// The scheduler is stopping: this run does nothing. Without
		// this, a run that starts just after the cancel would take a
		// connection and write, while the caller waits for the run to
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
		// No connection in time: the run is skipped and logged.
		return nil
	}
	// A connection that still holds the lock must not go back to the pool:
	// the next user of it would inherit the lock. It is closed instead.
	released := false
	defer func() {
		if !released {
			// The lock may still be held (the unlock failed, or the run
			// ended without one): the session must die with the run, so
			// the connection is taken out of the pool and closed.
			raw := conn.Hijack()
			_ = raw.Close(context.WithoutCancel(ctx))
		}
	}()
	key := LockKey(job.ID)
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
		return fmt.Errorf("lock: %w", err)
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

// acquireLockConn takes the connection a job run lives on. The wait is
// bounded: the pool is small and the API needs it, so a run that waits
// forever for a connection would starve the API's requests as well. A run
// that cannot get one in time is skipped and logged.
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
			return nil, err
		}
		r.log().WarnContext(ctx, "job skipped: no database connection in time",
			"job", job.ID, "timeout", timeout)
		return nil, nil
	}
	return conn, nil
}

// runSlot is tick's work, under the advisory lock. The bookkeeping runs on
// the lock's own connection, so the scheduler never holds two pool
// connections of its own.
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
		return fmt.Errorf("read slot %s: %w", slot, err)
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
				return fmt.Errorf("record the skipped slot %s: %w", slot, err)
			}
		} else if err := q.SkipSlot(bookCtx, jobsdb.SkipSlotParams{
			JobID: job.ID, Slot: slot, StartedAt: now, Error: &reason,
		}); err != nil {
			return fmt.Errorf("record the skipped slot %s: %w", slot, err)
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
			return fmt.Errorf("restart the abandoned slot %s: %w", slot, err)
		}
	} else {
		// The slot row is written before the job runs: a second runner of
		// the same slot finds it and does nothing.
		if err := q.InsertSlot(bookCtx, jobsdb.InsertSlotParams{JobID: job.ID, Slot: slot, StartedAt: now}); err != nil {
			if isUniqueViolation(err) {
				// Another runner recorded the slot first.
				return nil
			}
			return fmt.Errorf("record the slot %s: %w", slot, err)
		}
	}
	// A job without work (a nil Run) is recorded and does nothing.
	var runErr error
	if job.Run != nil {
		runErr = r.runJob(ctx, job, slot)
	} else {
		r.log().InfoContext(ctx, "job has no work yet", "job", job.ID, "slot", slot)
	}
	if runErr != nil && ctx.Err() != nil && errors.Is(runErr, ctx.Err()) {
		// The job was cut short by the shutdown. The slot is left
		// abandoned (neither an end nor an error), so the next start
		// runs it again: a deploy must not lose a slot's work.
		r.log().InfoContext(ctx, "job cut short by the shutdown: the slot runs again after the next start",
			"job", job.ID, "slot", slot)
		return runErr
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
		// The error is returned to the scheduler, which reports it once.
		// Reporting it here as well would report it twice.
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
			err = fmt.Errorf("panic: %v", p)
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
