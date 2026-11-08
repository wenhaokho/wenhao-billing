package auth

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/auth/authdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Failed logins are counted over a sliding window, per account and address
// pair and per address. An account is never locked by email alone, or anyone
// could lock its owner out.
const (
	throttleWindow = 15 * time.Minute
	pairLimit      = 10
	addressLimit   = 30
	keepFailures   = 24 * time.Hour
)

// clientAddress is the caller's address: Cloudflare's CF-Connecting-IP, else
// the left-most X-Forwarded-For, else the connection's host.
func clientAddress(cfConnectingIP, forwardedFor, remoteAddr string) string {
	if v := strings.TrimSpace(cfConnectingIP); v != "" {
		return v
	}
	first, _, _ := strings.Cut(forwardedFor, ",")
	if v := strings.TrimSpace(first); v != "" {
		return v
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

type throttle struct{ q *authdb.Queries }

// check refuses a login (429) when the pair or the address is at its limit.
// It runs before the password is verified: the right password from a locked
// pair is refused too.
func (t throttle) check(ctx context.Context, email, addr string, now time.Time) error {
	since := now.Add(-throttleWindow)
	var wait time.Duration
	locked := false
	pair, err := t.q.CountPairFailures(ctx, authdb.CountPairFailuresParams{Email: email, ClientAddr: addr, Since: since})
	if err != nil {
		return err
	}
	if pair >= pairLimit {
		oldest, err := t.q.OldestPairFailure(ctx, authdb.OldestPairFailureParams{Email: email, ClientAddr: addr, Since: since})
		if err != nil {
			return err
		}
		locked, wait = true, max(wait, oldest.Add(throttleWindow).Sub(now))
	}
	address, err := t.q.CountAddressFailures(ctx, authdb.CountAddressFailuresParams{ClientAddr: addr, Since: since})
	if err != nil {
		return err
	}
	if address >= addressLimit {
		oldest, err := t.q.OldestAddressFailure(ctx, authdb.OldestAddressFailureParams{ClientAddr: addr, Since: since})
		if err != nil {
			return err
		}
		locked, wait = true, max(wait, oldest.Add(throttleWindow).Sub(now))
	}
	if !locked {
		return nil
	}
	// Whole seconds, rounded up, at least one.
	seconds := max(1, int((wait+time.Second-1)/time.Second))
	return huma.ErrorWithHeaders(
		platform.Err(http.StatusTooManyRequests, "too_many_attempts", "too many failed sign-in attempts; try again later"),
		http.Header{"Retry-After": {strconv.Itoa(seconds)}},
	)
}

// fail records a failed login and drops failures older than a day.
func (t throttle) fail(ctx context.Context, email, addr string, now time.Time) error {
	if err := t.q.InsertFailure(ctx, authdb.InsertFailureParams{Email: email, ClientAddr: addr, FailedAt: now}); err != nil {
		return err
	}
	return t.q.PruneFailures(ctx, now.Add(-keepFailures))
}

// succeed forgets the pair's failures.
func (t throttle) succeed(ctx context.Context, email, addr string) error {
	return t.q.ClearPairFailures(ctx, authdb.ClearPairFailuresParams{Email: email, ClientAddr: addr})
}
