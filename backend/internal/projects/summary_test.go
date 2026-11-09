package projects_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// summaryOut is the summary as the client sees it, strings kept as sent.
type summaryOut struct {
	ProjectID         uuid.UUID `json:"project_id"`
	InvoicedTotal     string    `json:"invoiced_total"`
	PaidTotal         string    `json:"paid_total"`
	OutstandingTotal  string    `json:"outstanding_total"`
	ContractValue     *string   `json:"contract_value"`
	ContractRemaining *string   `json:"contract_remaining"`
	InvoiceCount      int       `json:"invoice_count"`
	BillTotal         string    `json:"bill_total"`
}

func summaryPath(id uuid.UUID) string { return projectPath(id) + "/summary" }

func projectInvoicesPath(id uuid.UUID) string { return projectPath(id) + "/invoices" }

func getSummary(t *testing.T, a *apitest.App, id uuid.UUID) summaryOut {
	t.Helper()
	return apitest.Decode[summaryOut](t, a.Do(t, http.MethodGet, summaryPath(id), nil), http.StatusOK)
}

// seedInvoice inserts an invoice with the given money, status and currency.
func seedInvoice(t *testing.T, a *apitest.App, customer, project uuid.UUID, status, currency, amount, balance string) {
	t.Helper()
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, project_id, invoice_type, currency, amount, balance_due, status)
VALUES ($1, $2, $3, 'STANDARD', $4, $5::numeric, $6::numeric, $7)`,
		uuid.New(), customer, project, currency, amount, balance, status)
}

func seedBill(t *testing.T, a *apitest.App, vendor, project uuid.UUID, status, currency, amount string) {
	t.Helper()
	exec(t, a, `INSERT INTO bills (bill_id, vendor_id, project_id, currency, amount, balance_due, status)
VALUES ($1, $2, $3, $4, $5::numeric, $5::numeric, $6)`, uuid.New(), vendor, project, currency, amount, status)
}

func insertVendor(t *testing.T, a *apitest.App) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'Vend')`, id)
	return id
}

func str(s string) *string { return &s }

func TestSummaryTotals(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	v := insertVendor(t, a)
	p := create(t, a, body(c, "SUM", map[string]any{"contract_value": "10000"}))
	other := create(t, a, body(c, "OTHER", nil))

	// Counted: OPEN, SENT, PARTIAL, PAID. Not counted: DRAFT, VOID, other project.
	seedInvoice(t, a, c, p.ProjectID, "OPEN", "IDR", "1000.5000", "1000.5000")
	seedInvoice(t, a, c, p.ProjectID, "SENT", "IDR", "2000", "2000")
	seedInvoice(t, a, c, p.ProjectID, "PARTIAL", "IDR", "3000", "1200.2500")
	seedInvoice(t, a, c, p.ProjectID, "PAID", "IDR", "400", "0")
	seedInvoice(t, a, c, p.ProjectID, "DRAFT", "IDR", "90000", "90000")
	seedInvoice(t, a, c, p.ProjectID, "VOID", "IDR", "70000", "70000")
	seedInvoice(t, a, c, other.ProjectID, "OPEN", "IDR", "5555", "5555")
	// Counted bills: OPEN, PARTIAL, PAID. Not: DRAFT, VOID.
	seedBill(t, a, v, p.ProjectID, "OPEN", "IDR", "10.1000")
	seedBill(t, a, v, p.ProjectID, "PARTIAL", "IDR", "20")
	seedBill(t, a, v, p.ProjectID, "PAID", "IDR", "30")
	seedBill(t, a, v, p.ProjectID, "DRAFT", "IDR", "4000")
	seedBill(t, a, v, p.ProjectID, "VOID", "IDR", "5000")
	seedBill(t, a, v, other.ProjectID, "OPEN", "IDR", "6000")

	got := getSummary(t, a, p.ProjectID)
	want := summaryOut{
		ProjectID: p.ProjectID, InvoicedTotal: "6400.5000", PaidTotal: "2199.7500", OutstandingTotal: "4200.7500",
		ContractValue: str("10000.0000"), ContractRemaining: str("3599.5000"), InvoiceCount: 4, BillTotal: "60.1000",
	}
	if got.ProjectID != want.ProjectID || got.InvoicedTotal != want.InvoicedTotal || got.PaidTotal != want.PaidTotal ||
		got.OutstandingTotal != want.OutstandingTotal || got.InvoiceCount != want.InvoiceCount || got.BillTotal != want.BillTotal ||
		got.ContractValue == nil || *got.ContractValue != *want.ContractValue ||
		got.ContractRemaining == nil || *got.ContractRemaining != *want.ContractRemaining {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

// A sum may exceed what one numeric(19,4) column holds (< 10^15): it is
// summed as plain numeric, not cast back to the column's type.
func TestSummarySumsBeyondColumnPrecision(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	v := insertVendor(t, a)
	p := create(t, a, body(c, "BIG", nil))
	for range 2 {
		seedInvoice(t, a, c, p.ProjectID, "OPEN", "IDR", "900000000000000", "900000000000000")
		seedBill(t, a, v, p.ProjectID, "OPEN", "IDR", "900000000000000")
	}
	got := getSummary(t, a, p.ProjectID)
	if got.InvoicedTotal != "1800000000000000.0000" || got.OutstandingTotal != "1800000000000000.0000" ||
		got.PaidTotal != "0.0000" || got.BillTotal != "1800000000000000.0000" {
		t.Errorf("got %+v", got)
	}
}

func TestSummaryContractRemainingCanBeNegative(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "OVER", map[string]any{"contract_value": "100"}))
	seedInvoice(t, a, c, p.ProjectID, "OPEN", "IDR", "250", "250")
	got := getSummary(t, a, p.ProjectID)
	if got.ContractRemaining == nil || *got.ContractRemaining != "-150.0000" {
		t.Errorf("%+v", got)
	}
}

func TestSummaryEmptyProject(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "EMPTY", nil))
	got := getSummary(t, a, p.ProjectID)
	if got.InvoicedTotal != "0.0000" || got.PaidTotal != "0.0000" || got.OutstandingTotal != "0.0000" ||
		got.BillTotal != "0.0000" || got.InvoiceCount != 0 || got.ContractValue != nil || got.ContractRemaining != nil {
		t.Errorf("%+v", got)
	}
	// Every field is present, the nulls included.
	rec := a.Do(t, http.MethodGet, summaryPath(p.ProjectID), nil)
	raw := apitest.Decode[map[string]any](t, rec, http.StatusOK)
	for _, k := range []string{
		"project_id", "invoiced_total", "paid_total", "outstanding_total", "contract_value",
		"contract_remaining", "invoice_count", "bill_total",
	} {
		if _, ok := raw[k]; !ok {
			t.Errorf("no %s: %s", k, rec.Body)
		}
	}
	// With a contract value and nothing invoiced, all of it remains.
	q := create(t, a, body(c, "BUDGET", map[string]any{"contract_value": "500"}))
	if g := getSummary(t, a, q.ProjectID); g.ContractRemaining == nil || *g.ContractRemaining != "500.0000" {
		t.Errorf("%+v", g)
	}
}

// Kept from Python: amounts in any currency are added together.
func TestSummaryMixesCurrencies(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	v := insertVendor(t, a)
	p := create(t, a, body(c, "MIXED", map[string]any{"contract_value": "1000"}))
	seedInvoice(t, a, c, p.ProjectID, "OPEN", "IDR", "100", "100")
	seedInvoice(t, a, c, p.ProjectID, "PAID", "USD", "10", "0")
	seedBill(t, a, v, p.ProjectID, "OPEN", "IDR", "7")
	seedBill(t, a, v, p.ProjectID, "OPEN", "SGD", "3")
	got := getSummary(t, a, p.ProjectID)
	if got.InvoicedTotal != "110.0000" || got.OutstandingTotal != "100.0000" || got.PaidTotal != "10.0000" ||
		got.BillTotal != "10.0000" || got.ContractRemaining == nil || *got.ContractRemaining != "890.0000" {
		t.Errorf("%+v", got)
	}
}

func TestProjectInvoicesExcludeTemplatesAndOrder(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := create(t, a, body(c, "INVS", nil))
	other := create(t, a, body(c, "ELSE", nil))
	ids := map[string]uuid.UUID{}
	for i, s := range []struct {
		name, status string
		template     bool
		project      uuid.UUID
	}{
		{"oldest", "DRAFT", false, p.ProjectID},
		{"void", "VOID", false, p.ProjectID},
		{"template", "DRAFT", true, p.ProjectID},
		{"newest", "PAID", false, p.ProjectID},
		{"elsewhere", "OPEN", false, other.ProjectID},
	} {
		id := uuid.New()
		ids[s.name] = id
		exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, project_id, invoice_type, currency, amount, balance_due, status, is_template, created_at)
VALUES ($1, $2, $3, 'STANDARD', 'IDR', 1, 1, $4, $5, '2026-01-01T00:00:00Z'::timestamptz + $6 * interval '1 hour')`,
			id, c, s.project, s.status, s.template, i)
	}
	got := apitest.Decode[[]invoices.InvoiceOut](t, a.Do(t, http.MethodGet, projectInvoicesPath(p.ProjectID), nil), http.StatusOK)
	if len(got) != 3 || got[0].InvoiceID != ids["newest"] || got[1].InvoiceID != ids["void"] || got[2].InvoiceID != ids["oldest"] {
		t.Errorf("got %d invoices, want newest, void, oldest: %+v", len(got), got)
	}
	// A project with no invoices answers an empty array, not null.
	rec := a.Do(t, http.MethodGet, projectInvoicesPath(create(t, a, body(c, "NONE", nil)).ProjectID), nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" && rec.Body.String() != "[]" {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

func TestSummaryAndInvoices404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodGet, summaryPath(id), nil), http.StatusNotFound, "project_not_found", "project not found", "", "")
	wantProblem(t, a.Do(t, http.MethodGet, projectInvoicesPath(id), nil), http.StatusNotFound, "project_not_found", "project not found", "", "")
}

func TestSummaryAndInvoicesRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	staffID := uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID)
	token, _, err := a.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a
	staff.Cookie = platform.SessionCookie + "=" + token
	id := uuid.New()
	for _, p := range []string{summaryPath(id), projectInvoicesPath(id)} {
		wantProblem(t, a.Anon(t, http.MethodGet, p, nil), http.StatusUnauthorized, "", "not authenticated", "", "")
		wantProblem(t, staff.Do(t, http.MethodGet, p, nil), http.StatusForbidden, "", "admin required", "", "")
	}
}
