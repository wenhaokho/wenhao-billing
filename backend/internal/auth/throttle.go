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

// attempt records this login attempt as a failure, then counts the window
// with it included, before the password is verified: parallel attempts each
// see the ones already in flight, so at most pairLimit of them for one pair
// (addressLimit for one address) ever reach the password check. Over a
// limit the attempt is refused (429), the right password included, and its
// row deleted again, so refused attempts do not extend the lock. A success
// clears the pair (succeed); a failure leaves the row counted.
func (t throttle) attempt(ctx context.Context, email, addr string, now time.Time) error {
	id, err := t.q.InsertFailure(ctx, authdb.InsertFailureParams{Email: email, ClientAddr: addr, FailedAt: now})
	if err != nil {
		return err
	}
	if err := t.q.PruneFailures(ctx, now.Add(-keepFailures)); err != nil {
		return err
	}
	since := now.Add(-throttleWindow)
	var wait time.Duration
	locked := false
	pair, err := t.q.CountPairFailures(ctx, authdb.CountPairFailuresParams{Email: email, ClientAddr: addr, Since: since})
	if err != nil {
		return err
	}
	if pair > pairLimit {
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
	if address > addressLimit {
		oldest, err := t.q.OldestAddressFailure(ctx, authdb.OldestAddressFailureParams{ClientAddr: addr, Since: since})
		if err != nil {
			return err
		}
		locked, wait = true, max(wait, oldest.Add(throttleWindow).Sub(now))
	}
	if !locked {
		return nil
	}
	if err := t.q.DeleteFailure(ctx, id); err != nil {
		return err
	}
	// Whole seconds, rounded up, at least one.
	seconds := max(1, int((wait+time.Second-1)/time.Second))
	return huma.ErrorWithHeaders(
		platform.Err(http.StatusTooManyRequests, "too_many_attempts", "too many failed sign-in attempts; try again later"),
		http.Header{"Retry-After": {strconv.Itoa(seconds)}},
	)
}

// succeed forgets the pair's failures, this attempt's included.
func (t throttle) succeed(ctx context.Context, email, addr string) error {
	return t.q.ClearPairFailures(ctx, authdb.ClearPairFailuresParams{Email: email, ClientAddr: addr})
}
