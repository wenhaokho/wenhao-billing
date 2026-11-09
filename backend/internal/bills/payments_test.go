package bills_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
)

func payPath(id uuid.UUID) string  { return billPath(id) + "/record-payment" }
func voidPath(id uuid.UUID) string { return billPath(id) + "/void" }

// payment is a record-payment body for amount, with the frontend's other
// fields.
func payment(amount any) map[string]any {
	return map[string]any{"amount": amount, "payment_date": "2026-01-15", "payer_reference": "TRX-1", "notes": "bank"}
}

func pay(t *testing.T, a *apitest.App, id uuid.UUID, amount any) bills.BillOut {
	t.Helper()
	return apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, payPath(id), payment(amount)), http.StatusOK)
}

// openBill is a bill of 1000 (one line 1 x 1000) in status, with extra on
// its create body.
func openBill(t *testing.T, a *apitest.App, status string, extra map[string]any) bills.BillOut {
	t.Helper()
	out := post(t, a, body(insertVendor(t, a), extra))
	if status != "DRAFT" {
		setStatus(t, a, out.BillID, status)
	}
	return out
}

func countRows(t *testing.T, a *apitest.App, table string) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordPaymentPartial(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	got := pay(t, a, b.BillID, 300.5)
	if got.Status != "PARTIAL" {
		t.Errorf("status %q, want PARTIAL", got.Status)
	}
	wantDec4(t, "balance_due", p4(got.BalanceDue), "699.5")
	wantDec4(t, "amount", p4(got.Amount), "1000")
	// A PARTIAL bill takes further payments; a string amount is a decimal.
	got = pay(t, a, b.BillID, "99.5")
	if got.Status != "PARTIAL" {
		t.Errorf("status %q, want PARTIAL", got.Status)
	}
	wantDec4(t, "balance_due", p4(got.BalanceDue), "600")
	if stored := get(t, a, b.BillID); stored.Status != "PARTIAL" || !stored.BalanceDue.Decimal().Equal(got.BalanceDue.Decimal()) {
		t.Errorf("stored %+v", stored)
	}
}

func TestRecordPaymentExactBalancePays(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	pay(t, a, b.BillID, 400)
	got := pay(t, a, b.BillID, "600.0000")
	if got.Status != "PAID" {
		t.Errorf("status %q, want PAID", got.Status)
	}
	if got.BalanceDue.Decimal().String() != "0" {
		t.Errorf("balance_due %s, want 0", got.BalanceDue.Decimal())
	}
	wantProblem(t, a.Do(t, http.MethodPost, payPath(b.BillID), payment(1)), http.StatusBadRequest,
		"bill_not_payable", "cannot record payment on PAID bill", "", "")
}

// The refused amount is printed as Python's str(Decimal(amount)), the
// balance at the column's four places. FastAPI json.loads the body before
// pydantic sees it, so a JSON number with a fraction or exponent is already
// a float: it prints as str(Decimal(repr(float))) (600.00 is 600.0). A JSON
// integer prints its digits, and a JSON string keeps its own digits and
// scale. Verified against FastAPI 0.162.2 / pydantic 2.14.3.
func TestRecordPaymentOverpaymentIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	pay(t, a, b.BillID, 500) // balance 500.0000
	for _, c := range []struct {
		raw, printed string
	}{
		{`600.00`, "600.0"},
		{`600.10`, "600.1"},
		{`6e2`, "600.0"},
		{`6.0E2`, "600.0"},
		{`0.6e3`, "600.0"},
		{`1.5e3`, "1500.0"},
		{`60000e-2`, "600.0"},
		{`600`, "600"},
		{`600.5`, "600.5"},
		{`500.0001`, "500.0001"},
		{`"600.00"`, "600.00"},
		{`"0600.5"`, "600.5"},
		{`"6e2"`, "6E+2"},
	} {
		rec := a.Do(t, http.MethodPost, payPath(b.BillID), `{"amount": `+c.raw+`, "payment_date": "2026-01-15"}`)
		wantProblem(t, rec, http.StatusBadRequest, "payment_exceeds_balance",
			"amount "+c.printed+" exceeds balance due 500.0000", "", "")
	}
	if stored := get(t, a, b.BillID); stored.Status != "PARTIAL" || stored.BalanceDue.Decimal().String() != "500" {
		t.Errorf("stored %s %s, want PARTIAL 500", stored.Status, stored.BalanceDue.Decimal())
	}
}

func TestRecordPaymentOnDraftIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, status := range []string{"DRAFT", "VOID", "PAID"} {
		b := openBill(t, a, status, nil)
		wantProblem(t, a.Do(t, http.MethodPost, payPath(b.BillID), payment(1)), http.StatusBadRequest,
			"bill_not_payable", "cannot record payment on "+status+" bill", "", "")
		if stored := get(t, a, b.BillID); stored.Status != status || stored.BalanceDue.Decimal().String() != "1000" {
			t.Errorf("%s: stored %s %s", status, stored.Status, stored.BalanceDue.Decimal())
		}
	}
}

func TestRecordPaymentValidation(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	const notPositive = "Input should be greater than 0"
	wantProblem(t, a.Do(t, http.MethodPost, payPath(b.BillID), payment(0)), http.StatusUnprocessableEntity,
		"", notPositive, "amount", notPositive)
	wantProblem(t, a.Do(t, http.MethodPost, payPath(b.BillID), payment("-1")), http.StatusUnprocessableEntity,
		"", notPositive, "amount", notPositive)
	wantFieldError(t, a.Do(t, http.MethodPost, payPath(b.BillID), payment("1.00001")), "amount", "value has too many decimal places (max 4)")
	wantFieldError(t, a.Do(t, http.MethodPost, payPath(b.BillID), map[string]any{"payment_date": "2026-01-15"}), "amount",
		"expected required property amount to be present")
	wantFieldError(t, a.Do(t, http.MethodPost, payPath(b.BillID), map[string]any{"amount": 1}), "payment_date",
		"expected required property payment_date to be present")
	// Nothing was paid; validation comes before the bill is read.
	if stored := get(t, a, b.BillID); stored.Status != "OPEN" || stored.BalanceDue.Decimal().String() != "1000" {
		t.Errorf("stored %s %s", stored.Status, stored.BalanceDue.Decimal())
	}
	wantProblem(t, a.Do(t, http.MethodPost, payPath(uuid.New()), payment(0)), http.StatusUnprocessableEntity,
		"", notPositive, "amount", notPositive)
}

// waitForLockWaits waits until n sessions of this test's database wait on a
// lock; false when they did not within five seconds. Each poll has its own
// short timeout, so a stuck query cannot outlive the bound.
func waitForLockWaits(t *testing.T, pool *pgxpool.Pool, n int) bool {
	t.Helper()
	for range 500 {
		var waiting int
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestRecordPaymentConcurrentNeverOverpays: two payments of 600 against a
// balance of 1000 each fit alone, not together. The test holds the bill's
// row lock until both requests wait on it. With the bill read FOR UPDATE,
// both wait at the read and the second reads the first's balance (400), so
// it is refused; without it both would read 1000, both wait at the write,
// and both succeed: Python's lost update.
func TestRecordPaymentConcurrentNeverOverpays(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)

	ctx := t.Context()
	hold, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM bills WHERE bill_id = $1 FOR UPDATE`, b.BillID); err != nil {
		t.Fatal(err)
	}
	// The goroutines serve the requests without t (a.Do may t.Fatal, which
	// only the test goroutine may call). Should the test goroutine fail
	// first, the deferred rollback releases the row and this cleanup, which
	// runs after it, waits for both.
	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	for i := range recs {
		wg.Go(func() {
			req := httptest.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, payPath(b.BillID),
				strings.NewReader(`{"amount": 600, "payment_date": "2026-01-15"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Cookie", a.Cookie)
			rec := httptest.NewRecorder()
			a.Handler.ServeHTTP(rec, req)
			recs[i] = rec
		})
	}
	waited := waitForLockWaits(t, a.Pool, 2)
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !waited {
		t.Error("the two payments never both waited on a lock")
	}

	var oks, refused int
	for _, rec := range recs {
		switch rec.Code {
		case http.StatusOK:
			oks++
		case http.StatusBadRequest:
			refused++
			wantProblem(t, rec, http.StatusBadRequest, "payment_exceeds_balance", "amount 600 exceeds balance due 400.0000", "", "")
		default:
			t.Errorf("payment: %d %s", rec.Code, rec.Body)
		}
	}
	if oks != 1 || refused != 1 {
		t.Errorf("%d paid, %d refused; want 1 and 1", oks, refused)
	}
	stored := get(t, a, b.BillID)
	if stored.Status != "PARTIAL" || stored.BalanceDue.Decimal().String() != "400" {
		t.Errorf("stored %s %s, want PARTIAL 400", stored.Status, stored.BalanceDue.Decimal())
	}
}

// A bill payment posts nothing: no payment row, no journal (port note 8).
func TestRecordPaymentWritesNoJournal(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	pay(t, a, b.BillID, 250)
	pay(t, a, b.BillID, 750)
	for _, table := range []string{"journal_entries", "journal_lines", "payments"} {
		if n := countRows(t, a, table); n != 0 {
			t.Errorf("%d rows in %s, want 0", n, table)
		}
	}
}

func void(t *testing.T, a *apitest.App, id uuid.UUID, body any) bills.BillOut {
	t.Helper()
	return apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, voidPath(id), body), http.StatusOK)
}

func TestVoidAppendsNote(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, c := range []struct {
		status string
		extra  map[string]any
		body   any
		notes  string
	}{
		// Null notes: the note starts with the newline.
		{"DRAFT", nil, map[string]any{"reason": "duplicate"}, "\n[voided] duplicate"},
		{"OPEN", map[string]any{"notes": "n"}, map[string]any{}, "n\n[voided] no reason given"},
		// Any other key is ignored.
		{"PARTIAL", map[string]any{"notes": ""}, map[string]any{"why": "x", "reason": "typo"}, "\n[voided] typo"},
	} {
		b := openBill(t, a, c.status, c.extra)
		got := void(t, a, b.BillID, c.body)
		if got.Status != "VOID" || got.Notes == nil || *got.Notes != c.notes || got.BalanceDue.Decimal().String() != "0" {
			t.Errorf("%s: status %s notes %q balance %s", c.status, got.Status, deref(got.Notes), got.BalanceDue.Decimal())
		}
		wantDec4(t, "amount", p4(got.Amount), "1000")
		if stored := get(t, a, b.BillID); stored.Status != "VOID" || deref(stored.Notes) != c.notes {
			t.Errorf("%s: stored %s %q", c.status, stored.Status, deref(stored.Notes))
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// The reason is any JSON value, written as Python's f-string writes what
// json.loads made of it; a falsy one is "no reason given".
func TestVoidReasonNonString(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, c := range []struct{ raw, want string }{
		{`42`, "42"},
		{`-7`, "-7"},
		{`123456789012345678901234567890`, "123456789012345678901234567890"},
		{`1.50`, "1.5"},
		{`1e2`, "100.0"},
		{`2.5E-3`, "0.0025"},
		{`0.0001`, "0.0001"},
		{`0.00001`, "1e-05"},
		{`1e16`, "1e+16"},
		{`1234567890123456.0`, "1234567890123456.0"},
		{`0.1`, "0.1"},
		{`true`, "True"},
		{`"0"`, "0"},
		{`"it's"`, "it's"},
		{`[1, "a", null, true, false, 2.5, []]`, "[1, 'a', None, True, False, 2.5, []]"},
		{`{"k": "it's", "n": {"x": [0.0]}}`, `{'k': "it's", 'n': {'x': [0.0]}}`},
		{`{"a": 1, "b": 2, "a": 3}`, "{'a': 3, 'b': 2}"},
		{
			`["a'b\"c", "q\"", "tab\there\n", "back\\slash", "\u00e9\u0001\u007f\u00a0"]`,
			`['a\'b"c', 'q"', 'tab\there\n', 'back\\slash', 'é\x01\x7f\xa0']`,
		},
		// Falsy: Python's `or` falls through to the default.
		{`null`, "no reason given"},
		{`false`, "no reason given"},
		{`0`, "no reason given"},
		{`-0`, "no reason given"},
		{`0.0`, "no reason given"},
		{`-0e5`, "no reason given"},
		{`1e-400`, "no reason given"},
		{`""`, "no reason given"},
		{`[]`, "no reason given"},
		{`{}`, "no reason given"},
	} {
		b := openBill(t, a, "OPEN", nil)
		got := void(t, a, b.BillID, `{"reason": `+c.raw+`}`)
		if want := "\n[voided] " + c.want; deref(got.Notes) != want {
			t.Errorf("reason %s: notes %q, want %q", c.raw, deref(got.Notes), want)
		}
	}
}

func TestVoidPaidIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, status := range []string{"PAID", "VOID"} {
		b := openBill(t, a, status, map[string]any{"notes": "keep"})
		wantProblem(t, a.Do(t, http.MethodPost, voidPath(b.BillID), map[string]any{"reason": "x"}), http.StatusBadRequest,
			"bill_not_voidable", "bill is "+status+" and cannot be voided", "", "")
		if stored := get(t, a, b.BillID); stored.Status != status || deref(stored.Notes) != "keep" ||
			stored.BalanceDue.Decimal().String() != "1000" {
			t.Errorf("%s: stored %s %q %s", status, stored.Status, deref(stored.Notes), stored.BalanceDue.Decimal())
		}
	}
}

func TestVoidRequiresBody(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	// As accept and convert: a JSON null body, or one that is not an
	// object, is Python's 422; no body at all is huma's 400 "request body is
	// required" (PORT-NOTES).
	for _, c := range []struct {
		name string
		body any
		code int
	}{
		{"null", "null", http.StatusUnprocessableEntity},
		{"list", `["reason"]`, http.StatusUnprocessableEntity},
		{"string", `"reason"`, http.StatusUnprocessableEntity},
		{"empty", nil, http.StatusBadRequest},
	} {
		if rec := a.Do(t, http.MethodPost, voidPath(b.BillID), c.body); rec.Code != c.code {
			t.Errorf("%s body: %d, want %d: %s", c.name, rec.Code, c.code, rec.Body)
		}
	}
	if got := get(t, a, b.BillID); got.Status != "OPEN" {
		t.Errorf("status %s after a refused void", got.Status)
	}
}

func TestBillPaymentRoutes404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPost, payPath(id), payment(1)), http.StatusNotFound, "bill_not_found", "bill not found", "", "")
	wantProblem(t, a.Do(t, http.MethodPost, voidPath(id), map[string]any{}), http.StatusNotFound, "bill_not_found", "bill not found", "", "")
}
