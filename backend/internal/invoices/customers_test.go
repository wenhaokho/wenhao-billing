package invoices

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const customersPath = "/api/v1/customers"

// balancesToday is Python's TODAY in test_customer_balances.py.
var balancesToday = time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)

// row is an invoice to store for the customer views; issue and due are
// dates ("" is NULL), created a timestamp ("" is now).
type row struct {
	customer        uuid.UUID
	typ, currency   string
	status          string
	amount, balance string
	issue, due      string
	created         string
	template        bool
}

// newRow is Python's _inv defaults: a SENT milestone of 1000 USD issued
// 2026-04-01 and due 2026-04-15.
func newRow(customer uuid.UUID) row {
	return row{
		customer: customer, typ: "MILESTONE", currency: "USD", status: "SENT",
		amount: "1000", issue: "2026-04-01", due: "2026-04-15",
	}
}

func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func insertRow(t *testing.T, pool *pgxpool.Pool, r row) uuid.UUID {
	t.Helper()
	id := uuid.New()
	balance := r.balance
	if balance == "" {
		balance = r.amount
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO invoices
(invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, issue_date, due_date, is_template, created_at)
VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric, $7, $8::date, $9::date, $10, COALESCE($11::timestamp, CURRENT_TIMESTAMP))`,
		id, r.customer, r.typ, r.currency, r.amount, balance, r.status, orNull(r.issue), orNull(r.due), r.template, orNull(r.created)); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertPayment(t *testing.T, pool *pgxpool.Pool, customer uuid.UUID, currency, amount, wht, status, date string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO payments
(payment_id, customer_id, amount, currency, payer_name, payment_date, intake_source, status, wht_amount)
VALUES ($1, $2, $3::numeric, $4, 'Payer', $5::date, 'MANUAL', $6, $7::numeric)`,
		uuid.New(), customer, amount, currency, date, status, wht); err != nil {
		t.Fatal(err)
	}
}

// balancesAt is CustomerBalances for today.
func balancesAt(t *testing.T, a *apitest.App, today time.Time) []CustomerBalanceRow {
	t.Helper()
	rows, err := CustomerBalances(t.Context(), invoicesdb.New(a.Pool), today)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func wantBalance(t *testing.T, got CustomerBalanceRow, customer uuid.UUID, currency, balance, overdue string) {
	t.Helper()
	if got.CustomerID != customer || got.Currency != currency ||
		!got.Balance.Decimal().Equal(decimal.RequireFromString(balance)) ||
		!got.Overdue.Decimal().Equal(decimal.RequireFromString(overdue)) {
		t.Errorf("row = %s %s %s %s, want %s %s %s %s", got.CustomerID, got.Currency, got.Balance.Decimal(), got.Overdue.Decimal(),
			customer, currency, balance, overdue)
	}
}

// Ports of tests/integration/test_customer_balances.py.

func TestEmptyDBReturnsEmptyList(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	if got := balancesAt(t, a, balancesToday); got == nil || len(got) != 0 {
		t.Errorf("got %v, want an empty, non-nil list", got)
	}
}

func TestCustomerWithNoOpenInvoicesExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	for _, s := range []struct{ status, balance string }{{"DRAFT", ""}, {"VOID", ""}, {"PAID", "0"}} {
		x := newRow(c)
		x.status, x.balance = s.status, s.balance
		insertRow(t, a.Pool, x)
	}
	if got := balancesAt(t, a, balancesToday); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestSentInvoiceNotYetDueContributesToBalanceOnly(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	x := newRow(c)
	x.due, x.balance = "2026-05-02", "500"
	insertRow(t, a.Pool, x)
	got := balancesAt(t, a, balancesToday)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	wantBalance(t, got[0], c, "USD", "500", "0")
}

func TestSentInvoicePastDueContributesToBothBalanceAndOverdue(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	x := newRow(c)
	x.due, x.balance = "2026-04-19", "700"
	insertRow(t, a.Pool, x)
	got := balancesAt(t, a, balancesToday)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	wantBalance(t, got[0], c, "USD", "700", "700")
}

func TestPartialUsesBalanceDueNotAmount(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	x := newRow(c)
	x.status, x.due, x.amount, x.balance = "PARTIAL", "2026-04-21", "1000", "250"
	insertRow(t, a.Pool, x)
	got := balancesAt(t, a, balancesToday)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	wantBalance(t, got[0], c, "USD", "250", "250")
}

func TestSumsAcrossMultipleOpenInvoicesSameCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	for _, s := range []struct{ due, balance string }{{"2026-04-21", "100"}, {"2026-04-27", "400"}} {
		x := newRow(c)
		x.due, x.balance = s.due, s.balance
		insertRow(t, a.Pool, x)
	}
	got := balancesAt(t, a, balancesToday)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	wantBalance(t, got[0], c, "USD", "500", "100")
}

func TestDraftVoidPaidTemplateAllExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	for _, s := range []struct {
		status, balance string
		template        bool
	}{{"DRAFT", "1", false}, {"VOID", "1", false}, {"PAID", "0", false}, {"SENT", "1", true}} {
		x := newRow(c)
		x.status, x.balance, x.template = s.status, s.balance, s.template
		insertRow(t, a.Pool, x)
	}
	if got := balancesAt(t, a, balancesToday); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestZeroBalanceRowsExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	x := newRow(c)
	x.due, x.balance = "2026-04-21", "0"
	insertRow(t, a.Pool, x)
	if got := balancesAt(t, a, balancesToday); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestMultipleCurrenciesProduceSeparateRows(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	usd := newRow(c)
	usd.due, usd.balance = "2026-04-27", "100"
	sgd := newRow(c)
	sgd.currency, sgd.due, sgd.balance = "SGD", "2026-04-21", "800"
	insertRow(t, a.Pool, usd)
	insertRow(t, a.Pool, sgd)
	got := balancesAt(t, a, balancesToday)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	// Ordered by currency: SGD before USD.
	wantBalance(t, got[0], c, "SGD", "800", "800")
	wantBalance(t, got[1], c, "USD", "100", "0")
}

func TestMultipleCustomersProduceSeparateRows(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c1 := insertCustomer(t, a.Pool, "NONE", "", false)
	c2 := insertCustomer(t, a.Pool, "NONE", "", false)
	x1 := newRow(c1)
	x1.due, x1.balance = "2026-04-27", "100"
	x2 := newRow(c2)
	x2.due, x2.balance = "2026-04-21", "300"
	insertRow(t, a.Pool, x1)
	insertRow(t, a.Pool, x2)
	got := balancesAt(t, a, balancesToday)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	// Ordered by customer id, whichever the ids are.
	want := map[uuid.UUID][2]string{c1: {"100", "0"}, c2: {"300", "300"}}
	first, second := c1, c2
	if second.String() < first.String() {
		first, second = second, first
	}
	wantBalance(t, got[0], first, "USD", want[first][0], want[first][1])
	wantBalance(t, got[1], second, "USD", want[second][0], want[second][1])
}

func TestBalancesRoute(t *testing.T) {
	t.Parallel()
	a := newApp(t) // clock: 2026-01-15T09:00Z
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	due := newRow(c)
	due.due, due.balance = "2026-01-15", "40" // due today: not overdue
	late := newRow(c)
	late.due, late.balance = "2026-01-14", "60"
	nodue := newRow(c) // no due date: never overdue
	nodue.due, nodue.balance = "", "5"
	insertRow(t, a.Pool, due)
	insertRow(t, a.Pool, late)
	insertRow(t, a.Pool, nodue)

	rec := a.Do(t, http.MethodGet, customersPath+"/balances", nil)
	got := apitest.Decode[[]CustomerBalanceRow](t, rec, http.StatusOK)
	if len(got) != 1 {
		t.Fatalf("got %s", rec.Body)
	}
	wantBalance(t, got[0], c, "USD", "105", "60")
	if !strings.Contains(rec.Body.String(), `"balance":"105.0000"`) || !strings.Contains(rec.Body.String(), `"overdue":"60.0000"`) {
		t.Errorf("not four-place strings: %s", rec.Body)
	}

	empty := newApp(t)
	if body := strings.TrimSpace(empty.Do(t, http.MethodGet, customersPath+"/balances", nil).Body.String()); body != "[]" {
		t.Errorf("empty = %s, want []", body)
	}
}

// bothApp registers the customers routes with the invoices routes, in the
// given order.
func bothApp(t *testing.T, invoicesFirst bool) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) {
		if invoicesFirst {
			Register(api, d.Pool, d.Clock, nil)
			customers.Register(api, d.Pool)
			return
		}
		customers.Register(api, d.Pool)
		Register(api, d.Pool, d.Clock, nil)
	})
}

// TestBalancesNotSwallowedByCustomerID: /customers/balances is its own
// route, never customers' /{customer_id} (which would answer 422 for "balances"
// not being a UUID), in either registration order; the customers routes keep
// working beside it.
func TestBalancesNotSwallowedByCustomerID(t *testing.T) {
	t.Parallel()
	for _, invoicesFirst := range []bool{true, false} {
		name := "customers first"
		if invoicesFirst {
			name = "invoices first"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := bothApp(t, invoicesFirst)
			c := insertCustomer(t, a.Pool, "NONE", "", false)
			x := newRow(c)
			x.balance = "9"
			insertRow(t, a.Pool, x)
			got := apitest.Decode[[]CustomerBalanceRow](t, a.Do(t, http.MethodGet, customersPath+"/balances", nil), http.StatusOK)
			if len(got) != 1 || got[0].CustomerID != c {
				t.Errorf("not the balances handler: %+v", got)
			}
			if rec := a.Do(t, http.MethodGet, customersPath+"/"+c.String(), nil); rec.Code != http.StatusOK {
				t.Errorf("customers' own route: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func summaryPath(c uuid.UUID) string { return customersPath + "/" + c.String() + "/billing-summary" }

func customerInvoicesPath(c uuid.UUID) string {
	return customersPath + "/" + c.String() + "/invoices"
}

func summaryOf(t *testing.T, a *apitest.App, c uuid.UUID) BillingSummary {
	t.Helper()
	rec := a.Do(t, http.MethodGet, summaryPath(c), nil)
	s := apitest.Decode[BillingSummary](t, rec, http.StatusOK)
	for _, k := range []string{"milestone", "recurring", "usage", "unpaid_invoices", "total_unpaid", "paid_last_12_months"} {
		if strings.Contains(rec.Body.String(), `"`+k+`":null`) {
			t.Errorf("%s is null: %s", k, rec.Body)
		}
	}
	return s
}

func idsOf(list []InvoiceOut) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(list))
	for _, inv := range list {
		ids = append(ids, inv.InvoiceID)
	}
	return ids
}

func wantAmounts(t *testing.T, name string, got []CurrencyAmount, want ...string) {
	t.Helper()
	// want is currency, amount pairs.
	if len(got) != len(want)/2 {
		t.Errorf("%s = %+v, want %v", name, got, want)
		return
	}
	for i, g := range got {
		if g.Currency != want[2*i] || !g.Amount.Decimal().Equal(decimal.RequireFromString(want[2*i+1])) {
			t.Errorf("%s[%d] = %s %s, want %s %s", name, i, g.Currency, g.Amount.Decimal(), want[2*i], want[2*i+1])
		}
	}
}

func TestBillingSummary(t *testing.T) {
	t.Parallel()
	a := newApp(t) // clock: 2026-01-15T09:00Z
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	other := insertCustomer(t, a.Pool, "NONE", "", false)

	mk := func(typ, currency, status, balance, due string) uuid.UUID {
		x := newRow(c)
		x.typ, x.currency, x.status, x.balance, x.due = typ, currency, status, balance, due
		return insertRow(t, a.Pool, x)
	}
	m1 := mk("MILESTONE", "USD", "OPEN", "100", "2026-03-01")
	m2 := mk("MILESTONE", "IDR", "DRAFT", "2000", "2026-02-01")
	rc := mk("RECURRING", "USD", "SENT", "50.5", "2026-02-15")
	us := mk("USAGE", "SGD", "PARTIAL", "7", "")
	mk("MILESTONE", "USD", "PAID", "0", "2026-01-01")
	mk("MILESTONE", "USD", "VOID", "999", "2026-01-01")
	foreign := newRow(other)
	insertRow(t, a.Pool, foreign)

	s := summaryOf(t, a, c)
	wantIDs(t, "milestone", idsOf(s.Milestone), m2, m1)
	wantIDs(t, "recurring", idsOf(s.Recurring), rc)
	wantIDs(t, "usage", idsOf(s.Usage), us)
	// Due date ascending, a missing one last.
	wantIDs(t, "unpaid_invoices", idsOf(s.UnpaidInvoices), m2, rc, m1, us)
	// Per currency, by currency, DRAFT balances included.
	wantAmounts(t, "total_unpaid", s.TotalUnpaid, "IDR", "2000", "SGD", "7", "USD", "150.5")
	wantAmounts(t, "paid_last_12_months", s.PaidLast12Months)
	wantDate(t, "last_invoice_issue_date", s.LastInvoiceIssueDate, "2026-04-01")
	if s.LastInvoiceID == nil {
		t.Error("no last_invoice_id")
	}
}

func TestBillingSummaryOpenInvoicesTieBreak(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	mk := func(created string) uuid.UUID {
		x := newRow(c)
		x.status, x.created = "OPEN", created
		return insertRow(t, a.Pool, x)
	}
	// One due date: the older invoice first.
	newer := mk("2026-01-02 00:00:00")
	older := mk("2026-01-01 00:00:00")
	wantIDs(t, "unpaid_invoices", idsOf(summaryOf(t, a, c).UnpaidInvoices), older, newer)
}

func TestBillingSummaryTwelveMonthWindow(t *testing.T) {
	t.Parallel()
	a := newApp(t) // today 2026-01-15: the window opens 2025-01-15
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	other := insertCustomer(t, a.Pool, "NONE", "", false)
	pay := func(customer uuid.UUID, currency, amount, wht, status, date string) {
		insertPayment(t, a.Pool, customer, currency, amount, wht, status, date)
	}
	pay(c, "USD", "100", "0", "CLEARED", "2025-01-15")  // exactly 365 days ago: in
	pay(c, "USD", "1000", "0", "CLEARED", "2025-01-14") // 366 days: out
	pay(c, "USD", "5", "0", "CLEARED", "2026-01-15")    // today: in
	pay(c, "USD", "7", "3", "CLEARED", "2026-01-20")    // future: in; cash only, no withholding
	pay(c, "IDR", "20", "0", "CLEARED", "2025-06-01")   // another currency
	pay(c, "USD", "400", "0", "PENDING_MANUAL_REVIEW", "2025-06-01")
	pay(c, "USD", "800", "0", "REJECTED", "2025-06-01")
	pay(other, "USD", "9000", "0", "CLEARED", "2025-06-01")

	s := summaryOf(t, a, c)
	wantAmounts(t, "paid_last_12_months", s.PaidLast12Months, "IDR", "20", "USD", "112")
}

func TestBillingSummaryLastInvoice(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	mk := func(issue, created string) uuid.UUID {
		x := newRow(c)
		x.status, x.issue, x.created = "PAID", issue, created
		x.balance = "0"
		return insertRow(t, a.Pool, x)
	}
	mk("2026-03-01", "2026-03-01 00:00:00")
	latest := mk("2026-05-01", "2026-05-01 00:00:00")
	mk("2026-04-01", "2026-06-01 00:00:00")
	s := summaryOf(t, a, c)
	wantDate(t, "last_invoice_issue_date", s.LastInvoiceIssueDate, "2026-05-01")
	if s.LastInvoiceID == nil || *s.LastInvoiceID != latest {
		t.Errorf("last_invoice_id = %v, want %s", s.LastInvoiceID, latest)
	}

	// Same issue date: the newer one.
	newer := mk("2026-05-01", "2026-05-01 12:00:00")
	s = summaryOf(t, a, c)
	if s.LastInvoiceID == nil || *s.LastInvoiceID != newer {
		t.Errorf("last_invoice_id = %v, want %s (the newer)", s.LastInvoiceID, newer)
	}

	// A NULL issue date sorts first, as Postgres orders Python's DESC.
	undated := mk("", "2026-01-01 00:00:00")
	s = summaryOf(t, a, c)
	if s.LastInvoiceID == nil || *s.LastInvoiceID != undated || s.LastInvoiceIssueDate != nil {
		t.Errorf("last = %v %v, want the undated %s with a null date", s.LastInvoiceID, s.LastInvoiceIssueDate, undated)
	}
}

func TestBillingSummaryEmptyCustomer(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	rec := a.Do(t, http.MethodGet, summaryPath(c), nil)
	body := rec.Body.String()
	for _, k := range []string{"milestone", "recurring", "usage", "unpaid_invoices", "total_unpaid", "paid_last_12_months"} {
		if !strings.Contains(body, `"`+k+`":[]`) {
			t.Errorf("%s is not []: %s", k, body)
		}
	}
	for _, k := range []string{"last_invoice_issue_date", "last_invoice_id"} {
		if !strings.Contains(body, `"`+k+`":null`) {
			t.Errorf("%s is not null: %s", k, body)
		}
	}
}

// TestBillingSummaryIncludesTemplates pins the Python behaviour kept: a
// recurring template is a DRAFT with its amount owing, and the summary and
// the customer's invoice list do not leave it out.
func TestBillingSummaryIncludesTemplates(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	x := newRow(c)
	x.typ, x.status, x.amount, x.template, x.currency = "RECURRING", "DRAFT", "300", true, "IDR"
	template := insertRow(t, a.Pool, x)

	s := summaryOf(t, a, c)
	wantIDs(t, "recurring", idsOf(s.Recurring), template)
	wantIDs(t, "unpaid_invoices", idsOf(s.UnpaidInvoices), template)
	wantAmounts(t, "total_unpaid", s.TotalUnpaid, "IDR", "300")
	wantIDs(t, "invoices", idsOf(apitest.Decode[[]InvoiceOut](t, a.Do(t, http.MethodGet, customerInvoicesPath(c), nil), http.StatusOK)), template)
	// The balances view, in contrast, never counts a template.
	if got := balancesAt(t, a, balancesToday); len(got) != 0 {
		t.Errorf("balances = %+v", got)
	}
}

func TestCustomerInvoicesOrder(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	other := insertCustomer(t, a.Pool, "NONE", "", false)
	mk := func(customer uuid.UUID, issue, created string) uuid.UUID {
		x := newRow(customer)
		x.issue, x.created = issue, created
		return insertRow(t, a.Pool, x)
	}
	old := mk(c, "2026-01-01", "2026-01-01 00:00:00")
	new1 := mk(c, "2026-03-01", "2026-03-01 00:00:00")
	new2 := mk(c, "2026-03-01", "2026-03-02 00:00:00")
	undated := mk(c, "", "2026-01-01 00:00:00")
	mk(other, "2026-02-01", "2026-02-01 00:00:00")
	// Two with everything equal: the id, descending, decides.
	tie1 := mk(c, "2026-02-01", "2026-02-01 00:00:00")
	tie2 := mk(c, "2026-02-01", "2026-02-01 00:00:00")
	tieFirst, tieSecond := tie1, tie2
	if tie2.String() > tie1.String() {
		tieFirst, tieSecond = tie2, tie1
	}

	rec := a.Do(t, http.MethodGet, customerInvoicesPath(c), nil)
	got := idsOf(apitest.Decode[[]InvoiceOut](t, rec, http.StatusOK))
	wantIDs(t, "invoices", got, undated, new2, new1, tieFirst, tieSecond, old)

	empty := insertCustomer(t, a.Pool, "NONE", "", false)
	if body := strings.TrimSpace(a.Do(t, http.MethodGet, customerInvoicesPath(empty), nil).Body.String()); body != "[]" {
		t.Errorf("empty = %s, want []", body)
	}
}

func TestCustomerViews404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	unknown := uuid.New()
	for _, p := range []string{summaryPath(unknown), customerInvoicesPath(unknown)} {
		wantProblem(t, a.Do(t, http.MethodGet, p, nil), http.StatusNotFound, "customer_not_found", "customer not found", "", "")
	}
}

func TestCustomerViewsRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	staffID := uuid.New()
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID); err != nil {
		t.Fatal(err)
	}
	token, _, err := a.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a
	staff.Cookie = platform.SessionCookie + "=" + token
	id := uuid.New()
	for _, p := range []string{customersPath + "/balances", summaryPath(id), customerInvoicesPath(id)} {
		wantProblem(t, a.Anon(t, http.MethodGet, p, nil), http.StatusUnauthorized, "", "not authenticated", "", "")
		wantProblem(t, staff.Do(t, http.MethodGet, p, nil), http.StatusForbidden, "", "admin required", "", "")
	}
}
