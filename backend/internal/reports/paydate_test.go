package reports_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
)

// PORT-NOTES 167: a payment's journal entry is posted on its payment date
// (00:00 UTC, never later than now), not when it is recorded. The payments
// here are recorded "today" (the database's now, 2026) against documents
// issued on 2025-01-01, dated accrualPaid (2025-02-01).

// recordInvoicePayment records a manual payment of id dated date and returns
// its id.
func recordInvoicePayment(t *testing.T, a *apitest.App, id uuid.UUID, amount, date string) uuid.UUID {
	t.Helper()
	return apitest.Decode[payments.PaymentOut](t, a.Do(t, http.MethodPost, "/api/v1/invoices/"+id.String()+"/record-payment",
		map[string]any{"amount": amount, "payment_date": date, "payer_name": "Acme"}), http.StatusCreated).PaymentID
}

func recordBillPayment(t *testing.T, a *apitest.App, id uuid.UUID, amount int, date string) {
	t.Helper()
	apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, "/api/v1/bills/"+id.String()+"/record-payment",
		map[string]any{"amount": amount, "payment_date": date}), http.StatusOK)
}

// paymentPostedAt is posted_at of the PAYMENT entry of payment id.
func paymentPostedAt(t *testing.T, a *apitest.App, id uuid.UUID) string {
	t.Helper()
	var at string
	if err := a.Pool.QueryRow(t.Context(), `SELECT posted_at::text FROM journal_entries
WHERE source_type = 'PAYMENT' AND source_id = $1`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// billPaymentPostedAt is posted_at of the active BILL_PAYMENT entry of bill
// id's one CLEARED payment.
func billPaymentPostedAt(t *testing.T, a *apitest.App, id uuid.UUID) string {
	t.Helper()
	var at string
	if err := a.Pool.QueryRow(t.Context(), `SELECT e.posted_at::text FROM journal_entries e
JOIN bill_payments p ON p.bill_payment_id = e.source_id
WHERE e.source_type = 'BILL_PAYMENT' AND p.bill_id = $1 AND p.status = 'CLEARED'
  AND e.reversed_by_entry_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM journal_entries o WHERE o.reversed_by_entry_id = e.entry_id)`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// postedNow fails unless the entry posted at `at` was posted within the last
// hour of the database's clock, not after it.
func postedNow(t *testing.T, a *apitest.App, at string) {
	t.Helper()
	var ok bool
	if err := a.Pool.QueryRow(t.Context(), `SELECT $1::timestamp <= LOCALTIMESTAMP
AND $1::timestamp > LOCALTIMESTAMP - interval '1 hour'`, at).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Errorf("posted_at %s, want the database's now", at)
	}
}

// tbBaseNet is the base net of code in cur, "0.0000" when it has no row.
func tbBaseNet(r tbReport, code, cur string) string {
	if row := tbFind(r, code, cur); row != nil {
		return row.BaseNet
	}
	return "0.0000"
}

const dayBeforePaid = "2025-01-31"

func TestInvoicePaymentPostedOnPaymentDate(t *testing.T) {
	t.Parallel()
	t.Run("same currency", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		c := newCustomer(t, a, "Acme")
		inv := issueInvoice(t, a, c, "IDR", "MILESTONE", "1000000").InvoiceID
		pid := recordInvoicePayment(t, a, inv, "500000", accrualPaid)
		wantEq(t, "payment posted_at", paymentPostedAt(t, a, pid), accrualPaid+" 00:00:00")

		before := tb(t, a, "?as_of="+dayBeforePaid)
		wantEq(t, "1100 IDR before", tbBaseNet(before, "1100", "IDR"), "1000000.0000")
		wantEq(t, "1000 IDR before", tbBaseNet(before, "1000", "IDR"), "0.0000")
		on := tb(t, a, "?as_of="+accrualPaid)
		wantEq(t, "1100 IDR on", tbBaseNet(on, "1100", "IDR"), "500000.0000")
		wantEq(t, "1000 IDR on", tbBaseNet(on, "1000", "IDR"), "500000.0000")
	})
	t.Run("cross rate with FX gain", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		c := newCustomer(t, a, "Acme")
		inv := issueInvoice(t, a, c, "USD", "RECURRING", "1000").InvoiceID
		pid := recordInvoicePayment(t, a, inv, "1000", accrualPaid)
		wantEq(t, "payment posted_at", paymentPostedAt(t, a, pid), accrualPaid+" 00:00:00")

		// Before: AR open at the issue rate, no bank, no realised gain.
		before := tb(t, a, "?as_of="+dayBeforePaid)
		wantEq(t, "1100 USD before", tbBaseNet(before, "1100", "USD"), "15000000.0000")
		wantEq(t, "1000 USD before", tbBaseNet(before, "1000", "USD"), "0.0000")
		wantEq(t, "4900 IDR before", tbBaseNet(before, "4900", "IDR"), "0.0000")
		// On: AR relieved, bank at 16000, gain 1,000,000.
		on := tb(t, a, "?as_of="+accrualPaid)
		wantEq(t, "1100 USD on", tbBaseNet(on, "1100", "USD"), "0.0000")
		wantEq(t, "1000 USD on", tbBaseNet(on, "1000", "USD"), "16000000.0000")
		wantEq(t, "4900 IDR on", tbBaseNet(on, "4900", "IDR"), "-1000000.0000")
		if on.TotalDebitBase != on.TotalCreditBase {
			t.Errorf("Σ base debit %s ≠ Σ base credit %s", on.TotalDebitBase, on.TotalCreditBase)
		}
	})
	t.Run("future payment date is capped at now", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		c := newCustomer(t, a, "Acme")
		inv := issueInvoice(t, a, c, "IDR", "MILESTONE", "1000000").InvoiceID
		pid := recordInvoicePayment(t, a, inv, "1000000", "2099-01-01")
		postedNow(t, a, paymentPostedAt(t, a, pid))
	})
}

// postedAts is posted_at of every entry of source id, as text, sorted.
func postedAts(t *testing.T, a *apitest.App, id uuid.UUID) []string {
	t.Helper()
	rows, err := a.Pool.Query(t.Context(), `SELECT posted_at::text FROM journal_entries WHERE source_id = $1 ORDER BY 1`, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func wantAllAt(t *testing.T, what string, got []string, n int, at string) {
	t.Helper()
	if len(got) != n {
		t.Fatalf("%s: %d entries %v, want %d", what, len(got), got, n)
	}
	for _, g := range got {
		if g != at {
			t.Errorf("%s: posted_at %v, want every one at %s", what, got, at)
			return
		}
	}
}

// A payment reversal is a correction: it is dated at the payment entry's
// posted_at (PORT-NOTES 167), so a payment cleared, reversed and cleared
// again shows once in the reports as of any date after its payment date and
// not at all before it.
func TestPaymentReversalDatedAtOriginal(t *testing.T) {
	t.Parallel()
	const midFeb = "2025-02-15"
	t.Run("invoice payment", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		c := newCustomer(t, a, "Acme")
		inv := issueInvoice(t, a, c, "IDR", "MILESTONE", "1000000").InvoiceID
		pid := recordInvoicePayment(t, a, inv, "500000", accrualPaid)
		apitest.Decode[payments.PaymentOut](t, a.Do(t, http.MethodPost, "/api/v1/payments/"+pid.String()+"/reverse",
			map[string]any{"reason": "wrong invoice"}), http.StatusOK)
		apitest.Decode[payments.PaymentOut](t, a.Do(t, http.MethodPost, "/api/v1/payments/"+pid.String()+"/manual-review",
			map[string]any{"invoice_id": inv}), http.StatusOK)
		wantAllAt(t, "payment entries", postedAts(t, a, pid), 3, accrualPaid+" 00:00:00")

		before := tb(t, a, "?as_of="+dayBeforePaid)
		wantEq(t, "1100 IDR before", tbBaseNet(before, "1100", "IDR"), "1000000.0000")
		wantEq(t, "1000 IDR before", tbBaseNet(before, "1000", "IDR"), "0.0000")
		mid := tb(t, a, "?as_of="+midFeb)
		wantEq(t, "1100 IDR mid-February", tbBaseNet(mid, "1100", "IDR"), "500000.0000")
		wantEq(t, "1000 IDR mid-February", tbBaseNet(mid, "1000", "IDR"), "500000.0000")
	})
	t.Run("bill payment", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		v := newVendor(t, a, "Supplier")
		bill := openBill(t, a, v, "IDR", "2000000").BillID
		recordBillPayment(t, a, bill, 2000000, accrualPaid)
		ps := apitest.Decode[[]bills.BillPaymentOut](t, a.Do(t, http.MethodGet, "/api/v1/bills/"+bill.String()+"/payments", nil), http.StatusOK)
		if len(ps) != 1 {
			t.Fatalf("%d bill payments, want 1", len(ps))
		}
		first := ps[0].BillPaymentID
		apitest.Decode[bills.BillPaymentOut](t, a.Do(t, http.MethodPost, "/api/v1/bills/"+bill.String()+"/payments/"+first.String()+"/reverse",
			map[string]any{"reason": "wrong bill"}), http.StatusOK)
		recordBillPayment(t, a, bill, 2000000, accrualPaid)
		wantAllAt(t, "reversed bill payment entries", postedAts(t, a, first), 2, accrualPaid+" 00:00:00")
		wantEq(t, "re-recorded bill payment posted_at", billPaymentPostedAt(t, a, bill), accrualPaid+" 00:00:00")

		before := tb(t, a, "?as_of="+dayBeforePaid)
		wantEq(t, "2000 IDR before", tbBaseNet(before, "2000", "IDR"), "-2000000.0000")
		wantEq(t, "1000 IDR before", tbBaseNet(before, "1000", "IDR"), "0.0000")
		mid := tb(t, a, "?as_of="+midFeb)
		wantEq(t, "2000 IDR mid-February", tbBaseNet(mid, "2000", "IDR"), "0.0000")
		wantEq(t, "1000 IDR mid-February", tbBaseNet(mid, "1000", "IDR"), "-2000000.0000")
	})
}

func TestBillPaymentPostedOnPaymentDate(t *testing.T) {
	t.Parallel()
	t.Run("record payment", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		v := newVendor(t, a, "Supplier")
		bill := openBill(t, a, v, "IDR", "2000000").BillID
		recordBillPayment(t, a, bill, 2000000, accrualPaid)
		wantEq(t, "bill payment posted_at", billPaymentPostedAt(t, a, bill), accrualPaid+" 00:00:00")

		before := tb(t, a, "?as_of="+dayBeforePaid)
		wantEq(t, "2000 IDR before", tbBaseNet(before, "2000", "IDR"), "-2000000.0000")
		wantEq(t, "1000 IDR before", tbBaseNet(before, "1000", "IDR"), "0.0000")
		on := tb(t, a, "?as_of="+accrualPaid)
		wantEq(t, "2000 IDR on", tbBaseNet(on, "2000", "IDR"), "0.0000")
		wantEq(t, "1000 IDR on", tbBaseNet(on, "1000", "IDR"), "-2000000.0000")
	})
	t.Run("future payment date is capped at now", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		v := newVendor(t, a, "Supplier")
		bill := openBill(t, a, v, "IDR", "2000000").BillID
		recordBillPayment(t, a, bill, 2000000, "2099-01-01")
		postedNow(t, a, billPaymentPostedAt(t, a, bill))
	})
	t.Run("PATCH to PAID posts on today", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		v := newVendor(t, a, "Supplier")
		bill := openBill(t, a, v, "IDR", "2000000").BillID
		apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPatch, "/api/v1/bills/"+bill.String(), map[string]any{"status": "PAID"}), http.StatusOK)
		var today string
		if err := a.Pool.QueryRow(t.Context(), `SELECT CURRENT_DATE::timestamp::text`).Scan(&today); err != nil {
			t.Fatal(err)
		}
		wantEq(t, "bill payment posted_at", billPaymentPostedAt(t, a, bill), today)
	})
}
