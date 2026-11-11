package payments

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

// Go's own (PORT-NOTES 161): a manual invoice payment posts its journal entry
// through the same posting as an approved payment, and can be reversed.

func reversePath(id uuid.UUID) string { return "/api/v1/payments/" + id.String() + "/reverse" }

// sourceOf is the source_type and source_id of the journal entry id.
func (e *env) sourceOf(t *testing.T, id uuid.UUID) (string, uuid.UUID) {
	t.Helper()
	var st string
	var sid uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT source_type, source_id FROM journal_entries WHERE entry_id = $1`, id).
		Scan(&st, &sid); err != nil {
		t.Fatal(err)
	}
	return st, sid
}

// trialBalanced fails unless every account's base debits equal its base
// credits summed over all accounts, and the ledger as a whole nets to 0.
func (e *env) trialBalanced(t *testing.T) {
	t.Helper()
	var debit, credit decimal.Decimal
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(base_amount_debit), 0), COALESCE(sum(base_amount_credit), 0) FROM journal_lines`).
		Scan(&debit, &credit); err != nil {
		t.Fatal(err)
	}
	if !debit.Equal(credit) {
		t.Errorf("trial balance: base debits %s, credits %s", debit, credit)
	}
}

func TestManualPaymentPostsJournalEntry(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	p := a.mustRecord(t, id, map[string]any{"amount": net, "wht_amount": wht})

	es := a.entries(t, p.PaymentID)
	if len(es) != 1 {
		t.Fatalf("%d journal entries, want 1", len(es))
	}
	je := es[0]
	if st, sid := a.sourceOf(t, je.id); st != "PAYMENT" || sid != p.PaymentID {
		t.Errorf("source %s %s, want PAYMENT %s", st, sid, p.PaymentID)
	}
	balanced(t, je)
	if got := je.codes(); !slices.Equal(got, []string{"1000", "1100", "1300"}) {
		t.Fatalf("codes %v", got)
	}
	bank, ar, tax := je.line("1000"), je.line("1100"), je.line("1300")
	if !bank.debit.Equal(dec(net)) || bank.currency != "IDR" {
		t.Errorf("bank %+v", bank)
	}
	if !ar.credit.Equal(dec(gross)) || !ar.baseCredit.Equal(dec(gross)) {
		t.Errorf("receivable %+v, want credit %s (amount + withheld)", ar, gross)
	}
	if !tax.debit.Equal(dec(wht)) {
		t.Errorf("prepaid tax %+v, want debit %s", tax, wht)
	}
	checkInvoice(t, a.env, id, "0", wht, "PAID")
}

// The customer paid the net in cash, then sent the PPh slip: the second,
// withholding-only payment (amount 0) books Prepaid Tax against the
// receivable with no bank line.
func TestManualWithholdingOnlyPaymentPosts(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	cash := a.mustRecord(t, id, map[string]any{"amount": net})
	slip := a.mustRecord(t, id, map[string]any{"amount": "0", "wht_amount": wht})
	checkInvoice(t, a.env, id, "0", wht, "PAID")

	if c := active(t, a.env, cash.PaymentID); !slices.Equal(c.codes(), []string{"1000", "1100"}) {
		t.Errorf("cash entry codes %v", c.codes())
	}
	je := active(t, a.env, slip.PaymentID)
	balanced(t, je)
	if got := je.codes(); !slices.Equal(got, []string{"1100", "1300"}) {
		t.Fatalf("slip entry codes %v, want [1100 1300]", got)
	}
	if !je.line("1300").debit.Equal(dec(wht)) || !je.line("1100").credit.Equal(dec(wht)) {
		t.Errorf("slip lines %+v", je.lines)
	}
	a.trialBalanced(t)
}

// Manual payments are always in the invoice currency. An invoice issued at
// one rate and paid when the rate has moved is posted exactly as an approved
// payment of the same money on the same day: every line at the payment
// date's rate, and the same realised FX lines (none, in one currency).
func TestManualPaymentCrossRateBooksFxGainLoss(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16500, '2026-04-01', 'TEST')`)
	c := a.customer(t, "Acme Corp", "Acme")
	twin := func() uuid.UUID {
		return a.invoice(t, inv{
			customer: c, currency: "USD", status: "SENT", balance: "1020", wht: "20",
			issue: day(2026, 1, 10), due: day(2026, 2, 10),
		})
	}
	manualInv, approvedInv := twin(), twin()

	manual := a.mustRecord(t, manualInv, map[string]any{"amount": "1000", "wht_amount": "20", "payment_date": "2026-04-20"})
	ref := "REF-TWIN"
	held := a.incoming(t, payload(amount("1000"), payer("Unknown Payer"), paid(day(2026, 4, 20)),
		func(p *IntakePayload) { p.PayerReference = &ref }))
	if held.Status != "CLEARED" {
		a.mustApprove(t, held.PaymentID, approvedInv)
	}

	m, ap := active(t, a.env, manual.PaymentID), active(t, a.env, held.PaymentID)
	balanced(t, m)
	if len(m.lines) != len(ap.lines) {
		t.Fatalf("manual codes %v, approved codes %v", m.codes(), ap.codes())
	}
	for i := range m.lines {
		ml, al := m.lines[i], ap.lines[i]
		if ml.code != al.code || ml.currency != al.currency || !ml.debit.Equal(al.debit) || !ml.credit.Equal(al.credit) ||
			!ml.baseDebit.Equal(al.baseDebit) || !ml.baseCredit.Equal(al.baseCredit) || !ml.rate.Equal(al.rate) {
			t.Errorf("line %d: manual %+v, approved %+v", i, ml, al)
		}
		if !ml.rate.Equal(dec("16500")) {
			t.Errorf("line %s at rate %s, want the payment date's 16500", ml.code, ml.rate)
		}
	}
	for _, fx := range []string{"4900", "5900"} {
		if (m.line(fx) == nil) != (ap.line(fx) == nil) {
			t.Errorf("FX line %s: manual %v, approved %v", fx, m.line(fx), ap.line(fx))
		}
	}
	checkInvoice(t, a.env, manualInv, "0", "20", "PAID")
	checkInvoice(t, a.env, approvedInv, "0", "20", "PAID")
}

func TestManualPaymentCanBeReversed(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	id := a.whtInvoice(t)
	p := a.mustRecord(t, id, map[string]any{"amount": net, "wht_amount": wht})
	// An intake payment of the same money on a twin invoice (the only one
	// still open), to compare the two reversals.
	twin := a.whtInvoice(t)
	in := a.incoming(t, whtPayload(net))
	if in.Status != "CLEARED" || in.InvoiceID.Bytes != twin {
		t.Fatalf("intake %s against %v", in.Status, in.InvoiceID)
	}

	got := apitest.Decode[PaymentOut](t, a.app.Do(t, http.MethodPost, reversePath(p.PaymentID), map[string]any{"reason": "keyed twice"}), http.StatusOK)
	if got.Status != "FLAGGED" {
		t.Errorf("payment %s, want FLAGGED", got.Status)
	}
	es := a.entries(t, p.PaymentID)
	if len(es) != 2 || es[0].reversedBy == nil || *es[0].reversedBy != es[1].id || es[1].memo != "Reversal: keyed twice" {
		t.Fatalf("entries %+v", es)
	}
	checkInvoice(t, a.env, id, gross, "0", "OPEN")
	a.trialBalanced(t)
	if logs := a.logs(t, p.PaymentID); len(logs) != 2 || logs[1].action != "REVERSED" {
		t.Errorf("logs %+v", logs)
	}

	a.mustReverse(t, in.PaymentID, "x")
	if m, i := a.invoiceState(t, id), a.invoiceState(t, twin); !m.balance.Equal(i.balance) || !m.whtBooked.Equal(i.whtBooked) || m.status != i.status {
		t.Errorf("manual reversal left %+v, intake reversal %+v", m, i)
	}
}

// stampFails is a HostingRestorer whose StampPaid fails: the record-payment
// transaction fails after the posting.
type stampFails struct{ NoHosting }

func (stampFails) StampPaid(context.Context, pgx.Tx, uuid.UUID) error {
	return errors.New("stamp boom")
}

func TestManualPaymentPostingFailureLeavesNothing(t *testing.T) {
	t.Parallel()
	nothing := func(t *testing.T, a *api) {
		t.Helper()
		for _, table := range []string{"payments", "reconciliation_log", "journal_entries", "journal_lines"} {
			if n := a.count(t, `SELECT count(*) FROM `+table); n != 0 {
				t.Errorf("%d rows in %s, want 0", n, table)
			}
		}
	}
	t.Run("missing rate", func(t *testing.T) {
		t.Parallel()
		a := newAPI(t, NoHosting{})
		id := a.rateless(t, "1000", "0")
		rec := a.record(t, id, map[string]any{"amount": "1000", "payment_date": "2025-12-01"})
		wantProblem(t, rec, http.StatusBadRequest, "fx_rate_missing", "no fx rate for USD->IDR on or before 2025-12-01")
		nothing(t, a)
		checkInvoice(t, a.env, id, "1000", "0", "SENT")
	})
	t.Run("failure after posting", func(t *testing.T) {
		t.Parallel()
		a := newAPI(t, stampFails{})
		c := a.customer(t, "Acme Corp")
		id := a.invoice(t, inv{customer: c, currency: "IDR", status: "OPEN", balance: "1000"})
		if rec := a.record(t, id, map[string]any{"amount": "1000"}); rec.Code != http.StatusInternalServerError {
			t.Errorf("status %d, want 500: %s", rec.Code, rec.Body)
		}
		nothing(t, a)
		checkInvoice(t, a.env, id, "1000", "0", "OPEN")
	})
}
