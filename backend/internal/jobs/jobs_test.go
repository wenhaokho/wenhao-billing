package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// uuidZero is an invoice id for the stub's call.
func uuidZero() uuid.UUID { return uuid.Nil }

// platformConfig is a config for the fx job.
func platformConfig() platform.Config { return platform.Config{BaseCurrency: "IDR"} }

// TestJobIdsAndSlots: the jobs are Python's four (scheduler.py:31-37), with
// Python's UTC slots.
func TestJobIdsAndSlots(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		id   string
		due  func(time.Time) time.Time
		want time.Time
	}{
		{RecurringScanID, DailySlot(2), time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)},
		{UsageLockScanID, DailySlot(3), time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)},
		{HostingEnforcementID, DailySlot(4), time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)},
		{FXRatesSyncID, WeeklySlot(time.Monday, 5), time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			if got := tc.due(now); !got.Equal(tc.want) {
				t.Errorf("slot = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestTheJobsCarryPythonIds: the jobs the API starts have Python's ids, so
// their advisory lock keys are Python's.
func TestTheJobsCarryPythonIds(t *testing.T) {
	t.Parallel()
	notifier := invoices.LogNotifier{}
	recurring := RecurringScan(nil, notifier)
	if recurring.ID != "recurring-scan-and-generate" {
		t.Errorf("the recurring job's id = %q", recurring.ID)
	}
	usage := UsageLockScan(nil)
	if usage.ID != "usage-lock-cutoff-scan" {
		t.Errorf("the usage job's id = %q", usage.ID)
	}
	fxJob := FXRatesSync(nil, platformConfig(), nil)
	if fxJob.ID != "fx-rates-weekly-sync" {
		t.Errorf("the fx job's id = %q", fxJob.ID)
	}
	hosting := HostingEnforcement(func(context.Context, time.Time) error { return nil })
	if hosting.ID != "hosting-overdue-enforcement" {
		t.Errorf("the hosting job's id = %q", hosting.ID)
	}
}

// TestTheStubAccrualFails: the usage job's accrual source fails like
// Python's stub (workers/tasks/usage_lock.py:23-24): the metering has no
// source yet, so a run with work to do fails.
func TestTheStubAccrualFails(t *testing.T) {
	t.Parallel()
	if _, err := StubAccrual(context.Background(), uuidZero()); err == nil {
		t.Error("the stub accrual source returned no error, want a failure")
	}
}

// TestHostingEnforcementRunsItsWork: the hosting job runs the work the
// integrations plan gives it, in its slot.
func TestHostingEnforcementRunsItsWork(t *testing.T) {
	t.Parallel()
	var gotSlot time.Time
	job := HostingEnforcement(func(_ context.Context, slot time.Time) error {
		gotSlot = slot
		return nil
	})
	want := time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)
	if got := job.Due(want.Add(time.Hour)); !got.Equal(want) {
		t.Errorf("slot = %s, want %s", got, want)
	}
	if err := job.Run(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if !gotSlot.Equal(want) {
		t.Errorf("the job ran in slot %s, want %s", gotSlot, want)
	}
}
