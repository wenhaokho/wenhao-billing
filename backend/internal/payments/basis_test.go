package payments

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

// Go's own (PORT-NOTES 163): payments relieve the receivable at the
// invoice's issue basis, and the receivable on the ledger tracks
// balance_due.

// basis is the invoice's receivable on the ledger.
func (e *env) basis(t *testing.T, id uuid.UUID) ledger.Basis {
	t.Helper()
	b, err := inTx(t, e, func(tx pgx.Tx) (ledger.Basis, error) { return ledger.InvoiceBasis(context.Background(), tx, id) })
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// An exact match against an invoice with no issue entry is held, never
// cleared: the ledger's refusal is among its reasons and nothing posts.
func TestIntakeAgainstUnissuedInvoiceHeld(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.customer(t, "Acme Corp", "Acme")
	invID := e.invoice(t, inv{
		customer: c, currency: "USD", status: "SENT", balance: "1000.0000",
		issue: day(2026, 4, 10), due: day(2026, 4, 24), unissued: true,
	})
	p := e.incoming(t, payload())
	if p.Status != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s, want PENDING_MANUAL_REVIEW", p.Status)
	}
	logs := e.logs(t, p.PaymentID)
	sentence := "invoice " + invID.String() + " has no issue entry — run api backpost-ledger"
	if len(logs) != 1 || logs[0].action != "HELD" || !slices.Contains(logs[0].reasons, sentence) {
		t.Errorf("logs = %+v, want one HELD with %q", logs, sentence)
	}
	if n := e.count(t, `SELECT count(*) FROM journal_entries`); n != 0 {
		t.Errorf("%d journal entries, want 0", n)
	}
	checkInvoice(t, e, invID, "1000.0000", "0", "SENT")
}

// Reversing a payment puts its relief back on the receivable, so paying
// again books the same realised FX.
func TestReverseRestoresBasis(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16500, '2026-04-01', 'TEST')`)
	c := a.customer(t, "Acme Corp", "Acme")
	id := a.invoice(t, inv{customer: c, currency: "USD", status: "SENT", balance: "1000", issue: day(2026, 1, 10)})
	issued := a.basis(t, id)
	if !issued.Issued || !issued.Face.Equal(dec("1000")) || !issued.Base.Equal(dec("16000000")) {
		t.Fatalf("issue basis %+v", issued)
	}

	first := a.mustRecord(t, id, map[string]any{"amount": "400"})
	firstGain := active(t, a.env, first.PaymentID).line("4900")
	if b := a.basis(t, id); !b.Face.Equal(dec("600")) || !b.Base.Equal(dec("9600000")) {
		t.Errorf("after paying 400: %+v", b)
	}
	a.mustReverse(t, first.PaymentID, "bounced")
	if b := a.basis(t, id); !b.Face.Equal(issued.Face) || !b.Base.Equal(issued.Base) {
		t.Errorf("after the reversal: %+v, want %+v", b, issued)
	}

	again := a.mustRecord(t, id, map[string]any{"amount": "400"})
	gain := active(t, a.env, again.PaymentID).line("4900")
	// 400 at 16500 less 400 at 16000.
	if firstGain == nil || gain == nil || !gain.baseCredit.Equal(dec("200000")) || !gain.baseCredit.Equal(firstGain.baseCredit) {
		t.Errorf("FX gain again %+v, first %+v; want 200000 both", gain, firstGain)
	}
}

// The receivable on the ledger is balance_due after every step: finalize,
// a partial payment, its reversal, the rest in cash, then the PPh slip.
func TestARLedgerMatchesBalanceDue(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		currency, amount, wht, partial, cash string
	}{
		{"IDR", "1020000", "20000", "400000", "1000000"},
		{"USD", "1020", "20", "400", "1000"},
	} {
		t.Run(c.currency, func(t *testing.T) {
			t.Parallel()
			a := newAPI(t, NoHosting{})
			a.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16500, '2026-04-01', 'TEST')`)
			cust := a.customer(t, "Acme Corp", "Acme")
			id := a.invoice(t, inv{customer: cust, currency: c.currency, status: "DRAFT", balance: c.amount, wht: c.wht, issue: day(2026, 1, 10)})
			step := func(name string) {
				t.Helper()
				b, s := a.basis(t, id), a.invoiceState(t, id)
				if !b.Issued || !b.Face.Equal(s.balance) {
					t.Errorf("%s: ledger receivable %s (issued %v), balance_due %s", name, b.Face, b.Issued, s.balance)
				}
			}
			if _, err := inTx(t, a.env, func(tx pgx.Tx) (invoices.InvoiceOut, error) {
				return invoices.FinalizeInvoice(context.Background(), tx, "IDR", id)
			}); err != nil {
				t.Fatal(err)
			}
			step("finalize")
			partial := a.mustRecord(t, id, map[string]any{"amount": c.partial})
			step("partial")
			a.mustReverse(t, partial.PaymentID, "wrong invoice")
			step("reverse")
			a.mustRecord(t, id, map[string]any{"amount": c.cash})
			step("cash")
			a.mustRecord(t, id, map[string]any{"amount": "0", "wht_amount": c.wht})
			step("withholding slip")
			if b := a.basis(t, id); !b.Face.IsZero() || !b.Base.IsZero() {
				t.Errorf("settled receivable %+v, want 0 and 0", b)
			}
			checkInvoice(t, a.env, id, "0", c.wht, "PAID")
		})
	}
}

// A payment cleared against a PARTIAL invoice that Python then voided (Go
// refuses that void): reversing it reverses the entry but leaves the
// invoice VOID with nothing owing.
func TestReverseAgainstVoidInvoiceKeepsItVoid(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
	p := a.mustRecord(t, id, map[string]any{"amount": "400"})
	a.exec(t, `UPDATE invoices SET status = 'VOID', balance_due = 0 WHERE invoice_id = $1`, id)

	got := a.mustReverse(t, p.PaymentID, "voided upstream")
	if got.Status != "FLAGGED" {
		t.Errorf("payment %s, want FLAGGED", got.Status)
	}
	es := a.entries(t, p.PaymentID)
	if len(es) != 2 || (es[0].reversedBy == nil && es[1].reversedBy == nil) {
		t.Errorf("entries %+v, want the payment's entry and its reversal", es)
	}
	checkInvoice(t, a.env, id, "0", "0", "VOID")
}

// PORT-NOTES 7 and 168: a cross-currency payment settles the whole balance;
// Python's reversal added the payment-currency amount back to the
// invoice-currency balance (16,000,000 on a USD 1000 invoice). Go takes the
// balance from the receivable the ledger still holds, so the invoice owes
// its USD 1000 again and a second cross-currency payment settles it at the
// issue basis with no false FX loss.
func TestCrossCurrencyReversalRestoresLedgerBalance(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	invID := a.sentInvoice(t)
	first := a.incoming(t, intakeIDR("16000000"))
	a.mustApprove(t, first.PaymentID, invID)
	checkInvoice(t, a.env, invID, "0", "0", "PAID")
	a.mustReverse(t, first.PaymentID, "bounced")
	checkInvoice(t, a.env, invID, "1000", "0", "OPEN")
	if b := a.basis(t, invID); !b.Face.Equal(dec("1000")) || !b.Base.Equal(dec("16000000")) {
		t.Fatalf("basis after the reversal %+v, want 1000 / 16000000", b)
	}

	second := a.incoming(t, intakeIDR("16000000"))
	if second.Status != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("second payment %s, want PENDING_MANUAL_REVIEW", second.Status)
	}
	if got := a.mustApprove(t, second.PaymentID, invID); got.Status != "CLEARED" {
		t.Errorf("second payment %s, want CLEARED", got.Status)
	}
	if n := a.count(t, `SELECT count(*) FROM journal_lines l JOIN chart_of_accounts c ON c.account_id = l.account_id WHERE c.code IN ('4900', '5900')`); n != 0 {
		t.Errorf("%d FX lines, want 0", n)
	}
	checkInvoice(t, a.env, invID, "0", "0", "PAID")
	if b := a.basis(t, invID); !b.Face.IsZero() || !b.Base.IsZero() {
		t.Errorf("settled receivable %+v, want 0 and 0", b)
	}
}

// PORT-NOTES 168: reversing one of two payments leaves the other's relief
// on the ledger, so the invoice is PARTIAL with what the ledger still holds;
// reversing the other too makes it OPEN at its amount.
func TestReverseOneOfTwoPaymentsLeavesPartial(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp", "Acme")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000", issue: day(2026, 1, 10)})
	first := a.mustRecord(t, id, map[string]any{"amount": "400"})
	second := a.mustRecord(t, id, map[string]any{"amount": "300"})
	checkInvoice(t, a.env, id, "300", "0", "PARTIAL")

	a.mustReverse(t, first.PaymentID, "bounced")
	checkInvoice(t, a.env, id, "700", "0", "PARTIAL")
	if b := a.basis(t, id); !b.Face.Equal(dec("700")) {
		t.Errorf("receivable %s, want 700", b.Face)
	}
	a.mustReverse(t, second.PaymentID, "bounced")
	checkInvoice(t, a.env, id, "1000", "0", "OPEN")
}

// PORT-NOTES 168: approval against a PAID invoice (Python's parity) relieves
// the receivable below 0; reversing it brings the receivable back to 0, so
// the invoice stays PAID owing nothing. Python's arithmetic re-opened it
// owing 500 the ledger did not hold, and a following intake of 500 cleared
// and booked the receivable at -500. That intake is held instead.
func TestReverseOverpaymentLeavesNoPhantomBalance(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	invID := a.sentInvoice(t)
	if p := a.incoming(t, payload()); p.Status != "CLEARED" {
		t.Fatalf("first payment %s, want CLEARED", p.Status)
	}
	checkInvoice(t, a.env, invID, "0", "0", "PAID")
	over := a.incoming(t, payload(amount("500")))
	if over.Status == "CLEARED" {
		t.Fatal("overpayment cleared, want held")
	}
	a.mustApprove(t, over.PaymentID, invID)
	if b := a.basis(t, invID); !b.Face.Equal(dec("-500")) {
		t.Fatalf("receivable after the overpayment %s, want -500", b.Face)
	}

	a.mustReverse(t, over.PaymentID, "duplicate")
	checkInvoice(t, a.env, invID, "0", "0", "PAID")
	if b := a.basis(t, invID); !b.Face.IsZero() {
		t.Errorf("receivable after the reversal %s, want 0", b.Face)
	}

	again := a.incoming(t, payload(amount("500")))
	if again.Status == "CLEARED" {
		t.Errorf("a later 500 cleared, want held")
	}
	if b := a.basis(t, invID); b.Face.IsNegative() {
		t.Errorf("receivable %s, want not below 0", b.Face)
	}
	checkInvoice(t, a.env, invID, "0", "0", "PAID")
}

// An invoice whose payment has an entry but which has no issue entry of its
// own (issued before accrual posting, not yet back-posted): the reversal is
// refused, since the ledger cannot say what is still owed.
func TestReverseAgainstUnissuedInvoiceRefused(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp", "Acme")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000", issue: day(2026, 1, 10)})
	p := a.mustRecord(t, id, map[string]any{"amount": "400"})
	a.exec(t, `DELETE FROM journal_lines WHERE entry_id IN (SELECT entry_id FROM journal_entries WHERE source_type = 'INVOICE' AND source_id = $1)`, id)
	a.exec(t, `DELETE FROM journal_entries WHERE source_type = 'INVOICE' AND source_id = $1`, id)

	_, err := inTx(t, a.env, func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return a.svc.Reverse(context.Background(), tx, p.PaymentID, nil, "bounced")
	})
	want := "invoice " + id.String() + " has no issue entry — run api backpost-ledger"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	checkInvoice(t, a.env, id, "600", "0", "PARTIAL")
}

// A receivable left above the invoice amount (the amount changed by hand
// after the issue entry) is no state the invoice can be in: the reversal is
// refused rather than guessing a balance.
func TestReverseWithReceivableAboveAmountRefused(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp", "Acme")
	id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000", issue: day(2026, 1, 10)})
	p := a.mustRecord(t, id, map[string]any{"amount": "400"})
	a.exec(t, `UPDATE invoices SET amount = 500, balance_due = 100 WHERE invoice_id = $1`, id)

	_, err := inTx(t, a.env, func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return a.svc.Reverse(context.Background(), tx, p.PaymentID, nil, "bounced")
	})
	want := "invoice " + id.String() + " ledger receivable 1000.0000 is above its amount 500.0000; correct the ledger first"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	checkInvoice(t, a.env, id, "100", "0", "PARTIAL")
}
