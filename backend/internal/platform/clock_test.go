package platform

import (
	"testing"
	"time"
)

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

func TestTodayUTCAcrossMidnightJakarta(t *testing.T) {
	t.Parallel()
	jakarta := time.FixedZone("WIB", 7*3600)
	got := TodayUTC(fixedClock(time.Date(2026, 1, 10, 3, 0, 0, 0, jakarta)))
	want := time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("TodayUTC = %v, want %v", got, want)
	}
}
