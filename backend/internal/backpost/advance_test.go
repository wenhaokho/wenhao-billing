package backpost

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Customer advances (controller ruling 2026-11-11, PORT-NOTES 166): Python's
// approve_manual_match does not check the status, so a PAID invoice could be
// paid again. Its receivable ends below 0 while balance_due is 0. The
// back-post keeps the negative receivable (the customer's credit) and lists
// it for the owner to refund or apply; it does not refuse the run.

// customer sets inv's customer to a new one named name.
func (e *env) customer(t *testing.T, inv uuid.UUID, name string) {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO customers (customer_id, name) VALUES ($1, $2)`, id, name)
	e.exec(t, `UPDATE invoices SET customer_id = $1 WHERE invoice_id = $2`, id, inv)
}

func TestBackpostKeepsOverpaidInvoicesAsAdvances(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// USD, issued at 15000: Python cleared 1000 and then 200 more, both at
	// 16000. Restated, the 1000 relieves the whole receivable at the issue
	// basis and the 200 relieves below 0 at the payment date's rate.
	usd := e.invoice(t, doc{number: "INV-OVER-USD", currency: "USD", status: "PAID", amount: "1000", balance: "0", issue: issued2025})
	e.customer(t, usd, "Acme Pte")
	e.pythonPayment(t, usd, "USD", "1000", paid2026, "16000")
	e.pythonPayment(t, usd, "USD", "200", paid2026, "16000")
	// IDR: Python's cleared payment kept, a 200000 manual payment (no entry)
	// posted by the run below 0.
	idr := e.invoice(t, doc{number: "INV-OVER-IDR", currency: "IDR", status: "PAID", amount: "1000000", balance: "0", issue: paid2026})
	e.customer(t, idr, "Beta CV")
	e.pythonPayment(t, idr, "IDR", "1000000", paid2026, "1")
	e.pythonPayment(t, idr, "IDR", "200000", paid2026, "")
	before := e.rows(t)

	rep, out := e.mustRun(t, true)
	if rep.Invoices != 2 || rep.PaymentsRestated != 2 || rep.PaymentsPosted != 1 || len(rep.Inconsistent) != 0 {
		t.Errorf("report = %+v", rep)
	}
	// The normal back-post only: two issue entries, two reversals of
	// Python's USD entries, two restated USD payments, one IDR manual payment.
	if after := e.rows(t); after[0] != before[0]+7 || after[2] != before[2] {
		t.Errorf("rows (entries, lines, bill payments) = %v, want %d entries from %v", after, before[0]+7, before)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM journal_entries WHERE source_type NOT IN ('INVOICE', 'PAYMENT')`); n != 0 {
		t.Errorf("entries of other sources = %d, want 0", n)
	}
	b := e.invoiceBasis(t, usd)
	eq(t, "USD AR face", b.Face, "-200")
	eq(t, "USD AR base", b.Base, "-3200000")
	b = e.invoiceBasis(t, idr)
	eq(t, "IDR AR face", b.Face, "-200000")
	eq(t, "IDR AR base", b.Base, "-200000")
	e.trialBalanced(t)

	checkAdvances := func(rep Report, out string) {
		t.Helper()
		if len(rep.Advances) != 2 {
			t.Fatalf("advances = %+v, want 2", rep.Advances)
		}
		got := map[uuid.UUID]Advance{}
		for _, a := range rep.Advances {
			got[a.InvoiceID] = a
		}
		if a := got[usd]; a.Customer != "Acme Pte" || a.Currency != "USD" || a.Label != "INV-OVER-USD" {
			t.Errorf("USD advance = %+v", a)
		}
		eq(t, "USD advance face", got[usd].Face, "-200")
		eq(t, "USD advance base", got[usd].Base, "-3200000")
		if a := got[idr]; a.Customer != "Beta CV" || a.Currency != "IDR" || a.Label != "INV-OVER-IDR" {
			t.Errorf("IDR advance = %+v", a)
		}
		eq(t, "IDR advance face", got[idr].Face, "-200000")
		eq(t, "IDR advance base", got[idr].Base, "-200000")
		for _, w := range []string{
			"customer advances (overpaid invoices): 2",
			"Customer advances (overpaid invoices) — owner to refund or apply:",
			"INVOICE INV-OVER-USD customer Acme Pte: overpaid USD 200.0000 (base IDR 3200000.0000)",
			"INVOICE INV-OVER-IDR customer Beta CV: overpaid IDR 200000.0000 (base IDR 200000.0000)",
		} {
			if !strings.Contains(out, w) {
				t.Errorf("output lacks %q:\n%s", w, out)
			}
		}
		if strings.Contains(out, "ledger face") {
			t.Errorf("an advance is reported as a face mismatch:\n%s", out)
		}
	}
	checkAdvances(rep, out)

	// Idempotent: a second run posts nothing and lists the same advances.
	posted := e.rows(t)
	rep, out = e.mustRun(t, true)
	if rep.Invoices != 0 || rep.PaymentsRestated != 0 || rep.PaymentsPosted != 0 {
		t.Errorf("second run report = %+v", rep)
	}
	if after := e.rows(t); after != posted {
		t.Errorf("second run rows = %v, want %v", after, posted)
	}
	checkAdvances(rep, out)
	e.trialBalanced(t)
}

// Only a PAID invoice is an advance: a PARTIAL one whose receivable the run
// leaves apart from its balance_due (here below 0 with nothing due) is still
// refused, nothing written.
func TestBackpostRefusesPartialOverpaid(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inv := e.invoice(t, doc{number: "INV-PART-OVER", currency: "IDR", status: "PARTIAL", amount: "1000000", balance: "0", issue: paid2026})
	e.pythonPayment(t, inv, "IDR", "1200000", paid2026, "")
	before := e.rows(t)

	rep, out, err := e.run(t, true)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused\n%s", err, out)
	}
	want := "invoice " + inv.String() + " INV-PART-OVER IDR: ledger face -200000.0000 ≠ balance_due 0.0000"
	if len(rep.Inconsistent) != 1 || !strings.Contains(out, want) {
		t.Errorf("inconsistent = %q, want %q\n%s", rep.Inconsistent, want, out)
	}
	if len(rep.Advances) != 0 || strings.Contains(out, "Customer advances") {
		t.Errorf("a refused PARTIAL invoice is listed as an advance:\n%s", out)
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows = %v, want %v", after, before)
	}
}
