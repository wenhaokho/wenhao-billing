package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func wantThrottled(t *testing.T, rec *httptest.ResponseRecorder, retryAfter string) {
	t.Helper()
	p := problemOf(rec)
	if rec.Code != http.StatusTooManyRequests || p.Code != "too_many_attempts" || p.Detail != "too many failed sign-in attempts; try again later" {
		t.Fatalf("got %d %s, want 429 too_many_attempts", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Retry-After"); got != retryAfter {
		t.Errorf("Retry-After %q, want %q", got, retryAfter)
	}
	noSessionCookie(t, rec)
}

func (a app) fail(t *testing.T, addr string, n int, header ...string) {
	t.Helper()
	for i := range n {
		if rec := a.login(t, addr, "wrong-password", false, header...); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
}

// Ten failures for one account from one address lock that pair, even for the
// right password, until the oldest failure leaves the 15-minute window.
func TestThrottlePairLimit(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	a.fail(t, adminEmail, 10)
	a.clock.Advance(5 * time.Minute)
	wantThrottled(t, a.login(t, adminEmail, password, false), "600")
	a.clock.Advance(10 * time.Minute)
	if rec := a.login(t, adminEmail, password, false); rec.Code != http.StatusOK {
		t.Errorf("after the window: %d %s", rec.Code, rec.Body)
	}
}

// Thirty failures from one address across accounts lock the address.
func TestThrottleAddressLimit(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	for i := range 30 {
		a.fail(t, fmt.Sprintf("u%d@example.com", i), 1)
		a.clock.Advance(time.Second)
	}
	// The oldest failure was 30 s ago.
	wantThrottled(t, a.login(t, adminEmail, password, false), "870")
	// Another address is untouched.
	if rec := a.login(t, adminEmail, password, false, "X-Forwarded-For", "198.51.100.7"); rec.Code != http.StatusOK {
		t.Errorf("other address: %d %s", rec.Code, rec.Body)
	}
}

// Failures from one address never lock the account for another address: an
// attacker cannot lock the owner out.
func TestThrottleNeverLocksEmailAlone(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	a.fail(t, adminEmail, 10, "X-Forwarded-For", "198.51.100.1")
	if rec := a.login(t, adminEmail, password, false, "X-Forwarded-For", "198.51.100.2"); rec.Code != http.StatusOK {
		t.Errorf("address B: %d %s", rec.Code, rec.Body)
	}
	wantThrottled(t, a.login(t, adminEmail, password, false, "X-Forwarded-For", "198.51.100.1"), "900")
}

func TestThrottleUsesCFConnectingIP(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	for i := range 10 {
		a.fail(t, adminEmail, 1, "CF-Connecting-IP", "203.0.113.5", "X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
	}
	wantThrottled(t, a.login(t, adminEmail, password, false, "CF-Connecting-IP", "203.0.113.5", "X-Forwarded-For", "10.0.0.99"), "900")
	if rec := a.login(t, adminEmail, password, false, "CF-Connecting-IP", "203.0.113.6", "X-Forwarded-For", "10.0.0.1"); rec.Code != http.StatusOK {
		t.Errorf("another CF address: %d %s", rec.Code, rec.Body)
	}
}

func TestSuccessfulLoginClearsPairCount(t *testing.T) {
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	a.fail(t, adminEmail, 9)
	a.mustLogin(t, adminEmail, false)
	a.fail(t, adminEmail, 9)
	if rec := a.login(t, adminEmail, password, false); rec.Code != http.StatusOK {
		t.Errorf("18 failures with a success between: %d %s", rec.Code, rec.Body)
	}
}

func TestFailedLoginPrunesDayOldRows(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `INSERT INTO login_attempts (email, client_addr, failed_at) VALUES ('old@example.com', '192.0.2.9', $1), ('recent@example.com', '192.0.2.9', $2)`,
		start.Add(-25*time.Hour), start.Add(-23*time.Hour)); err != nil {
		t.Fatal(err)
	}
	a.fail(t, adminEmail, 1)
	var emails []string
	rows, err := a.pool.Query(ctx, `SELECT email FROM login_attempts ORDER BY failed_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		emails = append(emails, e)
	}
	if fmt.Sprint(emails) != "[recent@example.com "+adminEmail+"]" {
		t.Errorf("rows %v", emails)
	}
}

func TestClientAddress(t *testing.T) {
	for _, c := range []struct{ cf, xff, remote, want string }{
		{"203.0.113.5", "10.0.0.1", "192.0.2.1:1234", "203.0.113.5"},
		{"", "198.51.100.1, 10.0.0.1", "192.0.2.1:1234", "198.51.100.1"},
		{"", " 198.51.100.1 ", "192.0.2.1:1234", "198.51.100.1"},
		{"", "", "192.0.2.1:1234", "192.0.2.1"},
		{"", "", "[2001:db8::1]:443", "2001:db8::1"},
		{"", "", "192.0.2.1", "192.0.2.1"},
	} {
		if got := clientAddress(c.cf, c.xff, c.remote); got != c.want {
			t.Errorf("clientAddress(%q, %q, %q) = %q, want %q", c.cf, c.xff, c.remote, got, c.want)
		}
	}
}
