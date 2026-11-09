package invoices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
)

func triggerPath(id uuid.UUID) string { return invoicesPath + "/recurring/" + id.String() + "/trigger" }

func postTrigger(t *testing.T, a *apitest.App, id uuid.UUID, key string) *httptest.ResponseRecorder {
	t.Helper()
	return a.Do(t, http.MethodPost, triggerPath(id), map[string]any{"cycle_key": key})
}

// recorder is a DraftNotifier that keeps every call.
type recorder struct {
	mu    sync.Mutex
	calls [][]DraftSummary
}

func (r *recorder) NotifyNewDrafts(_ context.Context, drafts []DraftSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, slices.Clone(drafts))
}

func (r *recorder) taken() [][]DraftSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := r.calls
	r.calls = nil
	return calls
}

// newAppNotifying is newApp with notify as the routes' draft notifier.
func newAppNotifying(t *testing.T, notify DraftNotifier) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) { Register(api, d.Pool, d.Clock, notify) })
}

// triggerTx is Trigger in its own committed transaction.
func triggerTx(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, key string, today time.Time) (InvoiceOut, bool, error) {
	t.Helper()
	var out InvoiceOut
	var created bool
	err := db.InTx(t.Context(), pool, func(tx pgx.Tx) error {
		var err error
		out, created, err = Trigger(t.Context(), tx, id, key, today)
		return err
	})
	return out, created, err
}

func mustTrigger(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, key string) InvoiceOut {
	t.Helper()
	out, _, err := triggerTx(t, pool, id, key, day("2026-01-15"))
	if err != nil {
		t.Fatalf("trigger %s %s: %v", id, key, err)
	}
	return out
}

// children are the ids of template's generated invoices, oldest first.
func children(t *testing.T, pool *pgxpool.Pool, template uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT invoice_id FROM invoices
WHERE is_template = false AND billing_cycle_ref->>'template_invoice_id' = $1::text ORDER BY created_at, invoice_id`, template.String())
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// Ports of tests/integration/test_recurring_scheduling.py (trigger).

func TestTriggerRejectsYearMonthCycleKey(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	wantProblem(t, postTrigger(t, a, tpl.InvoiceID, "2026-03"), http.StatusBadRequest, "invalid_cycle_key",
		"cycle_key must be an ISO date (YYYY-MM-DD), got '2026-03'", "", "")
}

func TestTriggerRejectsOffCycleDate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	wantProblem(t, postTrigger(t, a, tpl.InvoiceID, "2026-03-15"), http.StatusBadRequest, "not_a_cycle_start",
		"cycle_key '2026-03-15' is not a valid cycle start for this schedule", "", "")
	// Before the start is not a cycle start either.
	wantProblem(t, postTrigger(t, a, tpl.InvoiceID, "2025-12-01"), http.StatusBadRequest, "not_a_cycle_start",
		"cycle_key '2025-12-01' is not a valid cycle start for this schedule", "", "")
}

func TestTriggerAcceptsValidCycleStart(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	p := uuid.New()
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO projects (project_id, customer_id, code, name, currency) VALUES ($1, $2, 'P1', 'P', 'USD')`, p, c); err != nil {
		t.Fatal(err)
	}
	body := templateBody(c, monthly("2026-01-01"))
	body["project_id"], body["po_so_number"], body["notes"], body["footer"] = p, "PO-7", "Thanks", "Bank details"
	body["discount_type"], body["discount_value"] = "PERCENT", "10"
	body["line_items"] = []any{
		map[string]any{"description": "Second", "quantity": "2", "unit_price": "0.3333", "position": 1},
		map[string]any{"description": "First", "quantity": "1", "unit_price": "1000", "position": 0},
	}
	tpl := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusCreated)

	child := apitest.Decode[InvoiceOut](t, postTrigger(t, a, tpl.InvoiceID, "2026-03-01"), http.StatusOK)
	cfg := cfgOf(t, child.BillingCycleRef)
	if len(cfg) != 2 || cfg["cycle_key"] != "2026-03-01" || cfg["template_invoice_id"] != tpl.InvoiceID.String() {
		t.Errorf("billing_cycle_ref = %s", child.BillingCycleRef)
	}
	if child.InvoiceID == tpl.InvoiceID || child.IsTemplate || child.Status != "DRAFT" || child.InvoiceType != "RECURRING" {
		t.Errorf("child = %+v", child)
	}
	if child.InvoiceNumber == nil || *child.InvoiceNumber != "WH20260001" {
		t.Errorf("invoice_number = %v, want WH20260001", child.InvoiceNumber)
	}
	if *child.CustomerID != c || *child.ProjectID != p || *child.PoSoNumber != "PO-7" || child.Currency != "USD" ||
		*child.PaymentTerms != "Net 14" || *child.Notes != "Thanks" || *child.Footer != "Bank details" || *child.DiscountType != "PERCENT" {
		t.Errorf("copied fields = %+v", child)
	}
	wantDec4(t, "discount_value", child.DiscountValue, "10")
	wantDec4(t, "subtotal", child.Subtotal, tpl.Subtotal.Decimal().String())
	wantDec4(t, "amount", p4(child.Amount), tpl.Amount.Decimal().String())
	wantDec4(t, "balance_due", p4(child.BalanceDue), tpl.Amount.Decimal().String())
	if child.CoverageStart != nil || child.CoverageEnd != nil {
		t.Errorf("coverage on a non-hosting child: %v..%v", child.CoverageStart, child.CoverageEnd)
	}
	// Lines verbatim, in position order, the stored amounts kept.
	if len(child.LineItems) != len(tpl.LineItems) {
		t.Fatalf("lines = %+v", child.LineItems)
	}
	for i, got := range child.LineItems {
		want := tpl.LineItems[i]
		if got.LineItemID == want.LineItemID || got.Description != want.Description || got.Position != want.Position ||
			!got.Quantity.Decimal().Equal(want.Quantity.Decimal()) || !got.UnitPrice.Decimal().Equal(want.UnitPrice.Decimal()) ||
			!got.Amount.Decimal().Equal(want.Amount.Decimal()) || got.ItemID != nil {
			t.Errorf("line %d = %+v, want a copy of %+v", i, got, want)
		}
	}
	// The template is untouched.
	if after := getTemplate(t, a, tpl.InvoiceID); after.InvoiceNumber != nil || after.Status != "DRAFT" || after.IssueDate != nil {
		t.Errorf("template after trigger = %+v", after)
	}
}

func TestManualTriggerNotifiesUsersOnce(t *testing.T) {
	t.Parallel()
	n := &recorder{}
	a := newAppNotifying(t, n)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))

	child := apitest.Decode[InvoiceOut](t, postTrigger(t, a, tpl.InvoiceID, "2026-03-01"), http.StatusOK)
	calls := n.taken()
	if len(calls) != 1 || len(calls[0]) != 1 {
		t.Fatalf("notify calls = %+v, want one with one draft", calls)
	}
	got := calls[0][0]
	if got.InvoiceID != child.InvoiceID || got.InvoiceNumber != *child.InvoiceNumber || got.CycleDate != "2026-03-01" ||
		got.Currency != "USD" || !got.Amount.Equal(decimal.NewFromInt(1000)) || got.CustomerName == nil || *got.CustomerName != "Wht Customer" {
		t.Errorf("summary = %+v", got)
	}

	apitest.Decode[InvoiceOut](t, postTrigger(t, a, tpl.InvoiceID, "2026-03-01"), http.StatusOK)
	if calls := n.taken(); len(calls) != 0 {
		t.Errorf("replay notified %+v", calls)
	}
	// A refused trigger notifies nobody either.
	postTrigger(t, a, tpl.InvoiceID, "2026-03-02")
	if calls := n.taken(); len(calls) != 0 {
		t.Errorf("refusal notified %+v", calls)
	}
}

// panicky is a DraftNotifier that breaks its promise.
type panicky struct{}

func (panicky) NotifyNewDrafts(context.Context, []DraftSummary) { panic("mail server down") }

func TestTriggerSurvivesAFailingNotifier(t *testing.T) {
	t.Parallel()
	a := newAppNotifying(t, panicky{})
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	child := apitest.Decode[InvoiceOut](t, postTrigger(t, a, tpl.InvoiceID, "2026-03-01"), http.StatusOK)
	if got := children(t, a.Pool, tpl.InvoiceID); len(got) != 1 || got[0] != child.InvoiceID {
		t.Errorf("children = %v, want the committed %s", got, child.InvoiceID)
	}
}

func TestFindCycleInvoiceNoneBeforeTriggerThenFound(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	q := invoicesdb.New(a.Pool)
	if _, found, err := findCycleInvoice(t.Context(), q, tpl.InvoiceID, "2026-03-01"); err != nil || found {
		t.Fatalf("before trigger: found %v, err %v", found, err)
	}
	inv := mustTrigger(t, a.Pool, tpl.InvoiceID, "2026-03-01")
	got, found, err := findCycleInvoice(t.Context(), q, tpl.InvoiceID, "2026-03-01")
	if err != nil || !found || got.InvoiceID != inv.InvoiceID {
		t.Errorf("after trigger: %v %v %v, want %s", got.InvoiceID, found, err, inv.InvoiceID)
	}
}

// Port of test_invoice_withholding.py::test_recurring_cycle_uses_template_snapshot.
func TestRecurringCycleUsesTemplateSnapshot(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "PPH23", "", true)
	body := templateBody(c, monthly("2026-05-01"))
	body["currency"] = "IDR"
	body["line_items"] = []any{map[string]any{"description": "Retainer", "quantity": "1", "unit_price": "10000000"}}
	tpl := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusCreated)
	if tpl.WhtType != "PPH23" {
		t.Fatalf("template wht_type = %s", tpl.WhtType)
	}
	wantDec4(t, "template amount", p4(tpl.Amount), "10204082")

	if _, err := a.Pool.Exec(t.Context(), `UPDATE customers SET default_wht_type = 'NONE', default_wht_rate = NULL, default_wht_gross_up = false WHERE customer_id = $1`, c); err != nil {
		t.Fatal(err)
	}
	inv := mustTrigger(t, a.Pool, tpl.InvoiceID, "2026-05-01")
	if inv.WhtType != "PPH23" || !inv.WhtGrossUp {
		t.Errorf("wht = %s gross-up %v, want the template's PPH23 gross-up", inv.WhtType, inv.WhtGrossUp)
	}
	wantDec4(t, "wht_rate", inv.WhtRate, "2")
	wantDec4(t, "amount", p4(inv.Amount), tpl.Amount.Decimal().String())
	wantDec4(t, "balance_due", p4(inv.BalanceDue), tpl.Amount.Decimal().String())
	wantDec4(t, "wht_amount", p4(inv.WhtAmount), "204082")
}

// Port of test_invoice_internal_note.py::test_template_internal_note_copies_to_generated_child.
func TestTemplateInternalNoteCopiesToGeneratedChild(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	const note = "Client prefers invoices before the 5th — chase Budi if late."
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	body := templateBody(c, monthly("2026-01-01"))
	body["internal_note"] = note
	tpl := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusCreated)
	child := mustTrigger(t, a.Pool, tpl.InvoiceID, "2026-06-01")
	if child.InternalNote == nil || *child.InternalNote != note {
		t.Errorf("internal_note = %v, want %q", child.InternalNote, note)
	}
}

// insertHostingItem stores _mk_hosting_item (test_hosting.py:26-52): a
// hosting item with its Cloudflare target.
func insertHostingItem(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO items (item_id, name, item_type, default_currency, default_unit_price,
    is_sold, is_purchased, active, is_hosting, hosting_domain, hosting_grace_days, hosting_suspension_enabled, hosting_status)
VALUES ($1, 'Acme hosting', 'SERVICE', 'USD', 240, true, false, true, true, 'example.com', 3, true, 'ACTIVE')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO cloudflare_targets (target_id, item_id, zone_id, record_id, record_name,
    record_type, live_content, maintenance_content, proxied)
VALUES (gen_random_uuid(), $1, 'zone_123', 'record_123', 'example.com', 'CNAME', 'live.example.net', 'maintenance.example.net', true)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Port of test_hosting.py::test_trigger_recurring_cycle_stamps_coverage_on_hosting_invoice.
func TestTriggerRecurringCycleStampsCoverageOnHostingInvoice(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	item := insertHostingItem(t, a.Pool)
	sched := monthly("2026-06-01")
	sched["interval"] = 3
	body := templateBody(c, sched)
	body["payment_terms"] = "Net 7"
	body["line_items"] = []any{
		map[string]any{"item_id": item, "description": "Acme hosting - example.com", "quantity": "1.0000", "unit_price": "240.0000", "position": 0},
		// Every line gets the label, a hosting one or not (hosting.py:81-83);
		// one already carrying it is left alone.
		map[string]any{"description": "Setup\nCoverage: 2026-06-01 to 2026-08-31", "quantity": "1", "unit_price": "10", "position": 1},
	}
	tpl := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusCreated)

	inv := mustTrigger(t, a.Pool, tpl.InvoiceID, "2026-06-01")
	wantDate(t, "coverage_start", inv.CoverageStart, "2026-06-01")
	wantDate(t, "coverage_end", inv.CoverageEnd, "2026-08-31")
	if got := inv.LineItems[0].Description; got != "Acme hosting - example.com\nCoverage: 2026-06-01 to 2026-08-31" {
		t.Errorf("line 0 description = %q", got)
	}
	if got := inv.LineItems[1].Description; got != "Setup\nCoverage: 2026-06-01 to 2026-08-31" {
		t.Errorf("line 1 description = %q", got)
	}
	if tpl := getTemplate(t, a, tpl.InvoiceID); tpl.LineItems[0].Description != "Acme hosting - example.com" || tpl.CoverageStart != nil {
		t.Errorf("template changed: %+v", tpl)
	}
	// The end is month-clamped: a cycle on Nov 30 covers to Feb 27, the day
	// before Feb 28 (Nov 30 + 3 months, clamped).
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET billing_cycle_ref = jsonb_set(billing_cycle_ref, '{start_date}', '"2026-11-30"') WHERE invoice_id = $1`, tpl.InvoiceID); err != nil {
		t.Fatal(err)
	}
	inv = mustTrigger(t, a.Pool, tpl.InvoiceID, "2026-11-30")
	wantDate(t, "clamped coverage_start", inv.CoverageStart, "2026-11-30")
	wantDate(t, "clamped coverage_end", inv.CoverageEnd, "2027-02-27")
}

// New tests.

func TestTriggerRejectsCompactCycleKey(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	// Python's date.fromisoformat took both (port note: Go change 2).
	for _, key := range []string{"20260301", "2026-W09-7", "2026-03-01T00:00", " 2026-03-01", "2026-02-30", "0000-01-01"} {
		wantProblem(t, postTrigger(t, a, tpl.InvoiceID, key), http.StatusBadRequest, "invalid_cycle_key",
			"cycle_key must be an ISO date (YYYY-MM-DD), got '"+key+"'", "", "")
	}
	// Python's repr quoting: double quotes around a key with a single quote.
	wantProblem(t, postTrigger(t, a, tpl.InvoiceID, "it's"), http.StatusBadRequest, "invalid_cycle_key",
		`cycle_key must be an ISO date (YYYY-MM-DD), got "it's"`, "", "")
	if got := children(t, a.Pool, tpl.InvoiceID); len(got) != 0 {
		t.Errorf("children = %v", got)
	}
}

func TestTriggerReplayReturnsSameInvoice(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	first := postTrigger(t, a, tpl.InvoiceID, "2026-03-01")
	if first.Code != http.StatusOK {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	// The replay comes back unchanged, even after the template changed.
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoice_line_items SET unit_price = 5, amount = 5 WHERE invoice_id = $1`, tpl.InvoiceID); err != nil {
		t.Fatal(err)
	}
	setToday(t, a, "2026-01-16")
	replay := postTrigger(t, a, tpl.InvoiceID, "2026-03-01")
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Errorf("replay %d %s\nwant %s", replay.Code, replay.Body, first.Body)
	}
	next := apitest.Decode[InvoiceOut](t, postTrigger(t, a, tpl.InvoiceID, "2026-04-01"), http.StatusOK)
	if got := children(t, a.Pool, tpl.InvoiceID); len(got) != 2 || got[1] != next.InvoiceID {
		t.Errorf("children = %v, want two", got)
	}
}

func TestTriggerChildDatesAreToday(t *testing.T) {
	t.Parallel()
	a := newApp(t) // the clock reads 2026-01-15T09:00Z
	tpl := mkTemplate(t, a, monthly("2025-06-01"))
	// An old cycle is still dated today (Python: issue_date = today).
	child := apitest.Decode[InvoiceOut](t, postTrigger(t, a, tpl.InvoiceID, "2025-07-01"), http.StatusOK)
	wantDate(t, "issue_date", child.IssueDate, "2026-01-15")
	wantDate(t, "due_date", child.DueDate, "2026-01-29") // Net 14
	if *child.InvoiceNumber != "WH20260001" {
		t.Errorf("invoice_number = %s, want this year's", *child.InvoiceNumber)
	}
	// Terms that pass 9999-12-31 are refused, as create refuses them.
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET payment_terms = 'Net 99999999' WHERE invoice_id = $1`, tpl.InvoiceID); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, postTrigger(t, a, tpl.InvoiceID, "2025-08-01"), http.StatusBadRequest, "due_date_out_of_range", "due date is out of range", "", "")
	if got := children(t, a.Pool, tpl.InvoiceID); len(got) != 1 {
		t.Errorf("children = %v, want the first only", got)
	}
}

func TestTriggerUnknownTemplateIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, postTrigger(t, a, id, "2026-03-01"), http.StatusBadRequest, "template_invoice_not_found",
		"template invoice "+id.String()+" not found", "", "")
	// Not RECURRING comes before the cycle key is looked at.
	other := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	wantProblem(t, postTrigger(t, a, other.InvoiceID, "nonsense"), http.StatusBadRequest, "invoice_not_recurring",
		"invoice "+other.InvoiceID.String()+" is not RECURRING", "", "")
	// A missing cycle_key is the 422 of the field.
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	if rec := a.Do(t, http.MethodPost, triggerPath(tpl.InvoiceID), map[string]any{}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no cycle_key: %d %s", rec.Code, rec.Body)
	}
}

func TestTriggerOnChildInvoiceIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	tpl := mkTemplate(t, a, monthly("2026-01-01"))
	child := mustTrigger(t, a.Pool, tpl.InvoiceID, "2026-01-01")
	// The schedule comes before the cycle key.
	wantProblem(t, postTrigger(t, a, child.InvoiceID, "2026-03"), http.StatusBadRequest, "invalid_template_schedule",
		"template "+child.InvoiceID.String()+" has an invalid schedule: invalid frequency: None", "", "")
	// A template whose schedule was emptied.
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET billing_cycle_ref = NULL WHERE invoice_id = $1`, tpl.InvoiceID); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, postTrigger(t, a, tpl.InvoiceID, "2026-03-01"), http.StatusBadRequest, "invalid_template_schedule",
		"template "+tpl.InvoiceID.String()+" has an invalid schedule: schedule config is empty", "", "")
}

// TestTriggerIgnoresPauseAndEnd: a manual trigger still generates for a
// paused or ended template (recurring_schedule.py:185-186, by design).
func TestTriggerIgnoresPauseAndEnd(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	paused := mkTemplate(t, a, monthly("2026-01-01"))
	apitest.Decode[InvoiceOut](t, act(t, a, paused.InvoiceID, "PAUSE"), http.StatusOK)
	apitest.Decode[InvoiceOut](t, postTrigger(t, a, paused.InvoiceID, "2026-02-01"), http.StatusOK)
	ended := mkTemplate(t, a, monthly("2025-01-01"))
	endNow(t, a, ended.InvoiceID)
	apitest.Decode[InvoiceOut](t, postTrigger(t, a, ended.InvoiceID, "2026-02-01"), http.StatusOK)
}
