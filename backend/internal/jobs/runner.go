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
func start(ctx context.Context, pool *pgxpool.Pool, clock platform.Clock, report func(error), interval time.Duration, jobs ...Job) {
	r := newRunner(pool, clock, report)
	schedJobs := make([]sched.Job, 0, len(jobs))
	for _, j := range jobs {
		schedJobs = append(schedJobs, sched.Job{
			Name:  j.ID,
			Every: interval,
			// The lock is taken by the runner itself (it covers the slot
			// bookkeeping too), so sched's own key is not used.
			Run: func(ctx context.Context) error {
				return r.tick(ctx, j)
			},
		})
	}
	sched.Start(ctx, pool, func(job string, err error) {
		if report != nil {
			report(fmt.Errorf("job %s: %w", job, err))
		}
	}, schedJobs...)
}

// runner holds what a tick needs.
type runner struct {
	pool   *pgxpool.Pool
	clock  platform.Clock
	report func(error)
	logger *slog.Logger
}

// newRunner is the runner the tests drive.
func newRunner(pool *pgxpool.Pool, clock platform.Clock, report func(error), loggers ...*slog.Logger) *runner {
	r := &runner{pool: pool, clock: clock, report: report}
	if len(loggers) > 0 {
		r.logger = loggers[0]
	}
	return r
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
func (r *runner) tick(ctx context.Context, job Job) error {
	now := r.clock.Now().UTC()
	slot := job.Due(now)
	if slot.IsZero() {
		return nil
	}
	// The advisory lock is held on a connection of its own, like Python's
	// (scheduler.py:42-52): the lock belongs to the session, not to the
	// transaction.
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("job %s: %w", job.ID, err)
	}
	defer conn.Release()
	key := LockKey(job.ID)
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
		return fmt.Errorf("job %s: lock: %w", job.ID, err)
	}
	if !locked {
		r.log().InfoContext(ctx, "job skipped: already running in another process", "job", job.ID)
		return nil
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", key); err != nil {
			r.reportErr(fmt.Errorf("job %s: unlock: %w", job.ID, err))
		}
	}()
	return r.runSlot(ctx, job, slot, now)
}

// runSlot is tick's work, under the advisory lock.
func (r *runner) runSlot(ctx context.Context, job Job, slot, now time.Time) error {
	q := jobsdb.New(r.pool)
	_, err := q.GetSlot(ctx, jobsdb.GetSlotParams{JobID: job.ID, Slot: slot})
	switch {
	case err == nil:
		// The slot is recorded: it ran already.
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("job %s: read slot %s: %w", job.ID, slot, err)
	}
	if now.Sub(slot) > MaxSlotAge {
		reason := fmt.Sprintf("slot %s is older than %s: skipped", slot.Format(time.RFC3339), MaxSlotAge)
		if err := q.SkipSlot(ctx, jobsdb.SkipSlotParams{
			JobID: job.ID, Slot: slot, StartedAt: now, Error: &reason,
		}); err != nil {
			return fmt.Errorf("job %s: record the skipped slot %s: %w", job.ID, slot, err)
		}
		r.log().InfoContext(ctx, "job skipped: the slot is too old",
			"job", job.ID, "slot", slot, "now", now)
		return nil
	}
	// The slot row is written before the job runs: a second runner of the
	// same slot finds it and does nothing.
	if err := q.InsertSlot(ctx, jobsdb.InsertSlotParams{JobID: job.ID, Slot: slot, StartedAt: now}); err != nil {
		if isUniqueViolation(err) {
			// Another runner recorded the slot first.
			return nil
		}
		return fmt.Errorf("job %s: record the slot %s: %w", job.ID, slot, err)
	}
	runErr := job.Run(ctx, slot)
	finished := r.clock.Now().UTC()
	var errText *string
	if runErr != nil {
		text := runErr.Error()
		errText = &text
	}
	if err := q.FinishSlot(ctx, jobsdb.FinishSlotParams{
		FinishedAt: pgxTimestamptz(finished), Error: errText, JobID: job.ID, Slot: slot,
	}); err != nil {
		// The run itself is what matters: a failure to write the end of it
		// is reported, but the slot stays recorded, so it never runs twice.
		r.reportErr(fmt.Errorf("job %s: record the end of the slot %s: %w", job.ID, slot, err))
	}
	if runErr != nil {
		r.reportErr(fmt.Errorf("job %s: %w", job.ID, runErr))
		return runErr
	}
	r.log().InfoContext(ctx, "job finished", "job", job.ID, "slot", slot)
	return nil
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
