package invoices

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/schedule"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const templatesPath = invoicesPath + "/recurring-templates"

func templatePath(id uuid.UUID) string { return templatesPath + "/" + id.String() }

// monthly is a schedule body: every month from start, never ending.
func monthly(start string) map[string]any {
	return map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": start, "end_mode": "NEVER"}
}

// templateBody is _mk_template's payload (test_recurring_scheduling.py:29-61):
// USD, Net 14, one 1000 retainer line, on sched.
func templateBody(customer uuid.UUID, sched map[string]any) map[string]any {
	return map[string]any{
		"customer_id": customer, "currency": "USD", "payment_terms": "Net 14",
		"line_items": []any{map[string]any{"description": "Monthly retainer", "quantity": "1", "unit_price": "1000"}},
		"schedule":   sched,
	}
}

// mkTemplate creates a template for a new customer on sched.
func mkTemplate(t *testing.T, a *apitest.App, sched map[string]any) InvoiceOut {
	t.Helper()
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	return apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", templateBody(c, sched)), http.StatusCreated)
}

func getTemplate(t *testing.T, a *apitest.App, id uuid.UUID) InvoiceOut {
	t.Helper()
	return apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodGet, templatePath(id), nil), http.StatusOK)
}

// cfgOf is the stored billing_cycle_ref as the scanner reads it (numbers as
// json.Number); nil when null.
func cfgOf(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var cfg map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("billing_cycle_ref %s: %v", raw, err)
	}
	return cfg
}

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

// cycleKeyFor is the scanner's _cycle_key_for on the stored template: "" when
// no cycle is due.
func cycleKeyFor(t *testing.T, a *apitest.App, id uuid.UUID, today string) string {
	t.Helper()
	inv := get(t, a, id)
	key, _ := schedule.CycleKeyFor(cfgOf(t, inv.BillingCycleRef), day(today))
	return key
}

// insertChild stores a generated child of template directly (_mk_child,
// test_recurring_scheduling.py:276-298), bypassing the trigger.
func insertChild(t *testing.T, pool *pgxpool.Pool, template uuid.UUID, issue, cycleKey, status, created string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO invoices
    (invoice_id, invoice_type, currency, amount, balance_due, status, billing_cycle_ref, issue_date, due_date, created_at)
VALUES ($1, 'RECURRING', 'USD', 1000, 1000, $2, jsonb_build_object('template_invoice_id', $3::text, 'cycle_key', $4::text),
    $5::date, $5::date, $6::timestamp)`, id, status, template.String(), cycleKey, issue, created); err != nil {
		t.Fatal(err)
	}
	return id
}

func rows(t *testing.T, a *apitest.App) []RecurringTemplateRow {
	t.Helper()
	rec := a.Do(t, http.MethodGet, templatesPath+"/rows", nil)
	if strings.TrimSpace(rec.Body.String()) == "null" {
		t.Errorf("rows: null list")
	}
	return apitest.Decode[[]RecurringTemplateRow](t, rec, http.StatusOK)
}

func rowFor(t *testing.T, a *apitest.App, id uuid.UUID) RecurringTemplateRow {
	t.Helper()
	for _, r := range rows(t, a) {
		if r.TemplateID == id {
			return r
		}
	}
	t.Fatalf("no row for %s", id)
	return RecurringTemplateRow{}
}

func act(t *testing.T, a *apitest.App, id uuid.UUID, action string) *httptest.ResponseRecorder {
	t.Helper()
	return a.Do(t, http.MethodPatch, templatePath(id), map[string]any{"action": action})
}

func endNow(t *testing.T, a *apitest.App, id uuid.UUID) InvoiceOut {
	t.Helper()
	return apitest.Decode[InvoiceOut](t, act(t, a, id, "END_NOW"), http.StatusOK)
}

// setToday moves the clock to 09:00 UTC on date and signs the admin in again
// (the session is dated by the same clock).
func setToday(t *testing.T, a *apitest.App, date string) {
	t.Helper()
	a.Clock.Advance(day(date).Add(9 * time.Hour).Sub(a.Clock.Now()))
	token, _, err := a.Sessions.Issue(a.AdminID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	a.Cookie = platform.SessionCookie + "=" + token
}

func wantRowDate(t *testing.T, name string, got *platform.Date, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Errorf("%s = %s, want null", name, got.Format(time.DateOnly))
		}
		return
	}
	wantDate(t, name, got, want)
}

const endedSentence = "recurring template has ended and is view-only"

// Ports of tests/integration/test_recurring_scheduling.py.

func TestCreateTemplateStoresScheduleInBillingCycleRef(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "interval": 3, "start_date": "2026-01-15"})
	if !out.IsTemplate || out.InvoiceType != "RECURRING" || out.Status != "DRAFT" || out.InvoiceNumber != nil ||
		out.IssueDate != nil || out.DueDate != nil {
		t.Errorf("template = %+v", out)
	}
	// _schedule_to_json's keys only (no end_date / end_after_cycles), the
	// interval an integer, in jsonb's key order.
	if want := `{"end_mode":"NEVER","interval":3,"frequency":"MONTHLY","start_date":"2026-01-15"}`; string(out.BillingCycleRef) != want {
		t.Errorf("billing_cycle_ref = %s, want %s", out.BillingCycleRef, want)
	}
	wantDec4(t, "amount", p4(out.Amount), "1000")
	wantDec4(t, "balance_due", p4(out.BalanceDue), "1000")
	if out.PaymentTerms == nil || *out.PaymentTerms != "Net 14" || len(out.LineItems) != 1 {
		t.Errorf("terms/lines = %v %+v", out.PaymentTerms, out.LineItems)
	}
}

func TestCreateTemplatePersistsEndCondition(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, map[string]any{"frequency": "WEEKLY", "start_date": "2026-01-05", "end_mode": "ON_DATE", "end_date": "2026-06-30"})
	cfg := cfgOf(t, out.BillingCycleRef)
	if cfg["end_mode"] != "ON_DATE" || cfg["end_date"] != "2026-06-30" || cfg["interval"] != json.Number("1") {
		t.Errorf("cfg = %v", cfg)
	}
	// Each end field is stored whenever it is sent, whatever the end mode.
	out = mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_date": "2026-06-30", "end_after_cycles": 4})
	if want := `{"end_date":"2026-06-30","end_mode":"NEVER","interval":1,"frequency":"MONTHLY","start_date":"2026-01-01","end_after_cycles":4}`; string(out.BillingCycleRef) != want {
		t.Errorf("billing_cycle_ref = %s, want %s", out.BillingCycleRef, want)
	}
}

func TestCreateTemplateRejectsInconsistentEndCondition(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	for _, tc := range []struct {
		sched  map[string]any
		detail string
	}{
		{
			map[string]any{"frequency": "MONTHLY", "start_date": "2026-05-01", "end_mode": "AFTER_N", "end_after_cycles": nil},
			"invalid schedule: end_mode=AFTER_N requires positive integer end_after_cycles",
		},
		{
			map[string]any{"frequency": "MONTHLY", "start_date": "2026-05-01", "end_mode": "ON_DATE"},
			"invalid schedule: end_mode=ON_DATE requires end_date",
		},
	} {
		wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", templateBody(c, tc.sched)),
			http.StatusBadRequest, "invalid_schedule", tc.detail, "", "")
	}
	if got := apitest.Decode[[]InvoiceOut](t, a.Do(t, http.MethodGet, templatesPath, nil), http.StatusOK); len(got) != 0 {
		t.Errorf("stored %d templates", len(got))
	}
}

func TestCycleKeyNoneBeforeStart(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-05-01"))
	if got := cycleKeyFor(t, a, out.InvoiceID, "2026-04-30"); got != "" {
		t.Errorf("key = %q", got)
	}
}

func TestCycleKeyIsIsoCycleStartMonthly(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-05-01"))
	for today, want := range map[string]string{"2026-05-10": "2026-05-01", "2026-06-15": "2026-06-01"} {
		if got := cycleKeyFor(t, a, out.InvoiceID, today); got != want {
			t.Errorf("%s: key = %q, want %q", today, got, want)
		}
	}
}

func TestCycleKeyWeeklyIntervalTwo(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, map[string]any{"frequency": "WEEKLY", "interval": 2, "start_date": "2026-05-04"})
	for today, want := range map[string]string{"2026-05-17": "2026-05-04", "2026-05-18": "2026-05-18"} {
		if got := cycleKeyFor(t, a, out.InvoiceID, today); got != want {
			t.Errorf("%s: key = %q, want %q", today, got, want)
		}
	}
}

func TestCycleKeyNonePastEndDate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "ON_DATE", "end_date": "2026-03-31"})
	for today, want := range map[string]string{"2026-03-31": "2026-03-01", "2026-04-01": ""} {
		if got := cycleKeyFor(t, a, out.InvoiceID, today); got != want {
			t.Errorf("%s: key = %q, want %q", today, got, want)
		}
	}
}

func TestCycleKeyNonePastAfterN(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 3})
	for today, want := range map[string]string{"2026-03-15": "2026-03-01", "2026-04-01": ""} {
		if got := cycleKeyFor(t, a, out.InvoiceID, today); got != want {
			t.Errorf("%s: key = %q, want %q", today, got, want)
		}
	}
}

func TestCycleKeyReturnsNoneIfConfigMissing(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-05-01"))
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET billing_cycle_ref = NULL WHERE invoice_id = $1`, out.InvoiceID); err != nil {
		t.Fatal(err)
	}
	if got := get(t, a, out.InvoiceID); got.BillingCycleRef != nil && string(got.BillingCycleRef) != "null" {
		t.Fatalf("billing_cycle_ref = %s", got.BillingCycleRef)
	}
	if key, ok := schedule.CycleKeyFor(nil, day("2026-05-01")); ok {
		t.Errorf("key = %q", key)
	}
}

// The latest child by issue_date drives Previous, and Previous is its
// cycle_key, not its issue_date (a cycle generated late) nor the newest
// child's (a cycle backfilled after a later one).
func TestRowsPreviousIsCycleDateNotGenerationDate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	insertChild(t, a.Pool, out.InvoiceID, "2026-07-10", "2026-06-01", "DRAFT", "2026-07-10 10:00:00")
	insertChild(t, a.Pool, out.InvoiceID, "2026-05-02", "2026-05-01", "DRAFT", "2026-07-11 10:00:00")
	row := rowFor(t, a, out.InvoiceID)
	wantRowDate(t, "previous_issue_date", row.PreviousIssueDate, "2026-06-01")
	if row.GeneratedCount != 2 {
		t.Errorf("generated_count = %d", row.GeneratedCount)
	}
}

func TestRowsNextIsPreviousCyclePlusOneInterval(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	insertChild(t, a.Pool, out.InvoiceID, "2026-07-10", "2026-06-01", "DRAFT", "2026-07-10 10:00:00")
	row := rowFor(t, a, out.InvoiceID)
	wantRowDate(t, "previous_issue_date", row.PreviousIssueDate, "2026-06-01")
	wantRowDate(t, "next_run_date", row.NextRunDate, "2026-07-01")
}

// A child whose cycle_key is not an ISO date (a legacy "2026-07") falls back
// to its issue_date.
func TestRowsPreviousFallsBackToIssueDate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	insertChild(t, a.Pool, out.InvoiceID, "2026-07-03", "2026-07", "SENT", "2026-07-03 10:00:00")
	row := rowFor(t, a, out.InvoiceID)
	wantRowDate(t, "previous_issue_date", row.PreviousIssueDate, "2026-07-03")
	wantRowDate(t, "next_run_date", row.NextRunDate, "2026-08-01")
}

func TestRowsExcludesVoidChildren(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	insertChild(t, a.Pool, out.InvoiceID, "2026-06-01", "2026-06-01", "DRAFT", "2026-06-01 10:00:00")
	insertChild(t, a.Pool, out.InvoiceID, "2026-07-25", "2026-07-01", "VOID", "2026-07-25 10:00:00")
	row := rowFor(t, a, out.InvoiceID)
	if row.GeneratedCount != 1 {
		t.Errorf("generated_count = %d", row.GeneratedCount)
	}
	wantRowDate(t, "previous_issue_date", row.PreviousIssueDate, "2026-06-01")
}

func TestEndNowEndsTemplateWithOverdueCycle(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	insertChild(t, a.Pool, out.InvoiceID, "2026-06-01", "2026-06-01", "DRAFT", "2026-06-01 10:00:00")
	setToday(t, a, "2026-08-10") // the 2026-07-01 cycle is overdue
	endNow(t, a, out.InvoiceID)
	row := rowFor(t, a, out.InvoiceID)
	if row.Status != "ENDED" {
		t.Errorf("status = %s", row.Status)
	}
	wantRowDate(t, "next_run_date", row.NextRunDate, "")
	if key := cycleKeyFor(t, a, out.InvoiceID, "2026-08-10"); key != "" {
		t.Errorf("cycle key = %q", key)
	}
	cfg := cfgOf(t, getTemplate(t, a, out.InvoiceID).BillingCycleRef)
	if cfg["end_mode"] != "ON_DATE" || cfg["end_date"] != "2026-06-30" {
		t.Errorf("cfg = %v", cfg)
	}
}

func TestEndNowEndsTemplateThatNeverGenerated(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// Today is 2026-01-15: the first cycle (2026-01-01) is overdue. AFTER_N's
	// count goes: the template now ends on a date.
	out := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 12})
	got := endNow(t, a, out.InvoiceID)
	row := rowFor(t, a, out.InvoiceID)
	if row.Status != "ENDED" {
		t.Errorf("status = %s", row.Status)
	}
	wantRowDate(t, "next_run_date", row.NextRunDate, "")
	if key := cycleKeyFor(t, a, out.InvoiceID, "2026-01-15"); key != "" {
		t.Errorf("cycle key = %q", key)
	}
	if want := `{"end_date":"2025-12-31","end_mode":"ON_DATE","interval":1,"frequency":"MONTHLY","start_date":"2026-01-01"}`; string(got.BillingCycleRef) != want {
		t.Errorf("billing_cycle_ref = %s, want %s", got.BillingCycleRef, want)
	}
}

func TestEndNowWithFutureNextCycleEndsYesterday(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-02-24")) // today + 40 days
	endNow(t, a, out.InvoiceID)
	row := rowFor(t, a, out.InvoiceID)
	if row.Status != "ENDED" {
		t.Errorf("status = %s", row.Status)
	}
	wantRowDate(t, "next_run_date", row.NextRunDate, "")
	if key := cycleKeyFor(t, a, out.InvoiceID, "2026-01-15"); key != "" {
		t.Errorf("cycle key = %q", key)
	}
	if cfg := cfgOf(t, getTemplate(t, a, out.InvoiceID).BillingCycleRef); cfg["end_date"] != "2026-01-14" {
		t.Errorf("end_date = %v", cfg["end_date"])
	}
}

func TestEndNowOnPausedTemplateShowsEndedNotPaused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	apitest.Decode[InvoiceOut](t, act(t, a, out.InvoiceID, "PAUSE"), http.StatusOK)
	if row := rowFor(t, a, out.InvoiceID); row.Status != "PAUSED" {
		t.Errorf("status = %s, want PAUSED", row.Status)
	}
	endNow(t, a, out.InvoiceID)
	row := rowFor(t, a, out.InvoiceID)
	if row.Status != "ENDED" {
		t.Errorf("status = %s", row.Status)
	}
	wantRowDate(t, "next_run_date", row.NextRunDate, "")
	if _, ok := cfgOf(t, getTemplate(t, a, out.InvoiceID).BillingCycleRef)["paused"]; ok {
		t.Errorf("paused kept")
	}
}

func TestRowsEndedScheduleOutranksPausedFlag(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "ON_DATE", "end_date": "2026-03-31"})
	insertChild(t, a.Pool, out.InvoiceID, "2026-03-01", "2026-03-01", "DRAFT", "2026-03-01 10:00:00")
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET billing_cycle_ref = billing_cycle_ref || '{"paused": true}' WHERE invoice_id = $1`, out.InvoiceID); err != nil {
		t.Fatal(err)
	}
	if row := rowFor(t, a, out.InvoiceID); row.Status != "ENDED" {
		t.Errorf("status = %s", row.Status)
	}
}

// putBody is _template_put_payload (test_recurring_scheduling.py:502-514).
func putBody(customer uuid.UUID) map[string]any {
	return map[string]any{
		"customer_id": customer, "currency": "USD", "payment_terms": "Net 30",
		"line_items": []any{map[string]any{"description": "Edited", "quantity": 1, "unit_price": 5}},
		"schedule":   monthly("2026-01-01"),
	}
}

func TestEndedTemplateRejectsEdit(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	endNow(t, a, out.InvoiceID)
	wantProblem(t, a.Do(t, http.MethodPut, templatePath(out.InvoiceID), putBody(*out.CustomerID)),
		http.StatusConflict, "recurring_template_ended", endedSentence, "", "")
	// The ended check comes first: an otherwise refused body is a 409 too.
	body := putBody(*out.CustomerID)
	body["line_items"] = []any{}
	wantProblem(t, a.Do(t, http.MethodPut, templatePath(out.InvoiceID), body),
		http.StatusConflict, "recurring_template_ended", endedSentence, "", "")
	if got := getTemplate(t, a, out.InvoiceID); got.PaymentTerms == nil || *got.PaymentTerms != "Net 14" {
		t.Errorf("payment_terms = %v", got.PaymentTerms)
	}
}

func TestEndedTemplateRejectsPauseAndResume(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	first := cfgOf(t, endNow(t, a, out.InvoiceID).BillingCycleRef)["end_date"]
	for _, action := range []string{"PAUSE", "RESUME"} {
		wantProblem(t, act(t, a, out.InvoiceID, action), http.StatusConflict, "recurring_template_ended", endedSentence, "", "")
	}
	// END_NOW again keeps the end date, even a day later.
	setToday(t, a, "2026-01-16")
	if got := cfgOf(t, endNow(t, a, out.InvoiceID).BillingCycleRef)["end_date"]; got != first {
		t.Errorf("end_date = %v, want %v", got, first)
	}
	if row := rowFor(t, a, out.InvoiceID); row.Status != "ENDED" {
		t.Errorf("status = %s", row.Status)
	}
}

func TestActiveTemplateStillAcceptsEdit(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	got := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPut, templatePath(out.InvoiceID), putBody(*out.CustomerID)), http.StatusOK)
	if got.PaymentTerms == nil || *got.PaymentTerms != "Net 30" {
		t.Errorf("payment_terms = %v", got.PaymentTerms)
	}
	if len(got.LineItems) != 1 || got.LineItems[0].Description != "Edited" || got.LineItems[0].LineItemID == out.LineItems[0].LineItemID {
		t.Errorf("lines = %+v", got.LineItems)
	}
	wantDec4(t, "amount", p4(got.Amount), "5")
	wantDec4(t, "balance_due", p4(got.BalanceDue), "5")
	if !got.IsTemplate || got.InvoiceType != "RECURRING" || got.Status != "DRAFT" {
		t.Errorf("template = %+v", got)
	}
}

// Port of test_invoice_withholding.py::test_update_template_gross_up_only_keeps_type_and_rate.
func TestUpdateTemplateGrossUpOnlyKeepsTypeAndRate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	base := func(extra map[string]any) map[string]any {
		b := map[string]any{
			"customer_id": c, "currency": "IDR", "payment_terms": "Net 14",
			"line_items": []any{map[string]any{"description": "Retainer", "quantity": 1, "unit_price": 10000000}},
			"schedule":   monthly("2026-05-01"),
		}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	out := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template",
		base(map[string]any{"wht_type": "PPH23", "wht_rate": 3})), http.StatusCreated)
	wantDec4(t, "amount", p4(out.Amount), "10000000")
	got := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPut, templatePath(out.InvoiceID),
		base(map[string]any{"wht_gross_up": true})), http.StatusOK)
	if got.WhtType != "PPH23" || !got.WhtGrossUp {
		t.Errorf("wht = %s gross-up %v", got.WhtType, got.WhtGrossUp)
	}
	wantDec4(t, "wht_rate", got.WhtRate, "3")
	wantDec4(t, "amount", p4(got.Amount), "10309278")
	wantDec4(t, "wht_amount", p4(got.WhtAmount), "309278")
}

// New tests.

func TestTemplateRequiresLineItems(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	const sentence = "recurring template must have at least one line item"
	body := templateBody(c, monthly("2026-01-01"))
	body["line_items"] = []any{}
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusBadRequest, "template_line_items_required", sentence, "", "")
	delete(body, "line_items")
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusBadRequest, "template_line_items_required", sentence, "", "")
	// Checked before the schedule.
	body["schedule"] = map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "ON_DATE"}
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusBadRequest, "template_line_items_required", sentence, "", "")

	out := mkTemplate(t, a, monthly("2026-01-01"))
	put := putBody(c)
	put["line_items"] = []any{}
	wantProblem(t, a.Do(t, http.MethodPut, templatePath(out.InvoiceID), put), http.StatusBadRequest, "template_line_items_required", sentence, "", "")
	// The schedule is checked on PUT too.
	put = putBody(c)
	put["schedule"] = map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "AFTER_N"}
	wantProblem(t, a.Do(t, http.MethodPut, templatePath(out.InvoiceID), put), http.StatusBadRequest, "invalid_schedule",
		"invalid schedule: end_mode=AFTER_N requires positive integer end_after_cycles", "", "")
}

func TestTemplateFieldValidation(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	for name, edit := range map[string]func(b map[string]any){
		"no schedule":      func(b map[string]any) { delete(b, "schedule") },
		"no payment_terms": func(b map[string]any) { delete(b, "payment_terms") },
		"bad frequency": func(b map[string]any) {
			b["schedule"] = map[string]any{"frequency": "HOURLY", "start_date": "2026-01-01"}
		},
		"interval 0": func(b map[string]any) {
			b["schedule"] = map[string]any{"frequency": "DAILY", "interval": 0, "start_date": "2026-01-01"}
		},
		"end_after_cycles 0": func(b map[string]any) {
			b["schedule"] = map[string]any{"frequency": "DAILY", "start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 0}
		},
		"no start_date":       func(b map[string]any) { b["schedule"] = map[string]any{"frequency": "DAILY"} },
		"negative unit_price": func(b map[string]any) { b["line_items"] = []any{lineBody("-1")} },
	} {
		body := templateBody(c, monthly("2026-01-01"))
		edit(body)
		if rec := a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// payment_terms may be empty.
	body := templateBody(c, monthly("2026-01-01"))
	body["payment_terms"] = ""
	apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusCreated)
}

func TestPutMissingTemplateIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPut, templatePath(id), putBody(c)), http.StatusBadRequest,
		"recurring_template_not_found", "recurring template "+id.String()+" not found", "", "")
	// An invoice that is not a template is not found either.
	inv := insertInvoice(t, a.Pool, nil)
	wantProblem(t, a.Do(t, http.MethodPut, templatePath(inv), putBody(c)), http.StatusBadRequest,
		"recurring_template_not_found", "recurring template "+inv.String()+" not found", "", "")
}

func TestGetNonTemplateIs404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv := insertInvoice(t, a.Pool, nil)
	for _, id := range []uuid.UUID{uuid.New(), inv} {
		wantProblem(t, a.Do(t, http.MethodGet, templatePath(id), nil), http.StatusNotFound,
			"recurring_template_not_found", "recurring template not found", "", "")
		wantProblem(t, act(t, a, id, "PAUSE"), http.StatusNotFound,
			"recurring_template_not_found", "recurring template not found", "", "")
	}
	out := mkTemplate(t, a, monthly("2026-01-01"))
	if got := getTemplate(t, a, out.InvoiceID); got.InvoiceID != out.InvoiceID || !got.IsTemplate {
		t.Errorf("template = %+v", got)
	}
}

func TestPatchUnknownActionIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	wantProblem(t, act(t, a, out.InvoiceID, "stop"), http.StatusBadRequest, "unknown_action", "unknown action: stop", "", "")
	// Actions are upper-cased.
	got := apitest.Decode[InvoiceOut](t, act(t, a, out.InvoiceID, "pause"), http.StatusOK)
	if cfgOf(t, got.BillingCycleRef)["paused"] != true {
		t.Errorf("not paused: %s", got.BillingCycleRef)
	}
	got = apitest.Decode[InvoiceOut](t, act(t, a, out.InvoiceID, "Resume"), http.StatusOK)
	if want := `{"end_mode":"NEVER","interval":1,"frequency":"MONTHLY","start_date":"2026-01-01"}`; string(got.BillingCycleRef) != want {
		t.Errorf("billing_cycle_ref = %s, want %s", got.BillingCycleRef, want)
	}
}

func TestRowsMisconfigured(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	missing := mkTemplate(t, a, monthly("2026-01-01"))
	bad := mkTemplate(t, a, monthly("2026-01-01"))
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET billing_cycle_ref = NULL WHERE invoice_id = $1`, missing.InvoiceID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET billing_cycle_ref = '{"frequency": "HOURLY", "start_date": "2026-01-01"}' WHERE invoice_id = $1`, bad.InvoiceID); err != nil {
		t.Fatal(err)
	}
	insertChild(t, a.Pool, bad.InvoiceID, "2026-02-03", "2026-02-01", "OPEN", "2026-02-03 10:00:00")
	for _, id := range []uuid.UUID{missing.InvoiceID, bad.InvoiceID} {
		row := rowFor(t, a, id)
		if row.Status != "MISCONFIGURED" || row.ScheduleDescription != "Schedule not configured" ||
			row.Frequency != nil || row.StartDate != nil || row.EndMode != nil || row.NextRunDate != nil {
			t.Errorf("row = %+v", row)
		}
	}
	// The children are still counted and Previous still shown.
	row := rowFor(t, a, bad.InvoiceID)
	if row.GeneratedCount != 1 {
		t.Errorf("generated_count = %d", row.GeneratedCount)
	}
	wantRowDate(t, "previous_issue_date", row.PreviousIssueDate, "2026-02-01")
	// A misconfigured template is not ended: PUT and PAUSE are allowed.
	apitest.Decode[InvoiceOut](t, act(t, a, missing.InvoiceID, "PAUSE"), http.StatusOK)
	apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPut, templatePath(bad.InvoiceID), putBody(*bad.CustomerID)), http.StatusOK)
}

func TestRowsDescribe(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	older := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "interval": 3, "start_date": "2026-01-15"})
	newer := mkTemplate(t, a, map[string]any{"frequency": "WEEKLY", "start_date": "2026-03-02", "end_mode": "AFTER_N", "end_after_cycles": 2})
	for id, created := range map[uuid.UUID]string{older.InvoiceID: "2026-01-01 10:00:00", newer.InvoiceID: "2026-01-02 10:00:00"} {
		if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET created_at = $2::timestamp WHERE invoice_id = $1`, id, created); err != nil {
			t.Fatal(err)
		}
	}
	// A regular invoice is no row.
	post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	got := rows(t, a)
	if len(got) != 2 || got[0].TemplateID != newer.InvoiceID || got[1].TemplateID != older.InvoiceID {
		t.Fatalf("rows = %+v", got)
	}
	row := got[1]
	if row.ScheduleDescription != "Every 3 months on the 15th" || row.Status != "ACTIVE" || row.GeneratedCount != 0 ||
		row.CustomerID == nil || *row.CustomerID != *older.CustomerID || row.CustomerName == nil || *row.CustomerName != "Wht Customer" ||
		row.Currency != "USD" || row.PaymentTerms == nil || *row.PaymentTerms != "Net 14" ||
		row.Frequency == nil || *row.Frequency != "MONTHLY" || row.EndMode == nil || *row.EndMode != "NEVER" {
		t.Errorf("row = %+v", row)
	}
	wantDec4(t, "amount", p4(row.Amount), "1000")
	wantRowDate(t, "start_date", row.StartDate, "2026-01-15")
	wantRowDate(t, "next_run_date", row.NextRunDate, "2026-01-15") // nothing generated: the first cycle
	wantRowDate(t, "previous_issue_date", row.PreviousIssueDate, "")
	if got[0].ScheduleDescription != "Weekly on Monday" {
		t.Errorf("weekly = %q", got[0].ScheduleDescription)
	}
	// AFTER_N 2 with both cycles generated: ENDED.
	insertChild(t, a.Pool, newer.InvoiceID, "2026-03-09", "2026-03-09", "PAID", "2026-03-09 10:00:00")
	if row := rowFor(t, a, newer.InvoiceID); row.Status != "ENDED" || row.GeneratedCount != 1 || row.NextRunDate != nil {
		t.Errorf("ended row = %+v", row)
	}
	// The raw JSON: every field present, nulls included.
	rec := a.Do(t, http.MethodGet, templatesPath+"/rows", nil)
	for _, key := range []string{`"customer_name":`, `"next_run_date":null`, `"previous_issue_date":`, `"generated_count":1`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("rows JSON lacks %s: %s", key, rec.Body)
		}
	}
}

func TestPutPreservesPaused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 24})
	apitest.Decode[InvoiceOut](t, act(t, a, out.InvoiceID, "PAUSE"), http.StatusOK)
	got := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPut, templatePath(out.InvoiceID), putBody(*out.CustomerID)), http.StatusOK)
	// The payload's schedule replaces the stored one; only paused is kept.
	if want := `{"paused":true,"end_mode":"NEVER","interval":1,"frequency":"MONTHLY","start_date":"2026-01-01"}`; string(got.BillingCycleRef) != want {
		t.Errorf("billing_cycle_ref = %s, want %s", got.BillingCycleRef, want)
	}
	if row := rowFor(t, a, out.InvoiceID); row.Status != "PAUSED" {
		t.Errorf("status = %s", row.Status)
	}
	// Not paused: no flag is added.
	other := mkTemplate(t, a, monthly("2026-01-01"))
	got = apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPut, templatePath(other.InvoiceID), putBody(*other.CustomerID)), http.StatusOK)
	if _, ok := cfgOf(t, got.BillingCycleRef)["paused"]; ok {
		t.Errorf("paused added: %s", got.BillingCycleRef)
	}
}

func TestListTemplatesNewestFirst(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	older := mkTemplate(t, a, monthly("2026-01-01"))
	newer := mkTemplate(t, a, monthly("2026-01-01"))
	for id, created := range map[uuid.UUID]string{older.InvoiceID: "2026-01-01 10:00:00", newer.InvoiceID: "2026-01-02 10:00:00"} {
		if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET created_at = $2::timestamp WHERE invoice_id = $1`, id, created); err != nil {
			t.Fatal(err)
		}
	}
	post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	rec := a.Do(t, http.MethodGet, templatesPath, nil)
	got := apitest.Decode[[]InvoiceOut](t, rec, http.StatusOK)
	if len(got) != 2 || got[0].InvoiceID != newer.InvoiceID || got[1].InvoiceID != older.InvoiceID || len(got[0].LineItems) != 1 {
		t.Errorf("templates = %s", rec.Body)
	}
}

// TestTemplateScheduleCountsAtMost1000: interval and end_after_cycles are at
// most 1000 (Go change; Python's date arithmetic overflowed into a 500 far
// beyond that), on create and PUT.
func TestTemplateScheduleCountsAtMost1000(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	for _, tc := range []struct {
		field string
		sched map[string]any
	}{
		{"interval", map[string]any{"frequency": "DAILY", "interval": 1001, "start_date": "2026-01-01"}},
		{"end_after_cycles", map[string]any{"frequency": "DAILY", "start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 1001}},
	} {
		body := templateBody(c, tc.sched)
		wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusUnprocessableEntity,
			"", invalid, "schedule."+tc.field, "expected number <= 1000")
		put := putBody(*out.CustomerID)
		put["schedule"] = tc.sched
		wantProblem(t, a.Do(t, http.MethodPut, templatePath(out.InvoiceID), put), http.StatusUnprocessableEntity,
			"", invalid, "schedule."+tc.field, "expected number <= 1000")
	}
	body := templateBody(c, map[string]any{"frequency": "DAILY", "interval": 1000, "start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 1000})
	apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusCreated)
}

// TestTemplateLineItemsNullIs400: "line_items": null on a template create is
// the 400 an empty list is (port note 37), not Python's 422.
func TestTemplateLineItemsNullIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	body := templateBody(insertCustomer(t, a.Pool, "NONE", "", false), monthly("2026-01-01"))
	body["line_items"] = nil
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", body), http.StatusBadRequest,
		"template_line_items_required", "recurring template must have at least one line item", "", "")
}

// TestEndedTemplateUnknownActionIs400: the ended check covers PAUSE and
// RESUME only, so an unknown action on an ended template is still the 400.
func TestEndedTemplateUnknownActionIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := mkTemplate(t, a, monthly("2026-01-01"))
	endNow(t, a, out.InvoiceID)
	wantProblem(t, act(t, a, out.InvoiceID, "stop"), http.StatusBadRequest, "unknown_action", "unknown action: stop", "", "")
}

// TestPatchTemplateWhoseRefIsNotAnObject: a stored billing_cycle_ref that is
// not a JSON object (Python: dict() of it, a 500 for most) is read as an
// empty config, so PAUSE stores just the flag and RESUME an empty object.
func TestPatchTemplateWhoseRefIsNotAnObject(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, tc := range []struct{ stored, action, want string }{
		{`[1, 2]`, "PAUSE", `{"paused":true}`},
		{`"monthly"`, "RESUME", `{}`},
	} {
		out := mkTemplate(t, a, monthly("2026-01-01"))
		if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET billing_cycle_ref = $2::jsonb WHERE invoice_id = $1`, out.InvoiceID, tc.stored); err != nil {
			t.Fatal(err)
		}
		got := apitest.Decode[InvoiceOut](t, act(t, a, out.InvoiceID, tc.action), http.StatusOK)
		if string(got.BillingCycleRef) != tc.want {
			t.Errorf("%s on %s: billing_cycle_ref = %s, want %s", tc.action, tc.stored, got.BillingCycleRef, tc.want)
		}
	}
}

// TestRowsOrder pins the rows view's order (ListTemplateRows): newest template
// created_at first, then invoice_id descending for templates created at the
// same instant (PN 47; Python orders by created_at DESC only,
// invoices.py:215-228).
func TestRowsOrder(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	sched := map[string]any{"frequency": "MONTHLY", "start_date": "2026-01-15"}
	oldest, tieA, tieB := mkTemplate(t, a, sched).InvoiceID, mkTemplate(t, a, sched).InvoiceID, mkTemplate(t, a, sched).InvoiceID
	for id, created := range map[uuid.UUID]string{oldest: "2026-01-01 10:00:00", tieA: "2026-02-01 10:00:00", tieB: "2026-02-01 10:00:00"} {
		if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET created_at = $2::timestamp WHERE invoice_id = $1`, id, created); err != nil {
			t.Fatal(err)
		}
	}
	higher, lower := tieA, tieB
	if tieB.String() > tieA.String() {
		higher, lower = tieB, tieA
	}
	var got []uuid.UUID
	for _, r := range rows(t, a) {
		got = append(got, r.TemplateID)
	}
	if want := []uuid.UUID{higher, lower, oldest}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("rows order = %v, want %v (created_at DESC, then invoice_id DESC)", got, want)
	}
}
