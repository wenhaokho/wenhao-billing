package stats_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

func receivables(t *testing.T, a *apitest.App) stats.ReceivablesResponse {
	t.Helper()
	got, err := stats.Receivables(t.Context(), a.Pool, "IDR", ovDay)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// agingOf is "key amount count" per aging bucket, in order.
func agingOf(r stats.ReceivablesResponse) []string {
	out := []string{}
	for _, b := range r.Aging {
		out = append(out, b.Key+" "+b.Amount.Decimal().StringFixed(4)+" "+itoa(b.Count))
	}
	return out
}

func cashInOf(r stats.ReceivablesResponse) []string {
	out := []string{}
	for _, b := range r.CashIn {
		e := "n"
		if b.Estimate {
			e = "y"
		}
		out = append(out, b.Key+" "+b.OpenInvoices.Decimal().StringFixed(4)+" "+b.Recurring.Decimal().StringFixed(4)+" "+e)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

func days(n int) *time.Time { return d(ovDay.AddDate(0, 0, n)) }

func TestReceivablesEmpty(t *testing.T) {
	t.Parallel()
	r := receivables(t, newApp(t))
	eq(t, "aging", agingOf(r), []string{"not_due 0.0000 0", "d1_30 0.0000 0", "d31_60 0.0000 0", "d61_90 0.0000 0", "d90_plus 0.0000 0"})
	eq(t, "cash in", cashInOf(r), []string{"d0_30 0.0000 0.0000 n", "d31_60 0.0000 0.0000 n", "d61_90 0.0000 0.0000 n"})
	if r.NeedsAttention == nil || len(r.NeedsAttention) != 0 || r.Unconverted == nil || len(r.Unconverted) != 0 {
		t.Errorf("lists = %v, %v, want empty non-nil", r.NeedsAttention, r.Unconverted)
	}
	if r.OpenAR.Decimal().StringFixed(4) != "0.0000" || r.OverdueExcluded.Decimal().StringFixed(4) != "0.0000" || r.PastDueShare != nil {
		t.Errorf("totals = %+v", r)
	}
	if r.BaseCurrency != "IDR" || r.InvoiceCount != 0 || r.CustomerCount != 0 {
		t.Errorf("counts = %+v", r)
	}
}

func TestReceivablesAging(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	in := func(amount string, due *time.Time) { inv{cur: "IDR", amount: amount, due: due}.insert(t, a, c) }
	in("1", days(0))
	in("2", nil)
	in("4", days(-1))
	in("8", days(-30))
	in("16", days(-31))
	in("32", days(-91))
	r := receivables(t, a)
	eq(t, "aging", agingOf(r), []string{"not_due 3.0000 2", "d1_30 12.0000 2", "d31_60 16.0000 1", "d61_90 0.0000 0", "d90_plus 32.0000 1"})
	if r.OpenAR.Decimal().StringFixed(4) != "63.0000" || r.InvoiceCount != 6 || r.CustomerCount != 1 {
		t.Errorf("open ar = %s, %d invoices, %d customers", r.OpenAR.Decimal(), r.InvoiceCount, r.CustomerCount)
	}
	if r.PastDueShare == nil || *r.PastDueShare != 95.2 {
		t.Errorf("past due share = %v, want 95.2", r.PastDueShare)
	}
}

func TestReceivablesCashIn(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	in := func(amount string, due *time.Time) { inv{cur: "IDR", amount: amount, due: due}.insert(t, a, c) }
	in("1000", days(5))
	in("10", days(30))
	in("2000", days(31))
	in("5000", days(91))                                              // beyond the horizon
	in("7000", days(-3))                                              // overdue: only in overdue_excluded
	recurring(t, a, c, "100", "MONTHLY", "2026-01-02", "interval", 2) // runs Sep 2, today+40
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, is_template, billing_cycle_ref)
VALUES ($1, $2, 'USAGE', 'USD', 5, 0, 'DRAFT', false, '{"cut_off_day": 30}'::jsonb)`, uuid.New(), c)
	r := receivables(t, a)
	eq(t, "cash in", cashInOf(r), []string{
		"d0_30 1010.0000 80000.0000 y",
		"d31_60 2000.0000 1600000.0000 n",
		"d61_90 0.0000 0.0000 n",
	})
	if got := r.OverdueExcluded.Decimal().StringFixed(4); got != "7000.0000" {
		t.Errorf("overdue excluded = %s, want 7000.0000", got)
	}
}

func TestReceivablesNeedsAttention(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	var oldest uuid.UUID
	for i := 1; i <= 8; i++ {
		id := inv{cur: "IDR", amount: "100", due: days(-i)}.insert(t, a, c)
		if i == 8 {
			oldest = id
			exec(t, a, `UPDATE invoices SET wht_amount = 2 WHERE invoice_id = $1`, id)
		}
	}
	r := receivables(t, a)
	if len(r.NeedsAttention) != 6 {
		t.Fatalf("rows = %d, want 6", len(r.NeedsAttention))
	}
	for i, row := range r.NeedsAttention {
		if want := 8 - i; row.DaysLate != want {
			t.Errorf("row %d days late = %d, want %d", i, row.DaysLate, want)
		}
		if row.LastReminderAt != nil || row.CustomerName == nil || *row.CustomerName != "Acme" || row.CustomerID == nil || *row.CustomerID != c || row.Currency != "IDR" {
			t.Errorf("row %d = %+v", i, row)
		}
	}
	first := r.NeedsAttention[0]
	if first.InvoiceID != oldest || first.BalanceDue.Decimal().StringFixed(4) != "100.0000" || first.ExpectedCash.Decimal().StringFixed(4) != "98.0000" {
		t.Errorf("oldest = %+v, want expected cash 98.0000", first)
	}
	if got := r.NeedsAttention[1].ExpectedCash.Decimal().StringFixed(4); got != "100.0000" {
		t.Errorf("no withholding: expected cash = %s, want 100.0000", got)
	}
	if got := first.DueDate.Format("2006-01-02"); got != "2026-07-16" {
		t.Errorf("due date = %s", got)
	}
}

func TestReceivablesUnconverted(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	inv{cur: "AUD", amount: "500", due: days(-2)}.insert(t, a, c)
	r := receivables(t, a)
	eq(t, "unconverted", r.Unconverted, []string{"AUD"})
	eq(t, "aging", agingOf(r), []string{"not_due 0.0000 0", "d1_30 0.0000 1", "d31_60 0.0000 0", "d61_90 0.0000 0", "d90_plus 0.0000 0"})
	if r.OpenAR.Decimal().StringFixed(4) != "0.0000" || r.PastDueShare != nil || r.OverdueExcluded.Decimal().StringFixed(4) != "0.0000" {
		t.Errorf("totals = %+v", r)
	}
	if len(r.NeedsAttention) != 1 || r.NeedsAttention[0].Currency != "AUD" || r.NeedsAttention[0].BalanceDue.Decimal().StringFixed(4) != "500.0000" {
		t.Errorf("needs attention = %+v", r.NeedsAttention)
	}
}

func TestReceivablesAgingBoundaries(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	in := func(amount string, due *time.Time) { inv{cur: "IDR", amount: amount, due: due}.insert(t, a, c) }
	in("1", days(-60))
	in("2", days(-61))
	in("4", days(-90))
	r := receivables(t, a)
	eq(t, "aging", agingOf(r), []string{"not_due 0.0000 0", "d1_30 0.0000 0", "d31_60 1.0000 1", "d61_90 6.0000 2", "d90_plus 0.0000 0"})
}

func TestReceivablesAgingExcludesTemplatesAndDrafts(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	inv{cur: "IDR", amount: "10", due: days(-5), template: true}.insert(t, a, c)
	inv{cur: "IDR", amount: "20", due: days(-5), status: "DRAFT"}.insert(t, a, c)
	inv{cur: "IDR", amount: "40", due: days(-5)}.insert(t, a, c)
	r := receivables(t, a)
	eq(t, "aging", agingOf(r), []string{"not_due 0.0000 0", "d1_30 40.0000 1", "d31_60 0.0000 0", "d61_90 0.0000 0", "d90_plus 0.0000 0"})
	if len(r.NeedsAttention) != 1 {
		t.Errorf("needs attention = %d rows, want 1", len(r.NeedsAttention))
	}
}

func TestReceivablesNoCustomer(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, due_date)
VALUES ($1, NULL, 'MILESTONE', 'IDR', 100, 100, 'SENT', $2)`, uuid.New(), days(-3))
	r := receivables(t, a)
	if len(r.NeedsAttention) != 1 || r.NeedsAttention[0].CustomerID != nil || r.NeedsAttention[0].CustomerName != nil {
		t.Errorf("needs attention = %+v, want one row with null customer", r.NeedsAttention)
	}
}
