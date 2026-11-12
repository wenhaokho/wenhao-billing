package jobs

import (
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const nextBatchPath = "/api/v1/invoices/next-batch"

// nextBatchApp serves the invoice routes and the next-batch route, with the
// scheduler on or off and the route's clock at now (its own test clock: moving
// apitest's clock past 8 hours would expire the admin's session). The invoice
// routes are registered first so the test proves /invoices/{invoice_id} does
// not capture /invoices/next-batch.
func nextBatchApp(t *testing.T, scheduler bool, now time.Time) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) {
		invoices.Register(api, d.Pool, d.Clock, "IDR", nil)
		cfg := d.Config
		cfg.SchedulerEnabled = scheduler
		Register(api, cfg, testkit.NewClock(now))
	})
}

func nextBatch(t *testing.T, a *apitest.App) NextBatchOut {
	t.Helper()
	return apitest.Decode[NextBatchOut](t, a.Do(t, http.MethodGet, nextBatchPath, nil), http.StatusOK)
}

func TestNextBatchBeforeTheHourIsTodaysSlot(t *testing.T) {
	t.Parallel()
	a := nextBatchApp(t, true, time.Date(2026, 1, 16, 1, 59, 59, 0, time.UTC))
	got := nextBatch(t, a)
	want := time.Date(2026, 1, 16, 2, 0, 0, 0, time.UTC)
	if !got.SchedulerEnabled || got.NextBatchAt == nil || !got.NextBatchAt.Equal(want) {
		t.Fatalf("got %+v, want enabled and %s", got, want)
	}
}

func TestNextBatchAtOrAfterTheHourIsTomorrowsSlot(t *testing.T) {
	t.Parallel()
	for name, now := range map[string]time.Time{
		"at 02:00":    time.Date(2026, 1, 16, 2, 0, 0, 0, time.UTC),
		"after 02:00": time.Date(2026, 1, 16, 2, 1, 0, 0, time.UTC),
		"evening":     time.Date(2026, 1, 16, 23, 0, 0, 0, time.UTC),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := nextBatchApp(t, true, now)
			got := nextBatch(t, a)
			want := time.Date(2026, 1, 17, 2, 0, 0, 0, time.UTC)
			if got.NextBatchAt == nil || !got.NextBatchAt.Equal(want) {
				t.Fatalf("got %+v, want %s", got, want)
			}
		})
	}
}

func TestNextBatchSchedulerOffIsNull(t *testing.T) {
	t.Parallel()
	a := nextBatchApp(t, false, apitest.Start)
	rec := a.Do(t, http.MethodGet, nextBatchPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); got != `{"scheduler_enabled":false,"next_batch_at":null}`+"\n" && got != `{"scheduler_enabled":false,"next_batch_at":null}` {
		t.Fatalf("body = %q", got)
	}
}

func TestNextBatchRequiresAdmin(t *testing.T) {
	t.Parallel()
	a := nextBatchApp(t, true, apitest.Start)
	if rec := a.Anon(t, http.MethodGet, nextBatchPath, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d", rec.Code)
	}
	staffID := uuid.New()
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID); err != nil {
		t.Fatal(err)
	}
	token, _, err := a.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a
	staff.Cookie = platform.SessionCookie + "=" + token
	if rec := staff.Do(t, http.MethodGet, nextBatchPath, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("staff status = %d", rec.Code)
	}
}
