package invoices

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

func scan(t *testing.T, a *apitest.App, today string, notify DraftNotifier) int {
	t.Helper()
	n, err := Scan(t.Context(), a.Pool, day(today), notify)
	if err != nil {
		t.Fatalf("scan %s: %v", today, err)
	}
	return n
}

func wantChildKeys(t *testing.T, a *apitest.App, name string, template uuid.UUID, keys ...string) {
	t.Helper()
	ids := children(t, a.Pool, template)
	got := make([]string, 0, len(ids))
	for _, id := range ids {
		got = append(got, cfgOf(t, get(t, a, id).BillingCycleRef)["cycle_key"].(string))
	}
	if len(got) != len(keys) {
		t.Errorf("%s: children cycles %v, want %v", name, got, keys)
		return
	}
	for i := range keys {
		if got[i] != keys[i] {
			t.Errorf("%s: children cycles %v, want %v", name, got, keys)
			return
		}
	}
}

func TestScanGeneratesDueTemplatesAndSkipsPausedEnded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	due := mkTemplate(t, a, monthly("2026-01-01"))
	weekly := mkTemplate(t, a, map[string]any{"frequency": "WEEKLY", "interval": 1, "start_date": "2026-03-03"})
	paused := mkTemplate(t, a, monthly("2026-01-01"))
	apitest.Decode[InvoiceOut](t, act(t, a, paused.InvoiceID, "PAUSE"), http.StatusOK)
	onDate := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "ON_DATE", "end_date": "2026-02-15"})
	afterN := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 2})
	future := mkTemplate(t, a, monthly("2026-04-01"))
	broken := mkTemplate(t, a, monthly("2026-01-01"))
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET billing_cycle_ref = '{}' WHERE invoice_id = $1`, broken.InvoiceID); err != nil {
		t.Fatal(err)
	}

	if n := scan(t, a, "2026-03-10", nil); n != 2 {
		t.Errorf("generated %d, want 2", n)
	}
	wantChildKeys(t, a, "due", due.InvoiceID, "2026-03-01")
	wantChildKeys(t, a, "weekly", weekly.InvoiceID, "2026-03-10")
	for name, id := range map[string]uuid.UUID{
		"paused": paused.InvoiceID, "on date": onDate.InvoiceID, "after n": afterN.InvoiceID,
		"future": future.InvoiceID, "broken": broken.InvoiceID,
	} {
		wantChildKeys(t, a, name, id)
	}
	// The child is dated the scan's day.
	child := get(t, a, children(t, a.Pool, due.InvoiceID)[0])
	wantDate(t, "issue_date", child.IssueDate, "2026-03-10")
	wantDate(t, "due_date", child.DueDate, "2026-03-24")

	// A second scan the same day replays both (counted, as Python counts them).
	if n := scan(t, a, "2026-03-10", nil); n != 2 {
		t.Errorf("rerun generated %d, want 2", n)
	}
	wantChildKeys(t, a, "due after rerun", due.InvoiceID, "2026-03-01")
	// Missed cycles are never backfilled: only the cycle containing today.
	scan(t, a, "2026-05-02", nil)
	wantChildKeys(t, a, "due in May", due.InvoiceID, "2026-03-01", "2026-05-01")
}

func TestScanIsolatesAFailingTemplate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	n := &recorder{}
	// Visited first (oldest): its terms pass 9999-12-31, so its trigger fails.
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	body := templateBody(c, monthly("2026-01-01"))
	body["payment_terms"] = "Net 99999999"
	bad := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusCreated)
	good := mkTemplate(t, a, monthly("2026-01-01"))

	if got := scan(t, a, "2026-03-10", n); got != 1 {
		t.Errorf("generated %d, want 1", got)
	}
	wantChildKeys(t, a, "bad", bad.InvoiceID)
	wantChildKeys(t, a, "good", good.InvoiceID, "2026-03-01")
	if calls := n.taken(); len(calls) != 1 || len(calls[0]) != 1 {
		t.Errorf("notify calls = %+v, want the good template's draft", calls)
	}
}

func TestScanNotifiesOnceWithNewDraftsOnly(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	n := &recorder{}
	old := mkTemplate(t, a, monthly("2026-01-01"))
	fresh := mkTemplate(t, a, monthly("2026-02-01"))
	mustTrigger(t, a.Pool, old.InvoiceID, "2026-03-01")

	if got := scan(t, a, "2026-03-10", n); got != 2 {
		t.Errorf("generated %d, want 2 (the replay counts)", got)
	}
	calls := n.taken()
	if len(calls) != 1 || len(calls[0]) != 1 {
		t.Fatalf("notify calls = %+v, want one with the new draft", calls)
	}
	child := get(t, a, children(t, a.Pool, fresh.InvoiceID)[0])
	if s := calls[0][0]; s.InvoiceID != child.InvoiceID || s.CycleDate != "2026-03-01" || s.InvoiceNumber != *child.InvoiceNumber {
		t.Errorf("summary = %+v, want %s", s, child.InvoiceID)
	}

	scan(t, a, "2026-03-10", n)
	if calls := n.taken(); len(calls) != 0 {
		t.Errorf("rerun notified %+v", calls)
	}
}

// TestTriggerConcurrentWithScanOneChild: a manual trigger racing the scan
// for the same cycle makes one child (Go change 2: the template's advisory
// lock). Each round pauses the last template, so the scan visits only the
// new one and the two meet.
func TestTriggerConcurrentWithScanOneChild(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	const today = "2026-01-15" // apitest's clock
	for round := range 8 {
		tpl := mkTemplate(t, a, monthly(today))
		var wg sync.WaitGroup
		start := make(chan struct{})
		var scanErr error
		wg.Go(func() {
			<-start
			_, scanErr = Scan(t.Context(), a.Pool, day(today), &recorder{})
		})
		var rec *httptest.ResponseRecorder
		wg.Go(func() {
			<-start
			rec = a.Do(t, http.MethodPost, triggerPath(tpl.InvoiceID), map[string]any{"cycle_key": today})
		})
		close(start)
		wg.Wait()
		if scanErr != nil {
			t.Fatalf("round %d: scan: %v", round, scanErr)
		}
		out := apitest.Decode[InvoiceOut](t, rec, http.StatusOK)
		if got := children(t, a.Pool, tpl.InvoiceID); len(got) != 1 || got[0] != out.InvoiceID {
			t.Fatalf("round %d: children %v, want only the trigger's %s", round, got, out.InvoiceID)
		}
		apitest.Decode[InvoiceOut](t, act(t, a, tpl.InvoiceID, "PAUSE"), http.StatusOK)
	}
}

// TestScanCountsACommitBeforeTheContextEnds: when the context ends right
// after a trigger committed, that draft is still counted and notified, and
// the notifier gets a context that has not ended (the draft exists).
func TestScanCountsACommitBeforeTheContextEnds(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	first := mkTemplate(t, a, monthly("2026-01-01"))
	second := mkTemplate(t, a, monthly("2026-01-01"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// The context ends once the first trigger has committed.
	cancelAfterCommit := func(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, key string, today time.Time) (InvoiceOut, *DraftSummary, error) {
		out, summary, err := triggerAndSummarise(ctx, pool, id, key, today)
		cancel()
		return out, summary, err
	}
	n := &recorder{}

	got, err := scanWith(ctx, a.Pool, day("2026-03-10"), n, cancelAfterCommit)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got != 1 {
		t.Errorf("generated %d, want 1 (the committed draft)", got)
	}
	wantChildKeys(t, a, "first", first.InvoiceID, "2026-03-01")
	wantChildKeys(t, a, "second", second.InvoiceID)
	calls := n.taken()
	if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0].InvoiceID != children(t, a.Pool, first.InvoiceID)[0] {
		t.Fatalf("notify calls = %+v, want the first template's draft", calls)
	}
	if errs := n.contextErrs(); errs[0] != nil {
		t.Errorf("notifier's ctx.Err() = %v, want a live context", errs[0])
	}
}

// TestNotifyDraftsOutlivesTheCallersContext: the drafts are committed, so
// the notifier hears of them with the caller's context's values but not its
// end (Scan and the trigger route both notify through here).
func TestNotifyDraftsOutlivesTheCallersContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	n := &recorder{}
	notifyDrafts(ctx, n, []DraftSummary{{InvoiceNumber: "WH20260001"}})
	if errs := n.contextErrs(); len(errs) != 1 || errs[0] != nil {
		t.Errorf("notifier's ctx.Err() = %v, want one call with a live context", errs)
	}
}
