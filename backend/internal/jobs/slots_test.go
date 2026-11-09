package jobs

import (
	"testing"
	"time"
)

// TestLockKeysMatchPython: the advisory lock keys are the unsigned crc32 of
// the job id (Python's zlib.crc32, scheduler.py:41). Two of them are above
// 2^31, so they must reach Postgres as int64, never as an int32 cast: the
// Python and the Go backends have to lock the same key.
func TestLockKeysMatchPython(t *testing.T) {
	t.Parallel()
	want := map[string]int64{
		"recurring-scan-and-generate": 3587987709,
		"usage-lock-cutoff-scan":      2413487788,
		"hosting-overdue-enforcement": 1601569364,
		"fx-rates-weekly-sync":        1988665425,
	}
	for id, key := range want {
		if got := LockKey(id); got != key {
			t.Errorf("LockKey(%q) = %d, want %d", id, got, key)
		}
	}
}

// TestDueSlotsDailyAndWeekly: the slots are the UTC hours of the Python
// crons (scheduler.py:32-36): daily 02:00, 03:00, 04:00 and Monday 05:00.
func TestDueSlotsDailyAndWeekly(t *testing.T) {
	t.Parallel()
	// A Wednesday, 10 November 2026.
	now := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		due  func(time.Time) time.Time
		want time.Time
	}{
		{"recurring", DailySlot(2), time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)},
		{"usage", DailySlot(3), time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)},
		{"hosting", DailySlot(4), time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)},
		{"fx", WeeklySlot(time.Monday, 5), time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.due(now); !got.Equal(tc.want) {
				t.Errorf("slot = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestDueSlotsBeforeTheFirstSlot: before the slot of the day has passed,
// the latest due slot is the previous day's.
func TestDueSlotsBeforeTheFirstSlot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 1, 59, 0, 0, time.UTC)
	got := DailySlot(2)(now)
	want := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("slot = %s, want %s", got, want)
	}
}

// TestDueSlotsOnMonday: on a Monday the weekly slot is the same day, before
// 05:00 it is the previous Monday.
func TestDueSlotsOnMonday(t *testing.T) {
	t.Parallel()
	monday := time.Date(2026, 11, 9, 9, 30, 0, 0, time.UTC)
	if got, want := WeeklySlot(time.Monday, 5)(monday), monday.Truncate(24*time.Hour).Add(5*time.Hour); !got.Equal(want) {
		t.Errorf("slot = %s, want %s", got, want)
	}
	early := time.Date(2026, 11, 9, 4, 59, 0, 0, time.UTC)
	want := time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC)
	if got := WeeklySlot(time.Monday, 5)(early); !got.Equal(want) {
		t.Errorf("slot = %s, want %s", got, want)
	}
}
