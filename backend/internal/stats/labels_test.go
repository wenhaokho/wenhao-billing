package stats

import (
	"slices"
	"testing"
	"time"
)

func TestMonthLabels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		today time.Time
		first string
		last  string
	}{
		{"december", time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), "2025-01", "2025-12"},
		{"january", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2025-02", "2026-01"},
		{"july", time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC), "2025-08", "2026-07"},
		{"leap day", time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), "2023-03", "2024-02"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := monthLabels(c.today)
			if len(got) != 12 || got[0] != c.first || got[11] != c.last {
				t.Fatalf("labels = %v, want %s..%s", got, c.first, c.last)
			}
			if !slices.IsSorted(got) {
				t.Fatalf("labels not oldest first: %v", got)
			}
		})
	}
	// December: the twelve calendar months of the year, in order.
	dec := monthLabels(time.Date(2025, 12, 15, 0, 0, 0, 0, time.UTC))
	for i, l := range dec {
		if want := time.Date(2025, time.Month(i+1), 1, 0, 0, 0, 0, time.UTC).Format("2006-01"); l != want {
			t.Fatalf("label %d = %s, want %s", i, l, want)
		}
	}
}
