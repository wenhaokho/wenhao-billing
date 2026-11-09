package platform

import "time"

// Clock tells the time. Code that needs "now" takes a Clock, so tests fix it.
type Clock interface{ Now() time.Time }

// SystemClock is the real clock, and the only place time.Now is called.
type SystemClock struct{}

// Now is the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// TodayUTC is the clock's UTC calendar date, at midnight UTC (Python's
// date.today() in a UTC container). It is the one "today" every package uses.
func TodayUTC(c Clock) time.Time {
	n := c.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}
