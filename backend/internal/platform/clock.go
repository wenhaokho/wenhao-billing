package platform

import "time"

// Clock tells the time. Code that needs "now" takes a Clock, so tests fix it.
type Clock interface{ Now() time.Time }

// SystemClock is the real clock, and the only place time.Now is called.
type SystemClock struct{}

// Now is the current time.
func (SystemClock) Now() time.Time { return time.Now() }
