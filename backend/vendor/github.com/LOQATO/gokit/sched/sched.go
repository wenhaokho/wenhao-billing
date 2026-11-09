// Package sched runs jobs on a timer inside an API process, each under a
// Postgres advisory lock, so any number of processes can run the same jobs
// and only one does the work at a time.
package sched

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Job is work to repeat. LockKey identifies it to Postgres: a tick that
// cannot take the lock (another process holds it) is skipped.
type Job struct {
	Name    string
	Every   time.Duration
	LockKey int64
	Run     func(context.Context) error
}

// Start runs each job on its own timer until ctx ends. An error a job
// returns, a panic, or a failure to take the lock goes to report, and the
// job runs again on its next tick: one bad run never stops the schedule.
func Start(ctx context.Context, pool *pgxpool.Pool, report func(job string, err error), jobs ...Job) {
	for _, job := range jobs {
		go loop(ctx, pool, report, job)
	}
}

func loop(ctx context.Context, pool *pgxpool.Pool, report func(string, error), job Job) {
	ticker := time.NewTicker(job.Every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := tick(ctx, pool, job); err != nil {
				report(job.Name, err)
			}
		}
	}
}

func tick(ctx context.Context, pool *pgxpool.Pool, job Job) (err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("sched %s: %w", job.Name, err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", job.LockKey).Scan(&locked); err != nil {
		return fmt.Errorf("sched %s: lock: %w", job.Name, err)
	}
	if !locked {
		return nil
	}
	defer func() {
		if _, unlockErr := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", job.LockKey); unlockErr != nil && err == nil {
			err = fmt.Errorf("sched %s: unlock: %w", job.Name, unlockErr)
		}
	}()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("sched %s: panic: %v", job.Name, p)
		}
	}()
	return job.Run(ctx)
}
