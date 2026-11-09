// Package jobs runs the scheduled jobs (Python's workers/scheduler.py): each
// job has a slot, runs once per slot, and takes a Postgres advisory lock
// keyed on its id so the Python and the Go backends never run it at once.
package jobs

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The slots of the jobs, all in UTC (Python's crons, scheduler.py:31-37).
const (
	// RecurringScanHour is the hour of the recurring scan.
	RecurringScanHour = 2
	// UsageLockScanHour is the hour of the usage-lock cut-off scan.
	UsageLockScanHour = 3
	// HostingEnforcementHour is the hour of the hosting enforcement.
	HostingEnforcementHour = 4
	// FXRatesSyncHour is the hour of the weekly FX sync.
	FXRatesSyncHour = 5
)

// RecurringScan is scan_and_generate (workers/tasks/recurring.py:47-72): the
// recurring templates with a cycle due today generate their drafts, and the
// digest of the new drafts goes to the users.
//
// The job works on the slot's day, not on the day it happens to run: a run
// caught up after a restart does the day it was due for, so no day's work is
// skipped (and the slot of the next day does that day's work).
func RecurringScan(pool *pgxpool.Pool, notify invoices.DraftNotifier) Job {
	return Job{
		ID:  RecurringScanID,
		Due: DailySlot(RecurringScanHour),
		Run: func(ctx context.Context, slot time.Time) error {
			_, err := invoices.Scan(ctx, pool, slot, notify)
			return err
		},
	}
}

// UsageLockScan is cutoff_scan (workers/tasks/usage_lock.py:27-52): the
// usage drafts past their cut-off day are locked.
//
// The accrual source is Python's stub: it raises NotImplementedError, so a
// run with work to do fails as a whole (the change is kept, port note).
func UsageLockScan(pool *pgxpool.Pool) Job {
	return Job{
		ID:  UsageLockScanID,
		Due: DailySlot(UsageLockScanHour),
		Run: func(ctx context.Context, slot time.Time) error {
			_, err := invoices.CutoffScan(ctx, pool, slot, StubAccrual)
			return err
		},
	}
}

// StubAccrual is Python's _stub_accrued (workers/tasks/usage_lock.py:23-24):
// the usage metering has no source yet, so it fails. The whole run fails
// with it, as in Python.
func StubAccrual(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	return decimal.Zero, errors.New("wire usage-metering source before enabling usage-lock beat")
}

// FXRatesSync is sync_fx_rates (workers/tasks/fx_sync.py:60-66): the
// provider's rates are pulled into fx_rates.
func FXRatesSync(pool *pgxpool.Pool, cfg platform.Config, client *http.Client) Job {
	return Job{
		ID:  FXRatesSyncID,
		Due: WeeklySlot(time.Monday, FXRatesSyncHour),
		Run: func(ctx context.Context, _ time.Time) error {
			_, err := fx.Sync(ctx, pool, cfg, client)
			return err
		},
	}
}

// HostingEnforcement is daily_hosting_enforcement
// (workers/tasks/hosting_enforcement.py:43-47): the overdue hosting is
// suspended and the paid hosting is restored.
//
// The work itself is in the hosting package, which the integrations plan
// brings; the job's slot and lock are here, so the plan can add the job to
// the list the API starts.
func HostingEnforcement(run func(ctx context.Context, slot time.Time) error) Job {
	return Job{
		ID:  HostingEnforcementID,
		Due: DailySlot(HostingEnforcementHour),
		Run: run,
	}
}

// SchedulerEnabled reports whether the scheduler runs. The variable is
// SCHEDULER_ENABLED, Python's name (Python's default was true; Go's is
// false, Go change 8: the scheduler is off until the switch-over).
func SchedulerEnabled(vars map[string]string) bool {
	raw, ok := vars["SCHEDULER_ENABLED"]
	if !ok {
		return false
	}
	switch raw {
	case "true", "TRUE", "True", "1", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
}
