package stats_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

// ovDay is the "today" of the overview tests; the seeded rates are as of
// 2026-01-01.
var ovDay = date(2026, time.July, 24)

func ovWindow() stats.Window { return stats.NewWindow(stats.Period12m, ovDay) }

func overview(t *testing.T, a *apitest.App) stats.OverviewResponse {
	t.Helper()
	got, err := stats.Overview(t.Context(), a.Pool, "IDR", ovDay, ovWindow())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// paymentFull inserts a payment with its amount, withholding tax, date and
// status.
func paymentFull(t *testing.T, a *apitest.App, inv uuid.UUID, cur, amount, wht, status string, paid time.Time) {
	t.Helper()
	exec(t, a, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, wht_amount)
VALUES ($1, $2, $3, $4, 'Acme Corp', $5, 'TEST', $6, $7, $8)`, uuid.New(), inv, amount, cur, paid, uuid.NewString(), status, wht)
}

func TestOverviewEmpty(t *testing.T) {
	t.Parallel()
	r := overview(t, newApp(t))
	k := r.KPIs
	for name, got := range map[string]string{
		"invoiced": k.Invoiced.Amount.Decimal().StringFixed(4), "invoiced prev": k.Invoiced.Previous.Decimal().StringFixed(4),
		"collected": k.Collected.Amount.Decimal().StringFixed(4), "open_ar": k.OpenAR.Decimal().StringFixed(4),
		"overdue": k.Overdue.Decimal().StringFixed(4), "mrr": k.MonthlyRecurring.Decimal().StringFixed(4),
	} {
		if got != "0.0000" {
			t.Errorf("%s = %s, want 0.0000", name, got)
		}
	}
	if k.CollectedShareOfInvoiced != nil || k.OverdueShareOfOpenAR != nil || k.RecurringForeignShare != nil || k.RecurringTopForeignCurrency != nil {
		t.Errorf("shares not nil: %+v", k)
	}
	if len(r.Unconverted) != 0 || r.Unconverted == nil {
		t.Errorf("unconverted = %v, want empty non-nil", r.Unconverted)
	}
	if n := len(ovWindow().Buckets); len(k.Invoiced.Series) != n || len(k.Collected.Series) != n {
		t.Errorf("series lengths %d, %d, want %d", len(k.Invoiced.Series), len(k.Collected.Series), n)
	}
	for _, p := range k.Invoiced.Series {
		if p.Amount.Decimal().StringFixed(4) != "0.0000" {
			t.Errorf("series %s = %s", p.Label, p.Amount.Decimal())
		}
	}
}

func TestOverviewInvoicedWindow(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	w := ovWindow()
	in := func(amount, status string, issue time.Time, tmpl bool) {
		inv{cur: "IDR", amount: amount, status: status, issue: d(issue), template: tmpl}.insert(t, a, c)
	}
	in("100", "SENT", w.From, false)
	in("200", "PAID", w.To, false)
	in("50", "SENT", w.From.AddDate(0, 0, -1), false)
	in("999", "DRAFT", w.From, false)
	in("999", "VOID", w.From, false)
	in("999", "SENT", w.From, true)
	r := overview(t, a)
	if got := r.KPIs.Invoiced.Amount.Decimal().StringFixed(4); got != "300.0000" {
		t.Errorf("invoiced = %s", got)
	}
	if got := r.KPIs.Invoiced.Previous.Decimal().StringFixed(4); got != "50.0000" {
		t.Errorf("previous = %s", got)
	}
	var sum string
	first, last := r.KPIs.Invoiced.Series[0], r.KPIs.Invoiced.Series[len(r.KPIs.Invoiced.Series)-1]
	sum = first.Amount.Decimal().StringFixed(4) + "/" + last.Amount.Decimal().StringFixed(4)
	if sum != "100.0000/200.0000" {
		t.Errorf("first/last bucket = %s", sum)
	}
}

func TestOverviewCollectedAddsWHT(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	w := ovWindow()
	id := inv{cur: "IDR", amount: "200", issue: d(w.From)}.insert(t, a, c)
	paymentFull(t, a, id, "IDR", "98", "2", "CLEARED", w.From.AddDate(0, 0, 5))
	paymentFull(t, a, id, "IDR", "500", "0", "PENDING_MANUAL_REVIEW", w.From.AddDate(0, 0, 5))
	r := overview(t, a)
	if got := r.KPIs.Collected.Amount.Decimal().StringFixed(4); got != "100.0000" {
		t.Errorf("collected = %s", got)
	}
	if s := r.KPIs.CollectedShareOfInvoiced; s == nil || *s != 50 {
		t.Errorf("share = %v, want 50", s)
	}
}

func TestOverviewUnconverted(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	w := ovWindow()
	inv{cur: "SGD", amount: "10", issue: d(w.To)}.insert(t, a, c)
	inv{cur: "AUD", amount: "10", issue: d(w.To)}.insert(t, a, c)
	r := overview(t, a)
	eq(t, "unconverted", r.Unconverted, []string{"AUD"})
	if got := r.KPIs.Invoiced.Amount.Decimal().StringFixed(4); got != "120000.0000" {
		t.Errorf("invoiced = %s, want SGD only", got)
	}
}

func TestOverviewOverdueShare(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c1, c2 := newCustomer(t, a, "A"), newCustomer(t, a, "B")
	inv{cur: "IDR", amount: "300", due: d(ovDay.AddDate(0, 0, 5))}.insert(t, a, c1)
	inv{cur: "IDR", amount: "100", status: "PARTIAL", due: d(ovDay.AddDate(0, 0, -1))}.insert(t, a, c2)
	r := overview(t, a)
	k := r.KPIs
	if k.OpenAR.Decimal().StringFixed(4) != "400.0000" || k.Overdue.Decimal().StringFixed(4) != "100.0000" {
		t.Errorf("open/overdue = %s/%s", k.OpenAR.Decimal(), k.Overdue.Decimal())
	}
	if k.OverdueShareOfOpenAR == nil || *k.OverdueShareOfOpenAR != 25 {
		t.Errorf("share = %v, want 25", k.OverdueShareOfOpenAR)
	}
	if k.OpenARInvoiceCount != 2 || k.OpenARCustomerCount != 2 {
		t.Errorf("counts = %d, %d", k.OpenARInvoiceCount, k.OpenARCustomerCount)
	}
}

func TestOverviewRecurringForeignShare(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	template(t, a, c, "IDR", "12000", monthly())
	template(t, a, c, "SGD", "1", monthly())
	r := overview(t, a)
	k := r.KPIs
	if got := k.MonthlyRecurring.Decimal().StringFixed(4); got != "24000.0000" {
		t.Errorf("mrr = %s", got)
	}
	if k.RecurringForeignShare == nil || *k.RecurringForeignShare != 50 {
		t.Errorf("share = %v, want 50", k.RecurringForeignShare)
	}
	if k.RecurringTopForeignCurrency == nil || *k.RecurringTopForeignCurrency != "SGD" {
		t.Errorf("top = %v, want SGD", k.RecurringTopForeignCurrency)
	}
}
