package testkit

import (
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/i18n"
)

// Redis is an in-memory Redis for one test, closed when it ends.
func Redis(t testing.TB) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// Clock is a time source tests move by hand.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock starts a Clock at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now is the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// API is a Huma API on a chi router, with apierr's Envelope errors and c's
// Indonesian installed (process-wide, see apierr.Install)
// and the router's 404 and 405 answering in the same shape. Register
// operations on the API and serve requests through the mux.
func API(t testing.TB, c *i18n.Catalog) (huma.API, *chi.Mux) {
	t.Helper()
	apierr.Install(apierr.Envelope, c)
	mux := chi.NewMux()
	mux.NotFound(apierr.NotFound(apierr.Envelope, c))
	mux.MethodNotAllowed(apierr.MethodNotAllowed(apierr.Envelope, c))
	return humachi.New(mux, huma.DefaultConfig("test", "0.0.0")), mux
}
