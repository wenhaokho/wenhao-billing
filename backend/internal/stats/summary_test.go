package stats_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

// sumDay is the date test_invoices_summary.py runs on.
var sumDay = date(2026, time.April, 22)

func ago(base time.Time, days int) *time.Time { return d(base.AddDate(0, 0, days)) }

// sentDue is the suite's default invoice: SENT, USD 1000, issued 2026-04-01.
func sentDue(due *time.Time, amount string) inv {
	return inv{cur: "USD", amount: amount, status: "SENT", issue: d(date(2026, 4, 1)), due: due}
}

// pay records a payment of the invoice on the given date.
func pay(t *testing.T, a *apitest.App, invoice uuid.UUID, on time.Time, status string) {
	t.Helper()
	exec(t, a, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status)
VALUES ($1, $2, 10, 'USD', 'Acme Corp', $3, 'TEST', $4, $5)`, uuid.New(), invoice, on, uuid.NewString(), status)
}

func summary(t *testing.T, a *apitest.App) stats.InvoicesSummaryResponse {
	t.Helper()
	got, err := stats.InvoicesSummary(t.Context(), a.Pool, sumDay)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func avg(t *testing.T, s stats.InvoicesSummaryResponse) string {
	t.Helper()
	if s.AvgDaysToPay == nil {
		return "null"
	}
	b, _ := json.Marshal(*s.AvgDaysToPay)
	return string(b)
}

func TestSummaryEmptyDBReturnsZeroBucketsAndNullAvg(t *testing.T) {
	t.Parallel()
	s := summary(t, newApp(t))
	eq(t, "overdue", amounts(s.Overdue), []string{})
	eq(t, "due_30d", amounts(s.Due30d), []string{})
	if avg(t, s) != "null" {
		t.Fatalf("avg = %s", avg(t, s))
	}
}

func TestSummarySentInvoicePastDueAppearsInOverdue(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	sentDue(ago(sumDay, -5), "1000").insert(t, a, newCustomer(t, a, "Acme Corp"))
	s := summary(t, a)
	eq(t, "overdue", amounts(s.Overdue), []string{"USD 1000.0000"})
	eq(t, "due_30d", amounts(s.Due30d), []string{})
}

func TestSummaryPartialInvoicePastDueUsesBalanceDueNotAmount(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	i := sentDue(ago(sumDay, -2), "1000")
	i.status, i.balance = "PARTIAL", "400"
	i.insert(t, a, newCustomer(t, a, "Acme Corp"))
	eq(t, "overdue", amounts(summary(t, a).Overdue), []string{"USD 400.0000"})
}

func TestSummaryOverdueSumsMultipleInvoicesSameCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	sentDue(ago(sumDay, -1), "300").insert(t, a, c)
	sentDue(ago(sumDay, -10), "700").insert(t, a, c)
	eq(t, "overdue", amounts(summary(t, a).Overdue), []string{"USD 1000.0000"})
}

func TestSummaryOverdueSeparatesByCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	for cur, amt := range map[string]string{"USD": "500", "SGD": "800"} {
		i := sentDue(ago(sumDay, -1), amt)
		i.cur = cur
		i.insert(t, a, c)
	}
	eq(t, "overdue", amounts(summary(t, a).Overdue), []string{"SGD 800.0000", "USD 500.0000"})
}

func TestSummarySentInvoiceDueTodayIsInDue30dNotOverdue(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	sentDue(d(sumDay), "500").insert(t, a, newCustomer(t, a, "Acme Corp"))
	s := summary(t, a)
	eq(t, "overdue", amounts(s.Overdue), []string{})
	eq(t, "due_30d", amounts(s.Due30d), []string{"USD 500.0000"})
}

func TestSummarySentInvoiceDueIn30DaysInclusive(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	sentDue(ago(sumDay, 30), "600").insert(t, a, newCustomer(t, a, "Acme Corp"))
	eq(t, "due_30d", amounts(summary(t, a).Due30d), []string{"USD 600.0000"})
}

func TestSummarySentInvoiceDueIn31DaysExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	sentDue(ago(sumDay, 31), "999").insert(t, a, newCustomer(t, a, "Acme Corp"))
	s := summary(t, a)
	eq(t, "overdue", amounts(s.Overdue), []string{})
	eq(t, "due_30d", amounts(s.Due30d), []string{})
}

func TestSummaryDraftInvoiceExcludedFromBuckets(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	for _, off := range []int{-5, 5} {
		i := sentDue(ago(sumDay, off), "500")
		i.status = "DRAFT"
		i.insert(t, a, c)
	}
	s := summary(t, a)
	eq(t, "overdue", amounts(s.Overdue), []string{})
	eq(t, "due_30d", amounts(s.Due30d), []string{})
}

func TestSummaryVoidInvoiceExcludedFromBuckets(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	for _, off := range []int{-5, 5} {
		i := sentDue(ago(sumDay, off), "500")
		i.status = "VOID"
		i.insert(t, a, c)
	}
	s := summary(t, a)
	eq(t, "overdue", amounts(s.Overdue), []string{})
	eq(t, "due_30d", amounts(s.Due30d), []string{})
}

func TestSummaryTemplateInvoiceExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	for _, off := range []int{-5, 5} {
		i := sentDue(ago(sumDay, off), "500")
		i.template = true
		i.insert(t, a, c)
	}
	s := summary(t, a)
	eq(t, "overdue", amounts(s.Overdue), []string{})
	eq(t, "due_30d", amounts(s.Due30d), []string{})
}

func TestSummaryPaidInvoiceExcludedFromOverdueAndDue30d(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	i := sentDue(ago(sumDay, -5), "1000")
	i.status, i.balance = "PAID", "0"
	id := i.insert(t, a, newCustomer(t, a, "Acme Corp"))
	pay(t, a, id, *ago(sumDay, -3), "CLEARED")
	s := summary(t, a)
	eq(t, "overdue", amounts(s.Overdue), []string{})
	eq(t, "due_30d", amounts(s.Due30d), []string{})
}

func TestSummaryZeroBalanceAndNullDueDateExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	z := sentDue(ago(sumDay, -5), "100")
	z.balance = "0"
	z.insert(t, a, c)
	sentDue(nil, "200").insert(t, a, c)
	soon := sentDue(ago(sumDay, 5), "100")
	soon.balance = "0"
	soon.insert(t, a, c) // zero balance due in 5 days: not in due_30d either
	s := summary(t, a)
	eq(t, "overdue", amounts(s.Overdue), []string{})
	eq(t, "due_30d", amounts(s.Due30d), []string{})
}

// paidInvoice is a PAID invoice issued issueAgo days before sumDay and paid
// payAgo days before it, returning its id.
func paidInvoice(t *testing.T, a *apitest.App, c uuid.UUID, issueAgo, payAgo int, status string) uuid.UUID {
	t.Helper()
	id := inv{cur: "USD", amount: "1000", balance: "0", status: "PAID", issue: ago(sumDay, -issueAgo), due: d(sumDay)}.insert(t, a, c)
	pay(t, a, id, *ago(sumDay, -payAgo), status)
	return id
}

func TestSummaryAvgDaysToPaySingleInvoice(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	paidInvoice(t, a, newCustomer(t, a, "Acme Corp"), 20, 10, "CLEARED")
	if got := avg(t, summary(t, a)); got != "10" {
		t.Fatalf("avg = %s, want 10", got)
	}
}

func TestSummaryAvgDaysToPayAveragesAcrossInvoices(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	paidInvoice(t, a, c, 30, 20, "CLEARED") // 10 days
	paidInvoice(t, a, c, 30, 10, "CLEARED") // 20 days
	if got := avg(t, summary(t, a)); got != "15" {
		t.Fatalf("avg = %s, want 15", got)
	}
}

func TestSummaryAvgDaysToPayIgnoresPaymentsOlderThan90Days(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	paidInvoice(t, a, newCustomer(t, a, "Acme Corp"), 200, 180, "CLEARED")
	if got := avg(t, summary(t, a)); got != "null" {
		t.Fatalf("avg = %s, want null", got)
	}
}

func TestSummaryAvgDaysToPayWindowEdgeIs90DaysInclusive(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	paidInvoice(t, a, c, 100, 90, "CLEARED") // 10 days, on the edge: counted
	paidInvoice(t, a, c, 191, 91, "CLEARED") // 100 days, a day too old
	if got := avg(t, summary(t, a)); got != "10" {
		t.Fatalf("avg = %s, want 10", got)
	}
}

func TestSummaryAvgDaysToPayIgnoresNonClearedPayments(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	paidInvoice(t, a, newCustomer(t, a, "Acme Corp"), 20, 10, "PENDING_MANUAL_REVIEW")
	if got := avg(t, summary(t, a)); got != "null" {
		t.Fatalf("avg = %s, want null", got)
	}
}

func TestSummaryAvgDaysToPayOnlyCountsPaidInvoices(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	i := sentDue(d(sumDay), "1000")
	i.issue = ago(sumDay, -20)
	id := i.insert(t, a, c) // SENT with a cleared payment
	pay(t, a, id, *ago(sumDay, -10), "CLEARED")
	if got := avg(t, summary(t, a)); got != "null" {
		t.Fatalf("avg = %s, want null", got)
	}
}

func TestSummaryAvgDaysToPayCountsEachPayment(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	id := paidInvoice(t, a, c, 30, 20, "CLEARED") // 10 days
	pay(t, a, id, *ago(sumDay, -10), "CLEARED")   // 20 days, the same invoice
	paidInvoice(t, a, c, 30, 20, "CLEARED")       // 10 days
	// (10 + 20 + 10) / 3 per payment; per invoice it would be 15.
	if got := avg(t, summary(t, a)); got != "13.333333333333334" {
		t.Fatalf("avg = %s, want 13.333333333333334", got)
	}
}

const sumPath = "/api/v1/stats/invoices-summary"

func TestInvoicesSummaryRoute(t *testing.T) {
	t.Parallel()
	a := newApp(t) // the clock is 2026-01-15
	c := newCustomer(t, a, "Acme Corp")
	today := date(2026, 1, 15)
	sentDue(ago(today, -1), "300").insert(t, a, c)
	i := sentDue(ago(today, 30), "500")
	i.cur = "SGD"
	i.insert(t, a, c)
	// Per payment: 10, 10 and 20 days.
	for _, days := range []int{10, 10, 20} {
		id := inv{cur: "USD", amount: "1", balance: "0", status: "PAID", issue: ago(today, -30), due: d(today)}.insert(t, a, c)
		pay(t, a, id, *ago(today, days-30), "CLEARED")
	}

	rec := a.Do(t, http.MethodGet, sumPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	var got struct {
		Overdue []struct{ Currency, Amount string } `json:"overdue"`
		Due30d  []struct{ Currency, Amount string } `json:"due_30d"`
		Avg     *float64                            `json:"avg_days_to_pay"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Overdue) != 1 || got.Overdue[0].Currency != "USD" || got.Overdue[0].Amount != "300.0000" ||
		len(got.Due30d) != 1 || got.Due30d[0].Currency != "SGD" || got.Due30d[0].Amount != "500.0000" {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(body, `"avg_days_to_pay":13.333333333333334`) {
		t.Fatalf("avg_days_to_pay is not the JSON number 13.333333333333334: %s", body)
	}
	last := -1
	for _, k := range []string{`"overdue"`, `"due_30d"`, `"avg_days_to_pay"`} {
		idx := strings.Index(body, k)
		if idx <= last {
			t.Fatalf("field %s out of order in %s", k, body)
		}
		last = idx
	}
}

func TestInvoicesSummaryRouteEmptyIsNullAvgAndEmptyLists(t *testing.T) {
	t.Parallel()
	rec := newApp(t).Do(t, http.MethodGet, sumPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	b := strings.ReplaceAll(rec.Body.String(), " ", "")
	if !strings.Contains(b, `"overdue":[]`) || !strings.Contains(b, `"due_30d":[]`) || !strings.Contains(b, `"avg_days_to_pay":null`) {
		t.Fatalf("body = %s", b)
	}
}

func TestInvoicesSummaryRequiresAdmin(t *testing.T) {
	t.Parallel()
	requireAdmin(t, sumPath)
}
