package stats_test

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
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
		"collected": k.Collected.Amount.Decimal().StringFixed(4), "open_ar": k.OpenAR.Amount.Decimal().StringFixed(4),
		"overdue": k.Overdue.Amount.Decimal().StringFixed(4), "mrr": k.MonthlyRecurring.Amount.Decimal().StringFixed(4),
	} {
		if got != "0.0000" {
			t.Errorf("%s = %s, want 0.0000", name, got)
		}
	}
	if k.Collected.ShareOfInvoiced != nil || k.Overdue.ShareOfOpenAR != nil || k.MonthlyRecurring.ForeignShare != nil || k.MonthlyRecurring.TopForeignCurrency != nil {
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
	if s := r.KPIs.Collected.ShareOfInvoiced; s == nil || *s != 50 {
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
	if k.OpenAR.Amount.Decimal().StringFixed(4) != "400.0000" || k.Overdue.Amount.Decimal().StringFixed(4) != "100.0000" {
		t.Errorf("open/overdue = %s/%s", k.OpenAR.Amount.Decimal(), k.Overdue.Amount.Decimal())
	}
	if k.Overdue.ShareOfOpenAR == nil || *k.Overdue.ShareOfOpenAR != 25 {
		t.Errorf("share = %v, want 25", k.Overdue.ShareOfOpenAR)
	}
	if k.OpenAR.InvoiceCount != 2 || k.OpenAR.CustomerCount != 2 {
		t.Errorf("counts = %d, %d", k.OpenAR.InvoiceCount, k.OpenAR.CustomerCount)
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
	if got := k.MonthlyRecurring.Amount.Decimal().StringFixed(4); got != "24000.0000" {
		t.Errorf("mrr = %s", got)
	}
	if k.MonthlyRecurring.ForeignShare == nil || *k.MonthlyRecurring.ForeignShare != 50 {
		t.Errorf("share = %v, want 50", k.MonthlyRecurring.ForeignShare)
	}
	if k.MonthlyRecurring.TopForeignCurrency == nil || *k.MonthlyRecurring.TopForeignCurrency != "SGD" {
		t.Errorf("top = %v, want SGD", k.MonthlyRecurring.TopForeignCurrency)
	}
}

func TestOverviewRecurringForeignShareUnconverted(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	template(t, a, c, "IDR", "12000", monthly())
	template(t, a, c, "AUD", "5", monthly()) // no AUD rate
	r := overview(t, a)
	k := r.KPIs
	if got := k.MonthlyRecurring.Amount.Decimal().StringFixed(4); got != "12000.0000" {
		t.Errorf("mrr = %s, want 12000.0000", got)
	}
	if k.MonthlyRecurring.ForeignShare != nil {
		t.Errorf("share = %v, want nil: AUD MRR has no rate", *k.MonthlyRecurring.ForeignShare)
	}
	eq(t, "unconverted", r.Unconverted, []string{"AUD"})
}

func TestOverviewDueTodayIsNotOverdue(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	inv{cur: "IDR", amount: "100", due: d(ovDay)}.insert(t, a, c)
	k := overview(t, a).KPIs
	if k.OpenAR.Amount.Decimal().StringFixed(4) != "100.0000" || k.Overdue.Amount.Decimal().StringFixed(4) != "0.0000" {
		t.Errorf("open/overdue = %s/%s", k.OpenAR.Amount.Decimal(), k.Overdue.Amount.Decimal())
	}
}

func TestOverviewPreviousWindowEdges(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	w := ovWindow()
	inv{cur: "IDR", amount: "70", issue: d(w.PrevFrom)}.insert(t, a, c)
	inv{cur: "IDR", amount: "900", issue: d(w.PrevFrom.AddDate(0, 0, -1))}.insert(t, a, c)
	k := overview(t, a).KPIs
	if k.Invoiced.Previous.Decimal().StringFixed(4) != "70.0000" || k.Invoiced.Amount.Decimal().StringFixed(4) != "0.0000" {
		t.Errorf("previous/amount = %s/%s", k.Invoiced.Previous.Decimal(), k.Invoiced.Amount.Decimal())
	}
}

func TestOverviewDaysToPay(t *testing.T) {
	t.Parallel()
	t.Run("averages the window and the previous window", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		c := newCustomer(t, a, "Acme")
		w := ovWindow()
		pay := func(issue, paid time.Time) {
			id := inv{cur: "IDR", amount: "100", balance: "0", status: "PAID", issue: d(issue)}.insert(t, a, c)
			paymentFull(t, a, id, "IDR", "100", "0", "CLEARED", paid)
		}
		in := w.From.AddDate(0, 0, 30)
		pay(in.AddDate(0, 0, -10), in)
		pay(in.AddDate(0, 0, -20), in)
		prev := w.PrevFrom.AddDate(0, 0, 60)
		pay(prev.AddDate(0, 0, -40), prev)
		r := overview(t, a)
		if r.DaysToPay.Avg == nil || *r.DaysToPay.Avg != 15 {
			t.Errorf("avg = %v, want 15", r.DaysToPay.Avg)
		}
		if r.DaysToPay.PreviousAvg == nil || *r.DaysToPay.PreviousAvg != 40 {
			t.Errorf("previous_avg = %v, want 40", r.DaysToPay.PreviousAvg)
		}
	})
	t.Run("a payment dated before issue counts as zero days", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		c := newCustomer(t, a, "Acme")
		in := ovWindow().From.AddDate(0, 0, 30)
		pay := func(issue time.Time) {
			id := inv{cur: "IDR", amount: "100", balance: "0", status: "PAID", issue: d(issue)}.insert(t, a, c)
			paymentFull(t, a, id, "IDR", "100", "0", "CLEARED", in)
		}
		pay(in.AddDate(0, 0, 10)) // paid 10 days before issue: 0
		pay(in.AddDate(0, 0, -20))
		r := overview(t, a)
		if r.DaysToPay.Avg == nil || *r.DaysToPay.Avg != 10 {
			t.Errorf("avg = %v, want 10 (unclamped: 5)", r.DaysToPay.Avg)
		}
	})
	t.Run("nil with nothing paid", func(t *testing.T) {
		t.Parallel()
		r := overview(t, newApp(t))
		if r.DaysToPay.Avg != nil || r.DaysToPay.PreviousAvg != nil {
			t.Errorf("days to pay = %+v, want nils", r.DaysToPay)
		}
	})
}

// reconPayment inserts a payment with the given status, source, score and
// created_at plus, when action is set, one log row by actor (nil = engine).
func reconPayment(t *testing.T, a *apitest.App, status, source string, score *string, created time.Time, action string, actor *uuid.UUID) {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, confidence_score, created_at)
VALUES ($1, 10, 'IDR', 'Acme', '2026-07-01', $2, $3, $4, $5, $6)`, id, source, uuid.NewString(), status, score, created)
	if action != "" {
		exec(t, a, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, actor_user_id, created_at) VALUES ($1, $2, $3, ARRAY['x'], $4, $5)`,
			uuid.New(), id, action, actor, created)
	}
}

func TestOverviewRecon(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	w := ovWindow()
	in := w.From.AddDate(0, 0, 10)
	admin := uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash) VALUES ($1, 'a@example.com', 'x')`, admin)
	s90, s80 := "90", "80"
	reconPayment(t, a, "CLEARED", "WEBHOOK", &s90, in, "CLEARED", nil)
	reconPayment(t, a, "CLEARED", "MANUAL", nil, in, "CLEARED", &admin)
	reconPayment(t, a, "PENDING_MANUAL_REVIEW", "WEBHOOK", &s80, in, "HELD", nil)
	reconPayment(t, a, "FLAGGED", "WEBHOOK", nil, in, "FLAGGED", nil)
	reconPayment(t, a, "CLEARED", "WEBHOOK", &s90, w.From.AddDate(0, 0, -1), "CLEARED", nil)
	c1, c2 := newCustomer(t, a, "A"), newCustomer(t, a, "B")
	exec(t, a, `UPDATE customers SET matching_aliases = '["a","b"]'::jsonb WHERE customer_id = $1`, c1)
	exec(t, a, `UPDATE customers SET matching_aliases = '[]'::jsonb WHERE customer_id = $1`, c2)
	rc := overview(t, a).Reconciliation
	if rc.AutoCleared != 1 || rc.Manual != 1 || rc.Held != 2 {
		t.Errorf("counts = %d/%d/%d, want 1/1/2", rc.AutoCleared, rc.Manual, rc.Held)
	}
	if rc.AvgConfidence == nil || *rc.AvgConfidence != 85 {
		t.Errorf("avg confidence = %v, want 85", rc.AvgConfidence)
	}
	if rc.AliasesInUse != 2 || rc.CustomersWithAliases != 1 {
		t.Errorf("aliases = %d across %d customers, want 2 across 1", rc.AliasesInUse, rc.CustomersWithAliases)
	}
}

func TestOverviewTopCustomers(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	w := ovWindow()
	in := w.From.AddDate(0, 0, 5)
	var overdueCust uuid.UUID
	for i := 1; i <= 12; i++ {
		c := newCustomer(t, a, fmt.Sprintf("Cust %02d", i))
		inv{cur: "IDR", amount: strconv.Itoa(i * 100), status: "PAID", balance: "0", issue: d(in)}.insert(t, a, c)
		if i == 12 {
			overdueCust = c
			inv{cur: "IDR", amount: "40", status: "SENT", issue: d(w.PrevFrom), due: d(ovDay.AddDate(0, 0, -5))}.insert(t, a, c)
		}
	}
	tc := overview(t, a).TopCustomers
	if len(tc.Rows) != 10 {
		t.Fatalf("rows = %d, want 10", len(tc.Rows))
	}
	if tc.Rows[0].Name != "Cust 12" || tc.Rows[0].Amount.Decimal().StringFixed(4) != "1200.0000" || tc.Rows[9].Name != "Cust 03" {
		t.Errorf("order wrong: first %+v last %+v", tc.Rows[0], tc.Rows[9])
	}
	if tc.Rows[0].CustomerID != overdueCust || tc.Rows[0].Overdue.Decimal().StringFixed(4) != "40.0000" || !tc.Rows[1].Overdue.Decimal().IsZero() {
		t.Errorf("overdue wrong: %+v / %+v", tc.Rows[0], tc.Rows[1])
	}
	if tc.Others.Count != 2 || tc.Others.Amount.Decimal().StringFixed(4) != "300.0000" {
		t.Errorf("others = %+v, want 2 / 300", tc.Others)
	}
	sum := tc.Others.Share
	for _, r := range tc.Rows {
		sum += r.Share
	}
	if math.Abs(sum-100) > 0.01 {
		t.Errorf("shares sum to %v", sum)
	}
	// 1200+1100+1000 of 7800
	if tc.Top3Share == nil || math.Abs(*tc.Top3Share-100*3300.0/7800) > 0.1 {
		t.Errorf("top3 = %v", tc.Top3Share)
	}
}

func TestOverviewTopCustomersUnconverted(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme")
	in := ovWindow().From.AddDate(0, 0, 5)
	inv{cur: "IDR", amount: "100", issue: d(in)}.insert(t, a, c)
	inv{cur: "XXX", amount: "999", issue: d(in)}.insert(t, a, c)
	r := overview(t, a)
	if len(r.Unconverted) != 1 || r.Unconverted[0] != "XXX" {
		t.Errorf("unconverted = %v", r.Unconverted)
	}
	if len(r.TopCustomers.Rows) != 1 || r.TopCustomers.Rows[0].Amount.Decimal().StringFixed(4) != "100.0000" {
		t.Errorf("rows = %+v", r.TopCustomers.Rows)
	}
}

func TestOverviewRoute(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	const path = "/api/v1/stats/overview"
	got := apitest.Decode[stats.OverviewResponse](t, a.Do(t, http.MethodGet, path+"?period=90d", nil), http.StatusOK)
	if got.Period != "90d" {
		t.Errorf("period = %q", got.Period)
	}
	if rec := a.Do(t, http.MethodGet, path+"?period=7d", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("7d status %d: %s", rec.Code, rec.Body)
	}
	got = apitest.Decode[stats.OverviewResponse](t, a.Do(t, http.MethodGet, path, nil), http.StatusOK)
	if got.Period != "12m" {
		t.Errorf("default period = %q", got.Period)
	}
}
