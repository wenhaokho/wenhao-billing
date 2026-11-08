package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Throttle limits sign-in attempts per key (a lower-cased email, a
// normalised phone): Limit attempts in Window, counted from the first.
// Count every attempt with Hit BEFORE checking its password, never after a
// failure, or a burst of parallel guesses all pass the check together.
// Reset after a success.
type Throttle struct {
	Redis  *redis.Client
	Prefix string
	Limit  int
	Window time.Duration
}

// INCR and EXPIRE in one script: as two calls, a process that died between
// them left a counter with no TTL, locking the key for good. The TTL is set
// whenever the key has none, so such a counter heals on the next attempt.
var admit = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if redis.call('TTL', KEYS[1]) < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return count
`)

// Hit counts an attempt and reports whether it is within the limit.
func (t Throttle) Hit(ctx context.Context, key string) (bool, error) {
	n, err := admit.Run(ctx, t.Redis, []string{t.Prefix + ":" + key}, int(t.Window.Seconds())).Int()
	if err != nil {
		return false, fmt.Errorf("auth: throttle: %w", err)
	}
	return n <= t.Limit, nil
}

// Reset clears the key's count.
func (t Throttle) Reset(ctx context.Context, key string) error {
	if err := t.Redis.Del(ctx, t.Prefix+":"+key).Err(); err != nil {
		return fmt.Errorf("auth: throttle: %w", err)
	}
	return nil
}
