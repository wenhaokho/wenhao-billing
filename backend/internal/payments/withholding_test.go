package payments

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

// Ports of tests/integration/test_reconciliation_withholding.py. The
// invoice is what Python's create_invoice + finalize_invoice make of one line
// of NET with PPh 23 at 2% grossed up: GROSS due, WHT withheld, OPEN.

const (
	gross = "10204082"
	wht   = "204082"
	net   = "10000000"
)

// whtPayload is an IDR payment from Acme Corp on 2026-04-15.
func whtPayload(amt string, opts ...func(*IntakePayload)) IntakePayload {
	ref := "REF-WHT"
	return payload(append([]func(*IntakePayload){
		amount(amt), currency("IDR"), func(p *IntakePayload) { p.PayerReference = &ref },
	}, opts...)...)
}

func (e *env) whtInvoice(t *testing.T, ccy, balance, withheld string) uuid.UUID {
	t.Helper()
	c := e.customer(t, "Acme Corp", "Acme")
	return e.invoice(t, inv{
		customer: c, currency: ccy, status: "OPEN", balance: balance, wht: withheld,
		issue: day(2026, 4, 10), due: day(2026, 4, 24),
	})
}

// active is the payment's one entry that is not reversed.
func active(t *testing.T, e *env, paymentID uuid.UUID) jentry {
	t.Helper()
	var found []jentry
	for _, je := range e.entries(t, paymentID) {
		if je.reversedBy == nil {
			found = append(found, je)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d unreversed entries, want 1", len(found))
	}
	return found[0]
}

// balanced fails unless the entry nets to 0 in base and, when its lines
// share one currency, in that currency (an FX line is in the base currency).
func balanced(t *testing.T, je jentry) {
	t.Helper()
	n, b := je.nets()
	one := true
	for _, l := range je.lines {
		one = one && l.currency == je.lines[0].currency
	}
	if !b.IsZero() || (one && !n.IsZero()) {
		t.Errorf("entry nets %s, base %s; want 0 in base (and in its one currency)", n, b)
	}
}

func TestAutoClearBooksPrepaidTax(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.whtInvoice(t, "IDR", gross, wht)
	p := e.incoming(t, whtPayload(net))

	if p.Status != "CLEARED" || !p.WhtAmount.Equal(dec(wht)) {
		t.Fatalf("payment %s wht %s", p.Status, p.WhtAmount)
	}
	checkInvoice(t, e, invID, "0", wht, "PAID")
	je := active(t, e, p.PaymentID)
	balanced(t, je)
	if len(je.lines) != 3 {
		t.Fatalf("lines %v", je.codes())
	}
	if l := je.line("1000"); l == nil || !l.debit.Equal(dec(net)) {
		t.Errorf("bank %+v", l)
	}
	if l := je.line("1300"); l == nil || !l.debit.Equal(dec(wht)) {
		t.Errorf("prepaid tax %+v", l)
	}
	if l := je.line("1100"); l == nil || !l.credit.Equal(dec(gross)) {
		t.Errorf("receivable %+v", l)
	}
}

func TestReverseThenRepay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.whtInvoice(t, "IDR", gross, wht)
	first := e.incoming(t, whtPayload(net))
	if first.Status != "CLEARED" {
		t.Fatalf("first %s", first.Status)
	}
	e.mustReverse(t, first.PaymentID, "bounced")
	checkInvoice(t, e, invID, gross, "0", "OPEN")

	second := e.incoming(t, whtPayload(net))
	if second.Status != "CLEARED" || !second.WhtAmount.Equal(dec(wht)) {
		t.Fatalf("second %s wht %s", second.Status, second.WhtAmount)
	}
	checkInvoice(t, e, invID, "0", wht, "PAID")
}

func TestManualApproveNonMatchingAmountBooksNoWithholding(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.whtInvoice(t, "IDR", gross, wht)
	p := e.incoming(t, whtPayload("9600000"))
	if p.Status != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s", p.Status)
	}
	got := e.mustApprove(t, p.PaymentID, invID)
	if got.Status != "CLEARED" || !got.WhtAmount.IsZero() {
		t.Errorf("payment %s wht %s", got.Status, got.WhtAmount)
	}
	checkInvoice(t, e, invID, "604082", "0", "PARTIAL")
	je := active(t, e, p.PaymentID)
	if !slices.Equal(je.codes(), []string{"1000", "1100"}) {
		t.Errorf("accounts %v, want [1000 1100]", je.codes())
	}
	balanced(t, je)
}

// A USD invoice with PPh paid net in USD (plan 7 review focus 5). Bank and
// Prepaid Tax convert at the payment date's rate; the receivable is relieved
// at its issue basis (1020.41 at 16000), and the difference is a realised
// loss (PORT-NOTES 163; before, the receivable took the debits' bases).
func TestForeignCurrencyWithholdingBalancesInBase(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rate := "15204.60256196"
	e.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', $1, '2026-04-15', 'TEST')`, dec(rate))
	invID := e.whtInvoice(t, "USD", "1020.41", "20.41")
	p := e.incoming(t, whtPayload("1000.00", currency("USD")))

	if p.Status != "CLEARED" || !p.WhtAmount.Equal(dec("20.41")) {
		t.Fatalf("payment %s wht %s", p.Status, p.WhtAmount)
	}
	checkInvoice(t, e, invID, "0", "20.41", "PAID")
	je := active(t, e, p.PaymentID)
	balanced(t, je)
	for code, w := range map[string][2]string{"1000": {"USD", rate}, "1300": {"USD", rate}, "1100": {"USD", "16000"}, "5900": {"IDR", "1"}} {
		if l := je.line(code); l == nil || l.currency != w[0] || !l.rate.Equal(dec(w[1])) {
			t.Errorf("line %s = %+v, want %s at %s", code, l, w[0], w[1])
		}
	}
	want := map[string][2]string{ // base debit, base credit
		"1000": {"15204602.5620", "0"},
		"1300": {"310325.9383", "0"},
		"1100": {"0", "16326560"},
		"5900": {"811631.4997", "0"},
	}
	for code, w := range want {
		l := je.line(code)
		if l == nil || !l.baseDebit.Equal(dec(w[0])) || !l.baseCredit.Equal(dec(w[1])) {
			t.Errorf("%s = %+v, want base %s / %s", code, l, w[0], w[1])
		}
	}
}

func TestGrossPaymentIsHeldWithoutSideEffects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.whtInvoice(t, "IDR", gross, wht)
	p := e.incoming(t, whtPayload(gross))

	if p.Status != "PENDING_MANUAL_REVIEW" || !p.WhtAmount.IsZero() {
		t.Errorf("payment %s wht %s", p.Status, p.WhtAmount)
	}
	if n := len(e.entries(t, p.PaymentID)); n != 0 {
		t.Errorf("%d entries, want 0", n)
	}
	checkInvoice(t, e, invID, gross, "0", "OPEN")
}

// A USD invoice with PPh approved against an IDR payment: the withholding is
// booked in USD, the difference in IDR, and the entry balances in base.
func TestForeignCurrencyWithholdingCrossCurrencyApproval(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	invID := e.whtInvoice(t, "USD", "1020.41", "20.41")
	p := e.incoming(t, whtPayload("16000000"))
	if p.Status != "PENDING_MANUAL_REVIEW" || p.InvoiceID.Valid {
		t.Fatalf("payment %s invoice %v, want held with no candidate", p.Status, p.InvoiceID)
	}
	got := e.mustApprove(t, p.PaymentID, invID)
	if !got.WhtAmount.Equal(dec("20.41")) {
		t.Errorf("wht %s", got.WhtAmount)
	}
	checkInvoice(t, e, invID, "0", "20.41", "PAID")
	je := active(t, e, p.PaymentID)
	if _, base := je.nets(); !base.IsZero() {
		t.Errorf("base net %s", base)
	}
	// bank 16,000,000 + wht 20.41×16000 = 326,560 − AR 1020.41×16000 = 16,326,560: no difference.
	if l := je.line("1300"); l == nil || l.currency != "USD" || !l.baseDebit.Equal(dec("326560")) {
		t.Errorf("prepaid tax %+v", l)
	}
	if je.line("4900") != nil || je.line("5900") != nil {
		t.Errorf("accounts %v, want no FX line", je.codes())
	}
}
