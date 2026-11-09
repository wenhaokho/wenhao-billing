package quotations_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
)

func convertPath(id uuid.UUID) string { return quotePath(id) + "/convert-to-invoice" }

// convert converts quotation id with body b and decodes the 201 invoice.
func convert(t *testing.T, a *apitest.App, id uuid.UUID, b any) invoices.InvoiceOut {
	t.Helper()
	return apitest.Decode[invoices.InvoiceOut](t, a.Do(t, http.MethodPost, convertPath(id), b), http.StatusCreated)
}

// newAppWithInvoices also serves the invoice routes (the delete of a
// converted invoice).
func newAppWithInvoices(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) {
		invoices.Register(api, d.Pool, d.Clock, nil)
		quotations.Register(api, d.Pool, d.Clock)
	})
}

func insertCustomerWHT(t *testing.T, a *apitest.App, whtType string, rate *string, grossUp bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO customers (customer_id, name, default_wht_type, default_wht_rate, default_wht_gross_up)
VALUES ($1, 'Acme', $2, $3::numeric, $4)`, id, whtType, rate, grossUp)
	return id
}

func insertItem(t *testing.T, a *apitest.App) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO items (item_id, name, item_type) VALUES ($1, 'Build', 'SERVICE')`, id)
	return id
}

func wantDecimal(t *testing.T, name string, got platform.Dec4, want string) {
	t.Helper()
	if !got.Decimal().Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s = %s, want %s", name, got.Decimal(), want)
	}
}

// TestQuoteConversionAppliesCustomerDefault ports Python's
// test_invoice_withholding.py::test_quote_conversion_applies_customer_default:
// the customer's PPH23 gross-up default (rate 2) on the stored quote amount.
func TestQuoteConversionAppliesCustomerDefault(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomerWHT(t, a, "PPH23", nil, true)
	q := post(t, a, body(c, map[string]any{"line_items": []any{line(1, 10000000)}}))
	inv := convert(t, a, q.QuotationID, map[string]any{})
	if inv.WhtType != "PPH23" {
		t.Errorf("wht_type %s, want PPH23", inv.WhtType)
	}
	wantDecimal(t, "amount", inv.Amount, "10204082")
	wantDecimal(t, "balance_due", inv.BalanceDue, "10204082")
	wantDecimal(t, "wht_amount", inv.WhtAmount, "204082")
	wantDec4(t, "wht_base", inv.WhtBase, "10000000")
	wantDec4(t, "wht_rate", inv.WhtRate, "2")
	if !inv.WhtGrossUp {
		t.Error("wht_gross_up false, want the customer's true")
	}
}

// TestConvertFieldMapping checks every field of the invoice against
// quoting.py:296-321, and the quotation afterwards.
func TestConvertFieldMapping(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	p := insertProject(t, a, c)
	q := post(t, a, body(c, map[string]any{
		"project_id": p, "po_so_number": "PO-7", "currency": "usd",
		"discount_type": "PERCENT", "discount_value": "10",
		"payment_terms": "Net 30", "notes": "Thanks", "footer": "Bank details",
		"line_items": []any{line("2", "1500.5"), line("1", "99.99")},
	}))
	// The issue date is next year's: the number keeps today's year.
	inv := convert(t, a, q.QuotationID, map[string]any{"issue_date": "2027-02-01", "due_in_days": 5})

	if inv.CustomerID == nil || *inv.CustomerID != c {
		t.Errorf("customer_id %v, want %s", inv.CustomerID, c)
	}
	if inv.ProjectID == nil || *inv.ProjectID != p {
		t.Errorf("project_id %v, want %s", inv.ProjectID, p)
	}
	if inv.SourceQuoteID == nil || *inv.SourceQuoteID != q.QuotationID {
		t.Errorf("source_quote_id %v, want %s", inv.SourceQuoteID, q.QuotationID)
	}
	if inv.InvoiceType != "MILESTONE" || inv.Status != "DRAFT" || inv.IsTemplate {
		t.Errorf("type %s status %s template %v, want MILESTONE DRAFT false", inv.InvoiceType, inv.Status, inv.IsTemplate)
	}
	if inv.InvoiceNumber == nil || *inv.InvoiceNumber != "WH20260001" {
		t.Errorf("invoice_number %v, want WH20260001", inv.InvoiceNumber)
	}
	if inv.PoSoNumber == nil || *inv.PoSoNumber != "PO-7" || inv.Currency != "usd" {
		t.Errorf("po_so_number %v currency %s, want PO-7 usd", inv.PoSoNumber, inv.Currency)
	}
	// The quote's stored totals: subtotal 3100.99, 10% off is 2790.891.
	wantDec4(t, "subtotal", inv.Subtotal, "3100.99")
	wantDec4(t, "subtotal (quote)", inv.Subtotal, q.Subtotal.Decimal().String())
	if inv.DiscountType == nil || *inv.DiscountType != "PERCENT" {
		t.Errorf("discount_type %v, want PERCENT", inv.DiscountType)
	}
	wantDec4(t, "discount_value", inv.DiscountValue, "10")
	if inv.WhtType != "NONE" || inv.WhtRate != nil || inv.WhtGrossUp {
		t.Errorf("wht %s %v %v, want NONE nil false", inv.WhtType, inv.WhtRate, inv.WhtGrossUp)
	}
	wantDecimal(t, "quote amount", q.Amount, "2790.891")
	wantDec4(t, "wht_base", inv.WhtBase, "2790.891")
	wantDecimal(t, "wht_amount", inv.WhtAmount, "0")
	wantDecimal(t, "wht_booked", inv.WhtBooked, "0")
	wantDecimal(t, "amount", inv.Amount, "2790.891")
	wantDecimal(t, "balance_due", inv.BalanceDue, "2790.891")
	wantDate(t, "issue_date", inv.IssueDate, "2027-02-01")
	// Net 30 overrides due_in_days.
	wantDate(t, "due_date", inv.DueDate, "2027-03-03")
	if inv.PaymentTerms == nil || *inv.PaymentTerms != "Net 30" || inv.Notes == nil || *inv.Notes != "Thanks" ||
		inv.Footer == nil || *inv.Footer != "Bank details" {
		t.Errorf("terms %v notes %v footer %v", inv.PaymentTerms, inv.Notes, inv.Footer)
	}
	if inv.InternalNote != nil || inv.CoverageStart != nil || inv.CoverageEnd != nil || string(inv.BillingCycleRef) != "null" {
		t.Errorf("internal_note %v coverage %v..%v billing_cycle_ref %s, want all null",
			inv.InternalNote, inv.CoverageStart, inv.CoverageEnd, inv.BillingCycleRef)
	}
	if len(inv.LineItems) != 2 {
		t.Errorf("%d lines, want 2", len(inv.LineItems))
	}

	after := get(t, a, q.QuotationID)
	if after.Status != "INVOICED" || after.ConvertedInvoiceID == nil || *after.ConvertedInvoiceID != inv.InvoiceID {
		t.Errorf("quotation %s converted_invoice_id %v, want INVOICED %s", after.Status, after.ConvertedInvoiceID, inv.InvoiceID)
	}
	if after.AcceptedAt != nil || after.AcceptedBy != nil {
		t.Errorf("accepted_at %v accepted_by %v, want null (a DRAFT converts without acceptance)", after.AcceptedAt, after.AcceptedBy)
	}
}

// TestConvertCopiesLinesVerbatim: each line's item, position, description,
// quantity, price and stored amount, never recomputed.
func TestConvertCopiesLinesVerbatim(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	item := insertItem(t, a)
	first := line("1.2345", "10.0001")
	first["item_id"], first["position"], first["description"] = item, 2, "Design"
	second := line("3", "7")
	second["position"], second["description"] = 1, "Build"
	q := post(t, a, body(c, map[string]any{"line_items": []any{first, second}}))
	// A stored amount that is not qty × price: it is copied as stored.
	exec(t, a, `UPDATE quotation_line_items SET amount = 123.4567 WHERE quotation_id = $1 AND position = 1`, q.QuotationID)
	q = get(t, a, q.QuotationID)

	inv := convert(t, a, q.QuotationID, map[string]any{})
	if len(inv.LineItems) != len(q.LineItems) {
		t.Fatalf("%d lines, want %d", len(inv.LineItems), len(q.LineItems))
	}
	for i, want := range q.LineItems {
		got := inv.LineItems[i]
		if got.Description != want.Description || got.Position != want.Position ||
			(got.ItemID == nil) != (want.ItemID == nil) || (got.ItemID != nil && *got.ItemID != *want.ItemID) ||
			got.LineItemID == want.LineItemID {
			t.Errorf("line %d = %+v, want a copy of %+v", i, got, want)
		}
		wantDecimal(t, "quantity", got.Quantity, want.Quantity.Decimal().String())
		wantDecimal(t, "unit_price", got.UnitPrice, want.UnitPrice.Decimal().String())
		wantDecimal(t, "amount", got.Amount, want.Amount.Decimal().String())
	}
	wantDecimal(t, "line 0 amount", inv.LineItems[0].Amount, "123.4567")
	if inv.LineItems[1].ItemID == nil || *inv.LineItems[1].ItemID != item {
		t.Errorf("line 1 item %v, want %s", inv.LineItems[1].ItemID, item)
	}
	// The invoice totals are the quote's stored ones, not the copied lines'.
	wantDecimal(t, "amount", inv.Amount, q.Amount.Decimal().String())
}

// TestConvertTermsOverrideDueInDays: non-empty payment terms, even ones
// that do not parse, set the days; due_in_days counts only without terms.
func TestConvertTermsOverrideDueInDays(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, tc := range []struct {
		name  string
		terms any
		body  map[string]any
		due   string
	}{
		{"net 30", "Net 30", map[string]any{"due_in_days": 5}, "2026-02-14"},
		{"on receipt", "Due on receipt", map[string]any{"due_in_days": 5}, "2026-01-15"},
		{"unparseable", "whenever", map[string]any{"due_in_days": 5}, "2026-01-29"},
		{"no terms", nil, map[string]any{"due_in_days": 5}, "2026-01-20"},
		{"empty terms", "", map[string]any{"due_in_days": -3}, "2026-01-12"},
		{"no terms default", nil, map[string]any{}, "2026-01-29"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := post(t, a, body(c, map[string]any{"payment_terms": tc.terms}))
			inv := convert(t, a, q.QuotationID, tc.body)
			wantDate(t, "issue_date", inv.IssueDate, "2026-01-15")
			wantDate(t, "due_date", inv.DueDate, tc.due)
		})
	}
}

func TestConvertFromDraftSentAcceptedExpired(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, status := range []string{"DRAFT", "SENT", "ACCEPTED", "EXPIRED"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			q := post(t, a, body(c, nil))
			setStatus(t, a, q.QuotationID, status)
			inv := convert(t, a, q.QuotationID, map[string]any{})
			after := get(t, a, q.QuotationID)
			if after.Status != "INVOICED" || after.ConvertedInvoiceID == nil || *after.ConvertedInvoiceID != inv.InvoiceID {
				t.Errorf("after %s: %s %v, want INVOICED %s", status, after.Status, after.ConvertedInvoiceID, inv.InvoiceID)
			}
		})
	}
}

func TestConvertRefusedStatuses(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, status := range []string{"INVOICED", "VOID", "DECLINED"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			q := post(t, a, body(c, nil))
			setStatus(t, a, q.QuotationID, status)
			wantProblem(t, a.Do(t, http.MethodPost, convertPath(q.QuotationID), map[string]any{}), http.StatusBadRequest,
				"quotation_not_convertible", "cannot convert a "+status+" quotation to invoice", "", "")
			if after := get(t, a, q.QuotationID); after.Status != status || after.ConvertedInvoiceID != nil {
				t.Errorf("after: %s %v", after.Status, after.ConvertedInvoiceID)
			}
		})
	}
}

func TestConvertUnknownOrEmptyIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPost, convertPath(id), map[string]any{}), http.StatusBadRequest,
		"quotation_not_found", "quotation "+id.String()+" not found", "", "")

	q := post(t, a, body(insertCustomer(t, a), nil))
	patch(t, a, q.QuotationID, map[string]any{"line_items": []any{}})
	wantProblem(t, a.Do(t, http.MethodPost, convertPath(q.QuotationID), map[string]any{}), http.StatusBadRequest,
		"quotation_has_no_lines", "quotation has no line items to convert", "", "")
	if n := countRows(t, a, "invoices"); n != 0 {
		t.Errorf("stored %d invoices, want 0", n)
	}
}

func TestConvertTwiceIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	q := post(t, a, body(insertCustomer(t, a), nil))
	inv := convert(t, a, q.QuotationID, map[string]any{})
	wantProblem(t, a.Do(t, http.MethodPost, convertPath(q.QuotationID), map[string]any{}), http.StatusBadRequest,
		"quotation_not_convertible", "cannot convert a INVOICED quotation to invoice", "", "")
	if n := countRows(t, a, "invoices"); n != 1 {
		t.Errorf("stored %d invoices, want 1", n)
	}
	if after := get(t, a, q.QuotationID); after.ConvertedInvoiceID == nil || *after.ConvertedInvoiceID != inv.InvoiceID {
		t.Errorf("converted_invoice_id %v, want %s", after.ConvertedInvoiceID, inv.InvoiceID)
	}
}

// waitForLockWaits waits until n sessions of this test's database wait on a
// lock; false when they did not within five seconds.
func waitForLockWaits(t *testing.T, pool *pgxpool.Pool, n int) bool {
	t.Helper()
	for range 500 {
		var waiting int
		err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestConvertConcurrentMakesOneInvoice: two converts at once make one
// invoice. The test holds the invoice number lock until both requests wait
// on a lock: with the quotation read FOR UPDATE, one waits on the number
// lock and the other on the quotation row, then reads it INVOICED; without
// it both would read DRAFT and both make an invoice.
func TestConvertConcurrentMakesOneInvoice(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	q := post(t, a, body(insertCustomer(t, a), nil))

	ctx := t.Context()
	hold, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('invoice-number:2026', 0))`); err != nil {
		t.Fatal(err)
	}
	// The goroutines serve the requests without t (a.Do may t.Fatal, which
	// only the test goroutine may call). Should the test goroutine fail
	// first, the deferred rollback releases the lock and this cleanup, which
	// runs after it, waits for both.
	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	for i := range recs {
		wg.Go(func() {
			req := httptest.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, convertPath(q.QuotationID),
				strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Cookie", a.Cookie)
			rec := httptest.NewRecorder()
			a.Handler.ServeHTTP(rec, req)
			recs[i] = rec
		})
	}
	waited := waitForLockWaits(t, a.Pool, 2)
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !waited {
		t.Error("the two converts never both waited on a lock")
	}

	var created invoices.InvoiceOut
	var oks, refused int
	for _, rec := range recs {
		switch rec.Code {
		case http.StatusCreated:
			oks++
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
		case http.StatusBadRequest:
			refused++
			wantProblem(t, rec, http.StatusBadRequest, "quotation_not_convertible", "cannot convert a INVOICED quotation to invoice", "", "")
		default:
			t.Errorf("convert: %d %s", rec.Code, rec.Body)
		}
	}
	if oks != 1 || refused != 1 {
		t.Errorf("%d created, %d refused; want 1 and 1", oks, refused)
	}
	if n := countRows(t, a, "invoices"); n != 1 {
		t.Errorf("stored %d invoices, want 1", n)
	}
	if after := get(t, a, q.QuotationID); after.ConvertedInvoiceID == nil || *after.ConvertedInvoiceID != created.InvoiceID {
		t.Errorf("converted_invoice_id %v, want %s", after.ConvertedInvoiceID, created.InvoiceID)
	}
}

// TestConvertedInvoiceCannotBeDeleted: the quotation references its invoice,
// so the DRAFT cannot be deleted (plan 5's 409).
func TestConvertedInvoiceCannotBeDeleted(t *testing.T) {
	t.Parallel()
	a := newAppWithInvoices(t)
	q := post(t, a, body(insertCustomer(t, a), nil))
	inv := convert(t, a, q.QuotationID, map[string]any{})
	wantProblem(t, a.Do(t, http.MethodDelete, "/api/v1/invoices/"+inv.InvoiceID.String(), nil), http.StatusConflict,
		"invoice_referenced", "invoice is referenced by a payment or quotation", "", "")
	if n := countRows(t, a, "invoices"); n != 1 {
		t.Errorf("stored %d invoices, want 1", n)
	}
}

// TestConvertDueDateOutOfRangeIs400: a due date past 9999-12-31, by days or
// by terms whose N does not fit an int, is the invoices' 400 (Python: 500);
// nothing is written.
func TestConvertDueDateOutOfRangeIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, tc := range []struct {
		name  string
		terms any
		body  map[string]any
	}{
		{"days", nil, map[string]any{"issue_date": "9999-12-31", "due_in_days": 1}},
		{"terms", "Net 99999999999999999999", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := post(t, a, body(c, map[string]any{"payment_terms": tc.terms}))
			wantProblem(t, a.Do(t, http.MethodPost, convertPath(q.QuotationID), tc.body), http.StatusBadRequest,
				"due_date_out_of_range", "due date is out of range", "", "")
			if after := get(t, a, q.QuotationID); after.Status != "DRAFT" || after.ConvertedInvoiceID != nil {
				t.Errorf("after: %s %v", after.Status, after.ConvertedInvoiceID)
			}
		})
	}
	if n := countRows(t, a, "invoices"); n != 0 {
		t.Errorf("stored %d invoices, want 0", n)
	}
}

// TestConvertInvalidCustomerWithholdingIs400: the customer default is
// normalized as on an invoice create; its refusal is the 400.
func TestConvertInvalidCustomerWithholdingIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomerWHT(t, a, "PPH99", nil, false)
	q := post(t, a, body(c, nil))
	wantProblem(t, a.Do(t, http.MethodPost, convertPath(q.QuotationID), map[string]any{}), http.StatusBadRequest,
		"invalid_withholding", "Unknown withholding tax type: PPH99", "", "")
	if after := get(t, a, q.QuotationID); after.Status != "DRAFT" {
		t.Errorf("status %s, want DRAFT", after.Status)
	}
}

func TestConvertRequiresBody(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	q := post(t, a, body(insertCustomer(t, a), nil))
	// As accept: a JSON null body is Python's missing body (422); no body at
	// all is huma's 400 "request body is required".
	for _, c := range []struct {
		name string
		body any
		code int
	}{
		{"null", map[string]any(nil), http.StatusUnprocessableEntity},
		{"empty", nil, http.StatusBadRequest},
		{"null due_in_days", map[string]any{"due_in_days": nil}, http.StatusUnprocessableEntity},
	} {
		if rec := a.Do(t, http.MethodPost, convertPath(q.QuotationID), c.body); rec.Code != c.code {
			t.Errorf("%s body: %d, want %d: %s", c.name, rec.Code, c.code, rec.Body)
		}
	}
	if got := get(t, a, q.QuotationID); got.Status != "DRAFT" {
		t.Errorf("status %s after refused converts", got.Status)
	}
}
