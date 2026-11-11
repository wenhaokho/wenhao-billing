package stats_test

import (
	"testing"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

func utcDay(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

func TestNewWindow(t *testing.T) {
	t.Parallel()
	today := utcDay(2026, time.November, 12)
	tests := []struct {
		name       string
		period     stats.Period
		from, to   time.Time
		prevFrom   time.Time
		prevTo     time.Time
		buckets    int
		firstFrom  time.Time
		firstTo    time.Time
		firstLabel string
		lastTo     time.Time
	}{
		{
			name: "30d", period: "30d",
			from: utcDay(2026, time.October, 14), to: today,
			prevFrom: utcDay(2026, time.September, 14), prevTo: utcDay(2026, time.October, 13),
			buckets:   30,
			firstFrom: utcDay(2026, time.October, 14), firstTo: utcDay(2026, time.October, 14),
			firstLabel: "14 Oct", lastTo: today,
		},
		{
			name: "90d", period: "90d",
			from: utcDay(2026, time.August, 15), to: today,
			prevFrom: utcDay(2026, time.May, 17), prevTo: utcDay(2026, time.August, 14),
			buckets:   14,
			firstFrom: utcDay(2026, time.August, 15), firstTo: utcDay(2026, time.August, 16),
			firstLabel: "15 Aug", lastTo: today,
		},
		{
			name: "12m", period: "12m",
			from: utcDay(2025, time.November, 13), to: today,
			prevFrom: utcDay(2024, time.November, 13), prevTo: utcDay(2025, time.November, 12),
			buckets:   13,
			firstFrom: utcDay(2025, time.November, 13), firstTo: utcDay(2025, time.November, 30),
			firstLabel: "Nov", lastTo: today,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := stats.NewWindow(tc.period, today)
			if w.Period != tc.period {
				t.Errorf("Period = %q", w.Period)
			}
			if !w.From.Equal(tc.from) || !w.To.Equal(tc.to) {
				t.Errorf("window = %v..%v, want %v..%v", w.From, w.To, tc.from, tc.to)
			}
			if !w.PrevFrom.Equal(tc.prevFrom) || !w.PrevTo.Equal(tc.prevTo) {
				t.Errorf("prev = %v..%v, want %v..%v", w.PrevFrom, w.PrevTo, tc.prevFrom, tc.prevTo)
			}
			if len(w.Buckets) != tc.buckets {
				t.Fatalf("buckets = %d, want %d", len(w.Buckets), tc.buckets)
			}
			first, last := w.Buckets[0], w.Buckets[len(w.Buckets)-1]
			if !first.From.Equal(tc.firstFrom) || !first.To.Equal(tc.firstTo) || first.Label != tc.firstLabel {
				t.Errorf("first bucket = %+v", first)
			}
			if !last.To.Equal(tc.lastTo) {
				t.Errorf("last bucket To = %v, want %v", last.To, tc.lastTo)
			}
			// No gap or overlap: each bucket starts the day after the previous ends.
			if !first.From.Equal(w.From) {
				t.Errorf("first bucket From = %v, want %v", first.From, w.From)
			}
			for i := 1; i < len(w.Buckets); i++ {
				if want := w.Buckets[i-1].To.AddDate(0, 0, 1); !w.Buckets[i].From.Equal(want) {
					t.Errorf("bucket %d From = %v, want %v", i, w.Buckets[i].From, want)
				}
				if w.Buckets[i].To.Before(w.Buckets[i].From) {
					t.Errorf("bucket %d inverted: %+v", i, w.Buckets[i])
				}
			}
		})
	}

	t.Run("12m labels", func(t *testing.T) {
		t.Parallel()
		w := stats.NewWindow("12m", today)
		want := []string{"Nov", "Dec", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov"}
		for i, b := range w.Buckets {
			if b.Label != want[i] {
				t.Errorf("bucket %d label = %q, want %q", i, b.Label, want[i])
			}
		}
		if last := w.Buckets[12]; !last.From.Equal(utcDay(2026, time.November, 1)) {
			t.Errorf("last month From = %v", last.From)
		}
	})

	t.Run("90d weeks start on Monday", func(t *testing.T) {
		t.Parallel()
		w := stats.NewWindow("90d", today)
		for i, b := range w.Buckets[1:] {
			if b.From.Weekday() != time.Monday {
				t.Errorf("bucket %d starts on %v", i+1, b.From.Weekday())
			}
		}
		if got := w.Buckets[1].Label; got != "17 Aug" {
			t.Errorf("second label = %q", got)
		}
	})
}

func TestParsePeriod(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]stats.Period{"": "12m", "30d": "30d", "90d": "90d", "12m": "12m"} {
		t.Run("ok "+in, func(t *testing.T) {
			t.Parallel()
			got, err := stats.ParsePeriod(in)
			if err != nil || got != want {
				t.Fatalf("ParsePeriod(%q) = %q, %v; want %q", in, got, err, want)
			}
		})
	}
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		if _, err := stats.ParsePeriod("7d"); err == nil {
			t.Fatal("want error")
		}
	})
}
