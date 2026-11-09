package invoices

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const invoicesPath = "/api/v1/invoices"

func invoicePath(id uuid.UUID) string { return invoicesPath + "/" + id.String() }

type problem struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
	Errors []struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	} `json:"errors"`
}

const invalid = "Request validation failed"

// wantProblem checks the status and detail, the code when one is given, and
// that the errors list location with message when a location is given.
func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code, detail, location, message string) {
	t.Helper()
	p := apitest.Decode[problem](t, rec, status)
	if p.Detail != detail {
		t.Errorf("detail %q, want %q: %s", p.Detail, detail, rec.Body)
	}
	if code != "" && p.Code != code {
		t.Errorf("code %q, want %q: %s", p.Code, code, rec.Body)
	}
	if location == "" {
		return
	}
	for _, e := range p.Errors {
		if e.Location == location && e.Message == message {
			return
		}
	}
	t.Errorf("no error %q %q: %s", location, message, rec.Body)
}

// insertCustomer stores a customer with the given withholding defaults
// (rate "" is null).
func insertCustomer(t *testing.T, pool *pgxpool.Pool, whtType, rate string, grossUp bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var r *string
	if rate != "" {
		r = &rate
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO customers (customer_id, name, default_wht_type, default_wht_rate, default_wht_gross_up)
VALUES ($1, 'Wht Customer', $2, $3::numeric, $4)`, id, whtType, r, grossUp); err != nil {
		t.Fatal(err)
	}
	return id
}

func lineBody(price string) map[string]any {
	return map[string]any{"description": "Work", "quantity": "1", "unit_price": price}
}

// createBody is a create body for customer: IDR, one line at price, plus
// extra.
func createBody(customer uuid.UUID, price string, extra map[string]any) map[string]any {
	b := map[string]any{"customer_id": customer, "currency": "IDR", "line_items": []any{lineBody(price)}}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func post(t *testing.T, a *apitest.App, body map[string]any) InvoiceOut {
	t.Helper()
	return apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath, body), http.StatusCreated)
}

func patch(t *testing.T, a *apitest.App, id uuid.UUID, body map[string]any) InvoiceOut {
	t.Helper()
	return apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPatch, invoicePath(id), body), http.StatusOK)
}

func get(t *testing.T, a *apitest.App, id uuid.UUID) InvoiceOut {
	t.Helper()
	return apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodGet, invoicePath(id), nil), http.StatusOK)
}

// wantDec4 checks a decimal's value (any scale); want "" means null.
func wantDec4(t *testing.T, name string, got *platform.Dec4, want string) {
	t.Helper()
	switch {
	case want == "" && got == nil:
	case want == "" || got == nil:
		t.Errorf("%s = %v, want %q", name, got, want)
	case !got.Decimal().Equal(decimal.RequireFromString(want)):
		t.Errorf("%s = %s, want %s", name, got.Decimal(), want)
	}
}

func p4(d platform.Dec4) *platform.Dec4 { return &d }

func wantDate(t *testing.T, name string, got *platform.Date, want string) {
	t.Helper()
	if got == nil || got.Format(time.DateOnly) != want {
		t.Errorf("%s = %v, want %s", name, got, want)
	}
}

// Ports of tests/integration/test_invoice_withholding.py.

func TestCreateInvoiceGrossUpPph23(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	out := post(t, a, createBody(c, "10000000", map[string]any{"wht_type": "PPH23", "wht_gross_up": true}))
	wantDec4(t, "amount", p4(out.Amount), "10204082")
	wantDec4(t, "balance_due", p4(out.BalanceDue), "10204082")
	wantDec4(t, "wht_amount", p4(out.WhtAmount), "204082")
	wantDec4(t, "wht_base", out.WhtBase, "10000000")
	wantDec4(t, "wht_rate", out.WhtRate, "2")
	wantDec4(t, "expected_cash", p4(out.ExpectedCash), "10000000")
}

func TestCreateInvoiceInheritsCustomerDefault(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "PPH21", "", true)
	// Left out and sent as null both take the customer's default.
	for _, extra := range []map[string]any{nil, {"wht_type": nil, "wht_rate": nil, "wht_gross_up": nil}} {
		out := post(t, a, createBody(c, "10000000", extra))
		if out.WhtType != "PPH21" || !out.WhtGrossUp {
			t.Errorf("wht = %s, %v", out.WhtType, out.WhtGrossUp)
		}
		wantDec4(t, "wht_rate", out.WhtRate, "2.5")
	}
}

func TestExplicitNoneOverridesCustomerDefault(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "PPH23", "", true)
	out := post(t, a, createBody(c, "10000000", map[string]any{"wht_type": "NONE"}))
	if out.WhtType != "NONE" {
		t.Errorf("wht_type = %s", out.WhtType)
	}
	wantDec4(t, "wht_amount", p4(out.WhtAmount), "0")
	wantDec4(t, "amount", p4(out.Amount), "10000000")
}

func TestDiscountThenWithholding(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	out := post(t, a, createBody(c, "1000000", map[string]any{"discount_type": "PERCENT", "discount_value": 10, "wht_type": "PPH23"}))
	wantDec4(t, "amount", p4(out.Amount), "900000")
	wantDec4(t, "wht_amount", p4(out.WhtAmount), "18000")
	wantDec4(t, "subtotal", out.Subtotal, "1000000")
}

func TestUpdateDraftRecomputesWithholding(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	body := post(t, a, createBody(c, "10000000", map[string]any{"wht_type": "PPH23"}))
	wantDec4(t, "amount", p4(body.Amount), "10000000")
	out := patch(t, a, body.InvoiceID, map[string]any{"wht_gross_up": true})
	if out.WhtType != "PPH23" {
		t.Errorf("wht_type = %s", out.WhtType)
	}
	wantDec4(t, "amount", p4(out.Amount), "10204082")
	wantDec4(t, "balance_due", p4(out.BalanceDue), "10204082")
	wantDec4(t, "wht_amount", p4(out.WhtAmount), "204082")
}

func TestUpdateTypeSwitchResetsRate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	body := post(t, a, createBody(c, "10000000", map[string]any{"wht_type": "PPH23", "wht_rate": 3}))
	out := patch(t, a, body.InvoiceID, map[string]any{"wht_type": "PPH21"})
	wantDec4(t, "wht_rate", out.WhtRate, "2.5")
}

func TestCreateInvoiceRate100Returns400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath, createBody(c, "1000000", map[string]any{"wht_type": "PPH23", "wht_rate": 100})),
		http.StatusBadRequest, "invalid_withholding", "Withholding tax rate must be greater than 0 and less than 100", "", "")
}

func TestMilestoneInvoiceInheritsCustomerDefault(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "PPH21", "", false)
	out := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
		"customer_id": c, "currency": "IDR", "amount": 1000000, "milestone_ref": "M1",
	}), http.StatusCreated)
	if out.WhtType != "PPH21" {
		t.Errorf("wht_type = %s", out.WhtType)
	}
	wantDec4(t, "wht_rate", out.WhtRate, "2.5")
	wantDec4(t, "wht_amount", p4(out.WhtAmount), "25000")
	wantDec4(t, "amount", p4(out.Amount), "1000000")
}

// Ports of tests/integration/test_invoice_internal_note.py.

const note = "Chase Sam on the 5th; PO usually lags a week."

func TestInternalNoteRoundTripsThroughApi(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	out := post(t, a, map[string]any{
		"customer_id": c, "currency": "USD", "internal_note": note,
		"line_items": []any{map[string]any{"description": "Retainer", "quantity": 1, "unit_price": 100}},
	})
	if out.InternalNote == nil || *out.InternalNote != note {
		t.Errorf("internal_note = %v", out.InternalNote)
	}
	if got := patch(t, a, out.InvoiceID, map[string]any{"internal_note": "updated"}); got.InternalNote == nil || *got.InternalNote != "updated" {
		t.Errorf("patched internal_note = %v", got.InternalNote)
	}
	if got := get(t, a, out.InvoiceID); got.InternalNote == nil || *got.InternalNote != "updated" {
		t.Errorf("read internal_note = %v", got.InternalNote)
	}
}

func TestInternalNoteCanBeCleared(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	out := post(t, a, createBody(c, "100", map[string]any{"currency": "USD", "internal_note": note}))
	if got := patch(t, a, out.InvoiceID, map[string]any{"internal_note": nil}); got.InternalNote != nil {
		t.Errorf("internal_note = %q, want null", *got.InternalNote)
	}
}

// Port of tests/integration/test_invoice_type_preserved.py.

func TestPatchWithoutInvoiceTypeKeepsIt(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	for _, typ := range []string{"RECURRING", "USAGE"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			out := post(t, a, map[string]any{
				"customer_id": c, "currency": "USD", "invoice_type": typ,
				"line_items": []any{map[string]any{"description": "Retainer", "quantity": 1, "unit_price": 100}},
			})
			got := patch(t, a, out.InvoiceID, map[string]any{
				"notes":      "edited",
				"line_items": []any{map[string]any{"description": "Retainer", "quantity": 2, "unit_price": 100}},
			})
			if got.InvoiceType != typ || got.Notes == nil || *got.Notes != "edited" {
				t.Errorf("type %s, notes %v", got.InvoiceType, got.Notes)
			}
		})
	}
}

// New.

func TestCreateRequiresLineItems(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// Left out, empty, and (unlike Python's 422) null.
	for _, b := range []map[string]any{
		{"currency": "IDR"},
		{"currency": "IDR", "line_items": []any{}},
		{"currency": "IDR", "line_items": nil},
	} {
		wantProblem(t, a.Do(t, http.MethodPost, invoicesPath, b), http.StatusBadRequest,
			"line_items_required", "invoice must have at least one line item", "", "")
	}
}

func TestCreateLineValidation(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, c := range []struct {
		line              map[string]any
		location, message string
	}{
		{map[string]any{"description": "x", "quantity": "1.00001", "unit_price": 1}, "line_items.0.quantity", "value has too many decimal places (max 4)"},
		{map[string]any{"description": "x", "quantity": -1, "unit_price": 1}, "line_items.0.quantity", "Input should be greater than or equal to 0"},
		{map[string]any{"description": "x", "quantity": 1, "unit_price": "-0.5"}, "line_items.0.unit_price", "Input should be greater than or equal to 0"},
		{map[string]any{"description": "x", "quantity": 1, "unit_price": 1, "position": nil}, "line_items.0.position", "value cannot be null"},
	} {
		rec := a.Do(t, http.MethodPost, invoicesPath, map[string]any{"currency": "IDR", "line_items": []any{c.line}})
		p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
		found := false
		for _, e := range p.Errors {
			found = found || (e.Location == c.location && e.Message == c.message)
		}
		if !found {
			t.Errorf("%v: no %s %q: %s", c.line, c.location, c.message, rec.Body)
		}
	}
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath, map[string]any{"currency": "IDR", "discount_value": -1, "line_items": []any{lineBody("1")}}),
		http.StatusUnprocessableEntity, "", "Input should be greater than or equal to 0", "discount_value", "Input should be greater than or equal to 0")
}

func TestCreateDefaultsIssueDueAndNumber(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// 2026-01-16 01:00 UTC: "today" is the clock's UTC date.
	a.Clock.Advance(16 * time.Hour)
	// The session is 8 hours: sign in again at the new time.
	token, _, err := a.Sessions.Issue(a.AdminID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	a.Cookie = platform.SessionCookie + "=" + token
	first := post(t, a, map[string]any{
		"currency": "IDR", "payment_terms": "Net 30",
		"line_items": []any{
			map[string]any{"description": "a", "quantity": "0.5", "unit_price": "0.0001", "position": 2},
			map[string]any{"description": "b", "quantity": "0.5", "unit_price": "0.0001"},
		},
	})
	wantDate(t, "issue_date", first.IssueDate, "2026-01-16")
	wantDate(t, "due_date", first.DueDate, "2026-02-15")
	if first.InvoiceNumber == nil || *first.InvoiceNumber != "WH20260001" {
		t.Errorf("invoice_number = %v", first.InvoiceNumber)
	}
	if first.InvoiceType != "MILESTONE" || first.Status != "DRAFT" || first.IsTemplate || first.CustomerID != nil {
		t.Errorf("type %s, status %s, template %v, customer %v", first.InvoiceType, first.Status, first.IsTemplate, first.CustomerID)
	}
	// Lines in position order, each position as sent (0 when left out).
	if len(first.LineItems) != 2 || first.LineItems[0].Description != "b" || first.LineItems[0].Position != 0 || first.LineItems[1].Position != 2 {
		t.Errorf("lines = %+v", first.LineItems)
	}
	wantDec4(t, "line amount", p4(first.LineItems[0].Amount), "0.0001")
	wantDec4(t, "subtotal", first.Subtotal, "0.0001")
	wantDec4(t, "amount", p4(first.Amount), "0.0001")

	// An empty number and type take the defaults; terms and dates as given.
	second := post(t, a, map[string]any{
		"currency": "IDR", "invoice_number": "", "invoice_type": "", "payment_terms": "On Receipt",
		"issue_date": "2025-12-30", "line_items": []any{lineBody("1")},
	})
	if second.InvoiceNumber == nil || *second.InvoiceNumber != "WH20260002" || second.InvoiceType != "MILESTONE" {
		t.Errorf("number %v, type %s", second.InvoiceNumber, second.InvoiceType)
	}
	wantDate(t, "issue_date", second.IssueDate, "2025-12-30")
	wantDate(t, "due_date", second.DueDate, "2025-12-30")

	third := post(t, a, map[string]any{"currency": "IDR", "due_date": "2026-03-01", "line_items": []any{lineBody("1")}})
	wantDate(t, "due_date", third.DueDate, "2026-03-01")
	fourth := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	wantDate(t, "default due_date", fourth.DueDate, "2026-01-30")
}

func TestDueDateOutOfRangeIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	const sentence = "due date is out of range"
	// Terms beyond int, and terms that pass 9999-12-31.
	for _, terms := range []string{"Net 99999999999999999999", "Net 99999999"} {
		wantProblem(t, a.Do(t, http.MethodPost, invoicesPath, map[string]any{"currency": "IDR", "payment_terms": terms, "line_items": []any{lineBody("1")}}),
			http.StatusBadRequest, "due_date_out_of_range", sentence, "", "")
	}
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
		"customer_id": c, "currency": "IDR", "amount": 1, "milestone_ref": "M", "due_in_days": 9999999,
	}), http.StatusBadRequest, "due_date_out_of_range", sentence, "", "")
	// Centuries away but in range: accepted, as Python does.
	far := post(t, a, map[string]any{"currency": "IDR", "payment_terms": "Net 110000", "issue_date": "2026-01-15", "line_items": []any{lineBody("1")}})
	wantDate(t, "Net 110000 due_date", far.DueDate, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 110000).Format(time.DateOnly))
	for _, days := range []int{200000, -200000} {
		out := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
			"customer_id": c, "currency": "IDR", "amount": 1, "milestone_ref": "M", "issue_date": "2026-01-15", "due_in_days": days,
		}), http.StatusCreated)
		wantDate(t, "milestone due_date", out.DueDate, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC).AddDate(0, 0, days).Format(time.DateOnly))
	}
	// PATCH fills a missing due date from the terms, refused the same way.
	out := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	wantProblem(t, a.Do(t, http.MethodPatch, invoicePath(out.InvoiceID), map[string]any{"due_date": nil, "payment_terms": "Net 99999999"}),
		http.StatusBadRequest, "due_date_out_of_range", sentence, "", "")
}

func TestCreateDuplicateNumberIs409(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, map[string]any{"currency": "IDR", "invoice_number": "INV-1", "line_items": []any{lineBody("1")}})
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath, map[string]any{"currency": "IDR", "invoice_number": "INV-1", "line_items": []any{lineBody("1")}}),
		http.StatusConflict, "invoice_number_exists", "invoice number already exists", "", "")
	other := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	wantProblem(t, a.Do(t, http.MethodPatch, invoicePath(other.InvoiceID), map[string]any{"invoice_number": "INV-1"}),
		http.StatusConflict, "invoice_number_exists", "invoice number already exists", "", "")
}

func TestCreateUnknownCustomerIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	unknown := uuid.New()
	for _, c := range []struct {
		body              map[string]any
		location, message string
	}{
		{map[string]any{"customer_id": unknown}, "customer_id", "customer not found"},
		{map[string]any{"project_id": unknown}, "project_id", "project not found"},
		{
			map[string]any{"line_items": []any{lineBody("1"), map[string]any{"item_id": unknown, "description": "x", "quantity": 1, "unit_price": 1}}},
			"line_items.1.item_id", "item not found",
		},
	} {
		b := map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}}
		for k, v := range c.body {
			b[k] = v
		}
		wantProblem(t, a.Do(t, http.MethodPost, invoicesPath, b), http.StatusUnprocessableEntity, "", c.message, c.location, c.message)
	}
	// The same on PATCH and on the milestone route.
	out := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	wantProblem(t, a.Do(t, http.MethodPatch, invoicePath(out.InvoiceID), map[string]any{"customer_id": unknown}),
		http.StatusUnprocessableEntity, "", "customer not found", "customer_id", "customer not found")
	wantProblem(t, a.Do(t, http.MethodPatch, invoicePath(out.InvoiceID), map[string]any{
		"line_items": []any{map[string]any{"item_id": unknown, "description": "x", "quantity": 1, "unit_price": 1}},
	}), http.StatusUnprocessableEntity, "", "item not found", "line_items.0.item_id", "item not found")
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
		"customer_id": unknown, "currency": "IDR", "amount": 1, "milestone_ref": "M",
	}), http.StatusUnprocessableEntity, "", "customer not found", "customer_id", "customer not found")
	// Nothing was stored by the refused requests.
	if list := apitest.Decode[[]InvoiceOut](t, a.Do(t, http.MethodGet, invoicesPath, nil), http.StatusOK); len(list) != 1 {
		t.Errorf("stored %d invoices, want 1", len(list))
	}
}

func TestCreateOverlongPaymentTermsIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for field, n := range map[string]int{"payment_terms": 31, "invoice_type": 21, "invoice_number": 51, "po_so_number": 101, "currency": 4} {
		b := map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}}
		b[field] = strings.Repeat("x", n)
		rec := a.Do(t, http.MethodPost, invoicesPath, b)
		p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
		if len(p.Errors) != 1 || p.Errors[0].Location != field {
			t.Errorf("%s: %s", field, rec.Body)
		}
		// The column's own length is accepted.
		b[field] = strings.Repeat("x", n-1)
		if field == "invoice_number" {
			b[field] = strings.Repeat("9", n-1)
		}
		post(t, a, b)
	}
	wantProblem(t, a.Do(t, http.MethodPost, invoicesPath, map[string]any{"currency": "ID", "line_items": []any{lineBody("1")}}),
		http.StatusUnprocessableEntity, "", invalid, "", "")
}

// listed creates an invoice through the API and stamps its
// created_at, so the list order is known.
func listed(t *testing.T, a *apitest.App, body map[string]any, created string) uuid.UUID {
	t.Helper()
	out := post(t, a, body)
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET created_at = $2::timestamp WHERE invoice_id = $1`, out.InvoiceID, created); err != nil {
		t.Fatal(err)
	}
	return out.InvoiceID
}

func setStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, status string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE invoices SET status = $2 WHERE invoice_id = $1`, id, status); err != nil {
		t.Fatal(err)
	}
}

func listIDs(t *testing.T, a *apitest.App, query string) []uuid.UUID {
	t.Helper()
	rec := a.Do(t, http.MethodGet, invoicesPath+query, nil)
	list := apitest.Decode[[]InvoiceOut](t, rec, http.StatusOK)
	if strings.TrimSpace(rec.Body.String()) == "null" {
		t.Errorf("%s: null list", query)
	}
	ids := make([]uuid.UUID, 0, len(list))
	for _, inv := range list {
		ids = append(ids, inv.InvoiceID)
	}
	return ids
}

func wantIDs(t *testing.T, name string, got []uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", name, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", name, got, want)
			return
		}
	}
}

func TestListFiltersAndOrder(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	base := func(extra map[string]any) map[string]any {
		b := map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	oldest := listed(t, a, base(map[string]any{"customer_id": c}), "2026-01-01 10:00:00")
	sent := listed(t, a, base(map[string]any{"customer_id": c, "invoice_type": "USAGE"}), "2026-01-02 10:00:00")
	partial := listed(t, a, base(nil), "2026-01-03 10:00:00")
	// Same created_at as partial: the id breaks the tie, descending.
	tie := listed(t, a, base(nil), "2026-01-03 10:00:00")
	template := listed(t, a, base(map[string]any{"customer_id": c}), "2026-01-04 10:00:00")
	setStatus(t, a.Pool, sent, "SENT")
	setStatus(t, a.Pool, partial, "PARTIAL")
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET is_template = true WHERE invoice_id = $1`, template); err != nil {
		t.Fatal(err)
	}
	newer, older := partial, tie
	if tie.String() > partial.String() {
		newer, older = tie, partial
	}

	wantIDs(t, "all", listIDs(t, a, ""), newer, older, sent, oldest)
	wantIDs(t, "two statuses", listIDs(t, a, "?status=SENT&status=PARTIAL"), partial, sent)
	wantIDs(t, "customer", listIDs(t, a, "?customer_id="+c.String()), sent, oldest)
	wantIDs(t, "type", listIDs(t, a, "?invoice_type=USAGE"), sent)
	wantIDs(t, "empty type is no filter", listIDs(t, a, "?invoice_type="), newer, older, sent, oldest)
	wantIDs(t, "combined", listIDs(t, a, "?status=DRAFT&customer_id="+c.String()), oldest)
	wantIDs(t, "limit", listIDs(t, a, "?limit=2"), newer, older)
	wantIDs(t, "none", listIDs(t, a, "?status=VOID"))
	// Every invoice of the page carries its lines.
	list := apitest.Decode[[]InvoiceOut](t, a.Do(t, http.MethodGet, invoicesPath, nil), http.StatusOK)
	for _, inv := range list {
		if len(inv.LineItems) != 1 {
			t.Errorf("%s lines = %+v", inv.InvoiceID, inv.LineItems)
		}
	}
}

func TestListLimitBounds(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, q := range []string{"?limit=0", "?limit=501", "?limit=-1"} {
		if rec := a.Do(t, http.MethodGet, invoicesPath+q, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
	for _, q := range []string{"?limit=1", "?limit=500"} {
		apitest.Decode[[]InvoiceOut](t, a.Do(t, http.MethodGet, invoicesPath+q, nil), http.StatusOK)
	}
}

func TestGetInvoice404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodGet, invoicePath(uuid.New()), nil), http.StatusNotFound, "invoice_not_found", "invoice not found", "", "")
	// Templates are read too.
	id := insertInvoice(t, a.Pool, nil)
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET is_template = true WHERE invoice_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if got := get(t, a, id); !got.IsTemplate {
		t.Errorf("is_template = false")
	}
}

func TestPatchNonDraftIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	setStatus(t, a.Pool, out.InvoiceID, "OPEN")
	wantProblem(t, a.Do(t, http.MethodPatch, invoicePath(out.InvoiceID), map[string]any{"notes": "x"}),
		http.StatusBadRequest, "invoice_not_draft", "only DRAFT invoices can be edited (got OPEN)", "", "")
}

func TestPatchUnknownIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPatch, invoicePath(id), map[string]any{"notes": "x"}),
		http.StatusBadRequest, "invoice_not_found", "invoice "+id.String()+" not found", "", "")
}

func TestPatchNullCurrencyIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
	for _, f := range []string{"currency", "invoice_type"} {
		wantProblem(t, a.Do(t, http.MethodPatch, invoicePath(out.InvoiceID), map[string]any{f: nil}),
			http.StatusUnprocessableEntity, "", invalid, f, "value cannot be null")
	}
}

func TestPatchNotesOnlyKeepsTotals(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	out := post(t, a, createBody(c, "1000000", map[string]any{
		"invoice_type": "RECURRING", "discount_type": "AMOUNT", "discount_value": 1000, "wht_type": "PPH23", "wht_gross_up": true,
		"po_so_number": "PO-1", "payment_terms": "Net 7",
	}))
	got := patch(t, a, out.InvoiceID, map[string]any{"notes": "only notes"})
	if got.Notes == nil || *got.Notes != "only notes" {
		t.Errorf("notes = %v", got.Notes)
	}
	// Everything else as it was: type, lines, totals, withholding, dates.
	got.Notes, out.Notes = nil, nil
	g, _ := json.Marshal(got)
	o, _ := json.Marshal(out)
	if string(g) != string(o) {
		t.Errorf("changed\n got %s\nwant %s", g, o)
	}
}

func TestPatchLinesNullKeepsLines(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("5"), lineBody("7")}})
	got := patch(t, a, out.InvoiceID, map[string]any{"line_items": nil})
	if len(got.LineItems) != 2 || got.LineItems[0].LineItemID != out.LineItems[0].LineItemID {
		t.Errorf("lines = %+v", got.LineItems)
	}
	wantDec4(t, "amount", p4(got.Amount), "12")
	// A list replaces them, an empty one with none (totals 0).
	got = patch(t, a, out.InvoiceID, map[string]any{"line_items": []any{lineBody("3")}})
	if len(got.LineItems) != 1 || got.LineItems[0].LineItemID == out.LineItems[0].LineItemID {
		t.Errorf("replaced lines = %+v", got.LineItems)
	}
	wantDec4(t, "amount", p4(got.Amount), "3")
	got = patch(t, a, out.InvoiceID, map[string]any{"line_items": []any{}})
	if len(got.LineItems) != 0 {
		t.Errorf("emptied lines = %+v", got.LineItems)
	}
	wantDec4(t, "amount", p4(got.Amount), "0")
	wantDec4(t, "subtotal", got.Subtotal, "0")
}

func TestPatchIssueDateKeepsExistingDueDate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, map[string]any{"currency": "IDR", "issue_date": "2026-01-10", "line_items": []any{lineBody("1")}})
	wantDate(t, "due_date", out.DueDate, "2026-01-24")
	got := patch(t, a, out.InvoiceID, map[string]any{"issue_date": "2026-03-01"})
	wantDate(t, "issue_date", got.IssueDate, "2026-03-01")
	wantDate(t, "due_date", got.DueDate, "2026-01-24")
	// A cleared due date is filled from the issue date and terms.
	got = patch(t, a, out.InvoiceID, map[string]any{"due_date": nil, "payment_terms": "Net 10"})
	wantDate(t, "refilled due_date", got.DueDate, "2026-03-11")
	// No issue date: the due date stays null.
	got = patch(t, a, out.InvoiceID, map[string]any{"issue_date": nil, "due_date": nil})
	if got.IssueDate != nil || got.DueDate != nil {
		t.Errorf("dates = %v, %v", got.IssueDate, got.DueDate)
	}
}

func TestAwaitingFinalizationListsDraftsOnly(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	body := map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}}
	older := listed(t, a, body, "2026-01-01 10:00:00")
	newer := listed(t, a, body, "2026-01-02 10:00:00")
	open := listed(t, a, body, "2026-01-03 10:00:00")
	template := listed(t, a, body, "2026-01-04 10:00:00")
	setStatus(t, a.Pool, open, "OPEN")
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET is_template = true WHERE invoice_id = $1`, template); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "awaiting", listIDs(t, a, "/awaiting-finalization"), newer, older)
}

func TestMilestoneCreate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	rec := a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
		"customer_id": c, "currency": "USD", "amount": "250.5", "milestone_ref": "Phase 1", "issue_date": "2026-02-01", "due_in_days": -3,
	})
	out := apitest.Decode[InvoiceOut](t, rec, http.StatusCreated)
	if out.InvoiceType != "MILESTONE" || out.InvoiceNumber != nil || out.Subtotal != nil || len(out.LineItems) != 0 ||
		out.Status != "DRAFT" || out.PaymentTerms != nil || out.IsTemplate {
		t.Errorf("milestone = %s", rec.Body)
	}
	// The stored object, compacted on the wire as Python's JSON is.
	if string(out.BillingCycleRef) != `{"milestone_ref":"Phase 1"}` {
		t.Errorf("billing_cycle_ref = %s", out.BillingCycleRef)
	}
	wantDec4(t, "amount", p4(out.Amount), "250.5")
	wantDec4(t, "balance_due", p4(out.BalanceDue), "250.5")
	wantDate(t, "issue_date", out.IssueDate, "2026-02-01")
	wantDate(t, "due_date", out.DueDate, "2026-01-29")
	// Defaults: today and 14 days.
	out = apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
		"customer_id": c, "currency": "USD", "amount": 1, "milestone_ref": "Phase 2",
	}), http.StatusCreated)
	wantDate(t, "default issue_date", out.IssueDate, "2026-01-15")
	wantDate(t, "default due_date", out.DueDate, "2026-01-29")
	for _, amount := range []any{0, -1} {
		wantProblem(t, a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
			"customer_id": c, "currency": "USD", "amount": amount, "milestone_ref": "M",
		}), http.StatusUnprocessableEntity, "", "Input should be greater than 0", "amount", "Input should be greater than 0")
	}
}

// TestLiteralPathsNotSwallowed: the literal paths this task serves reach
// their own handlers, never {invoice_id}. The recurring-template paths come
// with their routes (Task 5), /customers/balances with the customer views.
func TestLiteralPathsNotSwallowed(t *testing.T) {
	t.Parallel()
	t.Run("milestone", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		c := insertCustomer(t, a.Pool, "NONE", "", false)
		out := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/milestone", map[string]any{
			"customer_id": c, "currency": "USD", "amount": 1, "milestone_ref": "M",
		}), http.StatusCreated)
		if string(out.BillingCycleRef) != `{"milestone_ref":"M"}` {
			t.Errorf("not the milestone handler: %+v", out)
		}
	})
	t.Run("awaiting-finalization", func(t *testing.T) {
		t.Parallel()
		a := newApp(t)
		draft := post(t, a, map[string]any{"currency": "IDR", "line_items": []any{lineBody("1")}})
		wantIDs(t, "awaiting-finalization", listIDs(t, a, "/awaiting-finalization"), draft.InvoiceID)
	})
}

func TestInvoiceRoutesRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// A signed-in staff user: authenticated, but not an admin.
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
	for _, c := range []struct{ method, p string }{
		{http.MethodPost, invoicesPath},
		{http.MethodPost, invoicesPath + "/milestone"},
		{http.MethodGet, invoicesPath},
		{http.MethodGet, invoicesPath + "/awaiting-finalization"},
		{http.MethodGet, invoicePath(id)},
		{http.MethodPatch, invoicePath(id)},
	} {
		wantProblem(t, a.Anon(t, c.method, c.p, map[string]any{}), http.StatusUnauthorized, "", "not authenticated", "", "")
		wantProblem(t, staff.Do(t, c.method, c.p, map[string]any{}), http.StatusForbidden, "", "admin required", "", "")
	}
}

func TestLogNotifierLogsEachDraft(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	n := LogNotifier{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	n.NotifyNewDrafts(t.Context(), nil)
	if buf.Len() != 0 {
		t.Errorf("logged %q for no drafts", buf.String())
	}
	n.NotifyNewDrafts(t.Context(), []DraftSummary{{InvoiceID: uuid.New(), InvoiceNumber: "WH20260001", Currency: "IDR", Amount: decimal.NewFromInt(1), CycleDate: "2026-02-01"}})
	if got := buf.String(); !strings.Contains(got, "invoice_number=WH20260001") || !strings.Contains(got, "amount=1.0000") {
		t.Errorf("logged %q", got)
	}
	// A nil logger is slog's default (nothing to log here, so no output).
	LogNotifier{}.NotifyNewDrafts(t.Context(), nil)
}
