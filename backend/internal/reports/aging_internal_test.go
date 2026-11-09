package reports

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestDaysPastDueIsCalendarDaysWhateverTheZone(t *testing.T) {
	t.Parallel()
	due := pgtype.Date{Time: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC), Valid: true}
	for _, c := range []struct {
		name string
		asOf time.Time
		want int
	}{
		{"UTC midnight", time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), 1},
		// 17 hours after the due date's UTC midnight, but the next calendar day.
		{"midnight in +07:00", time.Date(2026, 3, 31, 0, 0, 0, 0, time.FixedZone("WIB", 7*3600)), 1},
		// 31 hours after the due date's UTC midnight: still one calendar day by the zone's own date.
		{"late evening in -08:00", time.Date(2026, 3, 31, 23, 0, 0, 0, time.FixedZone("PST", -8*3600)), 1},
		{"same day", time.Date(2026, 3, 30, 23, 59, 0, 0, time.UTC), 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := daysPastDue(c.asOf, due); got != c.want {
				t.Errorf("daysPastDue = %d, want %d", got, c.want)
			}
		})
	}
	if got := daysPastDue(time.Now(), pgtype.Date{}); got != 0 {
		t.Errorf("null due date: %d, want 0", got)
	}
}
