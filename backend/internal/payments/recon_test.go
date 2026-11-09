package payments

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Go's own route tests for the reconciliation routes: no Python test covers
// them (inventory section 3).

const reconPath = "/api/v1/payments"

type auditRow struct {
	LogID        uuid.UUID  `json:"log_id"`
	PaymentID    uuid.UUID  `json:"payment_id"`
	Action       string     `json:"action"`
	Reasons      []string   `json:"reasons"`
	ActorUserID  *uuid.UUID `json:"actor_user_id"`
	ActorEmail   *string    `json:"actor_email"`
	CreatedAt    string     `json:"created_at"`
	PayerName    string     `json:"payer_name"`
	Amount       *string    `json:"amount"`
	Currency     string     `json:"currency"`
	InvoiceID    *uuid.UUID `json:"invoice_id"`
	IntakeSource string     `json:"intake_source"`
}

type logEntry struct {
	LogID       uuid.UUID  `json:"log_id"`
	Action      string     `json:"action"`
	Reasons     []string   `json:"reasons"`
	ActorUserID *uuid.UUID `json:"actor_user_id"`
	CreatedAt   string     `json:"created_at"`
}

// keys are the member names of the JSON object in raw, in order.
func keys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := d.Token(); err != nil {
		t.Fatal(err)
	}
	var out []string
	for d.More() {
		k, err := d.Token()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, k.(string))
		var skip json.RawMessage
		if err := d.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// firstObjectKeys is the key order of the first element of the JSON array in
// rec.
func firstObjectKeys(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var rows []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) == 0 {
		t.Fatalf("not a non-empty array: %v: %s", err, rec.Body)
	}
	return keys(t, rows[0])
}

// payment inserts a payment of status with payer and returns its id.
func (a *api) seedPayment(t *testing.T, payer, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	a.exec(t, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, adjustment_type)
VALUES ($1, 100, 'USD', $2, '2026-04-15', 'WEBHOOK', $3, $4, 'NONE')`, id, payer, id.String(), status)
	return id
}

// logAt inserts a log row for payment at ts; the id is returned.
func (a *api) logAt(t *testing.T, payment uuid.UUID, action string, ts time.Time, actor *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	a.exec(t, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, actor_user_id, created_at) VALUES ($1, $2, $3, ARRAY['r']::text[], $4, $5)`,
		id, payment, action, actor, ts)
	return id
}

func (a *api) audit(t *testing.T, query string) []auditRow {
	t.Helper()
	return apitest.Decode[[]auditRow](t, a.app.Do(t, http.MethodGet, reconPath+"/audit-log"+query, nil), http.StatusOK)
}

func auditIDs(rows []auditRow) []uuid.UUID {
	out := []uuid.UUID{}
	for _, r := range rows {
		out = append(out, r.LogID)
	}
	return out
}

func at(y int, m time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, m, d, h, mi, s, 0, time.UTC)
}

func TestAuditLogFiltersOrderLimit(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	acme := a.seedPayment(t, "Acme Corp", "CLEARED")
	beta := a.seedPayment(t, "Beta Ltd", "PENDING_MANUAL_REVIEW")
	admin := a.admin

	l1 := a.logAt(t, beta, "HELD", at(2026, 4, 1, 9, 0, 0), nil)
	l2 := a.logAt(t, acme, "CLEARED", at(2026, 4, 2, 0, 0, 0), nil)
	l3 := a.logAt(t, acme, "OVERRIDDEN", at(2026, 4, 2, 23, 59, 59), &admin)
	l4 := a.logAt(t, beta, "HELD", at(2026, 4, 3, 0, 0, 0), nil)
	// One instant, two rows: the key decides, descending.
	tieA := a.logAt(t, acme, "REVERSED", at(2026, 4, 4, 12, 0, 0), nil)
	tieB := a.logAt(t, acme, "REVERSED", at(2026, 4, 4, 12, 0, 0), nil)
	tie := []uuid.UUID{tieA, tieB}
	slices.SortFunc(tie, func(x, y uuid.UUID) int { return -slices.Compare(x[:], y[:]) })

	all := a.audit(t, "")
	want := append(slices.Clone(tie), l4, l3, l2, l1)
	if !slices.Equal(auditIDs(all), want) {
		t.Fatalf("order %v, want %v", auditIDs(all), want)
	}

	if got := a.audit(t, "?action=HELD"); !slices.Equal(auditIDs(got), []uuid.UUID{l4, l1}) {
		t.Errorf("action filter %v", auditIDs(got))
	}
	if got := a.audit(t, "?payer=acme"); !slices.Equal(auditIDs(got), append(slices.Clone(tie), l3, l2)) {
		t.Errorf("payer filter %v (ILIKE: case-insensitive substring)", auditIDs(got))
	}
	// An empty action or payer is no filter, as Python's truthiness.
	if got := a.audit(t, "?action=&payer="); len(got) != 6 {
		t.Errorf("empty filters listed %d, want 6", len(got))
	}
	// from_date 00:00 and to_date through its last second: the whole days.
	if got := a.audit(t, "?from_date=2026-04-02&to_date=2026-04-02"); !slices.Equal(auditIDs(got), []uuid.UUID{l3, l2}) {
		t.Errorf("one-day window %v, want [%s %s]", auditIDs(got), l3, l2)
	}
	if got := a.audit(t, "?to_date=2026-04-01"); !slices.Equal(auditIDs(got), []uuid.UUID{l1}) {
		t.Errorf("to_date %v", auditIDs(got))
	}
	if got := a.audit(t, "?from_date=2026-04-04"); len(got) != 2 {
		t.Errorf("from_date listed %d, want 2", len(got))
	}
	if got := a.audit(t, "?limit=2"); !slices.Equal(auditIDs(got), tie) {
		t.Errorf("limit 2 %v, want %v", auditIDs(got), tie)
	}
	wantProblemStatus(t, a.app.Do(t, http.MethodGet, reconPath+"/audit-log?from_date=nope", nil), http.StatusUnprocessableEntity)

	byID := map[uuid.UUID]auditRow{}
	for _, r := range all {
		byID[r.LogID] = r
	}
	r3 := byID[l3]
	if r3.ActorUserID == nil || *r3.ActorUserID != admin || r3.ActorEmail == nil || *r3.ActorEmail == "" {
		t.Errorf("actor %v %v", r3.ActorUserID, r3.ActorEmail)
	}
	if r3.PaymentID != acme || r3.PayerName != "Acme Corp" || r3.Currency != "USD" || r3.IntakeSource != "WEBHOOK" ||
		r3.Amount == nil || *r3.Amount != "100.0000" || r3.InvoiceID != nil || !slices.Equal(r3.Reasons, []string{"r"}) {
		t.Errorf("row %+v", r3)
	}
	if r3.CreatedAt != "2026-04-02T23:59:59Z" {
		t.Errorf("created_at %q", r3.CreatedAt)
	}
	if r1 := byID[l1]; r1.ActorUserID != nil || r1.ActorEmail != nil {
		t.Errorf("system row has an actor: %+v", r1)
	}
	got := firstObjectKeys(t, a.app.Do(t, http.MethodGet, reconPath+"/audit-log", nil))
	if !slices.Equal(got, []string{"log_id", "payment_id", "action", "reasons", "actor_user_id", "actor_email", "created_at", "payer_name", "amount", "currency", "invoice_id", "intake_source"}) {
		t.Errorf("keys %v", got)
	}
}

func wantProblemStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
}

func TestAuditLogPayerEscapesWildcards(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	pct := a.seedPayment(t, "100% Co", "CLEARED")
	under := a.seedPayment(t, "snake_case Ltd", "CLEARED")
	slash := a.seedPayment(t, `back\slash`, "CLEARED")
	plain := a.seedPayment(t, "Plain Inc", "CLEARED")
	ts := at(2026, 4, 1, 0, 0, 0)
	lp := a.logAt(t, pct, "CLEARED", ts, nil)
	lu := a.logAt(t, under, "CLEARED", ts.Add(time.Second), nil)
	ls := a.logAt(t, slash, "CLEARED", ts.Add(2*time.Second), nil)
	a.logAt(t, plain, "CLEARED", ts.Add(3*time.Second), nil)
	for _, tc := range []struct {
		name, payer string
		want        uuid.UUID
	}{
		{"percent", "%", lp},
		{"underscore", "_", lu},
		{"backslash", `\`, ls},
		{"percent in context", "0% C", lp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := a.audit(t, "?payer="+url.QueryEscape(tc.payer))
			if !slices.Equal(auditIDs(got), []uuid.UUID{tc.want}) {
				t.Errorf("payer %q matched %d rows, want only %s", tc.payer, len(got), tc.want)
			}
		})
	}
}

func TestAuditLogLimitBounds(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=501", "?limit=abc"} {
		wantProblemStatus(t, a.app.Do(t, http.MethodGet, reconPath+"/audit-log"+q, nil), http.StatusUnprocessableEntity)
	}
	for _, q := range []string{"", "?limit=1", "?limit=500"} {
		rec := a.app.Do(t, http.MethodGet, reconPath+"/audit-log"+q, nil)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Errorf("%q: %d %q, want 200 []", q, rec.Code, rec.Body)
		}
	}
}

func TestAwaitingReviewOnlyPending(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	rec := a.app.Do(t, http.MethodGet, reconPath+"/awaiting-review", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty: %d %q, want 200 []", rec.Code, rec.Body)
	}
	a.seedPayment(t, "Cleared", "CLEARED")
	a.seedPayment(t, "Flagged", "FLAGGED")
	var want []uuid.UUID
	ts := at(2026, 4, 1, 10, 0, 0)
	for i, n := range []string{"First", "Second"} {
		id := uuid.New()
		a.exec(t, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, adjustment_type, confidence_score, created_at)
VALUES ($1, 100, 'USD', $2, '2026-04-15', 'WEBHOOK', $3, 'PENDING_MANUAL_REVIEW', 'NONE', 87.5, $4)`, id, n, id.String(), ts.Add(time.Duration(i)*time.Hour))
		want = append([]uuid.UUID{id}, want...) // newest first
	}
	// A tie on created_at falls to the key, descending.
	tieA, tieB := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{tieA, tieB} {
		a.exec(t, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, adjustment_type, created_at)
VALUES ($1, 100, 'USD', 'Tie', '2026-04-15', 'WEBHOOK', $2, 'PENDING_MANUAL_REVIEW', 'NONE', $3)`, id, id.String(), ts.Add(-time.Hour))
	}
	tie := []uuid.UUID{tieA, tieB}
	slices.SortFunc(tie, func(x, y uuid.UUID) int { return -slices.Compare(x[:], y[:]) })
	want = append(want, tie...)

	rec = a.app.Do(t, http.MethodGet, reconPath+"/awaiting-review", nil)
	got := apitest.Decode[[]PaymentOut](t, rec, http.StatusOK)
	if !slices.Equal(ids(got), want) {
		t.Fatalf("listed %v, want %v", ids(got), want)
	}
	if got[0].ConfidenceScore == nil || got[0].ConfidenceScore.Decimal().String() != "87.5" || got[0].Status != "PENDING_MANUAL_REVIEW" {
		t.Errorf("first %+v", got[0])
	}
	if k := firstObjectKeys(t, rec); k[0] != "payment_id" || k[len(k)-1] != "created_at" {
		t.Errorf("keys %v", k)
	}
}

func TestGetPayment404(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	wantProblem(t, a.app.Do(t, http.MethodGet, reconPath+"/"+uuid.NewString(), nil), http.StatusNotFound, "payment_not_found", "payment not found")
	wantProblemStatus(t, a.app.Do(t, http.MethodGet, reconPath+"/not-a-uuid", nil), http.StatusUnprocessableEntity)
	id := a.seedPayment(t, "Acme", "FLAGGED")
	got := apitest.Decode[PaymentOut](t, a.app.Do(t, http.MethodGet, reconPath+"/"+id.String(), nil), http.StatusOK)
	if got.PaymentID != id || got.Status != "FLAGGED" || got.PayerName != "Acme" || got.Amount.Decimal().String() != "100" {
		t.Errorf("payment %+v", got)
	}
}

func TestPaymentLogUnknownIsEmpty(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	rec := a.app.Do(t, http.MethodGet, reconPath+"/"+uuid.NewString()+"/log", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("%d %q, want 200 []", rec.Code, rec.Body)
	}
	id := a.seedPayment(t, "Acme", "CLEARED")
	admin := a.admin
	ts := at(2026, 4, 1, 0, 0, 0)
	late := a.logAt(t, id, "REVERSED", ts.Add(time.Hour), &admin)
	tieA := a.logAt(t, id, "HELD", ts, nil)
	tieB := a.logAt(t, id, "OVERRIDDEN", ts, nil)
	tie := []uuid.UUID{tieA, tieB}
	slices.SortFunc(tie, func(x, y uuid.UUID) int { return slices.Compare(x[:], y[:]) })
	a.logAt(t, a.seedPayment(t, "Other", "CLEARED"), "HELD", ts, nil)

	rec = a.app.Do(t, http.MethodGet, reconPath+"/"+id.String()+"/log", nil)
	rows := apitest.Decode[[]logEntry](t, rec, http.StatusOK)
	var got []uuid.UUID
	for _, r := range rows {
		got = append(got, r.LogID)
	}
	if want := append(slices.Clone(tie), late); !slices.Equal(got, want) {
		t.Fatalf("log %v, want oldest first %v", got, want)
	}
	if last := rows[2]; last.Action != "REVERSED" || last.ActorUserID == nil || *last.ActorUserID != admin || !slices.Equal(last.Reasons, []string{"r"}) {
		t.Errorf("last %+v", last)
	}
	if k := firstObjectKeys(t, rec); !slices.Equal(k, []string{"log_id", "action", "reasons", "actor_user_id", "created_at"}) {
		t.Errorf("keys %v", k)
	}
}

// The literal paths are not read as a payment id.
func TestStaticPathsNotSwallowed(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	for _, p := range []string{"/audit-log", "/awaiting-review"} {
		rec := a.app.Do(t, http.MethodGet, reconPath+p, nil)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Errorf("%s: %d %q, want 200 []", p, rec.Code, rec.Body)
		}
	}
}

func (a *api) manualReview(t *testing.T, payment uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	return a.app.Do(t, http.MethodPost, reconPath+"/"+payment.String()+"/manual-review", body)
}

func (a *api) reverseOver(t *testing.T, payment uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	return a.app.Do(t, http.MethodPost, reconPath+"/"+payment.String()+"/reverse", body)
}

func TestManualReviewClearsOverRest(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	invID := a.sentInvoice(t)
	held := a.incoming(t, payload(amount("400"), payer("Nobody")))
	if held.Status != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s", held.Status)
	}
	// adjustment_type defaults to NONE.
	got := apitest.Decode[PaymentOut](t, a.manualReview(t, held.PaymentID, map[string]any{"invoice_id": invID}), http.StatusOK)
	if got.Status != "CLEARED" || got.AdjustmentType != "NONE" || got.InvoiceID == nil || *got.InvoiceID != invID {
		t.Errorf("payment %+v", got)
	}
	checkInvoice(t, a.env, invID, "600", "0", "PARTIAL")
	logs := a.logs(t, held.PaymentID)
	if last := logs[len(logs)-1]; last.action != "OVERRIDDEN" || last.actor == nil || *last.actor != a.admin {
		t.Errorf("log %+v", logs)
	}

	second := a.incoming(t, payload(amount("600"), payer("Nobody")))
	got = apitest.Decode[PaymentOut](t, a.manualReview(t, second.PaymentID, map[string]any{"invoice_id": invID, "adjustment_type": "BANK_FEE"}), http.StatusOK)
	if got.AdjustmentType != "BANK_FEE" {
		t.Errorf("adjustment %s", got.AdjustmentType)
	}
	checkInvoice(t, a.env, invID, "0", "0", "PAID")
	if n := a.count(t, `SELECT count(*) FROM journal_entries WHERE source_type = 'PAYMENT' AND source_id IN ($1, $2)`, held.PaymentID, second.PaymentID); n != 2 {
		t.Errorf("%d entries, want 2", n)
	}
}

func TestManualReviewUnknownAdjustmentIs400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	invID := a.sentInvoice(t)
	held := a.incoming(t, payload(amount("400"), payer("Nobody")))
	for _, tc := range []struct{ adj, detail string }{
		{"FOO", "unknown adjustment_type 'FOO'"},
		{"it's", `unknown adjustment_type "it's"`},
		{"", "unknown adjustment_type ''"},
		{"none", "unknown adjustment_type 'none'"},
	} {
		rec := a.manualReview(t, held.PaymentID, map[string]any{"invoice_id": invID, "adjustment_type": tc.adj})
		wantProblem(t, rec, http.StatusBadRequest, "", tc.detail)
	}
	for _, adj := range []string{"NONE", "PARTIAL_PAY", "CREDIT_ON_ACCOUNT", "BANK_FEE"} {
		p := a.incoming(t, payload(amount("1"), payer("Nobody")))
		got := apitest.Decode[PaymentOut](t, a.manualReview(t, p.PaymentID, map[string]any{"invoice_id": invID, "adjustment_type": adj}), http.StatusOK)
		if got.AdjustmentType != adj {
			t.Errorf("adjustment %s, want %s", got.AdjustmentType, adj)
		}
	}
	if got := a.payment0(t, held.PaymentID); got.Status != "PENDING_MANUAL_REVIEW" {
		t.Errorf("refused payment is %s", got.Status)
	}
	wantProblemStatus(t, a.manualReview(t, held.PaymentID, map[string]any{"adjustment_type": "NONE"}), http.StatusUnprocessableEntity)
	wantProblemStatus(t, a.manualReview(t, held.PaymentID, map[string]any{"invoice_id": invID, "adjustment_type": nil}), http.StatusUnprocessableEntity)
}

func (a *api) payment0(t *testing.T, id uuid.UUID) PaymentOut {
	t.Helper()
	return apitest.Decode[PaymentOut](t, a.app.Do(t, http.MethodGet, reconPath+"/"+id.String(), nil), http.StatusOK)
}

// Every service refusal is a 400 with its sentence and leaves nothing behind.
func TestManualReviewUnknownPaymentIs400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	invID := a.sentInvoice(t)
	unknown := uuid.New()
	wantProblem(t, a.manualReview(t, unknown, map[string]any{"invoice_id": invID}), http.StatusBadRequest, "payment_not_found", "payment "+unknown.String()+" not found")

	held := a.incoming(t, payload(amount("400"), payer("Nobody")))
	noInv := uuid.New()
	wantProblem(t, a.manualReview(t, held.PaymentID, map[string]any{"invoice_id": noInv}), http.StatusBadRequest, "invoice_not_found", "invoice "+noInv.String()+" not found")

	draft := a.invoice(t, inv{customer: a.customer(t, "Draft Co"), currency: "USD", status: "DRAFT", balance: "10"})
	wantProblem(t, a.manualReview(t, held.PaymentID, map[string]any{"invoice_id": draft}), http.StatusBadRequest, "", "cannot approve payment against DRAFT invoice")

	cleared := a.incoming(t, payload())
	wantProblem(t, a.manualReview(t, cleared.PaymentID, map[string]any{"invoice_id": invID}), http.StatusBadRequest, "payment_already_cleared", "payment "+cleared.PaymentID.String()+" already CLEARED")

	// A missing rate is a refusal too, and the transaction is rolled back.
	rateless := a.rateless(t, "1000", "0")
	old := a.incoming(t, payload(amount("999"), paid(day(2025, 12, 1))))
	wantProblem(t, a.manualReview(t, old.PaymentID, map[string]any{"invoice_id": rateless}), http.StatusBadRequest, "", "no fx rate for USD->IDR on or before 2025-12-01")
	checkInvoice(t, a.env, rateless, "1000", "0", "SENT")
	if got := a.payment0(t, old.PaymentID); got.Status != "PENDING_MANUAL_REVIEW" {
		t.Errorf("payment %s after the refusal", got.Status)
	}
	if got := a.payment0(t, held.PaymentID); got.Status != "PENDING_MANUAL_REVIEW" || got.InvoiceID != nil && *got.InvoiceID == draft {
		t.Errorf("held payment %+v", got)
	}
}

func TestReverseOverRest(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	invID := a.sentInvoice(t)
	p := a.incoming(t, payload())
	checkInvoice(t, a.env, invID, "0", "0", "PAID")

	got := apitest.Decode[PaymentOut](t, a.reverseOver(t, p.PaymentID, map[string]any{"reason": "bank bounced"}), http.StatusOK)
	if got.Status != "FLAGGED" {
		t.Errorf("status %s", got.Status)
	}
	checkInvoice(t, a.env, invID, "1000", "0", "OPEN")
	logs := a.logs(t, p.PaymentID)
	if last := logs[len(logs)-1]; last.action != "REVERSED" || !slices.Equal(last.reasons, []string{"bank bounced"}) || last.actor == nil || *last.actor != a.admin {
		t.Errorf("log %+v", logs)
	}
	if es := a.entries(t, p.PaymentID); len(es) != 2 || es[1].memo != "Reversal: bank bounced" {
		t.Errorf("entries %+v", es)
	}

	// Refusals: 400 with their sentence.
	wantProblem(t, a.reverseOver(t, p.PaymentID, map[string]any{"reason": "again"}), http.StatusBadRequest, "payment_not_reversible", "only CLEARED payments can be reversed (got FLAGGED)")
	unknown := uuid.New()
	wantProblem(t, a.reverseOver(t, unknown, map[string]any{"reason": "x"}), http.StatusBadRequest, "payment_not_found", "payment "+unknown.String()+" not found")
	manual := a.mustRecord(t, invID, map[string]any{"amount": "100"})
	wantProblem(t, a.reverseOver(t, manual.PaymentID, map[string]any{"reason": "x"}), http.StatusBadRequest, "no_active_entry", "no active journal entry to reverse for payment "+manual.PaymentID.String())

	// reason is required and may be empty.
	again := a.incoming(t, payload(amount("600"), payer("Nobody")))
	a.mustApprove(t, again.PaymentID, invID)
	wantProblemStatus(t, a.reverseOver(t, again.PaymentID, map[string]any{}), http.StatusUnprocessableEntity)
	wantProblemStatus(t, a.reverseOver(t, again.PaymentID, map[string]any{"reason": nil}), http.StatusUnprocessableEntity)
	if got := a.payment0(t, again.PaymentID); got.Status != "CLEARED" {
		t.Fatalf("a refused request changed the payment to %s", got.Status)
	}
	got = apitest.Decode[PaymentOut](t, a.reverseOver(t, again.PaymentID, map[string]any{"reason": ""}), http.StatusOK)
	if got.Status != "FLAGGED" {
		t.Errorf("empty reason: status %s", got.Status)
	}
}

// reason is bounded so "Reversal: <reason>" fits the memo's 500 characters
// (Go change: Python answered 500).
func TestReverseReasonBound(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.sentInvoice(t)
	p := a.incoming(t, payload())
	wantFieldError(t, a.reverseOver(t, p.PaymentID, map[string]any{"reason": strings.Repeat("x", 491)}), "reason")
	if got := a.payment0(t, p.PaymentID); got.Status != "CLEARED" {
		t.Fatalf("status %s after a refused reason", got.Status)
	}
	// 490 characters, multi-byte included, fit.
	got := apitest.Decode[PaymentOut](t, a.reverseOver(t, p.PaymentID, map[string]any{"reason": strings.Repeat("é", 490)}), http.StatusOK)
	if got.Status != "FLAGGED" {
		t.Errorf("status %s", got.Status)
	}
}

func TestReconRoutesRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	staffID := uuid.New()
	a.exec(t, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID)
	token, _, err := a.app.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a.app
	staff.Cookie = platform.SessionCookie + "=" + token
	id := uuid.NewString()
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, reconPath + "/audit-log", nil},
		{http.MethodGet, reconPath + "/awaiting-review", nil},
		{http.MethodGet, reconPath + "/" + id, nil},
		{http.MethodGet, reconPath + "/" + id + "/log", nil},
		{http.MethodPost, reconPath + "/" + id + "/manual-review", map[string]any{"invoice_id": uuid.NewString()}},
		{http.MethodPost, reconPath + "/" + id + "/reverse", map[string]any{"reason": "x"}},
	} {
		wantProblem(t, a.app.Anon(t, c.method, c.path, c.body), http.StatusUnauthorized, "", "not authenticated")
		wantProblem(t, staff.Do(t, c.method, c.path, c.body), http.StatusForbidden, "", "admin required")
	}
}
