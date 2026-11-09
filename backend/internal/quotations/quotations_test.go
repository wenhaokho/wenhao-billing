package quotations_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
)

const path = "/api/v1/quotations"

func quotePath(id uuid.UUID) string { return path + "/" + id.String() }

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
	return apitest.New(t, func(api huma.API, d apitest.Deps) { quotations.Register(api, d.Pool, d.Clock) })
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

func exec(t *testing.T, a *apitest.App, sql string, args ...any) {
	t.Helper()
	if _, err := a.Pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func insertCustomer(t *testing.T, a *apitest.App) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO customers (customer_id, name) VALUES ($1, 'Acme')`, id)
	return id
}

func insertProject(t *testing.T, a *apitest.App, customer uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, a, `INSERT INTO projects (project_id, customer_id, code, name, currency) VALUES ($1, $2, $3, 'P', 'IDR')`,
		id, customer, id.String()[:8])
	return id
}

func line(qty, price any) map[string]any {
	return map[string]any{"description": "Work", "quantity": qty, "unit_price": price}
}

// body is a create body for customer: IDR, one line 1 × 1000, plus extra.
func body(customer uuid.UUID, extra map[string]any) map[string]any {
	b := map[string]any{"customer_id": customer, "currency": "IDR", "line_items": []any{line(1, 1000)}}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func post(t *testing.T, a *apitest.App, b map[string]any) quotations.QuotationOut {
	t.Helper()
	return apitest.Decode[quotations.QuotationOut](t, a.Do(t, http.MethodPost, path, b), http.StatusCreated)
}

func patch(t *testing.T, a *apitest.App, id uuid.UUID, b map[string]any) quotations.QuotationOut {
	t.Helper()
	return apitest.Decode[quotations.QuotationOut](t, a.Do(t, http.MethodPatch, quotePath(id), b), http.StatusOK)
}

func get(t *testing.T, a *apitest.App, id uuid.UUID) quotations.QuotationOut {
	t.Helper()
	return apitest.Decode[quotations.QuotationOut](t, a.Do(t, http.MethodGet, quotePath(id), nil), http.StatusOK)
}

func setStatus(t *testing.T, a *apitest.App, id uuid.UUID, status string) {
	t.Helper()
	exec(t, a, `UPDATE quotations SET status = $2 WHERE quotation_id = $1`, id, status)
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

func TestCreateQuotationTotals(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, tc := range []struct {
		name             string
		lines            []any
		extra            map[string]any
		subtotal, amount string
	}{
		{"no discount, raw sum rounded once", []any{line(2, "100.005"), line("0.5", "0.0001")}, nil, "200.0101", "200.0101"},
		{"percent", []any{line(1, 1000)}, map[string]any{"discount_type": "PERCENT", "discount_value": "12.5"}, "1000", "875"},
		{"amount", []any{line(1, 1000)}, map[string]any{"discount_type": "AMOUNT", "discount_value": 250}, "1000", "750"},
		{"amount clamps at zero", []any{line(1, 1000)}, map[string]any{"discount_type": "AMOUNT", "discount_value": 1500}, "1000", "0"},
		{"percent above 100 clamps at zero", []any{line(1, 1000)}, map[string]any{"discount_type": "PERCENT", "discount_value": 150}, "1000", "0"},
		{"type without value is no discount", []any{line(1, 1000)}, map[string]any{"discount_type": "PERCENT"}, "1000", "1000"},
		{"value without type is no discount", []any{line(1, 1000)}, map[string]any{"discount_value": 10}, "1000", "1000"},
		// 0.0001 × 50% is 0.00005: half-up gives 0.0001 (half-even would give 0).
		{"percent rounds half-up", []any{line(1, "0.0001")}, map[string]any{"discount_type": "PERCENT", "discount_value": 50}, "0.0001", "0.0001"},
		// 0.5 × 0.0001 = 0.00005: the subtotal rounds half-up too.
		{"subtotal rounds half-up", []any{line("0.5", "0.0001")}, nil, "0.0001", "0.0001"},
	} {
		b := body(c, tc.extra)
		b["line_items"] = tc.lines
		got := post(t, a, b)
		wantDec4(t, tc.name+" subtotal", got.Subtotal, tc.subtotal)
		wantDec4(t, tc.name+" amount", p4(got.Amount), tc.amount)
	}

	// Line amounts are qty × price rounded half-up; positions are as sent
	// (0 when left out), and the lines are listed by position.
	b := body(c, nil)
	b["line_items"] = []any{
		map[string]any{"description": "b", "quantity": "0.5", "unit_price": "0.0001", "position": 2},
		map[string]any{"description": "a", "quantity": 3, "unit_price": "1.5"},
	}
	got := post(t, a, b)
	if len(got.LineItems) != 2 {
		t.Fatalf("lines = %+v", got.LineItems)
	}
	first, second := got.LineItems[0], got.LineItems[1]
	if first.Description != "a" || first.Position != 0 || second.Description != "b" || second.Position != 2 {
		t.Errorf("lines = %+v", got.LineItems)
	}
	wantDec4(t, "line a", p4(first.Amount), "4.5")
	wantDec4(t, "line b", p4(second.Amount), "0.0001")

	// Decimals are written with four places.
	rec := a.Do(t, http.MethodGet, quotePath(got.QuotationID), nil)
	if !strings.Contains(rec.Body.String(), `"amount":"4.5001"`) || !strings.Contains(rec.Body.String(), `"unit_price":"1.5000"`) {
		t.Errorf("body: %s", rec.Body)
	}
}

func TestCreateDefaultsNumberIssueValidUntil(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	// The clock is 2026-01-15: today, and today + 30 days.
	first := post(t, a, body(c, map[string]any{"currency": "usd"}))
	if first.QuotationNumber == nil || *first.QuotationNumber != "Q20260001" {
		t.Errorf("number = %v, want Q20260001", first.QuotationNumber)
	}
	wantDate(t, "issue_date", first.IssueDate, "2026-01-15")
	wantDate(t, "valid_until", first.ValidUntil, "2026-02-14")
	if first.Status != "DRAFT" || first.Currency != "usd" || first.CustomerID != c || first.ProjectID != nil ||
		first.DiscountType != nil || first.DiscountValue != nil || first.LastSentAt != nil || first.AcceptedAt != nil ||
		first.AcceptedBy != nil || first.ConvertedInvoiceID != nil || first.Notes != nil || first.Footer != nil {
		t.Errorf("%+v", first)
	}
	// "" is generated as null is; a supplied number is kept and does not
	// count toward the sequence.
	second := post(t, a, body(c, map[string]any{"quotation_number": ""}))
	if second.QuotationNumber == nil || *second.QuotationNumber != "Q20260002" {
		t.Errorf("number = %v, want Q20260002", second.QuotationNumber)
	}
	own := post(t, a, body(c, map[string]any{"quotation_number": "QUO-0001"}))
	if own.QuotationNumber == nil || *own.QuotationNumber != "QUO-0001" {
		t.Errorf("number = %v, want QUO-0001", own.QuotationNumber)
	}
	third := post(t, a, body(c, map[string]any{"quotation_number": nil}))
	if third.QuotationNumber == nil || *third.QuotationNumber != "Q20260003" {
		t.Errorf("number = %v, want Q20260003", third.QuotationNumber)
	}
	// valid_until defaults from the issue date sent; a valid_until sent is
	// kept, even before the issue date.
	dated := post(t, a, body(c, map[string]any{"issue_date": "2025-12-20"}))
	wantDate(t, "issue_date", dated.IssueDate, "2025-12-20")
	wantDate(t, "valid_until", dated.ValidUntil, "2026-01-19")
	both := post(t, a, body(c, map[string]any{"issue_date": "2026-03-01", "valid_until": "2026-02-01"}))
	wantDate(t, "valid_until", both.ValidUntil, "2026-02-01")

	// Every field is in the body, in QuotationOut's order.
	rec := a.Do(t, http.MethodGet, quotePath(first.QuotationID), nil)
	want := []string{
		"quotation_id", "customer_id", "project_id", "quotation_number", "po_so_number", "currency", "subtotal",
		"discount_type", "discount_value", "amount", "status", "issue_date", "valid_until", "payment_terms", "notes",
		"footer", "last_sent_at", "accepted_at", "accepted_by", "converted_invoice_id", "created_at", "line_items",
	}
	dec := json.NewDecoder(strings.NewReader(rec.Body.String()))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("keys = %v\nwant %v", keys, want)
	}
	var lineKeys []map[string]json.RawMessage
	var raw struct {
		LineItems json.RawMessage `json:"line_items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw.LineItems, &lineKeys); err != nil || len(lineKeys) != 1 {
		t.Fatalf("%v: %s", err, raw.LineItems)
	}
	for _, k := range []string{"line_item_id", "item_id", "description", "quantity", "unit_price", "amount", "position"} {
		if _, ok := lineKeys[0][k]; !ok {
			t.Errorf("line missing %s: %s", k, raw.LineItems)
		}
	}
	if _, ok := lineKeys[0]["created_at"]; ok || len(lineKeys[0]) != 7 {
		t.Errorf("line keys: %s", raw.LineItems)
	}
}

func TestCreateValidUntilOutOfRangeIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	// The last issue date whose default fits: 9999-12-01 + 30 days is 9999-12-31.
	last := post(t, a, body(c, map[string]any{"issue_date": "9999-12-01"}))
	wantDate(t, "valid_until", last.ValidUntil, "9999-12-31")
	for _, issue := range []string{"9999-12-02", "9999-12-31"} {
		wantProblem(t, a.Do(t, http.MethodPost, path, body(c, map[string]any{"issue_date": issue})),
			http.StatusBadRequest, "valid_until_out_of_range", "valid until is out of range", "", "")
	}
	// A valid_until sent needs no default.
	got := post(t, a, body(c, map[string]any{"issue_date": "9999-12-15", "valid_until": "9999-12-31"}))
	wantDate(t, "valid_until", got.ValidUntil, "9999-12-31")
	if n := countRows(t, a, "quotations"); n != 2 {
		t.Errorf("stored %d quotations, want 2", n)
	}
}

func TestCreateConcurrentNumbersAreConsecutive(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	const n = 10
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { recs[i] = a.Do(t, http.MethodPost, path, body(c, nil)) })
	}
	wg.Wait()
	numbers := make([]string, 0, n)
	for i, rec := range recs {
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d: %d %s", i, rec.Code, rec.Body)
		}
		var out quotations.QuotationOut
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.QuotationNumber == nil {
			t.Fatalf("create %d: %v %s", i, err, rec.Body)
		}
		numbers = append(numbers, *out.QuotationNumber)
	}
	slices.Sort(numbers)
	for i, got := range numbers {
		if want := fmt.Sprintf("Q2026%04d", i+1); got != want {
			t.Errorf("numbers = %v, want Q20260001..Q20260010", numbers)
			break
		}
	}
}

func TestCreateRequiresLineItems(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	// Left out, empty, and (unlike Python's 422) null.
	for _, b := range []map[string]any{
		{"customer_id": c, "currency": "IDR"},
		{"customer_id": c, "currency": "IDR", "line_items": []any{}},
		{"customer_id": c, "currency": "IDR", "line_items": nil},
	} {
		wantProblem(t, a.Do(t, http.MethodPost, path, b), http.StatusBadRequest,
			"line_items_required", "quotation must have at least one line item", "", "")
	}
	if n := countRows(t, a, "quotations"); n != 0 {
		t.Errorf("stored %d quotations", n)
	}
}

func TestCreateDuplicateNumberIs409(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	post(t, a, body(c, map[string]any{"quotation_number": "QUO-1"}))
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, map[string]any{"quotation_number": "QUO-1"})),
		http.StatusConflict, "quotation_number_exists", "quotation number already exists", "", "")
	other := post(t, a, body(c, nil))
	wantProblem(t, a.Do(t, http.MethodPatch, quotePath(other.QuotationID), map[string]any{"quotation_number": "QUO-1"}),
		http.StatusConflict, "quotation_number_exists", "quotation number already exists", "", "")
	if n := countRows(t, a, "quotations"); n != 2 {
		t.Errorf("stored %d quotations, want 2", n)
	}
}

func TestCreateUnknownCustomerIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	unknown := uuid.New()
	for _, tc := range []struct {
		extra             map[string]any
		location, message string
	}{
		{map[string]any{"customer_id": unknown}, "customer_id", "customer not found"},
		{map[string]any{"project_id": unknown}, "project_id", "project not found"},
		{
			map[string]any{"line_items": []any{line(1, 1), map[string]any{"item_id": unknown, "description": "x", "quantity": 1, "unit_price": 1}}},
			"line_items.1.item_id", "item not found",
		},
	} {
		wantProblem(t, a.Do(t, http.MethodPost, path, body(c, tc.extra)), http.StatusUnprocessableEntity, "", tc.message, tc.location, tc.message)
	}
	// The same on PATCH.
	out := post(t, a, body(c, nil))
	wantProblem(t, a.Do(t, http.MethodPatch, quotePath(out.QuotationID), map[string]any{"customer_id": unknown}),
		http.StatusUnprocessableEntity, "", "customer not found", "customer_id", "customer not found")
	wantProblem(t, a.Do(t, http.MethodPatch, quotePath(out.QuotationID), map[string]any{"project_id": unknown}),
		http.StatusUnprocessableEntity, "", "project not found", "project_id", "project not found")
	wantProblem(t, a.Do(t, http.MethodPatch, quotePath(out.QuotationID), map[string]any{
		"line_items": []any{map[string]any{"item_id": unknown, "description": "x", "quantity": 1, "unit_price": 1}},
	}), http.StatusUnprocessableEntity, "", "item not found", "line_items.0.item_id", "item not found")
	// The refused PATCH left the lines alone; nothing else was stored.
	if got := get(t, a, out.QuotationID); len(got.LineItems) != 1 {
		t.Errorf("lines = %+v", got.LineItems)
	}
	if n := countRows(t, a, "quotations"); n != 1 {
		t.Errorf("stored %d quotations, want 1", n)
	}
}

func TestCreateFieldValidationIs422(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	// Strings longer than their column (Python answered 500).
	for field, n := range map[string]int{"quotation_number": 51, "po_so_number": 101, "payment_terms": 31} {
		rec := a.Do(t, http.MethodPost, path, body(c, map[string]any{field: strings.Repeat("x", n)}))
		p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
		if len(p.Errors) != 1 || p.Errors[0].Location != field {
			t.Errorf("%s: %s", field, rec.Body)
		}
		post(t, a, body(c, map[string]any{field: strings.Repeat("y", n-1)}))
	}
	for _, cur := range []string{"ID", "IDRX"} {
		if rec := a.Do(t, http.MethodPost, path, body(c, map[string]any{"currency": cur})); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("currency %q: %d %s", cur, rec.Code, rec.Body)
		}
	}
	const negative = "Input should be greater than or equal to 0"
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, map[string]any{"discount_value": -1})),
		http.StatusUnprocessableEntity, "", negative, "discount_value", negative)
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, map[string]any{"line_items": []any{line(1, 1), line(-1, 1)}})),
		http.StatusUnprocessableEntity, "", negative, "line_items.1.quantity", negative)
	wantProblem(t, a.Do(t, http.MethodPost, path, body(c, map[string]any{"line_items": []any{line(1, "-0.5")}})),
		http.StatusUnprocessableEntity, "", negative, "line_items.0.unit_price", negative)
	if rec := a.Do(t, http.MethodPost, path, body(c, map[string]any{"discount_type": "FLAT"})); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("discount_type: %d %s", rec.Code, rec.Body)
	}
	// On PATCH a null for a NOT NULL column is a 422 at the field (Python: 500).
	out := post(t, a, body(c, nil))
	for _, field := range []string{"customer_id", "currency"} {
		rec := a.Do(t, http.MethodPatch, quotePath(out.QuotationID), map[string]any{field: nil})
		p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
		if len(p.Errors) != 1 || p.Errors[0].Location != field || p.Errors[0].Message != "value cannot be null" {
			t.Errorf("%s: %s", field, rec.Body)
		}
	}
	wantProblem(t, a.Do(t, http.MethodPatch, quotePath(out.QuotationID), map[string]any{"discount_value": -1}),
		http.StatusUnprocessableEntity, "", negative, "discount_value", negative)
}

func countRows(t *testing.T, a *apitest.App, table string) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// listed creates a quotation through the API and stamps its created_at, so
// the list order is known.
func listed(t *testing.T, a *apitest.App, b map[string]any, created string) uuid.UUID {
	t.Helper()
	out := post(t, a, b)
	exec(t, a, `UPDATE quotations SET created_at = $2::timestamp WHERE quotation_id = $1`, out.QuotationID, created)
	return out.QuotationID
}

func listIDs(t *testing.T, a *apitest.App, query string) []uuid.UUID {
	t.Helper()
	rec := a.Do(t, http.MethodGet, path+query, nil)
	list := apitest.Decode[[]quotations.QuotationOut](t, rec, http.StatusOK)
	if strings.TrimSpace(rec.Body.String()) == "null" {
		t.Errorf("%s: null list", query)
	}
	ids := make([]uuid.UUID, 0, len(list))
	for _, q := range list {
		ids = append(ids, q.QuotationID)
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

func TestListFiltersOrderLimit(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantIDs(t, "empty", listIDs(t, a, ""))
	c := insertCustomer(t, a)
	other := insertCustomer(t, a)
	p := insertProject(t, a, c)
	oldest := listed(t, a, body(c, map[string]any{"project_id": p}), "2026-01-01 10:00:00")
	sent := listed(t, a, body(other, nil), "2026-01-02 10:00:00")
	accepted := listed(t, a, body(c, nil), "2026-01-03 10:00:00")
	// Same created_at as accepted: the id breaks the tie, descending.
	tie := listed(t, a, body(c, nil), "2026-01-03 10:00:00")
	setStatus(t, a, sent, "SENT")
	setStatus(t, a, accepted, "ACCEPTED")
	newer, older := accepted, tie
	if tie.String() > accepted.String() {
		newer, older = tie, accepted
	}

	wantIDs(t, "all", listIDs(t, a, ""), newer, older, sent, oldest)
	wantIDs(t, "two statuses", listIDs(t, a, "?status=DRAFT&status=SENT"), tie, sent, oldest)
	wantIDs(t, "any status string", listIDs(t, a, "?status=nonsense"))
	wantIDs(t, "customer", listIDs(t, a, "?customer_id="+other.String()), sent)
	wantIDs(t, "project", listIDs(t, a, "?project_id="+p.String()), oldest)
	wantIDs(t, "combined", listIDs(t, a, "?status=DRAFT&customer_id="+c.String()), tie, oldest)
	wantIDs(t, "limit", listIDs(t, a, "?limit=2"), newer, older)
	// Every quotation of the page carries its lines.
	for _, q := range apitest.Decode[[]quotations.QuotationOut](t, a.Do(t, http.MethodGet, path, nil), http.StatusOK) {
		if len(q.LineItems) != 1 {
			t.Errorf("%s lines = %+v", q.QuotationID, q.LineItems)
		}
	}
	// limit is 1..500 (Python had no lower bound: a negative one was a 500).
	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=501"} {
		if rec := a.Do(t, http.MethodGet, path+q, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
	wantIDs(t, "limit 500", listIDs(t, a, "?limit=500"), newer, older, sent, oldest)
}

func TestGetQuotation404(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	wantProblem(t, a.Do(t, http.MethodGet, quotePath(uuid.New()), nil), http.StatusNotFound,
		"quotation_not_found", "quotation not found", "", "")
}

func TestPatchRepricesOnDiscountOnly(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	out := post(t, a, body(c, nil))
	lineID := out.LineItems[0].LineItemID

	got := patch(t, a, out.QuotationID, map[string]any{"discount_type": "PERCENT", "discount_value": 10})
	wantDec4(t, "subtotal", got.Subtotal, "1000")
	wantDec4(t, "amount", p4(got.Amount), "900")
	if len(got.LineItems) != 1 || got.LineItems[0].LineItemID != lineID {
		t.Errorf("lines replaced: %+v", got.LineItems)
	}
	// Only the value changes: the stored type applies it.
	got = patch(t, a, out.QuotationID, map[string]any{"discount_value": 25})
	wantDec4(t, "amount", p4(got.Amount), "750")
	// A null value clears the discount.
	got = patch(t, a, out.QuotationID, map[string]any{"discount_value": nil})
	if got.DiscountValue != nil || got.DiscountType == nil || *got.DiscountType != "PERCENT" {
		t.Errorf("discount = %v %v", got.DiscountType, got.DiscountValue)
	}
	wantDec4(t, "amount", p4(got.Amount), "1000")

	// New lines reprice with the stored discount; SENT is editable too.
	setStatus(t, a, out.QuotationID, "SENT")
	got = patch(t, a, out.QuotationID, map[string]any{
		"discount_type": "AMOUNT", "discount_value": 100, "line_items": []any{line(2, 300), line(1, "0.5")},
	})
	wantDec4(t, "subtotal", got.Subtotal, "600.5")
	wantDec4(t, "amount", p4(got.Amount), "500.5")
	if got.Status != "SENT" || len(got.LineItems) != 2 {
		t.Errorf("%+v", got)
	}
	// Fields left out are kept; fields sent are applied as sent.
	got = patch(t, a, out.QuotationID, map[string]any{"notes": "n", "currency": "sgd", "valid_until": nil, "issue_date": "2026-02-01"})
	if got.Notes == nil || *got.Notes != "n" || got.Currency != "sgd" || got.ValidUntil != nil {
		t.Errorf("%+v", got)
	}
	wantDate(t, "issue_date", got.IssueDate, "2026-02-01")
	wantDec4(t, "amount", p4(got.Amount), "500.5")
	if got.QuotationNumber == nil || *got.QuotationNumber != "Q20260001" {
		t.Errorf("number = %v", got.QuotationNumber)
	}
}

func TestPatchNullNumberClears(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	out := post(t, a, body(c, nil))
	// Kept (oddity 17): null clears the number and nothing re-generates it.
	if got := patch(t, a, out.QuotationID, map[string]any{"quotation_number": nil}); got.QuotationNumber != nil {
		t.Errorf("number = %v, want null", *got.QuotationNumber)
	}
	// "" is stored as "" on PATCH (create would generate one) ...
	if got := patch(t, a, out.QuotationID, map[string]any{"quotation_number": ""}); got.QuotationNumber == nil || *got.QuotationNumber != "" {
		t.Errorf("number = %v, want \"\"", got.QuotationNumber)
	}
	// ... so a second "" is a taken number.
	other := post(t, a, body(c, nil))
	wantProblem(t, a.Do(t, http.MethodPatch, quotePath(other.QuotationID), map[string]any{"quotation_number": ""}),
		http.StatusConflict, "quotation_number_exists", "quotation number already exists", "", "")
}

func TestPatchEmptyLinesAccepted(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	out := post(t, a, body(c, nil))
	// null keeps the lines.
	if got := patch(t, a, out.QuotationID, map[string]any{"line_items": nil}); len(got.LineItems) != 1 {
		t.Errorf("lines = %+v", got.LineItems)
	}
	// Kept (oddity 18): [] leaves no lines and a zero total.
	got := patch(t, a, out.QuotationID, map[string]any{"line_items": []any{}})
	if len(got.LineItems) != 0 {
		t.Errorf("lines = %+v", got.LineItems)
	}
	wantDec4(t, "subtotal", got.Subtotal, "0")
	wantDec4(t, "amount", p4(got.Amount), "0")
	if n := countRows(t, a, "quotation_line_items"); n != 0 {
		t.Errorf("%d lines stored", n)
	}
	rec := a.Do(t, http.MethodGet, quotePath(out.QuotationID), nil)
	if !strings.Contains(rec.Body.String(), `"line_items":[]`) {
		t.Errorf("body: %s", rec.Body)
	}
}

func TestPatchNonEditableIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, status := range []string{"ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
		out := post(t, a, body(c, nil))
		setStatus(t, a, out.QuotationID, status)
		wantProblem(t, a.Do(t, http.MethodPatch, quotePath(out.QuotationID), map[string]any{"notes": "x"}),
			http.StatusBadRequest, "quotation_not_editable", "only DRAFT or SENT quotations can be edited (got "+status+")", "", "")
		if got := get(t, a, out.QuotationID); got.Notes != nil {
			t.Errorf("%s: notes = %q", status, *got.Notes)
		}
	}
}

func TestPatchUnknownIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := uuid.New()
	wantProblem(t, a.Do(t, http.MethodPatch, quotePath(id), map[string]any{"notes": "x"}),
		http.StatusBadRequest, "quotation_not_found", "quotation "+id.String()+" not found", "", "")
}

func TestDeleteDraft204(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	out := post(t, a, body(c, nil))
	keep := post(t, a, body(c, nil))
	rec := a.Do(t, http.MethodDelete, quotePath(out.QuotationID), nil)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	wantProblem(t, a.Do(t, http.MethodGet, quotePath(out.QuotationID), nil), http.StatusNotFound, "quotation_not_found", "quotation not found", "", "")
	// Its lines went with it; the other quotation is untouched.
	if n := countRows(t, a, "quotation_line_items"); n != 1 {
		t.Errorf("%d lines stored, want 1", n)
	}
	get(t, a, keep.QuotationID)
	// A second delete finds nothing: Python's 400.
	wantProblem(t, a.Do(t, http.MethodDelete, quotePath(out.QuotationID), nil),
		http.StatusBadRequest, "quotation_not_found", "quotation "+out.QuotationID.String()+" not found", "", "")
}

func TestDeleteNonDraftIs400(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	for _, status := range []string{"SENT", "ACCEPTED", "VOID"} {
		out := post(t, a, body(c, nil))
		setStatus(t, a, out.QuotationID, status)
		wantProblem(t, a.Do(t, http.MethodDelete, quotePath(out.QuotationID), nil),
			http.StatusBadRequest, "quotation_not_deletable", "only DRAFT quotations can be deleted (got "+status+")", "", "")
		get(t, a, out.QuotationID)
	}
}

func TestQuotationRoutesRequireAdmin(t *testing.T) {
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
		{http.MethodGet, quotePath(id)},
		{http.MethodPatch, quotePath(id)},
		{http.MethodDelete, quotePath(id)},
	} {
		wantProblem(t, a.Anon(t, c.method, c.p, map[string]any{}), http.StatusUnauthorized, "", "not authenticated", "", "")
		wantProblem(t, staff.Do(t, c.method, c.p, map[string]any{}), http.StatusForbidden, "", "admin required", "", "")
	}
}
