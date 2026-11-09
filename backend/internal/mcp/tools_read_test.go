package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tests of the 17 read tools. They drive each tool through the SDK's own
// client over the transport, as the Python tests called the tool functions
// (test_mcp_read_customers.py, test_mcp_read_invoices.py,
// test_mcp_read_misc.py and test_mcp_reads_all.py), with the same values. Each
// test has its own database, so each seeds what it needs with plain SQL.

type readHarness struct{ *fix1Harness }

func newReadHarness(t *testing.T) *readHarness {
	t.Helper()
	return &readHarness{newFix1Harness(t, func(s *mcp.Server, d Deps) { registerReadTools(s, d) })}
}

func (h *readHarness) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// call runs a tool and returns its result, which must not be isError.
func (h *readHarness) call(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := h.connect(t).CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: isError: %s", name, resultText(res))
	}
	return res
}

// obj is the structured result of a dict-returning tool.
func (h *readHarness) obj(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := h.call(t, name, args)
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s: structuredContent %#v, want an object", name, res.StructuredContent)
	}
	return m
}

// rows is the {"result": [...]} list of a list tool.
func (h *readHarness) rows(t *testing.T, name string, args map[string]any) []map[string]any {
	t.Helper()
	m := h.obj(t, name, args)
	if len(m) != 1 {
		t.Fatalf("%s: structuredContent %v, want only a result", name, m)
	}
	list, ok := m["result"].([]any)
	if !ok {
		t.Fatalf("%s: result %#v, want a list", name, m["result"])
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		row, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("%s: row %#v, want an object", name, e)
		}
		out = append(out, row)
	}
	return out
}

func (h *readHarness) customer(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO customers (customer_id, name) VALUES ($1, $2)`, id, name)
	return id
}

func (h *readHarness) vendor(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO vendors (vendor_id, name) VALUES ($1, $2)`, id, name)
	return id
}

func (h *readHarness) item(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO items (item_id, name, item_type, default_currency, default_unit_price) VALUES ($1, $2, 'SERVICE', 'USD', 100)`, id, name)
	return id
}

func (h *readHarness) bill(t *testing.T, vendor uuid.UUID, number, status, amount string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO bills (bill_id, vendor_id, bill_number, currency, amount, balance_due, status) VALUES ($1, $2, NULLIF($3, ''), 'USD', $4::text::numeric, $4::text::numeric, $5)`,
		id, vendor, number, amount, status)
	return id
}

func (h *readHarness) invoice(t *testing.T, customer uuid.UUID, number, kind, status, amount string, template bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, invoice_number, currency, amount, balance_due, status, is_template) VALUES ($1, $2, $3, NULLIF($4, ''), 'USD', $5::text::numeric, $5::text::numeric, $6, $7)`,
		id, customer, kind, number, amount, status, template)
	return id
}

func (h *readHarness) quotation(t *testing.T, customer uuid.UUID, number, status, amount, currency string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO quotations (quotation_id, customer_id, quotation_number, currency, amount, status) VALUES ($1, $2, NULLIF($3, ''), $4, $5::text::numeric, $6)`,
		id, customer, number, currency, amount, status)
	return id
}

func (h *readHarness) project(t *testing.T, customer uuid.UUID, code, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO projects (project_id, customer_id, code, name, currency) VALUES ($1, $2, $3, $4, 'USD')`, id, customer, code, name)
	return id
}

func (h *readHarness) payment(t *testing.T, payer, status, amount string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, status, confidence_score) VALUES ($1, $2::text::numeric, 'USD', $3, '2026-01-15', 'EMAIL', $4, 97.5)`,
		id, amount, payer, status)
	return id
}

// wantObject fails unless got equals want as JSON.
func wantObject(t *testing.T, got any, want string) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %s, want %s", b, want)
	}
}

func ids(rows []map[string]any, key string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprint(r[key]))
	}
	return out
}

// textKeys is the top-level keys of the JSON object the result's text block
// holds, in the order the server wrote them.
func textKeys(t *testing.T, res *mcp.CallToolResult) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(resultText(res)))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("text %q is not an object", resultText(res))
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// ---- test_mcp_read_customers.py ----

func TestResolveCustomerUniqueMatch(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.customer(t, "Acme Pte Ltd")
	wantObject(t, h.obj(t, "resolve_customer", map[string]any{"query": "Acme"}), fmt.Sprintf(`{"customer_id": %q}`, id))
}

func TestResolveCustomerAmbiguousReturnsCandidates(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	a := h.customer(t, "Acme Pte Ltd")
	b := h.customer(t, "Acme Global Inc")
	out := h.obj(t, "resolve_customer", map[string]any{"query": "Acme"})
	if _, has := out["customer_id"]; has {
		t.Fatalf("ambiguous resolve took an action: %v", out)
	}
	cands, _ := out["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("candidates %v, want 2", out["candidates"])
	}
	got := map[string]bool{}
	for _, c := range cands {
		m := c.(map[string]any)
		if len(m) != 2 || m["customer_id"] == nil || m["name"] == nil {
			t.Fatalf("candidate %v, want exactly customer_id and name", m)
		}
		got[m["customer_id"].(string)] = true
	}
	if !got[a.String()] || !got[b.String()] {
		t.Fatalf("candidates %v, want %s and %s", got, a, b)
	}
}

func TestResolveCustomerNoMatchReturnsEmptyCandidates(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	h.customer(t, "Acme Pte Ltd")
	wantObject(t, h.obj(t, "resolve_customer", map[string]any{"query": "Nonexistent Corp"}), `{"candidates": []}`)
}

func TestListCustomersFiltersByNameSubstring(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	h.customer(t, "Acme Pte Ltd")
	h.customer(t, "Acme Global Inc")
	h.customer(t, "Zephyr Trading")
	rows := h.rows(t, "list_customers", map[string]any{"query": "Acme"})
	if len(rows) != 2 {
		t.Fatalf("rows %v, want 2", rows)
	}
	for _, r := range rows {
		if !strings.Contains(r["name"].(string), "Acme") {
			t.Fatalf("row %v does not match Acme", r)
		}
	}
}

func TestListCustomersNoQueryReturnsAll(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.customer(t, "Acme Pte Ltd")
	rows := h.rows(t, "list_customers", nil)
	if len(rows) != 1 || rows[0]["customer_id"] != id.String() || rows[0]["name"] != "Acme Pte Ltd" {
		t.Fatalf("rows %v", rows)
	}
	if len(rows[0]) != 3 {
		t.Fatalf("row %v, want customer_id, name and contact_email only", rows[0])
	}
}

func TestGetCustomerFound(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.customer(t, "Acme Pte Ltd")
	out := h.obj(t, "get_customer", map[string]any{"customer_id": id.String()})
	if out["customer_id"] != id.String() || out["name"] != "Acme Pte Ltd" {
		t.Fatalf("customer %v", out)
	}
}

func TestGetCustomerNotFoundReturnsEmptyDict(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	wantObject(t, h.obj(t, "get_customer", map[string]any{"customer_id": uuid.NewString()}), `{}`)
}

// ---- test_mcp_read_invoices.py ----

func TestListInvoicesExcludesTemplates(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	h.invoice(t, c, "", "MILESTONE", "DRAFT", "500.0000", true)
	rows := h.rows(t, "list_invoices", nil)
	if len(rows) != 1 || rows[0]["invoice_id"] != id.String() {
		t.Fatalf("rows %v, want only the non-template invoice", rows)
	}
}

func TestListInvoicesFiltersByStatus(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.invoice(t, c, "", "MILESTONE", "SENT", "100.0000", false)
	h.invoice(t, c, "", "MILESTONE", "DRAFT", "200.0000", false)
	rows := h.rows(t, "list_invoices", map[string]any{"status": []string{"SENT"}})
	if len(rows) != 1 || rows[0]["status"] != "SENT" {
		t.Fatalf("rows %v", rows)
	}
}

func TestListInvoicesFiltersByCustomerID(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	other := h.customer(t, "Other Co")
	id := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	h.invoice(t, other, "", "MILESTONE", "SENT", "300.0000", false)
	rows := h.rows(t, "list_invoices", map[string]any{"customer_id": c.String()})
	if len(rows) != 1 || rows[0]["invoice_id"] != id.String() {
		t.Fatalf("rows %v", rows)
	}
}

func TestListInvoicesFiltersByInvoiceType(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.invoice(t, c, "", "MILESTONE", "SENT", "100.0000", false)
	h.invoice(t, c, "", "RECURRING", "SENT", "100.0000", false)
	rows := h.rows(t, "list_invoices", map[string]any{"invoice_type": "RECURRING"})
	if len(rows) != 1 || rows[0]["invoice_type"] != "RECURRING" {
		t.Fatalf("rows %v", rows)
	}
}

// An empty invoice_type and an empty status list are no filter (Python:
// `if invoice_type:` and `if status:`).
func TestListInvoicesEmptyFiltersAreNoFilter(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.invoice(t, c, "", "MILESTONE", "SENT", "100.0000", false)
	h.invoice(t, c, "", "RECURRING", "DRAFT", "100.0000", false)
	for name, args := range map[string]map[string]any{
		"empty type":   {"invoice_type": ""},
		"empty status": {"status": []string{}},
		"null filters": {"invoice_type": nil, "status": nil, "customer_id": nil},
		"empty id":     {"customer_id": ""},
	} {
		if rows := h.rows(t, "list_invoices", args); len(rows) != 2 {
			t.Fatalf("%s: %d rows, want 2", name, len(rows))
		}
	}
}

func TestListInvoicesLimitClampedTo500(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	if rows := h.rows(t, "list_invoices", map[string]any{"limit": 10000}); len(rows) != 1 {
		t.Fatalf("rows %v", rows)
	}
}

func TestListInvoicesReturnsMoneyAsDecimalStrings(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	rows := h.rows(t, "list_invoices", nil)
	if rows[0]["amount"] != "1000.0000" {
		t.Fatalf("amount %#v, want the string 1000.0000", rows[0]["amount"])
	}
}

func TestGetInvoiceFound(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	out := h.obj(t, "get_invoice", map[string]any{"invoice_id": id.String()})
	if out["invoice_id"] != id.String() || out["invoice_number"] != "INV-0001" || out["amount"] != "1000.0000" {
		t.Fatalf("invoice %v", out)
	}
}

func TestGetInvoiceNotFoundReturnsEmptyDict(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	wantObject(t, h.obj(t, "get_invoice", map[string]any{"invoice_id": uuid.NewString()}), `{}`)
}

// A template is not in the list but get_invoice finds it (Python: db.get).
func TestGetInvoiceFindsATemplate(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.invoice(t, c, "", "RECURRING", "DRAFT", "10.0000", true)
	out := h.obj(t, "get_invoice", map[string]any{"invoice_id": id.String()})
	if out["is_template"] != true {
		t.Fatalf("invoice %v, want a template", out)
	}
}

func TestGetAndListInvoicesIncludeWithholdingFields(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	detail := h.obj(t, "get_invoice", map[string]any{"invoice_id": id.String()})
	row := h.rows(t, "list_invoices", nil)[0]
	for _, out := range []map[string]any{detail, row} {
		for _, k := range []string{"wht_type", "wht_rate", "wht_gross_up", "wht_amount", "expected_cash"} {
			if _, ok := out[k]; !ok {
				t.Fatalf("%v has no %s", out, k)
			}
		}
		if out["wht_type"] != "NONE" || out["expected_cash"] != out["balance_due"] {
			t.Fatalf("withholding fields %v", out)
		}
	}
}

// expected_cash is the cash still expected after the withholding that has
// not been booked: balance_due less wht_amount.
func TestInvoiceExpectedCashNetsUnbookedWithholding(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	h.exec(t, `UPDATE invoices SET wht_type = 'PPH23', wht_rate = 2, wht_amount = 20 WHERE invoice_id = $1`, id)
	out := h.obj(t, "get_invoice", map[string]any{"invoice_id": id.String()})
	if out["wht_amount"] != "20.0000" || out["wht_rate"] != "2.0000" || out["expected_cash"] != "980.0000" {
		t.Fatalf("invoice %v, want wht_amount 20.0000, wht_rate 2.0000 and expected_cash 980.0000", out)
	}
}

// ---- test_mcp_read_misc.py ----

func TestGetBusinessProfileReturnsDict(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	h.exec(t, `UPDATE business_profile SET name = 'Wenhao Studio', address = '1 Raffles Place, Singapore', contact_email = 'hello@wenhao.example', contact_phone = '+65 6000 0000' WHERE id = 1`)
	out := h.obj(t, "get_business_profile", nil)
	if out["name"] != "Wenhao Studio" || out["contact_email"] != "hello@wenhao.example" {
		t.Fatalf("profile %v", out)
	}
	if _, ok := out["updated_at"].(string); !ok {
		t.Fatalf("updated_at %#v, want a string", out["updated_at"])
	}
	if out["has_logo"] != false || out["id"] != float64(1) {
		t.Fatalf("profile %v, want id 1 and has_logo false", out)
	}
}

func TestGetBusinessProfileOmitsInlineLogo(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	h.exec(t, `UPDATE business_profile SET logo_url = $1 WHERE id = 1`, "data:image/png;base64,"+strings.Repeat("A", 200_000))
	out := h.obj(t, "get_business_profile", nil)
	if out["has_logo"] != true || out["logo_url"] != "<inline image/png image, 195 KB omitted>" {
		t.Fatalf("logo %v / %v", out["has_logo"], out["logo_url"])
	}
}

// A data: URI with no media type prints as "unknown", as in Python.
func TestGetBusinessProfileInlineLogoWithoutMime(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	h.exec(t, `UPDATE business_profile SET logo_url = $1 WHERE id = 1`, "data:,"+strings.Repeat("A", 2048))
	out := h.obj(t, "get_business_profile", nil)
	if out["logo_url"] != "<inline unknown image, 2 KB omitted>" {
		t.Fatalf("logo_url %v", out["logo_url"])
	}
}

func TestGetBusinessProfileKeepsRegularLogoURL(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	h.exec(t, `UPDATE business_profile SET logo_url = 'https://cdn.example/logo.png' WHERE id = 1`)
	out := h.obj(t, "get_business_profile", nil)
	if out["has_logo"] != true || out["logo_url"] != "https://cdn.example/logo.png" {
		t.Fatalf("logo %v / %v", out["has_logo"], out["logo_url"])
	}
}

// Go change 13: the profile is row id 1, and no row is {}.
func TestGetBusinessProfileMissingRowIsEmpty(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	h.exec(t, `DELETE FROM business_profile`)
	wantObject(t, h.obj(t, "get_business_profile", nil), `{}`)
}

func TestGetStatsReturnsDictWithExpectedKeys(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	out := h.obj(t, "get_stats", nil)
	for _, k := range []string{
		"awaiting_review_count", "draft_count", "sent_count", "customer_count", "auto_cleared_last_30d",
		"open_ar_by_currency", "open_quotation_count", "open_quotation_pipeline", "mrr_by_currency",
		"base_currency", "open_ar_base_by_currency", "open_ar_unconverted",
	} {
		if _, ok := out[k]; !ok {
			t.Fatalf("stats %v has no %s", out, k)
		}
	}
	if out["draft_count"] != float64(0) || out["customer_count"] != float64(0) {
		t.Fatalf("empty database: %v", out)
	}
	wantObject(t, out["open_ar_by_currency"], `[]`)
	wantObject(t, out["open_quotation_pipeline"], `[]`)
	if out["base_currency"] != "IDR" {
		t.Fatalf("base_currency %v, want the configured IDR", out["base_currency"])
	}
}

func TestGetStatsRendersMoneyAsExactDecimalStrings(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Corp")
	h.invoice(t, c, "", "MILESTONE", "SENT", "1234.5600", false)
	h.quotation(t, c, "", "ACCEPTED", "9876.1200", "SGD")
	out := h.obj(t, "get_stats", nil)
	wantObject(t, out["open_ar_by_currency"], `[{"currency": "USD", "amount": "1234.5600"}]`)
	wantObject(t, out["open_quotation_pipeline"], `[{"currency": "SGD", "amount": "9876.1200"}]`)
}

// ---- test_mcp_reads_all.py ----

func TestListBillsReturnsList(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	id := h.bill(t, v, "BILL-0001", "OPEN", "500.0000")
	rows := h.rows(t, "list_bills", nil)
	if len(rows) != 1 || rows[0]["bill_id"] != id.String() || rows[0]["amount"] != "500.0000" {
		t.Fatalf("rows %v", rows)
	}
}

func TestListBillsFiltersByStatus(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	h.bill(t, v, "BILL-0001", "OPEN", "500.0000")
	h.bill(t, v, "", "VOID", "10.0000")
	rows := h.rows(t, "list_bills", map[string]any{"status": []string{"OPEN"}})
	if len(rows) != 1 || rows[0]["status"] != "OPEN" {
		t.Fatalf("rows %v", rows)
	}
}

func TestListBillsFiltersByVendorID(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	other := h.vendor(t, "Other Vendor")
	id := h.bill(t, v, "BILL-0001", "OPEN", "500.0000")
	h.bill(t, other, "", "OPEN", "20.0000")
	rows := h.rows(t, "list_bills", map[string]any{"vendor_id": v.String()})
	if len(rows) != 1 || rows[0]["bill_id"] != id.String() {
		t.Fatalf("rows %v", rows)
	}
}

func TestGetBillFound(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	id := h.bill(t, v, "BILL-0001", "OPEN", "500.0000")
	out := h.obj(t, "get_bill", map[string]any{"bill_id": id.String()})
	if out["bill_id"] != id.String() || out["bill_number"] != "BILL-0001" {
		t.Fatalf("bill %v", out)
	}
}

func TestGetBillNotFoundReturnsEmptyDict(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	wantObject(t, h.obj(t, "get_bill", map[string]any{"bill_id": uuid.NewString()}), `{}`)
}

func TestListQuotationsReturnsList(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.quotation(t, c, "QUO-0001", "DRAFT", "750.0000", "USD")
	rows := h.rows(t, "list_quotations", nil)
	if len(rows) != 1 || rows[0]["quotation_id"] != id.String() || rows[0]["amount"] != "750.0000" {
		t.Fatalf("rows %v", rows)
	}
}

func TestListQuotationsFiltersByStatus(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.quotation(t, c, "QUO-0001", "DRAFT", "750.0000", "USD")
	h.quotation(t, c, "", "SENT", "50.0000", "USD")
	rows := h.rows(t, "list_quotations", map[string]any{"status": []string{"DRAFT"}})
	if len(rows) != 1 || rows[0]["status"] != "DRAFT" {
		t.Fatalf("rows %v", rows)
	}
}

func TestListQuotationsFiltersByCustomerID(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	other := h.customer(t, "Other Co")
	id := h.quotation(t, c, "QUO-0001", "DRAFT", "750.0000", "USD")
	h.quotation(t, other, "", "DRAFT", "50.0000", "USD")
	rows := h.rows(t, "list_quotations", map[string]any{"customer_id": c.String()})
	if len(rows) != 1 || rows[0]["quotation_id"] != id.String() {
		t.Fatalf("rows %v", rows)
	}
}

func TestGetQuotationFound(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.quotation(t, c, "QUO-0001", "DRAFT", "750.0000", "USD")
	out := h.obj(t, "get_quotation", map[string]any{"quotation_id": id.String()})
	if out["quotation_id"] != id.String() || out["quotation_number"] != "QUO-0001" {
		t.Fatalf("quotation %v", out)
	}
}

func TestGetQuotationNotFoundReturnsEmptyDict(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	wantObject(t, h.obj(t, "get_quotation", map[string]any{"quotation_id": uuid.NewString()}), `{}`)
}

func TestListProjectsReturnsList(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.project(t, c, "ATLAS-26", "Atlas Project")
	rows := h.rows(t, "list_projects", nil)
	if len(rows) != 1 || rows[0]["project_id"] != id.String() || rows[0]["code"] != "ATLAS-26" {
		t.Fatalf("rows %v", rows)
	}
}

func TestListProjectsFiltersByCustomerID(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	other := h.customer(t, "Other Co")
	id := h.project(t, c, "ATLAS-26", "Atlas Project")
	h.project(t, other, "OTHER-26", "Other Project")
	rows := h.rows(t, "list_projects", map[string]any{"customer_id": c.String()})
	if len(rows) != 1 || rows[0]["project_id"] != id.String() {
		t.Fatalf("rows %v", rows)
	}
}

func TestGetProjectFound(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	id := h.project(t, c, "ATLAS-26", "Atlas Project")
	out := h.obj(t, "get_project", map[string]any{"project_id": id.String()})
	if out["project_id"] != id.String() || out["name"] != "Atlas Project" {
		t.Fatalf("project %v", out)
	}
}

func TestGetProjectNotFoundReturnsEmptyDict(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	wantObject(t, h.obj(t, "get_project", map[string]any{"project_id": uuid.NewString()}), `{}`)
}

func TestListItemsReturnsList(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.item(t, "Web Hosting")
	rows := h.rows(t, "list_items", nil)
	if len(rows) != 1 || rows[0]["item_id"] != id.String() || rows[0]["name"] != "Web Hosting" {
		t.Fatalf("rows %v", rows)
	}
}

func TestListItemsFiltersByQuery(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.item(t, "Web Hosting")
	h.item(t, "Design Retainer")
	rows := h.rows(t, "list_items", map[string]any{"query": "Hosting"})
	if len(rows) != 1 || rows[0]["item_id"] != id.String() {
		t.Fatalf("rows %v", rows)
	}
}

func TestListVendorsReturnsList(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.vendor(t, "Acme Vendor Pte Ltd")
	rows := h.rows(t, "list_vendors", nil)
	if len(rows) != 1 || rows[0]["vendor_id"] != id.String() || rows[0]["name"] != "Acme Vendor Pte Ltd" {
		t.Fatalf("rows %v", rows)
	}
}

func TestListVendorsFiltersByQuery(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.vendor(t, "Acme Vendor Pte Ltd")
	h.vendor(t, "Zephyr Supplies")
	rows := h.rows(t, "list_vendors", map[string]any{"query": "Acme"})
	if len(rows) != 1 || rows[0]["vendor_id"] != id.String() {
		t.Fatalf("rows %v", rows)
	}
}

func TestResolveVendorUniqueMatch(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.vendor(t, "Acme Vendor Pte Ltd")
	wantObject(t, h.obj(t, "resolve_vendor", map[string]any{"query": "Acme"}), fmt.Sprintf(`{"vendor_id": %q}`, id))
}

func TestResolveVendorAmbiguousReturnsCandidates(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	a := h.vendor(t, "Acme Vendor Pte Ltd")
	b := h.vendor(t, "Acme Global Supplies")
	out := h.obj(t, "resolve_vendor", map[string]any{"query": "Acme"})
	if _, has := out["vendor_id"]; has {
		t.Fatalf("ambiguous resolve took an action: %v", out)
	}
	cands, _ := out["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("candidates %v, want 2", out["candidates"])
	}
	got := map[string]bool{}
	for _, c := range cands {
		m := c.(map[string]any)
		if len(m) != 2 || m["vendor_id"] == nil || m["name"] == nil {
			t.Fatalf("candidate %v, want exactly vendor_id and name", m)
		}
		got[m["vendor_id"].(string)] = true
	}
	if !got[a.String()] || !got[b.String()] {
		t.Fatalf("candidates %v, want %s and %s", got, a, b)
	}
}

func TestResolveVendorNoMatchReturnsEmptyCandidates(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	h.vendor(t, "Acme Vendor Pte Ltd")
	wantObject(t, h.obj(t, "resolve_vendor", map[string]any{"query": "Nonexistent Supplier"}), `{"candidates": []}`)
}

func TestGetReconciliationQueueReturnsList(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.payment(t, "Ambiguous Payer", "PENDING_MANUAL_REVIEW", "250.0000")
	rows := h.rows(t, "get_reconciliation_queue", nil)
	if len(rows) != 1 || rows[0]["payment_id"] != id.String() || rows[0]["status"] != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("rows %v", rows)
	}
	if rows[0]["confidence_score"] != "97.50" || rows[0]["amount"] != "250.0000" {
		t.Fatalf("row %v, want confidence_score 97.50 and amount 250.0000 at the column scale", rows[0])
	}
}

func TestGetReconciliationQueueExcludesOtherStatuses(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.payment(t, "Ambiguous Payer", "PENDING_MANUAL_REVIEW", "250.0000")
	h.payment(t, "Cleared Payer", "CLEARED", "100.0000")
	rows := h.rows(t, "get_reconciliation_queue", nil)
	if len(rows) != 1 || rows[0]["payment_id"] != id.String() {
		t.Fatalf("rows %v", rows)
	}
}

func TestGetReconciliationQueueRespectsLimit(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	for i := range 4 {
		h.payment(t, fmt.Sprintf("Payer %d", i), "PENDING_MANUAL_REVIEW", "1.0000")
	}
	if rows := h.rows(t, "get_reconciliation_queue", map[string]any{"limit": 2}); len(rows) != 2 {
		t.Fatalf("rows %v, want 2", rows)
	}
}

// ---- what Go adds ----

// listCalls is every list tool with an argument-free call, and the table
// whose rows it lists.
var listCalls = []struct {
	tool, table string
}{
	{"list_bills", "bills"},
	{"list_items", "items"},
	{"list_vendors", "vendors"},
	{"list_customers", "customers"},
	{"list_invoices", "invoices"},
	{"list_projects", "projects"},
	{"list_quotations", "quotations"},
	{"get_reconciliation_queue", "payments"},
}

// TestListToolsTieBreakAndEscape is Go change 12: every list ends with the
// id tie-break, and a name filter matches %, _ and \ literally.
func TestListToolsTieBreakAndEscape(t *testing.T) {
	t.Parallel()

	t.Run("tie-break by id", func(t *testing.T) {
		t.Parallel()
		h := newReadHarness(t)
		c := h.customer(t, "Same Name")
		v := h.vendor(t, "Same Name")
		// One statement per table: the rows share created_at (the statement's
		// transaction time), so only the id can order them.
		h.exec(t, `INSERT INTO customers (customer_id, name) SELECT gen_random_uuid(), 'Same Name' FROM generate_series(1, 5)`)
		h.exec(t, `INSERT INTO vendors (vendor_id, name) SELECT gen_random_uuid(), 'Same Name' FROM generate_series(1, 5)`)
		h.exec(t, `INSERT INTO items (item_id, name, item_type) SELECT gen_random_uuid(), 'Same Name', 'SERVICE' FROM generate_series(1, 5)`)
		h.exec(t, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status) SELECT gen_random_uuid(), $1, 'MILESTONE', 'USD', 1, 1, 'SENT' FROM generate_series(1, 5)`, c)
		h.exec(t, `INSERT INTO bills (bill_id, vendor_id, currency, amount, balance_due, status) SELECT gen_random_uuid(), $1, 'USD', 1, 1, 'OPEN' FROM generate_series(1, 5)`, v)
		h.exec(t, `INSERT INTO quotations (quotation_id, customer_id, currency, amount, status) SELECT gen_random_uuid(), $1, 'USD', 1, 'DRAFT' FROM generate_series(1, 5)`, c)
		h.exec(t, `INSERT INTO projects (project_id, customer_id, code, name, currency) SELECT gen_random_uuid(), $1, 'P' || g, 'P', 'USD' FROM generate_series(1, 5) g`, c)
		h.exec(t, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, status) SELECT gen_random_uuid(), 1, 'USD', 'P', '2026-01-15', 'EMAIL', 'PENDING_MANUAL_REVIEW' FROM generate_series(1, 5)`)
		keys := map[string]struct {
			key  string
			desc bool
		}{
			// Named lists order by name, id; the others newest first, id descending.
			"list_customers": {"customer_id", false}, "list_vendors": {"vendor_id", false}, "list_items": {"item_id", false},
			"list_invoices": {"invoice_id", true}, "list_bills": {"bill_id", true}, "list_quotations": {"quotation_id", true},
			"list_projects": {"project_id", true}, "get_reconciliation_queue": {"payment_id", true},
		}
		for _, lc := range listCalls {
			k := keys[lc.tool]
			got := ids(h.rows(t, lc.tool, nil), k.key)
			// The seeded customer and vendor are one more row of the named lists
			// (created earlier, so they sort wherever their ids put them).
			if len(got) < 5 {
				t.Fatalf("%s: %d rows", lc.tool, len(got))
			}
			want := slices.Clone(got)
			slices.Sort(want)
			if k.desc {
				slices.Reverse(want)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("%s: ids %v, want %v (tie-break by id)", lc.tool, got, want)
			}
		}
	})

	for _, c := range []struct {
		tool    string
		seed    func(t *testing.T, h *readHarness, name string)
		resolve bool
	}{
		{"list_customers", func(t *testing.T, h *readHarness, n string) { h.customer(t, n) }, false},
		{"list_vendors", func(t *testing.T, h *readHarness, n string) { h.vendor(t, n) }, false},
		{"list_items", func(t *testing.T, h *readHarness, n string) { h.item(t, n) }, false},
		{"resolve_customer", func(t *testing.T, h *readHarness, n string) { h.customer(t, n) }, true},
		{"resolve_vendor", func(t *testing.T, h *readHarness, n string) { h.vendor(t, n) }, true},
	} {
		t.Run("escape "+c.tool, func(t *testing.T) {
			t.Parallel()
			h := newReadHarness(t)
			for _, n := range []string{"100% Co", "100X Co", "a_b Ltd", "aXb Ltd", `back\slash Ltd`, `backXslash Ltd`} {
				c.seed(t, h, n)
			}
			// matches reports the names a query finds.
			matches := func(q string) []string {
				if !c.resolve {
					return ids(h.rows(t, c.tool, map[string]any{"query": q}), "name")
				}
				out := h.obj(t, c.tool, map[string]any{"query": q})
				if cands, ok := out["candidates"].([]any); ok {
					var names []string
					for _, e := range cands {
						names = append(names, e.(map[string]any)["name"].(string))
					}
					return names
				}
				// One match: the id of the row; look its name up.
				for k, v := range out {
					if strings.HasSuffix(k, "_id") {
						table, col := "customers", "customer_id"
						if c.tool == "resolve_vendor" {
							table, col = "vendors", "vendor_id"
						}
						var name string
						if err := h.pool.QueryRow(t.Context(), "SELECT name FROM "+table+" WHERE "+col+" = $1", v).Scan(&name); err != nil {
							t.Fatal(err)
						}
						return []string{name}
					}
				}
				t.Fatalf("resolve answered %v", out)
				return nil
			}
			for q, want := range map[string]string{
				"100%":   "100% Co",
				"a_b":    "a_b Ltd",
				`k\s`:    `back\slash Ltd`,
				"100% C": "100% Co",
			} {
				got := matches(q)
				if len(got) != 1 || got[0] != want {
					t.Fatalf("query %q matched %v, want only %q", q, got, want)
				}
			}
			// An unescaped % would match everything; the plain text still works.
			if got := matches("100"); len(got) != 2 {
				t.Fatalf("query 100 matched %v, want both 100 rows", got)
			}
		})
	}
}

// TestListLimitIsClamped: Python's min(limit, 500), and a negative limit is
// an empty list rather than a database error (Go change 12).
func TestListLimitIsClamped(t *testing.T) {
	t.Parallel()
	t.Run("above 500 is 500", func(t *testing.T) {
		t.Parallel()
		h := newReadHarness(t)
		h.exec(t, `INSERT INTO customers (customer_id, name) SELECT gen_random_uuid(), 'C' || g FROM generate_series(1, 501) g`)
		if n := len(h.rows(t, "list_customers", map[string]any{"limit": 10000})); n != 500 {
			t.Fatalf("limit 10000 returned %d rows, want 500", n)
		}
		if n := len(h.rows(t, "list_customers", nil)); n != 200 {
			t.Fatalf("default limit returned %d rows, want 200", n)
		}
		if n := len(h.rows(t, "list_customers", map[string]any{"limit": 3})); n != 3 {
			t.Fatalf("limit 3 returned %d rows, want 3", n)
		}
	})
	t.Run("zero and negative are empty", func(t *testing.T) {
		t.Parallel()
		h := newReadHarness(t)
		c := h.customer(t, "Acme")
		v := h.vendor(t, "Acme")
		h.item(t, "Acme")
		h.invoice(t, c, "", "MILESTONE", "SENT", "1.0000", false)
		h.bill(t, v, "", "OPEN", "1.0000")
		h.quotation(t, c, "", "DRAFT", "1.0000", "USD")
		h.project(t, c, "A-1", "A")
		h.payment(t, "P", "PENDING_MANUAL_REVIEW", "1.0000")
		for _, lc := range listCalls {
			if n := len(h.rows(t, lc.tool, nil)); n != 1 {
				t.Fatalf("%s: %d rows with the default limit, want 1", lc.tool, n)
			}
			for _, limit := range []int{0, -1, -1000} {
				if rows := h.rows(t, lc.tool, map[string]any{"limit": limit}); len(rows) != 0 {
					t.Fatalf("%s limit %d: %d rows, want none", lc.tool, limit, len(rows))
				}
			}
		}
	})
}

// A bad id is {"error": sentence} from a dict tool (Go change 11), and an
// isError result with the sentence from a list tool, which Python also
// raises for and whose {"result"} schema cannot carry an {"error"}.
func TestBadIDArguments(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	for _, c := range []struct{ tool, arg string }{
		{"get_bill", "bill_id"},
		{"get_customer", "customer_id"},
		{"get_invoice", "invoice_id"},
		{"get_project", "project_id"},
		{"get_quotation", "quotation_id"},
	} {
		res := h.call(t, c.tool, map[string]any{c.arg: "not-a-uuid"})
		want := fmt.Sprintf("invalid %s: not-a-uuid is not a valid UUID", c.arg)
		wantObject(t, res.StructuredContent, fmt.Sprintf(`{"error": %q}`, want))
	}
	for _, c := range []struct{ tool, arg string }{
		{"list_bills", "vendor_id"},
		{"list_invoices", "customer_id"},
		{"list_projects", "customer_id"},
		{"list_quotations", "customer_id"},
	} {
		res, err := h.connect(t).CallTool(t.Context(), &mcp.CallToolParams{Name: c.tool, Arguments: map[string]any{c.arg: "nope"}})
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("invalid %s: nope is not a valid UUID", c.arg)
		if !res.IsError || strings.TrimSpace(resultText(res)) != want || res.StructuredContent != nil {
			t.Fatalf("%s: isError=%v text=%q structured=%v, want an isError result saying %q", c.tool, res.IsError, resultText(res), res.StructuredContent, want)
		}
	}
}

// Python's UUID(x) takes the hyphenated, braced, urn: and bare 32-digit
// spellings in either case.
func TestIDArgumentSpellings(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	id := h.customer(t, "Acme")
	s := id.String()
	for _, spelling := range []string{
		s, strings.ToUpper(s), "{" + s + "}", "urn:uuid:" + s, strings.ReplaceAll(s, "-", ""),
	} {
		if got := h.obj(t, "get_customer", map[string]any{"customer_id": spelling}); got["customer_id"] != s {
			t.Fatalf("get_customer(%q) = %v", spelling, got)
		}
	}
}

// The keys of every result are Python's field lists, in order.
func TestReadResultKeysAndOrder(t *testing.T) {
	t.Parallel()
	h := newReadHarness(t)
	c := h.customer(t, "Acme")
	v := h.vendor(t, "Acme")
	bill := h.bill(t, v, "B-1", "OPEN", "1.0000")
	inv := h.invoice(t, c, "I-1", "MILESTONE", "SENT", "1.0000", false)
	quo := h.quotation(t, c, "Q-1", "DRAFT", "1.0000", "USD")
	proj := h.project(t, c, "A-1", "A")
	h.item(t, "I")
	h.payment(t, "P", "PENDING_MANUAL_REVIEW", "1.0000")

	for _, tc := range []struct {
		tool string
		args map[string]any
		want []string
		list bool
	}{
		{"get_bill", map[string]any{"bill_id": bill.String()}, billDetailFields, false},
		{"get_customer", map[string]any{"customer_id": c.String()}, customerDetailFields, false},
		{"get_invoice", map[string]any{"invoice_id": inv.String()}, concat(invoiceDetailFields, "expected_cash"), false},
		{"get_project", map[string]any{"project_id": proj.String()}, projectDetailFields, false},
		{"get_quotation", map[string]any{"quotation_id": quo.String()}, quotationDetailFields, false},
		{"get_stats", nil, statsFields, false},
		{"get_business_profile", nil, []string{
			"id", "name", "address", "contact_email", "contact_phone", "invoice_title", "invoice_summary", "logo_url",
			"default_notes", "npwp", "npwp_name", "updated_at", "has_logo",
		}, false},
	} {
		res := h.call(t, tc.tool, tc.args)
		if got := textKeys(t, res); !slices.Equal(got, tc.want) {
			t.Fatalf("%s: keys %v, want %v", tc.tool, got, tc.want)
		}
		// The structured content carries the same keys.
		m, _ := res.StructuredContent.(map[string]any)
		if len(m) != len(tc.want) {
			t.Fatalf("%s: structured keys %v, want %v", tc.tool, m, tc.want)
		}
	}

	for _, tc := range []struct {
		tool string
		want []string
	}{
		{"list_bills", billFields},
		{"list_items", itemFields},
		{"list_vendors", vendorFields},
		{"list_customers", customerFields},
		{"list_invoices", concat(invoiceFields, "expected_cash")},
		{"list_projects", projectFields},
		{"list_quotations", quotationFields},
		{"get_reconciliation_queue", paymentFields},
	} {
		res := h.call(t, tc.tool, nil)
		var wrapped struct {
			Result []json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(resultText(res)), &wrapped); err != nil || len(wrapped.Result) != 1 {
			t.Fatalf("%s: text %q: %v", tc.tool, resultText(res), err)
		}
		first := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(wrapped.Result[0])}}}
		if got := textKeys(t, first); !slices.Equal(got, tc.want) {
			t.Fatalf("%s: keys %v, want %v", tc.tool, got, tc.want)
		}
	}
}

// ---- tools/list ----

// normSchema reduces a JSON schema to what a client acts on, so the Go SDK's
// spelling and Python's compare: property names, types (a nullable type is
// the type plus nullable), required, defaults, items and
// additionalProperties. Descriptions and titles are prose.
func normSchema(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normSchema(e)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			switch k {
			case "description", "title", "$schema":
				continue
			}
			out[k] = normSchema(e)
		}
		// anyOf [T, null] and type [T, "null"] are T, nullable.
		if alts, ok := out["anyOf"].([]any); ok && len(alts) == 2 {
			for i, alt := range alts {
				if m, ok := alt.(map[string]any); ok && len(m) == 1 && m["type"] == "null" {
					other, _ := alts[1-i].(map[string]any)
					delete(out, "anyOf")
					for k, e := range other {
						out[k] = e
					}
					out["nullable"] = true
				}
			}
		}
		if types, ok := out["type"].([]any); ok && len(types) == 2 {
			for i, ty := range types {
				if ty == "null" {
					out["type"] = types[1-i]
					out["nullable"] = true
				}
			}
		}
		if req, ok := out["required"].([]any); ok {
			sorted := slices.Clone(req)
			slices.SortFunc(sorted, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
			out["required"] = sorted
		}
		return out
	}
	return v
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestToolsListMatchesPython compares tools/list with the one recorded from
// the frozen Python server: for every tool Go has, the description is
// Python's whole docstring, the input schema has Python's property names,
// types, required and defaults, the output schema is the open object or the
// {"result": ...} wrapper, and the tools come in Python's order. It covers
// ping, the read tools and the write tools now; each later task's tools join
// it as they are registered.
func TestToolsListMatchesPython(t *testing.T) {
	t.Parallel()
	h := newFix1Harness(t, func(*mcp.Server, Deps) {})
	srv := httptest.NewServer(NewHandler(Deps{Pool: h.pool, Clock: h.clock, Cfg: h.cfg}))
	t.Cleanup(srv.Close)
	h.srv = srv
	list, err := h.connect(t).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile("testdata/python_session.json")
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		ToolsList []struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			InputSchema  any    `json:"inputSchema"`
			OutputSchema any    `json:"outputSchema"`
		} `json:"tools_list"`
	}
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	pythonOrder := make([]string, 0, len(session.ToolsList))
	for _, p := range session.ToolsList {
		pythonOrder = append(pythonOrder, p.Name)
	}

	var goOrder []string
	for _, tool := range list.Tools {
		goOrder = append(goOrder, tool.Name)
	}
	var wantOrder []string
	for _, n := range pythonOrder {
		if slices.Contains(goOrder, n) {
			wantOrder = append(wantOrder, n)
		}
	}
	if !slices.Equal(goOrder, wantOrder) {
		t.Fatalf("tools/list order %v, want Python's order %v (every Go tool must be one of Python's)", goOrder, wantOrder)
	}
	if len(goOrder) < 37 {
		t.Fatalf("tools/list has %d tools, want at least ping, the 17 read tools and the 19 write tools", len(goOrder))
	}

	for _, tool := range list.Tools {
		idx := slices.IndexFunc(session.ToolsList, func(p struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			InputSchema  any    `json:"inputSchema"`
			OutputSchema any    `json:"outputSchema"`
		},
		) bool {
			return p.Name == tool.Name
		})
		p := session.ToolsList[idx]
		if tool.Description != p.Description {
			t.Errorf("%s: description %q, want Python's %q", tool.Name, tool.Description, p.Description)
		}
		if got, want := normSchema(asJSON(t, tool.InputSchema)), normSchema(p.InputSchema); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: inputSchema %s, want %s", tool.Name, mustJSON(got), mustJSON(want))
		}
		if got, want := normSchema(asJSON(t, tool.OutputSchema)), normSchema(p.OutputSchema); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: outputSchema %s, want %s", tool.Name, mustJSON(got), mustJSON(want))
		}
	}
}

func mustJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSpace(b.String())
}
