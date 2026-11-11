package bills_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Go's own (PORT-NOTES 165): a bill payment is stored, posted Dr 2000 / Cr
// 1000 at the bill's issue basis with realised FX, listed and reversible;
// void is refused while a bill has payments; PATCH to PAID records one.

func paymentsPath(id uuid.UUID) string { return billPath(id) + "/payments" }

func reversePath(id, pid uuid.UUID) string {
	return paymentsPath(id) + "/" + pid.String() + "/reverse"
}

func listPayments(t *testing.T, a *apitest.App, id uuid.UUID) []bills.BillPaymentOut {
	t.Helper()
	return apitest.Decode[[]bills.BillPaymentOut](t, a.Do(t, http.MethodGet, paymentsPath(id), nil), http.StatusOK)
}

func reverse(t *testing.T, a *apitest.App, id, pid uuid.UUID, reason string) bills.BillPaymentOut {
	t.Helper()
	return apitest.Decode[bills.BillPaymentOut](t, a.Do(t, http.MethodPost, reversePath(id, pid), map[string]any{"reason": reason}), http.StatusOK)
}

// payEntries are the BILL_PAYMENT entries of bill payment pid, as billEntries.
func payEntries(t *testing.T, a *apitest.App, pid uuid.UUID) []bEntry {
	t.Helper()
	return billEntriesOf(t, a, "BILL_PAYMENT", pid)
}

// ledgerNet is Σ base debit − base credit on code over every entry of bill
// id and of its payments.
func ledgerNet(t *testing.T, a *apitest.App, id uuid.UUID, code string) string {
	t.Helper()
	var s string
	if err := a.Pool.QueryRow(t.Context(), `SELECT COALESCE(sum(l.base_amount_debit - l.base_amount_credit), 0)::numeric(19,4)::text
FROM journal_lines l JOIN journal_entries e ON e.entry_id = l.entry_id JOIN chart_of_accounts c ON c.account_id = l.account_id
WHERE c.code = $2 AND ((e.source_type = 'BILL' AND e.source_id = $1)
   OR (e.source_type = 'BILL_PAYMENT' AND e.source_id IN (SELECT bill_payment_id FROM bill_payments WHERE bill_id = $1)))`, id, code).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecordBillPaymentStoresAndPosts(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	got := apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, payPath(b.BillID), map[string]any{
		"amount": 400, "payment_date": "2026-01-20", "payer_name": "Ops account", "payer_reference": "TRX-9", "notes": "bank",
	}), http.StatusOK)
	if got.Status != "PARTIAL" {
		t.Errorf("status %s", got.Status)
	}
	wantDec4(t, "balance_due", p4(got.BalanceDue), "600")
	ps := listPayments(t, a, b.BillID)
	if len(ps) != 1 {
		t.Fatalf("payments %+v", ps)
	}
	p := ps[0]
	if p.BillID != b.BillID || p.Currency != "IDR" || p.PaymentDate.Format(time.DateOnly) != "2026-01-20" ||
		deref(p.PayerName) != "Ops account" || deref(p.Reference) != "TRX-9" || deref(p.Notes) != "bank" ||
		p.Status != "CLEARED" || p.Source != "MANUAL" || p.ReversedAt != nil || p.ReversalReason != nil || p.CreatedAt.IsZero() {
		t.Errorf("payment %+v", p)
	}
	wantDec4(t, "amount", p4(p.Amount), "400")
	es := payEntries(t, a, p.BillPaymentID)
	if len(es) != 1 {
		t.Fatalf("entries %+v", es)
	}
	if es[0].memo != "Payment "+p.BillPaymentID.String()+" of bill "+b.BillID.String() {
		t.Errorf("memo %q", es[0].memo)
	}
	want := map[string]string{"2000": "400.0000/0.0000 400.0000/0.0000", "1000": "0.0000/400.0000 0.0000/400.0000"}
	if len(es[0].lines) != len(want) {
		t.Errorf("lines %v", es[0].lines)
	}
	for code, w := range want {
		if es[0].lines[code] != w {
			t.Errorf("%s line %q, want %q", code, es[0].lines[code], w)
		}
	}
	// The second payment clears the payable exactly.
	pay(t, a, b.BillID, 600)
	if n := ledgerNet(t, a, b.BillID, "2000"); n != "0.0000" {
		t.Errorf("net 2000 %s, want 0", n)
	}
	if n := ledgerNet(t, a, b.BillID, "1000"); n != "-1000.0000" {
		t.Errorf("net 1000 %s, want -1000", n)
	}
	// An unknown bill has no payments to list.
	wantProblem(t, a.Do(t, http.MethodGet, paymentsPath(uuid.New()), nil), http.StatusNotFound, "bill_not_found", "bill not found", "", "")
}

// A USD bill opened at 15000 and paid at 16000 books a loss; paid at 14000, a
// gain.
func TestUSDBillPaymentBooksFX(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, on, code, line string }{
		{"loss", "2025-02-01", "5900", "100000.0000/0.0000 100000.0000/0.0000"},
		{"gain", "2025-03-01", "4900", "0.0000/100000.0000 0.0000/100000.0000"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			exec(t, a, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES ('USD', 'IDR', 15000, '2025-01-01', 'TEST'), ('USD', 'IDR', 16000, '2025-02-01', 'TEST'), ('USD', 'IDR', 14000, '2025-03-01', 'TEST')`)
			out := post(t, a, body(insertVendor(t, a), map[string]any{"currency": "USD", "issue_date": "2025-01-01", "line_items": []any{line(1, 100)}}))
			patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
			apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, payPath(out.BillID), map[string]any{"amount": 100, "payment_date": c.on}), http.StatusOK)
			ps := listPayments(t, a, out.BillID)
			es := payEntries(t, a, ps[0].BillPaymentID)
			if len(es) != 1 || es[0].lines[c.code] != c.line || es[0].lines["2000"] != "100.0000/0.0000 1500000.0000/0.0000" {
				t.Errorf("entries %+v", es)
			}
			if n := ledgerNet(t, a, out.BillID, "2000"); n != "0.0000" {
				t.Errorf("net 2000 %s", n)
			}
		})
	}
}

func TestReverseBillPayment(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	pay(t, a, b.BillID, 400)
	if got := pay(t, a, b.BillID, 600); got.Status != "PAID" {
		t.Fatalf("status %s", got.Status)
	}
	ps := listPayments(t, a, b.BillID)
	if len(ps) != 2 {
		t.Fatalf("payments %+v", ps)
	}
	first, second := ps[0], ps[1]
	if !first.Amount.Decimal().Equal(dec4("400").Decimal()) {
		first, second = second, first
	}

	got := reverse(t, a, b.BillID, second.BillPaymentID, "wrong amount")
	if got.Status != "REVERSED" || got.ReversedAt == nil || deref(got.ReversalReason) != "wrong amount" {
		t.Errorf("reversed %+v", got)
	}
	es := payEntries(t, a, second.BillPaymentID)
	if len(es) != 2 || !find(t, es, "Payment "+second.BillPaymentID.String()+" of bill "+b.BillID.String()).reversed {
		t.Fatalf("entries %+v", es)
	}
	if r := find(t, es, "Reversal of payment on bill "+b.BillID.String()+": wrong amount"); r.lines["1000"] != "600.0000/0.0000 600.0000/0.0000" {
		t.Errorf("reversal 1000 line %q", r.lines["1000"])
	}
	stored := get(t, a, b.BillID)
	if stored.Status != "PARTIAL" {
		t.Errorf("status %s, want PARTIAL", stored.Status)
	}
	wantDec4(t, "balance_due", p4(stored.BalanceDue), "600")

	reverse(t, a, b.BillID, first.BillPaymentID, "bounced")
	stored = get(t, a, b.BillID)
	if stored.Status != "OPEN" {
		t.Errorf("status %s, want OPEN", stored.Status)
	}
	wantDec4(t, "balance_due", p4(stored.BalanceDue), "1000")
	// The ledger is back to the bill alone: AP at its amount, the bank untouched.
	if n := ledgerNet(t, a, b.BillID, "2000"); n != "-1000.0000" {
		t.Errorf("net 2000 %s, want -1000", n)
	}
	if n := ledgerNet(t, a, b.BillID, "1000"); n != "0.0000" {
		t.Errorf("net 1000 %s, want 0", n)
	}

	before := countRows(t, a, "journal_entries")
	wantProblem(t, a.Do(t, http.MethodPost, reversePath(b.BillID, first.BillPaymentID), map[string]any{"reason": "again"}),
		http.StatusBadRequest, "bill_payment_not_reversible", "bill payment is REVERSED and cannot be reversed", "", "")
	wantJournalRows(t, a, before)
	if p := listPayments(t, a, b.BillID); len(p) != 2 || p[0].Status != "REVERSED" || p[1].Status != "REVERSED" {
		t.Errorf("payments %+v", p)
	}

	// Unknown ids, another bill's payment, and a missing or empty reason.
	other := openBill(t, a, "OPEN", nil)
	wantProblem(t, a.Do(t, http.MethodPost, reversePath(other.BillID, first.BillPaymentID), map[string]any{"reason": "x"}),
		http.StatusNotFound, "bill_payment_not_found", "bill payment not found", "", "")
	wantProblem(t, a.Do(t, http.MethodPost, reversePath(b.BillID, uuid.New()), map[string]any{"reason": "x"}),
		http.StatusNotFound, "bill_payment_not_found", "bill payment not found", "", "")
	wantProblem(t, a.Do(t, http.MethodPost, reversePath(uuid.New(), first.BillPaymentID), map[string]any{"reason": "x"}),
		http.StatusNotFound, "bill_not_found", "bill not found", "", "")
	for _, body := range []any{map[string]any{"reason": ""}, map[string]any{}} {
		if rec := a.Do(t, http.MethodPost, reversePath(b.BillID, first.BillPaymentID), body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("reason %v: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestVoidBillWithPaymentRefused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", map[string]any{"notes": "keep"})
	pay(t, a, b.BillID, 400)
	before := countRows(t, a, "journal_entries")
	wantProblem(t, a.Do(t, http.MethodPost, voidPath(b.BillID), map[string]any{"reason": "x"}), http.StatusBadRequest,
		"bill_has_payments", "cannot void a bill with payments — reverse them first", "", "")
	stored := get(t, a, b.BillID)
	if stored.Status != "PARTIAL" || deref(stored.Notes) != "keep" || stored.BalanceDue.Decimal().String() != "600" {
		t.Errorf("stored %s %q %s", stored.Status, deref(stored.Notes), stored.BalanceDue.Decimal())
	}
	wantJournalRows(t, a, before)
	if n := activeEntries(t, a, b.BillID); n != 1 {
		t.Errorf("%d active bill entries, want 1", n)
	}

	reverse(t, a, b.BillID, listPayments(t, a, b.BillID)[0].BillPaymentID, "wrong bill")
	if got := void(t, a, b.BillID, map[string]any{"reason": "x"}); got.Status != "VOID" {
		t.Errorf("status %s", got.Status)
	}
	for _, code := range []string{"2000", "1000", "6000"} {
		if n := ledgerNet(t, a, b.BillID, code); n != "0.0000" {
			t.Errorf("net %s %s, want 0", code, n)
		}
	}
}

// today is the database's CURRENT_DATE.
func today(t *testing.T, a *apitest.App) string {
	t.Helper()
	var d string
	if err := a.Pool.QueryRow(t.Context(), `SELECT CURRENT_DATE::text`).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPatchToPaidRecordsPayment(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		from     string
		paid     string // paid by record-payment first
		recorded string // the STATUS_UPDATE payment
	}{
		{"OPEN", "", "1000"},
		{"PARTIAL", "250", "750"},
		{"DRAFT", "", "1000"},
	} {
		t.Run(c.from, func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			status := c.from
			if status == "PARTIAL" {
				status = "OPEN" // made PARTIAL by the payment below
			}
			b := openBill(t, a, status, nil)
			if c.paid != "" {
				pay(t, a, b.BillID, c.paid)
			}
			got := patch(t, a, b.BillID, map[string]any{"status": "PAID", "notes": "settled"})
			if got.Status != "PAID" || deref(got.Notes) != "settled" {
				t.Errorf("status %s notes %q", got.Status, deref(got.Notes))
			}
			wantDec4(t, "balance_due", p4(got.BalanceDue), "0")
			ps := listPayments(t, a, b.BillID)
			p := ps[len(ps)-1]
			if p.Source != "STATUS_UPDATE" || p.Status != "CLEARED" || deref(p.Notes) != "status set to PAID via update" ||
				p.PaymentDate.Format(time.DateOnly) != today(t, a) || p.PayerName != nil || p.Reference != nil {
				t.Errorf("payment %+v", p)
			}
			wantDec4(t, "amount", p4(p.Amount), c.recorded)
			if n := activeEntries(t, a, b.BillID); n != 1 {
				t.Errorf("%d active bill entries, want 1 (a DRAFT is opened first)", n)
			}
			if es := payEntries(t, a, p.BillPaymentID); len(es) != 1 {
				t.Errorf("payment entries %+v", es)
			}
			if n := ledgerNet(t, a, b.BillID, "2000"); n != "0.0000" {
				t.Errorf("net 2000 %s, want 0", n)
			}
		})
	}
	t.Run("bill of 0", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		out := post(t, a, map[string]any{"vendor_id": insertVendor(t, a), "currency": "IDR"})
		if got := patch(t, a, out.BillID, map[string]any{"status": "PAID"}); got.Status != "PAID" {
			t.Errorf("status %s", got.Status)
		}
		if ps := listPayments(t, a, out.BillID); len(ps) != 0 {
			t.Errorf("payments %+v", ps)
		}
		wantJournalRows(t, a, 0)
	})
	t.Run("PARTIAL other edits refused", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		b := openBill(t, a, "OPEN", nil)
		pay(t, a, b.BillID, 250)
		// A PARTIAL bill sent to PAID with a change to what it posted is
		// refused: it has payments.
		wantProblem(t, a.Do(t, http.MethodPatch, billPath(b.BillID), map[string]any{"status": "PAID", "line_items": []any{line(2, 1000)}}),
			http.StatusBadRequest, "bill_has_payments", "cannot re-post a bill with payments — reverse them first", "", "")
		if got := get(t, a, b.BillID); got.Status != "PARTIAL" || len(got.LineItems) != 1 {
			t.Errorf("stored %s %d lines", got.Status, len(got.LineItems))
		}
	})
}

// An OPEN bill cannot normally hold a payment that is not reversed (a
// payment makes it PARTIAL or PAID); should one, an edit that re-posts and a
// return to DRAFT are refused rather than unposting under the payment.
func TestOpenEditWithPaymentRefused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	b := openBill(t, a, "OPEN", nil)
	pay(t, a, b.BillID, 400)
	setStatus(t, a, b.BillID, "OPEN")
	before := countRows(t, a, "journal_entries")
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(b.BillID), map[string]any{"line_items": []any{line(2, 1000)}}),
		http.StatusBadRequest, "bill_has_payments", "cannot re-post a bill with payments — reverse them first", "", "")
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(b.BillID), map[string]any{"status": "DRAFT"}),
		http.StatusBadRequest, "bill_has_payments", "cannot return a bill with payments to draft — reverse them first", "", "")
	wantJournalRows(t, a, before)
	stored := get(t, a, b.BillID)
	if stored.Status != "OPEN" || stored.BalanceDue.Decimal().String() != "600" || len(stored.LineItems) != 1 {
		t.Errorf("stored %s %s %d lines", stored.Status, stored.BalanceDue.Decimal(), len(stored.LineItems))
	}
	// An edit that changes nothing posted is not a re-post, and is allowed.
	patch(t, a, b.BillID, map[string]any{"line_items": []any{line(1, 1000)}, "notes": "same"})
	wantJournalRows(t, a, before)
	wantDec4(t, "balance_due", p4(get(t, a, b.BillID).BalanceDue), "600")
}

// An edit re-posts only when the amount, currency, issue day or expense
// split actually changed.
func TestOpenEditRepostsOnlyOnChange(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), map[string]any{"issue_date": "2026-01-10"}))
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	for _, same := range []map[string]any{
		// Python's oddity 6: a discount-only PATCH does not reprice.
		{"discount_type": "AMOUNT", "discount_value": 100},
		{"discount_type": nil, "discount_value": nil},
		{"line_items": []any{map[string]any{"description": "renamed", "quantity": 1, "unit_price": 1000}}},
		{"line_items": []any{line(1, 400), line(1, 600)}},
		{"currency": "idr", "issue_date": "2026-01-10"},
	} {
		patch(t, a, out.BillID, same)
		if es := billEntries(t, a, out.BillID); len(es) != 1 {
			t.Fatalf("after %v: %d entries, want 1", same, len(es))
		}
	}
	// The split moves to 5100: a re-post of the same amount.
	patch(t, a, out.BillID, map[string]any{"line_items": []any{accountLine(accountID(t, a, "5100"), 1, 1000)}})
	if es := billEntries(t, a, out.BillID); len(es) != 3 {
		t.Fatalf("%d entries, want 3", len(es))
	}
	wantD(t, "net 5100", netBase(t, a, out.BillID, "5100"), "1000")
	wantD(t, "net 6000", netBase(t, a, out.BillID, "6000"), "0")
	// A null issue date on a bill created that day is the same issue day.
	exec(t, a, `UPDATE bills SET created_at = '2026-01-10 08:00:00' WHERE bill_id = $1`, out.BillID)
	patch(t, a, out.BillID, map[string]any{"issue_date": nil})
	if es := billEntries(t, a, out.BillID); len(es) != 3 {
		t.Fatalf("%d entries after nulling the issue date, want 3", len(es))
	}
}

// Every bill entry's memo names the bill by its number, else its id.
func TestBillMemosUseNumber(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), map[string]any{"bill_number": "B-9"}))
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	patch(t, a, out.BillID, map[string]any{"line_items": []any{line(2, 1000)}})
	patch(t, a, out.BillID, map[string]any{"status": "DRAFT"})
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	pay(t, a, out.BillID, 500)
	pid := listPayments(t, a, out.BillID)[0].BillPaymentID
	reverse(t, a, out.BillID, pid, "typo")
	void(t, a, out.BillID, map[string]any{})
	es := billEntries(t, a, out.BillID)
	for _, memo := range []string{"Bill B-9 opened", "Edit of bill B-9", "Bill B-9 returned to draft", "Void of bill B-9"} {
		find(t, es, memo)
	}
	ps := payEntries(t, a, pid)
	find(t, ps, "Payment "+pid.String()+" of bill B-9")
	find(t, ps, "Reversal of payment on bill B-9: typo")
}

// An edit that re-posts and a void at once: the second waits on the bill's
// row lock and acts on what the first committed, so the bill ends with no
// active entry (voided after the re-post) or is refused (edited after the
// void), never with two.
func TestConcurrentEditAndVoid(t *testing.T) {
	t.Parallel()
	edit := bills.BillUpdate{LineItems: &[]bills.BillLineItemIn{{Description: "x", Quantity: dec4("3"), UnitPrice: dec4("1000")}}}
	run := map[string]func(context.Context, pgx.Tx, uuid.UUID) error{
		"edit": func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
			_, err := bills.UpdateBill(ctx, tx, base, id, edit)
			return err
		},
		"void": func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
			_, err := bills.UpdateBill(ctx, tx, base, id, bills.BillUpdate{Status: platform.NotNull[string]{Set: true, Value: "VOID"}})
			return err
		},
	}
	for _, order := range [][2]string{{"edit", "void"}, {"void", "edit"}} {
		t.Run(order[0]+" first", func(t *testing.T) {
			t.Parallel()
			a := newApp(t)
			out := openBill(t, a, "OPEN", nil)
			ctx := context.Background()
			first, err := a.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = first.Rollback(ctx) }()
			if err := run[order[0]](ctx, first, out.BillID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- db.InTx(ctx, a.Pool, func(tx pgx.Tx) error { return run[order[1]](ctx, tx, out.BillID) })
			}()
			if !waitForLockWaits(t, a.Pool, 1) {
				t.Fatal("the second never waited on a lock")
			}
			if err := first.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if order[0] == "void" && !errors.Is(err, bills.ErrBilling) {
				t.Errorf("edit after void: %v, want a refusal", err)
			}
			if order[0] == "edit" && err != nil {
				t.Errorf("void after edit: %v", err)
			}
			if got := get(t, a, out.BillID); got.Status != "VOID" {
				t.Errorf("status %s", got.Status)
			}
			if n := activeEntries(t, a, out.BillID); n != 0 {
				t.Errorf("%d active entries, want 0", n)
			}
			wantD(t, "net 2000", netBase(t, a, out.BillID, "2000"), "0")
		})
	}
}

func TestBillPaymentRoutesAdminOnly(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	staffID := uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID)
	token, _, err := a.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a
	staff.Cookie = platform.SessionCookie + "=" + token
	id, pid := uuid.New(), uuid.New()
	for _, c := range []struct{ method, p string }{
		{http.MethodGet, paymentsPath(id)},
		{http.MethodPost, reversePath(id, pid)},
	} {
		wantProblem(t, a.Anon(t, c.method, c.p, map[string]any{"reason": "x"}), http.StatusUnauthorized, "", "not authenticated", "", "")
		wantProblem(t, staff.Do(t, c.method, c.p, map[string]any{"reason": "x"}), http.StatusForbidden, "", "admin required", "", "")
	}
}
