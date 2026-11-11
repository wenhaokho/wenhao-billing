package stats

import (
	"fmt"
	"time"
)

// Period is a dashboard analytics window: "30d", "90d" or "12m".
type Period string

// The supported periods.
const (
	Period30d Period = "30d"
	Period90d Period = "90d"
	Period12m Period = "12m"
)

// Bucket is one chart bucket; From and To are inclusive UTC dates.
type Bucket struct {
	Label    string
	From, To time.Time
}

// Window is a Period resolved against a day: the window itself, the equal-length
// window before it, and the chart buckets that tile [From, To].
type Window struct {
	Period           Period
	From, To         time.Time // To = today
	PrevFrom, PrevTo time.Time // equal length, ending the day before From
	Buckets          []Bucket  // days (30d), ISO weeks (90d), calendar months (12m), oldest first
}

// ParsePeriod maps a query value to a Period; empty means 12m.
func ParsePeriod(s string) (Period, error) {
	switch Period(s) {
	case "":
		return Period12m, nil
	case Period30d, Period90d, Period12m:
		return Period(s), nil
	}
	return "", fmt.Errorf("unknown period %q (want 30d, 90d or 12m)", s)
}

// NewWindow resolves p against today (a UTC calendar day).
func NewWindow(p Period, today time.Time) Window {
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	w := Window{Period: p, To: today}
	switch p {
	case Period30d:
		w.From = today.AddDate(0, 0, -29)
		w.PrevFrom = w.From.AddDate(0, 0, -30)
		w.Buckets = dayBuckets(w.From, w.To)
	case Period90d:
		w.From = today.AddDate(0, 0, -89)
		w.PrevFrom = w.From.AddDate(0, 0, -90)
		w.Buckets = weekBuckets(w.From, w.To)
	case Period12m:
		w.From = today.AddDate(-1, 0, 1)
		w.PrevFrom = w.From.AddDate(-1, 0, 0)
		w.Buckets = monthBuckets(w.From, w.To)
		if n := len(w.Buckets); n > 12 {
			// The clipped first and last months share a name; the year tells them apart.
			w.Buckets[0].Label = w.Buckets[0].From.Format("Jan 2006")
			w.Buckets[n-1].Label = w.Buckets[n-1].From.Format("Jan 2006")
		}
	default:
		return NewWindow(Period12m, today)
	}
	w.PrevTo = w.From.AddDate(0, 0, -1)
	return w
}

func dayLabel(t time.Time) string { return t.Format("2 Jan") }

func dayBuckets(from, to time.Time) []Bucket {
	var out []Bucket
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, Bucket{Label: dayLabel(d), From: d, To: d})
	}
	return out
}

// weekBuckets splits [from, to] at Mondays (ISO weeks), clipping the ends.
func weekBuckets(from, to time.Time) []Bucket {
	var out []Bucket
	for start := from; !start.After(to); {
		// Days until the next Monday: 1..7.
		next := start.AddDate(0, 0, 7-(int(start.Weekday())+6)%7)
		end := earlier(next.AddDate(0, 0, -1), to)
		out = append(out, Bucket{Label: dayLabel(start), From: start, To: end})
		start = next
	}
	return out
}

// monthBuckets splits [from, to] at month starts, clipping the ends.
func monthBuckets(from, to time.Time) []Bucket {
	var out []Bucket
	for start := from; !start.After(to); {
		next := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		end := earlier(next.AddDate(0, 0, -1), to)
		out = append(out, Bucket{Label: start.Format("Jan"), From: start, To: end})
		start = next
	}
	return out
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
