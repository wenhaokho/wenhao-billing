package bills_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const path = "/api/v1/bills"

func billPath(id uuid.UUID) string { return path + "/" + id.String() }

type problem struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
	Errors []struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	} `json:"errors"`
}

func newApp(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) { bills.Register(api, d.Pool, d.Clock, base) })
}

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

// wantFieldError checks a 422 that lists message at location.
func wantFieldError(t *testing.T, rec *httptest.ResponseRecorder, location, message string) {
	t.Helper()
	p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
	for _, e := range p.Errors {
		if e.Location == location && e.Message == message {
			return
		}
	}
	t.Errorf("no error %q %q: %s", location, message, rec.Body)
}

func exec(t *testing.T, a *apitest.App, sql string, args ...any) {
	t.Helper()
	if _, err := a.Pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func insertVendor(t *testing.T, a *apitest.App) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'Vend')`, id)
	return id
}

func insertProject(t *testing.T, a *apitest.App) uuid.UUID {
	t.Helper()
	customer, id := uuid.New(), uuid.New()
	exec(t, a, `INSERT INTO customers (customer_id, name) VALUES ($1, 'Acme')`, customer)
	exec(t, a, `INSERT INTO projects (project_id, customer_id, code, name, currency) VALUES ($1, $2, $3, 'P', 'IDR')`,
		id, customer, id.String()[:8])
	return id
}

func insertAccount(t *testing.T, a *apitest.App) int32 {
	t.Helper()
	var id int32
	err := a.Pool.QueryRow(t.Context(),
		`INSERT INTO chart_of_accounts (code, name, type) VALUES ('6-100', 'Hosting cost', 'EXPENSE') RETURNING account_id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertItem(t *testing.T, a *apitest.App) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO items (item_id, name, item_type) VALUES ($1, 'Item', 'SERVICE')`, id)
	return id
}

func line(qty, price any) map[string]any {
	return map[string]any{"description": "Work", "quantity": qty, "unit_price": price}
}

// body is a create body for vendor: IDR, one line 1 x 1000, plus extra.
func body(vendor uuid.UUID, extra map[string]any) map[string]any {
	b := map[string]any{"vendor_id": vendor, "currency": "IDR", "line_items": []any{line(1, 1000)}}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func post(t *testing.T, a *apitest.App, b map[string]any) bills.BillOut {
	t.Helper()
	return apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, path, b), http.StatusCreated)
}

func patch(t *testing.T, a *apitest.App, id uuid.UUID, b map[string]any) bills.BillOut {
	t.Helper()
	return apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPatch, billPath(id), b), http.StatusOK)
}

func get(t *testing.T, a *apitest.App, id uuid.UUID) bills.BillOut {
	t.Helper()
	return apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodGet, billPath(id), nil), http.StatusOK)
}

func setStatus(t *testing.T, a *apitest.App, id uuid.UUID, status string) {
	t.Helper()
	exec(t, a, `UPDATE bills SET status = $2 WHERE bill_id = $1`, id, status)
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

func TestCreateBillSetsFieldsAndTotals(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor, project, item, account := insertVendor(t, a), insertProject(t, a), insertItem(t, a), insertAccount(t, a)
	out := post(t, a, map[string]any{
		"vendor_id": vendor, "project_id": project, "bill_number": "INV-9", "po_number": "PO-1", "currency": "usd",
		"issue_date": "2026-01-10", "due_date": "2026-02-10", "payment_terms": "Net 30", "notes": "n",
		"discount_type": "PERCENT", "discount_value": "10",
		"line_items": []any{
			map[string]any{"item_id": item, "expense_account_id": account, "description": "A", "quantity": "2", "unit_price": "100.5"},
			map[string]any{"description": "B", "quantity": 1, "unit_price": 50},
		},
	})
	if out.Status != "DRAFT" || out.Currency != "USD" || out.VendorID != vendor || *out.ProjectID != project {
		t.Errorf("%+v", out)
	}
	wantDec4(t, "subtotal", out.Subtotal, "251")
	wantDec4(t, "amount", p4(out.Amount), "225.9")
	wantDec4(t, "balance_due", p4(out.BalanceDue), "225.9")
	wantDec4(t, "discount_value", out.DiscountValue, "10")
	if *out.BillNumber != "INV-9" || *out.PoNumber != "PO-1" || *out.PaymentTerms != "Net 30" || *out.Notes != "n" ||
		out.IssueDate.Format(time.DateOnly) != "2026-01-10" || out.DueDate.Format(time.DateOnly) != "2026-02-10" {
		t.Errorf("%+v", out)
	}
	if len(out.LineItems) != 2 || *out.LineItems[0].ItemID != item || *out.LineItems[0].ExpenseAccountID != account ||
		out.LineItems[0].Description != "A" || out.LineItems[1].ItemID != nil || out.LineItems[1].ExpenseAccountID != nil {
		t.Fatalf("lines %+v", out.LineItems)
	}
	wantDec4(t, "line 0 amount", p4(out.LineItems[0].Amount), "201")
	wantDec4(t, "line 1 amount", p4(out.LineItems[1].Amount), "50")
	if got := get(t, a, out.BillID); !got.Amount.Decimal().Equal(out.Amount.Decimal()) || len(got.LineItems) != 2 {
		t.Errorf("stored %+v", got)
	}
}

// Python's `ln.position or idx`: a position of 0 is the line's index in the
// list, where quotations and invoices keep the 0.
func TestBillPositionZeroBecomesIndex(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	withPos := func(p int) map[string]any { l := line(1, 1); l["position"] = p; return l }
	out := post(t, a, body(insertVendor(t, a), map[string]any{
		"line_items": []any{line(1, 1), line(1, 1), withPos(7), withPos(0)},
	}))
	var got []int
	for _, l := range out.LineItems {
		got = append(got, l.Position)
	}
	// Listed by position: index 0 (0), index 1 (1), index 3 (0 -> 3), then the explicit 7.
	if want := []int{0, 1, 3, 7}; len(got) != 4 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Errorf("positions %v, want %v", got, want)
	}
}

func TestCreateBillWithoutLines(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor := insertVendor(t, a)
	for _, b := range []map[string]any{
		{"vendor_id": vendor, "currency": "IDR"},
		{"vendor_id": vendor, "currency": "IDR", "line_items": []any{}},
	} {
		out := post(t, a, b)
		wantDec4(t, "subtotal", out.Subtotal, "0")
		wantDec4(t, "amount", p4(out.Amount), "0")
		wantDec4(t, "balance_due", p4(out.BalanceDue), "0")
		if out.Status != "DRAFT" || out.LineItems == nil || len(out.LineItems) != 0 {
			t.Errorf("%+v", out)
		}
		if out.IssueDate != nil || out.DueDate != nil {
			t.Errorf("dates are not defaulted: %+v", out)
		}
	}
}

func TestCreateUppercasesCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor := insertVendor(t, a)
	out := post(t, a, body(vendor, map[string]any{"currency": "usd"}))
	if out.Currency != "USD" {
		t.Errorf("currency %q", out.Currency)
	}
	// Python's str.upper lengthens "ß" to "SS": the stored currency would no
	// longer fit (Python's 500), so it is a 422 at currency (Go change 4).
	wantFieldError(t, a.Do(t, http.MethodPost, path, body(vendor, map[string]any{"currency": "ßab"})),
		"currency", "String should have at most 3 characters")
	wantFieldError(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"currency": "ßab"}),
		"currency", "String should have at most 3 characters")
	if got := patch(t, a, out.BillID, map[string]any{"currency": "sgd"}); got.Currency != "SGD" {
		t.Errorf("patched currency %q", got.Currency)
	}
}

func TestCreateUnknownVendorIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPost, path, body(uuid.New(), nil)),
		http.StatusUnprocessableEntity, "", "vendor not found", "vendor_id", "vendor not found")
	wantProblem(t, a.Do(t, http.MethodPost, path, body(insertVendor(t, a), map[string]any{"project_id": uuid.New()})),
		http.StatusUnprocessableEntity, "", "project not found", "project_id", "project not found")
	if rec := a.Do(t, http.MethodPost, path, map[string]any{"currency": "IDR"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a create without a vendor: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateUnknownExpenseAccountIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor := insertVendor(t, a)
	l := line(1, 1)
	l["expense_account_id"] = 987654
	wantProblem(t, a.Do(t, http.MethodPost, path, body(vendor, map[string]any{"line_items": []any{line(1, 1), l}})),
		http.StatusUnprocessableEntity, "", "expense account not found", "line_items.1.expense_account_id", "expense account not found")
	l = line(1, 1)
	l["item_id"] = uuid.New()
	wantProblem(t, a.Do(t, http.MethodPost, path, body(vendor, map[string]any{"line_items": []any{l}})),
		http.StatusUnprocessableEntity, "", "item not found", "line_items.0.item_id", "item not found")
	// Nothing was stored by the refused creates.
	if n := apitest.Decode[[]bills.BillOut](t, a.Do(t, http.MethodGet, path, nil), http.StatusOK); len(n) != 0 {
		t.Errorf("%d bills stored", len(n))
	}
}

func TestCreateRefusesBadFields(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor := insertVendor(t, a)
	for _, c := range []struct {
		extra            map[string]any
		location, detail string
	}{
		{map[string]any{"bill_number": strings.Repeat("x", 101)}, "bill_number", "expected length <= 100"},
		{map[string]any{"po_number": strings.Repeat("x", 101)}, "po_number", "expected length <= 100"},
		{map[string]any{"payment_terms": strings.Repeat("x", 31)}, "payment_terms", "expected length <= 30"},
		{map[string]any{"discount_type": "FLAT"}, "discount_type", `expected value to be one of "PERCENT, AMOUNT"`},
		{map[string]any{"discount_value": -1}, "discount_value", "Input should be greater than or equal to 0"},
		{map[string]any{"line_items": []any{line(-1, 1)}}, "line_items.0.quantity", "Input should be greater than or equal to 0"},
		{map[string]any{"line_items": []any{line(1, -1)}}, "line_items.0.unit_price", "Input should be greater than or equal to 0"},
		{map[string]any{"currency": "ID"}, "currency", "expected length >= 3"},
	} {
		wantFieldError(t, a.Do(t, http.MethodPost, path, body(vendor, c.extra)), c.location, c.detail)
	}
}

func TestListFiltersAndOrder(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	v1, v2 := insertVendor(t, a), insertVendor(t, a)
	first, second := post(t, a, body(v1, nil)), post(t, a, body(v2, nil))
	third := post(t, a, body(v1, map[string]any{"line_items": []any{line(1, 1), line(2, 2)}}))
	setStatus(t, a, first.BillID, "OPEN")
	setStatus(t, a, second.BillID, "PARTIAL")
	// created_at decides, newest first; the id breaks a tie.
	exec(t, a, `UPDATE bills SET created_at = '2026-01-01 00:00:00' WHERE bill_id = $1`, first.BillID)
	exec(t, a, `UPDATE bills SET created_at = '2026-01-02 00:00:00' WHERE bill_id IN ($1, $2)`, second.BillID, third.BillID)

	ids := func(query string) []uuid.UUID {
		out := apitest.Decode[[]bills.BillOut](t, a.Do(t, http.MethodGet, path+query, nil), http.StatusOK)
		got := []uuid.UUID{}
		for _, b := range out {
			got = append(got, b.BillID)
		}
		return got
	}
	tie := []uuid.UUID{second.BillID, third.BillID}
	if second.BillID.String() < third.BillID.String() {
		tie = []uuid.UUID{third.BillID, second.BillID}
	}
	if all := ids(""); len(all) != 3 || all[0] != tie[0] || all[1] != tie[1] || all[2] != first.BillID {
		t.Errorf("order %v, want %v then %v", all, tie, first.BillID)
	}
	if got := ids("?status=OPEN&status=PARTIAL"); len(got) != 2 || got[0] != second.BillID || got[1] != first.BillID {
		t.Errorf("OPEN+PARTIAL: %v", got)
	}
	if got := ids("?status=OPEN"); len(got) != 1 || got[0] != first.BillID {
		t.Errorf("OPEN: %v", got)
	}
	if got := ids("?status=DRAFT"); len(got) != 1 || got[0] != third.BillID {
		t.Errorf("DRAFT: %v", got)
	}
	if got := ids("?status=NOPE"); len(got) != 0 {
		t.Errorf("unknown status: %v", got)
	}
	if got := ids("?vendor_id=" + v2.String()); len(got) != 1 || got[0] != second.BillID {
		t.Errorf("vendor: %v", got)
	}
	if got := ids("?vendor_id=" + v1.String() + "&status=DRAFT"); len(got) != 1 || got[0] != third.BillID {
		t.Errorf("vendor and status: %v", got)
	}
	// Lines ride along, each bill's own.
	for _, b := range apitest.Decode[[]bills.BillOut](t, a.Do(t, http.MethodGet, path, nil), http.StatusOK) {
		want := 1
		if b.BillID == third.BillID {
			want = 2
		}
		if len(b.LineItems) != want {
			t.Errorf("bill %s has %d lines, want %d", b.BillID, len(b.LineItems), want)
		}
	}
	if rec := a.Do(t, http.MethodGet, path+"?vendor_id=nope", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad vendor_id: %d", rec.Code)
	}
}

func TestListEmptyIsArray(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	if got := strings.TrimSpace(a.Do(t, http.MethodGet, path, nil).Body.String()); got != "[]" {
		t.Errorf("body %s", got)
	}
}

func TestGetBill404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodGet, billPath(uuid.New()), nil), http.StatusNotFound, "bill_not_found", "bill not found", "", "")
}

func TestPatchUnknownBillIs404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(uuid.New()), map[string]any{"notes": "x"}),
		http.StatusNotFound, "bill_not_found", "bill not found", "", "")
}

// status is writable to any of the five; it is the only way a bill opens.
func TestPatchStatusOpensBill(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	got := patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	if got.Status != "OPEN" {
		t.Errorf("status %q", got.Status)
	}
	wantDec4(t, "balance_due", p4(got.BalanceDue), "1000")
	// An OPEN bill is still editable; PAID comes only from a payment
	// (PORT-NOTES 164), where Python set it as any other field.
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"status": "PAID"}),
		http.StatusBadRequest, "bill_status_not_settable", "PAID comes from recording a payment", "", "")
	setStatus(t, a, out.BillID, "PAID")
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"status": "OPEN"}),
		http.StatusBadRequest, "bill_not_editable", "bill is PAID and cannot be edited", "", "")
	wantFieldError(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"status": "BOGUS"}),
		"status", `expected value to be one of "DRAFT, OPEN, PARTIAL, PAID, VOID"`)
}

func TestPatchPaidIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	for _, status := range []string{"PARTIAL", "PAID", "VOID"} {
		setStatus(t, a, out.BillID, status)
		wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"notes": "x"}),
			http.StatusBadRequest, "bill_not_editable", "bill is "+status+" and cannot be edited", "", "")
		if got := get(t, a, out.BillID); got.Notes != nil {
			t.Errorf("%s: notes changed to %q", status, *got.Notes)
		}
	}
}

// Changing the lines of an OPEN bill reprices amount and re-baselines
// balance_due (PORT-NOTES 164: an OPEN bill has no payment; Python
// re-baselined it only while the bill was DRAFT, oddity 6).
func TestPatchOpenBillLinesRebaselinesBalance(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	patch(t, a, out.BillID, map[string]any{"status": "OPEN"})
	got := patch(t, a, out.BillID, map[string]any{"line_items": []any{line(3, 1000)}})
	wantDec4(t, "amount", p4(got.Amount), "3000")
	wantDec4(t, "subtotal", got.Subtotal, "3000")
	wantDec4(t, "balance_due", p4(got.BalanceDue), "3000")
	if len(got.LineItems) != 1 {
		t.Errorf("lines %+v", got.LineItems)
	}
	// Opening and repricing in one PATCH: re-baselined too.
	d := post(t, a, body(insertVendor(t, a), nil))
	got = patch(t, a, d.BillID, map[string]any{"status": "OPEN", "line_items": []any{line(2, 1000)}})
	wantDec4(t, "amount", p4(got.Amount), "2000")
	wantDec4(t, "balance_due", p4(got.BalanceDue), "2000")
	// Back to DRAFT and repriced in one PATCH: re-baselined.
	got = patch(t, a, d.BillID, map[string]any{"status": "DRAFT", "line_items": []any{line(5, 1000)}})
	wantDec4(t, "balance_due", p4(got.BalanceDue), "5000")
}

func TestPatchDraftLinesRebaselinesBalance(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), map[string]any{"discount_type": "AMOUNT", "discount_value": 100}))
	got := patch(t, a, out.BillID, map[string]any{"line_items": []any{line(2, 1000), line(1, 500)}})
	wantDec4(t, "subtotal", got.Subtotal, "2500")
	wantDec4(t, "amount", p4(got.Amount), "2400")
	wantDec4(t, "balance_due", p4(got.BalanceDue), "2400")
	if len(got.LineItems) != 2 {
		t.Errorf("lines %+v", got.LineItems)
	}
	// The new discount of the same PATCH applies to the new lines.
	got = patch(t, a, out.BillID, map[string]any{"discount_type": "PERCENT", "discount_value": 50, "line_items": []any{line(1, 1000)}})
	wantDec4(t, "amount", p4(got.Amount), "500")
	wantDec4(t, "balance_due", p4(got.BalanceDue), "500")
}

// A PATCH without line_items keeps the lines and the totals, even when the
// discount changes (oddity 6); an empty list clears the lines and zeroes the
// bill; null keeps the lines.
func TestPatchLineItemsOmittedNullAndEmpty(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	got := patch(t, a, out.BillID, map[string]any{"discount_type": "PERCENT", "discount_value": 50, "notes": "n"})
	wantDec4(t, "amount", p4(got.Amount), "1000")
	wantDec4(t, "discount_value", got.DiscountValue, "50")
	if len(got.LineItems) != 1 || *got.Notes != "n" {
		t.Errorf("%+v", got)
	}
	got = patch(t, a, out.BillID, map[string]any{"line_items": nil})
	if len(got.LineItems) != 1 {
		t.Errorf("null line_items changed the lines: %+v", got.LineItems)
	}
	got = patch(t, a, out.BillID, map[string]any{"line_items": []any{}})
	wantDec4(t, "amount", p4(got.Amount), "0")
	wantDec4(t, "subtotal", got.Subtotal, "0")
	wantDec4(t, "balance_due", p4(got.BalanceDue), "0")
	if got.LineItems == nil || len(got.LineItems) != 0 {
		t.Errorf("lines %+v", got.LineItems)
	}
}

func TestPatchOmitsKeepsAndNullClears(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor, project := insertVendor(t, a), insertProject(t, a)
	out := post(t, a, body(vendor, map[string]any{
		"project_id": project, "bill_number": "B", "po_number": "P", "issue_date": "2026-01-10", "payment_terms": "Net 30", "notes": "n",
	}))
	got := patch(t, a, out.BillID, map[string]any{"bill_number": "C", "currency": "eur", "project_id": nil, "due_date": "2026-03-01"})
	if *got.BillNumber != "C" || got.Currency != "EUR" || got.ProjectID != nil || got.DueDate.Format(time.DateOnly) != "2026-03-01" {
		t.Errorf("%+v", got)
	}
	if *got.PoNumber != "P" || got.IssueDate.Format(time.DateOnly) != "2026-01-10" || *got.PaymentTerms != "Net 30" ||
		*got.Notes != "n" || got.VendorID != vendor {
		t.Errorf("fields not sent changed: %+v", got)
	}
	got = patch(t, a, out.BillID, map[string]any{"notes": nil, "bill_number": nil, "discount_type": nil})
	if got.Notes != nil || got.BillNumber != nil || got.DiscountType != nil {
		t.Errorf("null did not clear: %+v", got)
	}
}

func TestPatchNullVendorIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	for _, field := range []string{"vendor_id", "currency", "status"} {
		rec := a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{field: nil})
		p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
		found := false
		for _, e := range p.Errors {
			found = found || strings.HasSuffix(e.Location, field)
		}
		if !found {
			t.Errorf("%s: no error at the field: %s", field, rec.Body)
		}
	}
}

func TestPatchUnknownReferencesAre422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out := post(t, a, body(insertVendor(t, a), nil))
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"vendor_id": uuid.New()}),
		http.StatusUnprocessableEntity, "", "vendor not found", "vendor_id", "vendor not found")
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"project_id": uuid.New()}),
		http.StatusUnprocessableEntity, "", "project not found", "project_id", "project not found")
	l := line(1, 1)
	l["expense_account_id"] = 987654
	wantProblem(t, a.Do(t, http.MethodPatch, billPath(out.BillID), map[string]any{"line_items": []any{l}}),
		http.StatusUnprocessableEntity, "", "expense account not found", "line_items.0.expense_account_id", "expense account not found")
	// The refused PATCH rolled back: the old line stands.
	if got := get(t, a, out.BillID); len(got.LineItems) != 1 {
		t.Errorf("lines %+v", got.LineItems)
	}
}

func TestBillRoutesRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// A signed-in staff user: authenticated, but not an admin.
	staffID := uuid.New()
	exec(t, a, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID)
	token, _, err := a.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *a
	staff.Cookie = platform.SessionCookie + "=" + token
	id := uuid.New()
	for _, c := range []struct{ method, p string }{
		{http.MethodPost, path},
		{http.MethodGet, path},
		{http.MethodGet, billPath(id)},
		{http.MethodPatch, billPath(id)},
		{http.MethodPost, billPath(id) + "/record-payment"},
		{http.MethodPost, billPath(id) + "/void"},
	} {
		wantProblem(t, a.Anon(t, c.method, c.p, map[string]any{}), http.StatusUnauthorized, "", "not authenticated", "", "")
		wantProblem(t, staff.Do(t, c.method, c.p, map[string]any{}), http.StatusForbidden, "", "admin required", "", "")
	}
}
