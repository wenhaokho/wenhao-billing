package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

type workerJob func(context.Context) error

// Each job runs serially and waits between attempts, including after errors or panics.
func startWorkers(ctx context.Context, enabled bool, interval, timeout time.Duration, logger *log.Logger, jobs ...workerJob) *sync.WaitGroup {
	w := &sync.WaitGroup{}
	if !enabled {
		return w
	}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		w.Add(1)
		go func(job workerJob) {
			defer w.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				func() {
					defer func() {
						if recover() != nil {
							logger.Print("delivery worker failed (panic)")
						}
					}()
					runCtx, cancel := context.WithTimeout(ctx, timeout)
					defer cancel()
					if err := job(runCtx); err != nil && ctx.Err() == nil {
						logger.Print("delivery worker failed")
					}
				}()
				timer := time.NewTimer(interval)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					return
				case <-timer.C:
				}
			}
		}(job)
	}
	return w
}
func schedulerEnabledFromEnv() (bool, error) {
	v, ok := os.LookupEnv("SCHEDULER_ENABLED")
	if !ok {
		return true, nil
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		return false, errors.New("invalid SCHEDULER_ENABLED")
	}
	return enabled, nil
}

type loggedSender struct {
	sender email.Sender
	logger *log.Logger
}

func (s loggedSender) Send(ctx context.Context, message email.Message) error {
	err := s.sender.Send(ctx, message)
	if err != nil && ctx.Err() == nil {
		s.logger.Print("recurring delivery failed")
	}
	return err
}

type loggedHostingProvider struct {
	provider platform.HostingRestoreProvider
	logger   *log.Logger
}

func (p loggedHostingProvider) Restore(ctx context.Context, target platform.HostingRestoreTarget) error {
	err := p.provider.Restore(ctx, target)
	if err != nil && ctx.Err() == nil {
		p.logger.Print("hosting restore failed")
	}
	return err
}

// nextRecurringScan returns the next scheduled 02:00 UTC, strictly after now.
func nextRecurringScan(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func nextUsageScan(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func nextHostingScan(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func startDailyWorker(ctx context.Context, enabled bool, now func() time.Time, wait func(time.Duration) (<-chan time.Time, func()), logger *log.Logger, job workerJob) *sync.WaitGroup {
	return startScheduledDailyWorker(ctx, enabled, now, wait, logger, nextRecurringScan, "recurring scan", job)
}

func startScheduledDailyWorker(ctx context.Context, enabled bool, now func() time.Time, wait func(time.Duration) (<-chan time.Time, func()), logger *log.Logger, next func(time.Time) time.Time, label string, job workerJob) *sync.WaitGroup {
	w := &sync.WaitGroup{}
	if !enabled || job == nil {
		return w
	}
	w.Add(1)
	go func() {
		defer w.Done()
		for {
			t := now()
			ch, stop := wait(next(t).Sub(t))
			select {
			case <-ctx.Done():
				stop()
				return
			case <-ch:
				stop()
			}
			if ctx.Err() != nil {
				return
			}
			func() {
				defer func() {
					if recover() != nil {
						logger.Print(label + " failed (panic)")
					}
				}()
				if err := job(ctx); err != nil && ctx.Err() == nil {
					logger.Print(label + " failed")
				}
			}()
		}
	}()
	return w
}
func dailyTimer(d time.Duration) (<-chan time.Time, func()) {
	timer := time.NewTimer(d)
	return timer.C, func() { timer.Stop() }
}

// usageScanJob logs only structured, allow-listed details, never provider or database errors.
func usageScanJob(logger *log.Logger, scan func(context.Context) (int, error)) workerJob {
	return func(ctx context.Context) error {
		count, err := scan(ctx)
		if ctx.Err() != nil {
			return err
		}
		var report *platform.UsageScanError
		if errors.As(err, &report) {
			logger.Printf("usage cutoff scan finished: locked=%d failed=%d", report.Locked, len(report.Failures))
			for _, failure := range report.Failures {
				var id pgtype.UUID
				if id.Scan(failure.InvoiceID) != nil {
					continue
				}
				switch failure.Category {
				case platform.UsageMeterFailed, platform.UsageInvalidBillingRef, platform.UsageInvoiceReadFailed, platform.UsageInvoiceUpdateFailed:
					value, _ := id.Value()
					logger.Printf("usage cutoff scan invoice=%s category=%s", value, failure.Category)
				}
			}
		} else if err == nil {
			logger.Printf("usage cutoff scan finished: locked=%d failed=0", count)
		}
		return err
	}
}

// nextFXSync returns the next Monday at 05:00 UTC, strictly after now.
func nextFXSync(now time.Time) time.Time {
	now = now.UTC()
	days := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	next := time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, time.UTC).AddDate(0, 0, days)
	if !next.After(now) {
		next = next.AddDate(0, 0, 7)
	}
	return next
}

// weeklyFXJob bounds provider and lock waits and reports only allow-listed status.
func weeklyFXJob(logger *log.Logger, timeout time.Duration, run func(context.Context) error) workerJob {
	return func(parent context.Context) error {
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()
		err := run(ctx)
		if parent.Err() != nil {
			return nil
		}
		status := "finished"
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			status = "timeout"
		case errors.Is(err, platform.ErrFXJobAlreadyRunning):
			status = "skipped"
		case err != nil:
			status = "failed"
		}
		logger.Printf("job=fx-rates-weekly-sync status=%s", status)
		return nil
	}
}
