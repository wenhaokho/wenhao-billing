package schedule_test

import (
	"testing"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/schedule"
)

func TestNoneConfigReturnsNone(t *testing.T) {
	t.Parallel()
	_, ok := schedule.CycleKeyFor(nil, day(2026, 5, 1))
	assert.False(t, ok)
}

func TestMalformedConfigReturnsNone(t *testing.T) {
	t.Parallel()
	_, ok := schedule.CycleKeyFor(map[string]any{"frequency": "NOPE"}, day(2026, 5, 1))
	assert.False(t, ok)
}

func TestBeforeStartReturnsNone(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"}
	_, ok := schedule.CycleKeyFor(cfg, day(2026, 4, 30))
	assert.False(t, ok)
}

func TestMonthlyReturnsISOCycleStart(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"}
	k, ok := schedule.CycleKeyFor(cfg, day(2026, 5, 10))
	assert.True(t, ok)
	assert.Equal(t, "2026-05-01", k)
	k, ok = schedule.CycleKeyFor(cfg, day(2026, 6, 15))
	assert.True(t, ok)
	assert.Equal(t, "2026-06-01", k)
}

func TestWeeklyIntervalTwo(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{"frequency": "WEEKLY", "interval": 2, "start_date": "2026-05-04"}
	k, _ := schedule.CycleKeyFor(cfg, day(2026, 5, 17))
	assert.Equal(t, "2026-05-04", k)
	k, _ = schedule.CycleKeyFor(cfg, day(2026, 5, 18))
	assert.Equal(t, "2026-05-18", k)
}

func TestPastEndDateReturnsNone(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"frequency": "MONTHLY", "interval": 1, "start_date": "2026-01-01",
		"end_mode": "ON_DATE", "end_date": "2026-03-31",
	}
	k, ok := schedule.CycleKeyFor(cfg, day(2026, 3, 31))
	assert.True(t, ok)
	assert.Equal(t, "2026-03-01", k)
	_, ok = schedule.CycleKeyFor(cfg, day(2026, 4, 1))
	assert.False(t, ok)
}

func TestPausedTemplateReturnsNoneEvenWhenCycleIsDue(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01", "paused": true}
	_, ok := schedule.CycleKeyFor(cfg, day(2026, 5, 15))
	assert.False(t, ok)
	cfg["paused"] = false // without paused the cycle is due
	k, ok := schedule.CycleKeyFor(cfg, day(2026, 5, 15))
	assert.True(t, ok)
	assert.Equal(t, "2026-05-01", k)
}

func TestPastAfterNReturnsNone(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"frequency": "MONTHLY", "interval": 1, "start_date": "2026-01-01",
		"end_mode": "AFTER_N", "end_after_cycles": 3,
	}
	k, ok := schedule.CycleKeyFor(cfg, day(2026, 3, 15))
	assert.True(t, ok)
	assert.Equal(t, "2026-03-01", k)
	_, ok = schedule.CycleKeyFor(cfg, day(2026, 4, 1))
	assert.False(t, ok)
}
