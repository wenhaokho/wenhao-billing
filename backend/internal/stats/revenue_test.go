package stats_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

// revDay is the date test_revenue_monthly_base.py runs on; the seeded rates are
// as of 2026-01-01.
var revDay = date(2026, time.July, 24)

// inv is one invoice of the revenue and summary tests; a nil due date or
// issue date is NULL.
type inv struct {
	cur, amount, balance, status string
	issue, due                   *time.Time
	template                     bool
}

func d(t time.Time) *time.Time { return &t }

func (i inv) insert(t *testing.T, a *apitest.App, cust uuid.UUID) uuid.UUID {
	t.Helper()
	if i.status == "" {
		i.status = "SENT"
	}
	if i.cur == "" {
		i.cur = "USD"
	}
	if i.balance == "" {
		i.balance = i.amount
	}
	id := uuid.New()
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, issue_date, due_date, is_template)
VALUES ($1, $2, 'MILESTONE', $3, $4, $5, $6, $7, $8, $9)`, id, cust, i.cur, i.amount, i.balance, i.status, i.issue, i.due, i.template)
	return id
}

func revenue(t *testing.T, a *apitest.App) stats.MonthlyRevenueResponse {
	t.Helper()
	got, err := stats.MonthlyRevenue(t.Context(), a.Pool, "IDR", revDay)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func month(t *testing.T, r stats.MonthlyRevenueResponse, label string) stats.MonthlyRevenueBucket {
	t.Helper()
	for _, m := range r.Months {
		if m.Month == label {
			return m
		}
	}
	t.Fatalf("no month %s in %v", label, r.Months)
	return stats.MonthlyRevenueBucket{}
}

// dict renders a currency dict as "CUR=amount" strings in currency order.
func dict(m map[string]platform.Dec4) []string {
	b, _ := json.Marshal(m)
	var raw map[string]string
	_ = json.Unmarshal(b, &raw)
	out := []string{}
	for _, k := range []string{"GBP", "IDR", "SGD", "USD"} {
		if v, ok := raw[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	if len(out) != len(raw) {
		return append(out, "UNEXPECTED KEYS", string(b))
	}
	return out
}

func TestRevenueLabelsSpan12MonthsOldestFirst(t *testing.T) {
	t.Parallel()
	r := revenue(t, newApp(t))
	if len(r.Months) != 12 || r.Months[0].Month != "2025-08" || r.Months[11].Month != "2026-07" {
		t.Fatalf("months = %v", r.Months)
	}
	for _, m := range r.Months {
		if len(m.Totals)+len(m.NativeTotals)+len(m.UnpaidTotals)+len(m.NativeUnpaidTotals) != 0 {
			t.Fatalf("month %s not empty: %+v", m.Month, m)
		}
	}
	eq(t, "unconverted", r.Unconverted, []string{})
	eq(t, "currencies", r.Currencies, []string{})
}

func TestRevenueBaseCurrencyUsesRateOne(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv{cur: "IDR", amount: "5000000", issue: d(date(2026, 7, 10))}.insert(t, a, newCustomer(t, a, "Acme Corp"))
	r := revenue(t, a)
	jul := month(t, r, "2026-07")
	eq(t, "native", dict(jul.NativeTotals), []string{"IDR=5000000.0000"})
	eq(t, "base", dict(jul.Totals), []string{"IDR=5000000.0000"})
	eq(t, "unconverted", r.Unconverted, []string{})
}

func TestRevenueForeignCurrencyConvertedViaSeededRate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	inv{cur: "USD", amount: "100", issue: d(date(2026, 6, 15))}.insert(t, a, c)
	inv{cur: "SGD", amount: "100", issue: d(date(2026, 6, 20))}.insert(t, a, c)
	r := revenue(t, a)
	jun := month(t, r, "2026-06")
	eq(t, "base", dict(jun.Totals), []string{"SGD=1200000.0000", "USD=1600000.0000"})
	eq(t, "native", dict(jun.NativeTotals), []string{"SGD=100.0000", "USD=100.0000"})
	eq(t, "currencies", r.Currencies, []string{"SGD", "USD"})
}

func TestRevenueMissingRateCurrencyReportedUnconverted(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv{cur: "GBP", amount: "100", issue: d(date(2026, 5, 5))}.insert(t, a, newCustomer(t, a, "Acme Corp"))
	r := revenue(t, a)
	may := month(t, r, "2026-05")
	eq(t, "native", dict(may.NativeTotals), []string{"GBP=100.0000"})
	eq(t, "base", dict(may.Totals), []string{})
	eq(t, "unconverted", r.Unconverted, []string{"GBP"})
	eq(t, "currencies", r.Currencies, []string{})
}

func TestRevenueDraftTemplateAndOutOfWindowExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	inv{cur: "IDR", amount: "111", status: "DRAFT", issue: d(date(2026, 7, 1))}.insert(t, a, c)
	inv{cur: "IDR", amount: "222", template: true, issue: d(date(2026, 7, 1))}.insert(t, a, c)
	inv{cur: "IDR", amount: "333", issue: d(date(2025, 7, 1))}.insert(t, a, c)
	inv{cur: "IDR", amount: "444", status: "VOID", issue: d(date(2026, 7, 1))}.insert(t, a, c)
	inv{cur: "IDR", amount: "555"}.insert(t, a, c) // no issue date
	r := revenue(t, a)
	for _, m := range r.Months {
		if len(m.Totals)+len(m.NativeTotals) != 0 {
			t.Fatalf("month %s = %+v, want empty", m.Month, m)
		}
	}
	eq(t, "unconverted", r.Unconverted, []string{})
}

func TestRevenueDraftIsExcludedAlone(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv{cur: "IDR", amount: "111", status: "DRAFT", issue: d(date(2026, 7, 1))}.insert(t, a, newCustomer(t, a, "Acme Corp"))
	eq(t, "jul", dict(month(t, revenue(t, a), "2026-07").NativeTotals), []string{})
}

func TestRevenueTemplateIsExcludedAlone(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv{cur: "IDR", amount: "222", template: true, issue: d(date(2026, 7, 1))}.insert(t, a, newCustomer(t, a, "Acme Corp"))
	eq(t, "jul", dict(month(t, revenue(t, a), "2026-07").NativeTotals), []string{})
}

func TestRevenueWindowStartIsFirstDayOfOldestMonth(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	inv{cur: "IDR", amount: "10", issue: d(date(2025, 8, 1))}.insert(t, a, c)
	inv{cur: "IDR", amount: "20", issue: d(date(2025, 7, 31))}.insert(t, a, c)
	r := revenue(t, a)
	eq(t, "2025-08", dict(month(t, r, "2025-08").NativeTotals), []string{"IDR=10.0000"})
	// 2025-07-31 is before the window and is not counted.
}

func TestRevenueEveryRevenueStatusCounts(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	for _, s := range []string{"OPEN", "SENT", "PARTIAL", "PAID"} {
		inv{cur: "IDR", amount: "10", status: s, issue: d(date(2026, 7, 1))}.insert(t, a, c)
	}
	eq(t, "jul", dict(month(t, revenue(t, a), "2026-07").NativeTotals), []string{"IDR=40.0000"})
}

func TestRevenueSameCurrencySummedAcrossInvoicesInAMonth(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	inv{cur: "USD", amount: "100", issue: d(date(2026, 3, 3))}.insert(t, a, c)
	inv{cur: "USD", amount: "50", issue: d(date(2026, 3, 28))}.insert(t, a, c)
	mar := month(t, revenue(t, a), "2026-03")
	eq(t, "native", dict(mar.NativeTotals), []string{"USD=150.0000"})
	eq(t, "base", dict(mar.Totals), []string{"USD=2400000.0000"})
}

func TestRevenueUnpaidSplitTracksRemainingBalance(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	inv{cur: "IDR", amount: "1000", issue: d(date(2026, 7, 1))}.insert(t, a, c)
	inv{cur: "USD", amount: "100", balance: "40", status: "PARTIAL", issue: d(date(2026, 7, 2))}.insert(t, a, c)
	inv{cur: "USD", amount: "50", balance: "0", status: "PAID", issue: d(date(2026, 7, 3))}.insert(t, a, c)
	jul := month(t, revenue(t, a), "2026-07")
	eq(t, "native", dict(jul.NativeTotals), []string{"IDR=1000.0000", "USD=150.0000"})
	eq(t, "native unpaid", dict(jul.NativeUnpaidTotals), []string{"IDR=1000.0000", "USD=40.0000"})
	eq(t, "base unpaid", dict(jul.UnpaidTotals), []string{"IDR=1000.0000", "USD=640000.0000"})
}

func TestRevenueFullyPaidMonthHasNoUnpaidEntry(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv{cur: "USD", amount: "50", balance: "0", status: "PAID", issue: d(date(2026, 7, 3))}.insert(t, a, newCustomer(t, a, "Acme Corp"))
	jul := month(t, revenue(t, a), "2026-07")
	eq(t, "native unpaid", dict(jul.NativeUnpaidTotals), []string{})
	eq(t, "base unpaid", dict(jul.UnpaidTotals), []string{})
	eq(t, "native", dict(jul.NativeTotals), []string{"USD=50.0000"})
}

func TestRevenueZeroAmountInvoiceStillCreatesEntry(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv{cur: "USD", amount: "0", status: "PAID", issue: d(date(2026, 7, 3))}.insert(t, a, newCustomer(t, a, "Acme Corp"))
	r := revenue(t, a)
	jul := month(t, r, "2026-07")
	eq(t, "native", dict(jul.NativeTotals), []string{"USD=0.0000"})
	eq(t, "base", dict(jul.Totals), []string{"USD=0.0000"})
	eq(t, "currencies", r.Currencies, []string{"USD"})
}

func TestRevenueUnconvertedCurrencyHasNoBaseUnpaid(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	inv{cur: "GBP", amount: "100", issue: d(date(2026, 5, 5))}.insert(t, a, newCustomer(t, a, "Acme Corp"))
	may := month(t, revenue(t, a), "2026-05")
	eq(t, "native unpaid", dict(may.NativeUnpaidTotals), []string{"GBP=100.0000"})
	eq(t, "base unpaid", dict(may.UnpaidTotals), []string{})
}

func TestRevenueConvertsHalfEvenAtTodaysRate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	exec(t, a, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('EUR', 'IDR', 0.00005, '2026-01-01', 'TEST')`)
	c := newCustomer(t, a, "Acme Corp")
	inv{cur: "EUR", amount: "1", issue: d(date(2026, 7, 1))}.insert(t, a, c) // 0.00005 -> 0.0000, a tie to even
	inv{cur: "EUR", amount: "3", issue: d(date(2026, 6, 1))}.insert(t, a, c) // 0.00015 -> 0.0002
	r := revenue(t, a)
	if got := month(t, r, "2026-07").Totals["EUR"].Decimal().StringFixed(4); got != "0.0000" {
		t.Fatalf("EUR 1 = %s, want 0.0000", got)
	}
	if got := month(t, r, "2026-06").Totals["EUR"].Decimal().StringFixed(4); got != "0.0002" {
		t.Fatalf("EUR 3 = %s, want 0.0002", got)
	}
	// The unpaid conversion rounds the same way (the balance equals the amount).
	if got := month(t, r, "2026-07").UnpaidTotals["EUR"].Decimal().StringFixed(4); got != "0.0000" {
		t.Fatalf("unpaid EUR 1 = %s, want 0.0000", got)
	}
	if got := month(t, r, "2026-06").UnpaidTotals["EUR"].Decimal().StringFixed(4); got != "0.0002" {
		t.Fatalf("unpaid EUR 3 = %s, want 0.0002", got)
	}
}

// requireAdmin checks that path answers 401 without a session and 403 to a
// staff user.
func requireAdmin(t *testing.T, path string) {
	t.Helper()
	a := newApp(t)
	staffID := uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID)
	token, _, err := a.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a
	staff.Cookie = platform.SessionCookie + "=" + token
	if p := apitest.Decode[problem](t, a.Anon(t, http.MethodGet, path, nil), http.StatusUnauthorized); p.Detail != "not authenticated" {
		t.Errorf("anon: %q", p.Detail)
	}
	if p := apitest.Decode[problem](t, staff.Do(t, http.MethodGet, path, nil), http.StatusForbidden); p.Detail != "admin required" {
		t.Errorf("staff: %q", p.Detail)
	}
}

const revPath = "/api/v1/stats/revenue-monthly"

func TestRevenueMonthlyRoute(t *testing.T) {
	t.Parallel()
	a := newApp(t) // the clock is 2026-01-15, so the window is 2025-02..2026-01
	c := newCustomer(t, a, "Acme Corp")
	inv{cur: "USD", amount: "100", balance: "40", status: "PARTIAL", issue: d(date(2026, 1, 5))}.insert(t, a, c)
	inv{cur: "SGD", amount: "10", issue: d(date(2026, 1, 6))}.insert(t, a, c)
	inv{cur: "GBP", amount: "7", issue: d(date(2025, 12, 6))}.insert(t, a, c)

	rec := a.Do(t, http.MethodGet, revPath, nil)
	got := apitest.Decode[stats.MonthlyRevenueResponse](t, rec, http.StatusOK)
	if got.BaseCurrency != "IDR" || len(got.Months) != 12 || got.Months[0].Month != "2025-02" || got.Months[11].Month != "2026-01" {
		t.Fatalf("response = %+v", got)
	}
	eq(t, "currencies", got.Currencies, []string{"SGD", "USD"})
	eq(t, "unconverted", got.Unconverted, []string{"GBP"})
	jan := got.Months[11]
	eq(t, "totals", dict(jan.Totals), []string{"SGD=120000.0000", "USD=1600000.0000"})
	eq(t, "unpaid", dict(jan.UnpaidTotals), []string{"SGD=120000.0000", "USD=640000.0000"})

	body := rec.Body.String()
	if !strings.Contains(body, `"totals":{"SGD":"120000.0000","USD":"1600000.0000"}`) {
		t.Fatalf("totals keys not in currency order: %s", body)
	}
	last := -1
	for _, k := range []string{`"month"`, `"totals"`, `"native_totals"`, `"unpaid_totals"`, `"native_unpaid_totals"`} {
		i := strings.LastIndex(body, k)
		if i <= last {
			t.Fatalf("field %s out of order in %s", k, body)
		}
		last = i
	}
	if strings.Contains(body, "null") {
		t.Fatalf("body has null: %s", body)
	}
}

func TestRevenueMonthlyRequiresAdmin(t *testing.T) {
	t.Parallel()
	requireAdmin(t, revPath)
}

func TestRevenueFutureMonthDropped(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	inv{cur: "USD", amount: "100", issue: d(date(2026, 3, 1))}.insert(t, a, c) // after the clock's 2026-01-15
	got := apitest.Decode[stats.MonthlyRevenueResponse](t, a.Do(t, http.MethodGet, revPath, nil), http.StatusOK)
	for _, m := range got.Months {
		if len(m.NativeTotals)+len(m.Totals) != 0 {
			t.Fatalf("month %s = %+v, want empty", m.Month, m)
		}
	}
	eq(t, "currencies", got.Currencies, []string{})
}
