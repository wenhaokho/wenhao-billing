package backpost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Review round 1 (PORT-NOTES 166): restatement follows what Python actually
// relieved, in clearing order; inconsistent data and ledger refusals refuse
// the run with every case listed.

// jl is a journal line of a Python-era fixture entry: face debit or credit
// in ccy and its base debit or credit.
type jl struct {
	code, ccy        string
	dr, cr, bdr, bcr string
}

// rawPayment stores a payment row with status, created at on 12:00.
func (e *env) rawPayment(t *testing.T, inv uuid.UUID, currency, amount string, on time.Time, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, status, created_at)
VALUES ($1, $2, $3, $4, 'Acme', $5, 'EMAIL', $6, $7)`, id, inv, dec(amount), currency, on, status, on.Add(12*time.Hour))
	return id
}

// rawEntry stores a PAYMENT entry for payment posted at at, with lines.
func (e *env) rawEntry(t *testing.T, payment uuid.UUID, at time.Time, lines ...jl) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO journal_entries (entry_id, posted_at, source_type, source_id, memo) VALUES ($1, $2, 'PAYMENT', $3, 'python')`, id, at, payment)
	z := func(s string) decimal.Decimal {
		if s == "" {
			return decimal.Zero
		}
		return dec(s)
	}
	for _, l := range lines {
		e.exec(t, `INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency, base_amount_debit, base_amount_credit, fx_rate)
VALUES ($1, $2, (SELECT account_id FROM chart_of_accounts WHERE code = $3), $4, $5, $6, $7, $8, 1)`,
			uuid.New(), id, l.code, z(l.dr), z(l.cr), l.ccy, z(l.bdr), z(l.bcr))
	}
	return id
}

// reversedBy marks orig reversed by rev (Python's reverse_payment).
func (e *env) reversedBy(t *testing.T, orig, rev uuid.UUID) {
	t.Helper()
	e.exec(t, `UPDATE journal_entries SET reversed_by_entry_id = $2 WHERE entry_id = $1`, orig, rev)
}

// paymentBaseNet is Σ base credit − base debit on code over every PAYMENT
// entry of the invoice's payments.
func (e *env) paymentBaseNet(t *testing.T, inv uuid.UUID, code string) decimal.Decimal {
	t.Helper()
	var n decimal.Decimal
	if err := e.pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(l.base_amount_credit - l.base_amount_debit), 0)
FROM journal_lines l JOIN chart_of_accounts a ON a.account_id = l.account_id
JOIN journal_entries j ON j.entry_id = l.entry_id
WHERE a.code = $2 AND j.source_type = 'PAYMENT' AND j.source_id IN (SELECT payment_id FROM payments WHERE invoice_id = $1)`, inv, code).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// C1, the reviewer's probe: a USD 1000 invoice; P1 USD 300 dated 02-10 and
// cleared first; P2 IDR 11,000,000 dated 02-05 and approved later, when
// Python relieved the 700 USD left at 16000 (5900 200,000). Restated in
// clearing order with Python's reliefs, the receivable ends at 0 and the
// realised FX is a gain of 300,000 + 500,000.
func TestBackpostRestatesInClearingOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inv := e.invoice(t, doc{number: "INV-PROBE", currency: "USD", status: "PAID", amount: "1000", balance: "0", issue: issued2025})
	p1 := e.rawPayment(t, inv, "USD", "300", day(2026, 2, 10), "CLEARED")
	e.rawEntry(t, p1, day(2026, 2, 10).Add(10*time.Hour),
		jl{code: "1000", ccy: "USD", dr: "300", bdr: "4800000"},
		jl{code: "1100", ccy: "USD", cr: "300", bcr: "4800000"})
	p2 := e.rawPayment(t, inv, "IDR", "11000000", day(2026, 2, 5), "CLEARED")
	e.rawEntry(t, p2, day(2026, 2, 12).Add(10*time.Hour),
		jl{code: "1000", ccy: "IDR", dr: "11000000", bdr: "11000000"},
		jl{code: "5900", ccy: "IDR", dr: "200000", bdr: "200000"},
		jl{code: "1100", ccy: "USD", cr: "700", bcr: "11200000"})

	rep, out := e.mustRun(t, true)
	if rep.PaymentsRestated != 2 {
		t.Errorf("report = %+v", rep)
	}
	b := e.invoiceBasis(t, inv)
	eq(t, "AR face", b.Face, "0")
	eq(t, "AR base", b.Base, "0")
	eq(t, "4900 net credit on the payments", e.paymentBaseNet(t, inv, "4900"), "800000")
	eq(t, "5900 net credit on the payments", e.paymentBaseNet(t, inv, "5900"), "0")
	for p, want := range map[uuid.UUID]time.Time{p1: day(2026, 2, 10).Add(10 * time.Hour), p2: day(2026, 2, 12).Add(10 * time.Hour)} {
		entry, _ := e.activeEntry(t, "PAYMENT", p)
		if at := e.postedAt(t, entry); !at.Equal(want) {
			t.Errorf("restated %s posted_at = %s, want Python's %s", p, at, want)
		}
	}
	if strings.Contains(out, "inconsistent") {
		t.Errorf("unexpected refusal:\n%s", out)
	}
	e.trialBalanced(t)
}

// C1: a restated invoice whose ledger receivable would not end at its
// balance_due refuses the run (exit 1), listed, nothing written; an invoice
// with no payments posted by the run is only warned (TestBackpostWarnsOnFaceMismatch).
func TestBackpostRefusesRestatedFaceMismatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inv := e.invoice(t, doc{number: "INV-APART", currency: "USD", status: "PARTIAL", amount: "1000", balance: "500", issue: issued2025})
	e.pythonPayment(t, inv, "USD", "300", paid2026, "16000")
	manual := e.invoice(t, doc{number: "INV-APART-IDR", currency: "IDR", status: "PARTIAL", amount: "1000000", balance: "100000", issue: paid2026})
	e.pythonPayment(t, manual, "IDR", "300000", paid2026, "")
	before := e.rows(t)

	rep, out, err := e.run(t, true)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused\n%s", err, out)
	}
	want := []string{
		"invoice " + inv.String() + " INV-APART USD: ledger face 700.0000 ≠ balance_due 500.0000",
		"invoice " + manual.String() + " INV-APART-IDR IDR: ledger face 700000.0000 ≠ balance_due 100000.0000",
	}
	if len(rep.Inconsistent) != len(want) {
		t.Errorf("inconsistent = %q, want %q", rep.Inconsistent, want)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if !strings.Contains(out, "backpost-ledger refused: nothing written") || strings.Contains(out, " issued ") {
		t.Errorf("output lacks the refusal, or lists postings that were not kept:\n%s", out)
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows = %v, want %v", after, before)
	}
}

// M1: a payment whose PAYMENT entries do not net to its active entry (a
// reversal Python reversed again), or a non-CLEARED payment whose entries
// do not net to 0, refuses the run before anything is written.
func TestBackpostRefusesUnpairedReversals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fixtures(t)
	inv := e.invoice(t, doc{number: "INV-TWICE", currency: "IDR", status: "PAID", amount: "300", balance: "0", issue: paid2026})
	p := e.rawPayment(t, inv, "IDR", "300", paid2026, "CLEARED")
	clearing := func() []jl {
		return []jl{{code: "1000", ccy: "IDR", dr: "300", bdr: "300"}, {code: "1100", ccy: "IDR", cr: "300", bcr: "300"}}
	}
	undo := func() []jl {
		return []jl{{code: "1000", ccy: "IDR", cr: "300", bcr: "300"}, {code: "1100", ccy: "IDR", dr: "300", bdr: "300"}}
	}
	at := paid2026.Add(10 * time.Hour)
	e1 := e.rawEntry(t, p, at, clearing()...)
	e2 := e.rawEntry(t, p, at.Add(time.Hour), undo()...)
	e.reversedBy(t, e1, e2)
	e.rawEntry(t, p, at.Add(2*time.Hour), clearing()...)
	e4 := e.rawEntry(t, p, at.Add(3*time.Hour), clearing()...) // the reversal reversed
	e.reversedBy(t, e2, e4)

	flagged := e.invoice(t, doc{number: "INV-FLAG", currency: "IDR", status: "OPEN", amount: "500", issue: paid2026})
	f := e.rawPayment(t, flagged, "IDR", "200", paid2026, "FLAGGED")
	e.rawEntry(t, f, at, jl{code: "1000", ccy: "IDR", dr: "200", bdr: "200"}, jl{code: "1100", ccy: "IDR", cr: "200", bcr: "200"})
	before := e.rows(t)

	rep, out, err := e.run(t, false)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused\n%s", err, out)
	}
	want := []string{
		"PAYMENT " + p.String() + " of INVOICE INV-TWICE (CLEARED): its entries relieve 600.0000 IDR net but should relieve 300.0000 (its reversals do not pair)",
		"PAYMENT " + f.String() + " of INVOICE INV-FLAG (FLAGGED): its entries relieve 200.0000 IDR net but should relieve 0.0000 (its reversals do not pair)",
	}
	if len(rep.Inconsistent) != len(want) {
		t.Errorf("inconsistent = %q, want %q", rep.Inconsistent, want)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows = %v, want %v", after, before)
	}
}

// M2: every posting the ledger refuses is listed (each document under its
// own savepoint), not only the first; the run is refused and writes
// nothing. Here Python's cross-currency entries relieved more than each
// invoice's face.
func TestBackpostListsEveryLedgerRefusal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fixtures(t)
	var ids []uuid.UUID
	for _, n := range []string{"INV-OVER-1", "INV-OVER-2"} {
		inv := e.invoice(t, doc{number: n, currency: "USD", status: "PAID", amount: "100", balance: "0", issue: issued2025})
		p := e.rawPayment(t, inv, "IDR", "2400000", paid2026, "CLEARED")
		e.rawEntry(t, p, paid2026.Add(10*time.Hour),
			jl{code: "1000", ccy: "IDR", dr: "2400000", bdr: "2400000"},
			jl{code: "1100", ccy: "USD", cr: "150", bcr: "2400000"})
		ids = append(ids, inv)
	}
	before := e.rows(t)

	rep, out, err := e.run(t, false)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused\n%s", err, out)
	}
	if len(rep.Inconsistent) != 2 {
		t.Errorf("inconsistent = %q, want both invoices", rep.Inconsistent)
	}
	for i, n := range []string{"INV-OVER-1", "INV-OVER-2"} {
		w := "INVOICE " + n + ": payment "
		found := false
		for _, s := range rep.Inconsistent {
			if strings.HasPrefix(s, w) && strings.Contains(s, "would relieve 150.0000 USD but the ledger receivable is 100.0000 USD") &&
				strings.Contains(s, ids[i].String()) {
				found = true
			}
		}
		if !found {
			t.Errorf("no refusal listed for %s: %q", n, rep.Inconsistent)
		}
	}
	if after := e.rows(t); after != before {
		t.Errorf("rows = %v, want %v", after, before)
	}
}

// M3: a Python payment on the ledger before its invoice's issue date is
// warned (the restatement keeps Python's posted_at).
func TestBackpostWarnsPaymentBeforeIssue(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inv := e.invoice(t, doc{number: "INV-EARLY", currency: "USD", status: "PAID", amount: "100", balance: "0", issue: day(2026, 2, 10)})
	p := e.pythonPayment(t, inv, "USD", "100", paid2026, "16000")
	manual := e.invoice(t, doc{number: "INV-EARLY-M", currency: "IDR", status: "PAID", amount: "100", balance: "0", issue: day(2026, 2, 10)})
	m := e.pythonPayment(t, manual, "IDR", "100", paid2026, "")
	late := e.invoice(t, doc{number: "INV-LATE", currency: "USD", status: "PAID", amount: "100", balance: "0", issue: day(2026, 1, 20)})
	e.pythonPayment(t, late, "USD", "100", paid2026, "16000")

	rep, _ := e.mustRun(t, false)
	want := []string{
		"PAYMENT " + p.String() + " of INVOICE INV-EARLY is on the ledger on 2026-02-01, before the invoice's issue date 2026-02-10: the receivable is below 0 in between",
		"PAYMENT " + m.String() + " of INVOICE INV-EARLY-M is on the ledger on 2026-02-01, before the invoice's issue date 2026-02-10: the receivable is below 0 in between",
	}
	if len(rep.Warnings) != len(want) {
		t.Errorf("warnings = %q, want %q", rep.Warnings, want)
	}
	for _, w := range want {
		if !strings.Contains(strings.Join(rep.Warnings, "\n"), w) {
			t.Errorf("warnings lack %q: %q", w, rep.Warnings)
		}
	}
}

// C1: a restated payment relieves what Python's entry relieved, not the
// receivable left on the ledger. P1 USD 300 cleared, then P2 IDR approved
// across currencies relieving the 700 left, then P1 reversed under Python
// (balance_due back to 300). With P1's entries netting to 0, P2 must
// relieve 700 (face ends 300 = balance_due), not the 1000 the ledger shows.
func TestBackpostRestatesWhatPythonRelieved(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inv := e.invoice(t, doc{number: "INV-REV", currency: "USD", status: "PARTIAL", amount: "1000", balance: "300", issue: issued2025})
	p1 := e.rawPayment(t, inv, "USD", "300", day(2026, 2, 3), "FLAGGED")
	c := e.rawEntry(t, p1, day(2026, 2, 3).Add(10*time.Hour),
		jl{code: "1000", ccy: "USD", dr: "300", bdr: "4800000"},
		jl{code: "1100", ccy: "USD", cr: "300", bcr: "4800000"})
	r := e.rawEntry(t, p1, day(2026, 2, 20).Add(10*time.Hour),
		jl{code: "1000", ccy: "USD", cr: "300", bcr: "4800000"},
		jl{code: "1100", ccy: "USD", dr: "300", bdr: "4800000"})
	e.reversedBy(t, c, r)
	p2 := e.rawPayment(t, inv, "IDR", "11000000", day(2026, 2, 5), "CLEARED")
	e.rawEntry(t, p2, day(2026, 2, 5).Add(10*time.Hour),
		jl{code: "1000", ccy: "IDR", dr: "11000000", bdr: "11000000"},
		jl{code: "5900", ccy: "IDR", dr: "200000", bdr: "200000"},
		jl{code: "1100", ccy: "USD", cr: "700", bcr: "11200000"})

	rep, out := e.mustRun(t, true)
	if rep.PaymentsRestated != 1 {
		t.Errorf("report = %+v\n%s", rep, out)
	}
	b := e.invoiceBasis(t, inv)
	eq(t, "AR face", b.Face, "300")
	eq(t, "AR base", b.Base, "4500000")
	e.trialBalanced(t)
}
