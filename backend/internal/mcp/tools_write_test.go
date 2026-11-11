package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgertest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

// The tests of the 19 write tools. They port test_mcp_write_customers.py (5),
// test_mcp_write_invoices.py (13) and test_mcp_writes_all.py (41) with
// Python's values, driving each tool through the SDK's client over the
// transport as the read tests do, and add the tests of Go changes 11 and 14.
// The Python seeds are inserted with plain SQL.

// draftRecorder is the DraftNotifier of the harness: it records each call,
// and which status the drafts' invoices had for a second connection at that
// moment (a committed row is visible to it, an uncommitted one is not).
type draftRecorder struct {
	pool *pgxpool.Pool

	mu      sync.Mutex
	calls   [][]invoices.DraftSummary
	visible []string
}

func (r *draftRecorder) NotifyNewDrafts(ctx context.Context, drafts []invoices.DraftSummary) {
	status := "invisible"
	var s string
	if err := r.pool.QueryRow(ctx, `SELECT status FROM invoices WHERE invoice_id = $1`, drafts[0].InvoiceID).Scan(&s); err == nil {
		status = s
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, drafts)
	r.visible = append(r.visible, status)
}

// paymentRecorder is the AfterPayment hook of the harness: it records the
// status the payment had for a second connection when the hook ran.
type paymentRecorder struct {
	pool *pgxpool.Pool

	mu      sync.Mutex
	got     []paymentsdb.Payment
	visible []string
}

func (r *paymentRecorder) hook(ctx context.Context, p paymentsdb.Payment) {
	var s string
	if err := r.pool.QueryRow(ctx, `SELECT status FROM payments WHERE payment_id = $1`, p.PaymentID).Scan(&s); err != nil {
		s = "invisible"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, p)
	r.visible = append(r.visible, s)
}

type writeHarness struct {
	*readHarness
	drafts *draftRecorder
	paid   *paymentRecorder
}

func newWriteHarness(t *testing.T) *writeHarness {
	t.Helper()
	w := &writeHarness{drafts: &draftRecorder{}, paid: &paymentRecorder{}}
	w.readHarness = &readHarness{newFix1Harness(t, func(s *mcp.Server, d Deps) {
		d.Notifier = w.drafts
		d.AfterPayment = w.paid.hook
		d.Payments = payments.Service{BaseCurrency: d.Cfg.BaseCurrency}
		registerReadTools(s, d)
		registerWriteTools(s, d)
	})}
	w.drafts.pool, w.paid.pool = w.pool, w.pool
	return w
}

// refused calls a tool whose answer must be a refusal, {"error": sentence}
// and nothing else, and returns the sentence.
func (h *writeHarness) refused(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	m := h.obj(t, name, args)
	msg, ok := m["error"].(string)
	if !ok || len(m) != 1 {
		t.Fatalf("%s: answer %v, want only {\"error\": sentence}", name, m)
	}
	return msg
}

// done calls a tool whose answer must be a result, not a refusal.
func (h *writeHarness) done(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	m := h.obj(t, name, args)
	if msg, has := m["error"]; has {
		t.Fatalf("%s: refused: %v", name, msg)
	}
	return m
}

// cell is the first column of the first row of a query.
func (h *writeHarness) cell(t *testing.T, sql string, args ...any) any {
	t.Helper()
	var v any
	if err := h.pool.QueryRow(t.Context(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func (h *writeHarness) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func lines(desc, qty, price string) []any {
	return []any{map[string]any{"description": desc, "quantity": qty, "unit_price": price}}
}

func decOf(t *testing.T, v any) decimal.Decimal {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%v is not a decimal string", v)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func wantDec(t *testing.T, got any, want string) {
	t.Helper()
	if d := decOf(t, got); !d.Equal(decimal.RequireFromString(want)) {
		t.Fatalf("got %v, want %s", got, want)
	}
}

// ---- test_mcp_write_customers.py ----

func TestCustomerDefaultWithholdingRoundtrip(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	created := h.done(t, "create_customer", map[string]any{"name": "Withholder Co", "default_wht_type": "PPH23", "default_wht_gross_up": true})
	if created["default_wht_type"] != "PPH23" || created["default_wht_gross_up"] != true {
		t.Fatalf("created %v", created)
	}
	wantDec(t, created["default_wht_rate"], "2")
	out := h.done(t, "update_customer", map[string]any{"customer_id": created["customer_id"], "changes": map[string]any{"default_wht_type": "NONE"}})
	if out["default_wht_type"] != "NONE" || out["default_wht_rate"] != nil || out["default_wht_gross_up"] != false {
		t.Fatalf("updated %v", out)
	}
}

func TestCreateCustomerInvalidWhtTypeReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.refused(t, "create_customer", map[string]any{"name": "Bad WHT Co", "default_wht_type": "PPH99"})
	if n := h.count(t, `SELECT count(*) FROM customers`); n != 0 {
		t.Fatalf("%d customers stored", n)
	}
}

func TestCreateCustomerOutOfRangeWhtRateReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.refused(t, "create_customer", map[string]any{"name": "Bad Rate Co", "default_wht_type": "PPH23", "default_wht_rate": "150"})
}

func TestCreateCustomerUnparseableWhtRateReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	got := h.refused(t, "create_customer", map[string]any{"name": "Bad Rate Co", "default_wht_type": "PPH23", "default_wht_rate": "abc"})
	if got != "invalid default_wht_rate: 'abc'" {
		t.Fatalf("sentence %q", got)
	}
}

func TestUpdateCustomerUnparseableWhtRateReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	created := h.done(t, "create_customer", map[string]any{"name": "Rate Edit Co"})
	h.refused(t, "update_customer", map[string]any{
		"customer_id": created["customer_id"],
		"changes":     map[string]any{"default_wht_type": "PPH23", "default_wht_rate": "abc"},
	})
}

// ---- test_mcp_write_invoices.py ----

func TestCreateInvoiceMakesDraft(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	out := h.done(t, "create_invoice", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": lines("Design", "40", "100")})
	if out["status"] != "DRAFT" || out["amount"] != "4000.0000" || out["customer_id"] != c.String() {
		t.Fatalf("invoice %v", out)
	}
}

func TestCreateInvoiceAppliesPercentDiscount(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	out := h.done(t, "create_invoice", map[string]any{
		"customer_id": c.String(), "currency": "USD", "line_items": lines("Dev", "10", "100"),
		"discount_type": "PERCENT", "discount_value": "10",
	})
	if out["amount"] != "900.0000" {
		t.Fatalf("amount %v", out["amount"])
	}
}

func TestFinalizeMovesDraftToOpen(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	// Finalize posts the issue entry at the issue date's USD rate (PORT-NOTES 162).
	h.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16000, '2026-01-01', 'TEST')`)
	c := h.customer(t, "Acme Pte Ltd")
	created := h.done(t, "create_invoice", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": lines("x", "1", "10")})
	out := h.done(t, "finalize_invoice", map[string]any{"invoice_id": created["invoice_id"]})
	// Finalizing issues the invoice (OPEN); it only becomes SENT once emailed.
	if out["status"] != "OPEN" {
		t.Fatalf("status %v", out["status"])
	}
}

func TestVoidInvoiceSetsVoidAndZeroesBalance(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	// An issued invoice has its issue entry (PORT-NOTES 162): issue it through
	// finalize, then mark it sent.
	created := h.done(t, "create_invoice", map[string]any{"customer_id": c.String(), "currency": "IDR", "line_items": lines("x", "1", "1000")})
	h.done(t, "finalize_invoice", map[string]any{"invoice_id": created["invoice_id"]})
	h.exec(t, `UPDATE invoices SET status = 'SENT' WHERE invoice_id = $1`, created["invoice_id"])
	out := h.done(t, "void_invoice", map[string]any{"invoice_id": created["invoice_id"]})
	// Python printed "0": a computed value keeps Python's scale. Go prints
	// the column scale (an allowed difference for write results).
	if out["status"] != "VOID" || out["balance_due"] != "0.0000" {
		t.Fatalf("invoice %v", out)
	}
}

// The tools post as the routes do (PORT-NOTES 162): finalize_invoice the
// issue entry, void_invoice its reversal.
func TestMCPFinalizeAndVoidPost(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16000, '2026-01-01', 'TEST')`)
	c := h.customer(t, "Acme Pte Ltd")
	created := h.done(t, "create_invoice", map[string]any{"customer_id": c.String(), "currency": "USD", "invoice_type": "RECURRING", "line_items": lines("x", "1", "10")})
	id := created["invoice_id"]
	h.done(t, "finalize_invoice", map[string]any{"invoice_id": id})
	lineSQL := `SELECT string_agg(a.code || ' ' || l.debit::text || '/' || l.credit::text || ' ' || l.base_amount_debit::text || '/' || l.base_amount_credit::text, ', ' ORDER BY a.code)
FROM journal_lines l JOIN journal_entries e ON e.entry_id = l.entry_id JOIN chart_of_accounts a ON a.account_id = l.account_id
WHERE e.source_type = 'INVOICE' AND e.source_id = $1::uuid AND e.reversed_by_entry_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id)`
	if got, want := h.cell(t, lineSQL, id), "1100 10.0000/0.0000 160000.0000/0.0000, 4100 0.0000/10.0000 0.0000/160000.0000"; got != want {
		t.Errorf("issue entry %v, want %s", got, want)
	}
	h.done(t, "void_invoice", map[string]any{"invoice_id": id})
	if n := h.count(t, `SELECT count(*) FROM journal_entries WHERE source_type = 'INVOICE' AND source_id = $1::uuid`, id); n != 2 {
		t.Errorf("%d entries, want the issue entry and its reversal", n)
	}
	if n := h.count(t, `SELECT count(*) FROM journal_entries WHERE source_type = 'INVOICE' AND source_id = $1::uuid AND reversed_by_entry_id IS NOT NULL`, id); n != 1 {
		t.Errorf("%d reversed entries, want 1", n)
	}
	// Refusals come back as the tool's error sentence.
	other := h.done(t, "create_invoice", map[string]any{"customer_id": c.String(), "currency": "EUR", "line_items": lines("x", "1", "10")})
	if msg := h.refused(t, "finalize_invoice", map[string]any{"invoice_id": other["invoice_id"]}); msg != "no fx rate for EUR->IDR on or before 2026-01-15" {
		t.Errorf("refusal %q", msg)
	}
}

func TestUpdateInvoiceEditsDraft(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	created := h.done(t, "create_invoice", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": lines("x", "1", "10")})
	out := h.done(t, "update_invoice", map[string]any{"invoice_id": created["invoice_id"], "changes": map[string]any{"notes": "please pay promptly"}})
	if out["invoice_id"] != created["invoice_id"] {
		t.Fatalf("updated %v", out)
	}
	if got := h.cell(t, `SELECT notes FROM invoices WHERE invoice_id = $1`, created["invoice_id"]); got != "please pay promptly" {
		t.Fatalf("notes %v", got)
	}
}

func TestCreateInvoiceWithoutLineItemsReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	got := h.refused(t, "create_invoice", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": []any{}})
	if got != "invoice must have at least one line item" {
		t.Fatalf("sentence %q", got)
	}
	if n := h.count(t, `SELECT count(*) FROM invoices WHERE customer_id = $1`, c); n != 0 {
		t.Fatalf("a failed create_invoice persisted %d invoice(s)", n)
	}
}

func TestFinalizeNonDraftReturnsErrorAndLeavesStateUnchanged(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	inv := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	if got := h.refused(t, "finalize_invoice", map[string]any{"invoice_id": inv.String()}); got != "only DRAFT invoices can be finalized (got SENT)" {
		t.Fatalf("sentence %q", got)
	}
	if got := h.cell(t, `SELECT status || ' ' || balance_due::text FROM invoices WHERE invoice_id = $1`, inv); got != "SENT 1000.0000" {
		t.Fatalf("row %v", got)
	}
}

func TestUpdateInvoiceOnNonDraftReturnsErrorAndLeavesNotesUnchanged(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	inv := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	if got := h.refused(t, "update_invoice", map[string]any{"invoice_id": inv.String(), "changes": map[string]any{"notes": "attempted change"}}); got != "only DRAFT invoices can be edited (got SENT)" {
		t.Fatalf("sentence %q", got)
	}
	if got := h.cell(t, `SELECT notes FROM invoices WHERE invoice_id = $1`, inv); got != nil {
		t.Fatalf("notes %v", got)
	}
}

func TestCreateRecurringTemplateAndTriggerRecurring(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	template := h.done(t, "create_recurring_template", map[string]any{
		"customer_id": c.String(), "currency": "USD", "line_items": lines("Hosting", "1", "50"),
		"schedule":      map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-08-01", "end_mode": "NEVER"},
		"payment_terms": "Net 14",
	})
	if template["status"] != "DRAFT" || template["amount"] != "50.0000" {
		t.Fatalf("template %v", template)
	}
	triggered := h.done(t, "trigger_recurring", map[string]any{"template_id": template["invoice_id"], "cycle_key": "2026-08-01"})
	if triggered["status"] != "DRAFT" || triggered["amount"] != "50.0000" || triggered["invoice_id"] == template["invoice_id"] {
		t.Fatalf("triggered %v", triggered)
	}
	// Idempotent: the same cycle_key returns the generated invoice, not a new one.
	again := h.done(t, "trigger_recurring", map[string]any{"template_id": template["invoice_id"], "cycle_key": "2026-08-01"})
	if again["invoice_id"] != triggered["invoice_id"] {
		t.Fatalf("second trigger %v, want the first's invoice %v", again["invoice_id"], triggered["invoice_id"])
	}
}

func TestTriggerRecurringOnNonRecurringTemplateReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	inv := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	if got := h.refused(t, "trigger_recurring", map[string]any{"template_id": inv.String(), "cycle_key": "2026-08-01"}); got != fmt.Sprintf("invoice %s is not RECURRING", inv) {
		t.Fatalf("sentence %q", got)
	}
}

func TestMCPCreateInvoiceWithPph23GrossUp(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	out := h.done(t, "create_invoice", map[string]any{
		"customer_id": c.String(), "currency": "IDR", "line_items": lines("Design", "1", "980000"),
		"wht_type": "PPH23", "wht_rate": "2", "wht_gross_up": true,
	})
	if out["wht_type"] != "PPH23" || out["wht_gross_up"] != true {
		t.Fatalf("invoice %v", out)
	}
	wantDec(t, out["amount"], "1000000")
	wantDec(t, out["wht_amount"], "20000")
	wantDec(t, out["expected_cash"], "980000")
}

func TestMCPCreateInvoiceInvalidWhtRateReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.refused(t, "create_invoice", map[string]any{
		"customer_id": c.String(), "currency": "IDR", "line_items": lines("x", "1", "1000"),
		"wht_type": "PPH23", "wht_rate": "150",
	})
}

func TestMCPUpdateInvoiceSetsWithholding(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	created := h.done(t, "create_invoice", map[string]any{"customer_id": c.String(), "currency": "IDR", "line_items": lines("x", "1", "100000")})
	out := h.done(t, "update_invoice", map[string]any{"invoice_id": created["invoice_id"], "changes": map[string]any{"wht_type": "PPH23", "wht_rate": "2"}})
	if out["wht_type"] != "PPH23" {
		t.Fatalf("invoice %v", out)
	}
	wantDec(t, out["wht_amount"], "2000")
	wantDec(t, out["expected_cash"], "98000")
}

// ---- test_mcp_writes_all.py: customers ----

func TestCreateCustomerSetsFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	out := h.done(t, "create_customer", map[string]any{"name": "Beta Pte Ltd", "contact_email": "ap@beta.com"})
	if out["name"] != "Beta Pte Ltd" || out["contact_email"] != "ap@beta.com" {
		t.Fatalf("customer %v", out)
	}
}

func TestUpdateCustomerEditsFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	out := h.done(t, "update_customer", map[string]any{"customer_id": c.String(), "changes": map[string]any{"notes": "VIP"}})
	if out["customer_id"] != c.String() {
		t.Fatalf("customer %v", out)
	}
	// notes is not in the narrow field list: confirm with a direct read.
	if got := h.cell(t, `SELECT notes FROM customers WHERE customer_id = $1`, c); got != "VIP" {
		t.Fatalf("notes %v", got)
	}
}

func TestUpdateCustomerNotFoundReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	id := uuid.New()
	if got := h.refused(t, "update_customer", map[string]any{"customer_id": id.String(), "changes": map[string]any{"notes": "x"}}); got != fmt.Sprintf("customer %s not found", id) {
		t.Fatalf("sentence %q", got)
	}
}

func TestCreateCustomerStoresPhoneAndAddressInTheirOwnColumns(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	out := h.done(t, "create_customer", map[string]any{
		"name": "Gamma Ltd", "contact_phone": "+62 811 2233", "billing_address1": "Jl. Sudirman 52", "billing_city": "Jakarta",
		"billing_postal_code": "12190", "billing_country": "Indonesia", "website": "https://gamma.example",
	})
	row := h.cell(t, `SELECT concat_ws('|', contact_phone, billing_address1, billing_city, billing_postal_code, billing_country, website, coalesce(notes, 'NULL')) FROM customers WHERE customer_id = $1`, out["customer_id"])
	if row != "+62 811 2233|Jl. Sudirman 52|Jakarta|12190|Indonesia|https://gamma.example|NULL" {
		t.Fatalf("row %v", row)
	}
}

func TestCreateCustomerEchoesBackContactAndAddress(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	out := h.done(t, "create_customer", map[string]any{"name": "Delta Ltd", "contact_phone": "+62 811 0000", "billing_city": "Bandung"})
	if out["contact_phone"] != "+62 811 0000" || out["billing_city"] != "Bandung" {
		t.Fatalf("customer %v", out)
	}
}

func TestUpdateCustomerRejectsUnknownFieldNames(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	got := h.refused(t, "update_customer", map[string]any{"customer_id": c.String(), "changes": map[string]any{"phone": "+62 811 4455", "address": "Jl. Thamrin 1"}})
	if !strings.HasPrefix(got, "unknown field(s): address, phone. Valid fields: ") || !strings.Contains(got, "contact_phone") {
		t.Fatalf("sentence %q", got)
	}
	if got := h.cell(t, `SELECT concat_ws('|', contact_phone, billing_address1) FROM customers WHERE customer_id = $1`, c); got != "" {
		t.Fatalf("row changed: %v", got)
	}
}

func TestUpdateCustomerWritesAddressFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.done(t, "update_customer", map[string]any{"customer_id": c.String(), "changes": map[string]any{"contact_phone": "+62 811 9999", "billing_address1": "Jl. Thamrin 1"}})
	if got := h.cell(t, `SELECT concat_ws('|', contact_phone, billing_address1) FROM customers WHERE customer_id = $1`, c); got != "+62 811 9999|Jl. Thamrin 1" {
		t.Fatalf("row %v", got)
	}
}

// ---- projects ----

func TestCreateProjectNormalizesCodeAndCurrency(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	out := h.done(t, "create_project", map[string]any{"customer_id": c.String(), "code": "atlas-30", "name": "Atlas", "currency": "usd"})
	if out["code"] != "ATLAS-30" || out["currency"] != "USD" {
		t.Fatalf("project %v", out)
	}
}

func TestCreateProjectDuplicateCodeReturnsErrorAndPersistsOnlyOne(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	h.done(t, "create_project", map[string]any{"customer_id": c.String(), "code": "DUP-9", "name": "First", "currency": "USD"})
	h.refused(t, "create_project", map[string]any{"customer_id": c.String(), "code": "DUP-9", "name": "Second", "currency": "USD"})
	if n := h.count(t, `SELECT count(*) FROM projects WHERE code = 'DUP-9'`); n != 1 {
		t.Fatalf("%d projects with the code", n)
	}
}

func TestUpdateProjectEditsFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	p := h.project(t, h.customer(t, "Acme Pte Ltd"), "ATLAS-26", "Atlas Project")
	out := h.done(t, "update_project", map[string]any{"project_id": p.String(), "changes": map[string]any{"status": "ON_HOLD"}})
	if out["status"] != "ON_HOLD" {
		t.Fatalf("project %v", out)
	}
}

func TestUpdateProjectRejectsUnknownFieldNames(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	p := h.project(t, h.customer(t, "Acme Pte Ltd"), "ATLAS-26", "Atlas Project")
	got := h.refused(t, "update_project", map[string]any{"project_id": p.String(), "changes": map[string]any{"project_name": "Renamed", "budget": "5000"}})
	if !strings.Contains(got, "project_name") || !strings.Contains(got, "budget") {
		t.Fatalf("sentence %q", got)
	}
	if got := h.cell(t, `SELECT name FROM projects WHERE project_id = $1`, p); got != "Atlas Project" {
		t.Fatalf("name %v", got)
	}
}

func TestCreateProjectEchoesNotes(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	out := h.done(t, "create_project", map[string]any{"customer_id": c.String(), "code": "ECHO-1", "name": "Echo", "currency": "USD", "notes": "phase one only"})
	if out["notes"] != "phase one only" {
		t.Fatalf("project %v", out)
	}
}

// ---- items / vendors ----

func TestCreateItemSetsFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	out := h.done(t, "create_item", map[string]any{"name": "Consulting", "item_type": "SERVICE", "default_unit_price": "150"})
	if out["name"] != "Consulting" {
		t.Fatalf("item %v", out)
	}
	wantDec(t, out["default_unit_price"], "150")
}

func TestUpdateItemEditsFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	item := h.item(t, "Web Hosting")
	out := h.done(t, "update_item", map[string]any{"item_id": item.String(), "changes": map[string]any{"active": false}})
	if out["active"] != false {
		t.Fatalf("item %v", out)
	}
}

func TestUpdateItemNotFoundReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	id := uuid.New()
	if got := h.refused(t, "update_item", map[string]any{"item_id": id.String(), "changes": map[string]any{"active": false}}); got != fmt.Sprintf("item %s not found", id) {
		t.Fatalf("sentence %q", got)
	}
}

func TestUpdateItemRejectsUnknownFieldNames(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	item := h.item(t, "Web Hosting")
	got := h.refused(t, "update_item", map[string]any{"item_id": item.String(), "changes": map[string]any{"unit_price": "99", "price_currency": "USD"}})
	if !strings.Contains(got, "unit_price") || !strings.Contains(got, "price_currency") {
		t.Fatalf("sentence %q", got)
	}
	if got := h.cell(t, `SELECT default_unit_price::text FROM items WHERE item_id = $1`, item); got != "100.0000" {
		t.Fatalf("price %v", got)
	}
}

func TestCreateItemSetsAccountIDs(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	revenue := h.cell(t, `SELECT account_id FROM chart_of_accounts WHERE type = 'INCOME' ORDER BY account_id LIMIT 1`)
	expense := h.cell(t, `SELECT account_id FROM chart_of_accounts WHERE type = 'EXPENSE' ORDER BY account_id LIMIT 1`)
	out := h.done(t, "create_item", map[string]any{"name": "Retainer", "item_type": "SERVICE", "revenue_account_id": revenue, "expense_account_id": expense})
	got := h.cell(t, `SELECT revenue_account_id || '|' || expense_account_id FROM items WHERE item_id = $1`, out["item_id"])
	if got != fmt.Sprintf("%v|%v", revenue, expense) {
		t.Fatalf("accounts %v, want %v|%v", got, revenue, expense)
	}
}

func TestCreateItemEchoesDescriptionAndFlags(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	out := h.done(t, "create_item", map[string]any{"name": "Support Plan", "item_type": "SERVICE", "description": "24/7 cover", "is_purchased": true})
	if out["description"] != "24/7 cover" || out["is_purchased"] != true || out["is_sold"] != true {
		t.Fatalf("item %v", out)
	}
}

func TestCreateVendorSetsFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	if out := h.done(t, "create_vendor", map[string]any{"name": "Acme Supplies"}); out["name"] != "Acme Supplies" {
		t.Fatalf("vendor %v", out)
	}
}

func TestCreateVendorCanSetActive(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	out := h.done(t, "create_vendor", map[string]any{"name": "Dormant Supplies", "active": false})
	if got := h.cell(t, `SELECT active FROM vendors WHERE vendor_id = $1`, out["vendor_id"]); got != false {
		t.Fatalf("active %v", got)
	}
}

func TestCreateVendorEchoesTaxIDAndNotes(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	out := h.done(t, "create_vendor", map[string]any{"name": "Beta Supplies", "tax_id": "01.234.567.8-901.000", "notes": "net 45 agreed"})
	if out["tax_id"] != "01.234.567.8-901.000" || out["notes"] != "net 45 agreed" {
		t.Fatalf("vendor %v", out)
	}
}

func TestUpdateVendorEditsFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	out := h.done(t, "update_vendor", map[string]any{"vendor_id": v.String(), "changes": map[string]any{"notes": "Preferred"}})
	if out["vendor_id"] != v.String() {
		t.Fatalf("vendor %v", out)
	}
	if got := h.cell(t, `SELECT notes FROM vendors WHERE vendor_id = $1`, v); got != "Preferred" {
		t.Fatalf("notes %v", got)
	}
}

func TestUpdateVendorRejectsUnknownFieldNames(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	got := h.refused(t, "update_vendor", map[string]any{"vendor_id": v.String(), "changes": map[string]any{"phone": "+62 811 4455", "address": "Jl. Thamrin 1"}})
	if !strings.Contains(got, "phone") || !strings.Contains(got, "address") {
		t.Fatalf("sentence %q", got)
	}
	if got := h.cell(t, `SELECT notes FROM vendors WHERE vendor_id = $1`, v); got != nil {
		t.Fatalf("notes %v", got)
	}
}

func TestCreateItemDuplicateSkuReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.done(t, "create_item", map[string]any{"name": "First", "item_type": "SERVICE", "sku": "DUP-SKU-1"})
	got := h.refused(t, "create_item", map[string]any{"name": "Second", "item_type": "SERVICE", "sku": "DUP-SKU-1"})
	if got != "sku already exists" {
		t.Fatalf("sentence %q, want the mapped sentence, not driver text", got)
	}
	if n := h.count(t, `SELECT count(*) FROM items WHERE sku = 'DUP-SKU-1'`); n != 1 {
		t.Fatalf("%d items with the sku", n)
	}
}

// test_create_vendor_integrity_error_returns_error patched the vendor service
// to raise an IntegrityError, because vendors have no constraint to violate.
// Ported as "a refused write becomes {"error"}", with real constraints: a
// duplicate SKU on update (Python raised there), and a bill for a vendor that
// does not exist (a foreign key). Neither answer holds driver text.
func TestRefusedWriteBecomesErrorResult(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.done(t, "create_item", map[string]any{"name": "First", "item_type": "SERVICE", "sku": "TAKEN"})
	second := h.done(t, "create_item", map[string]any{"name": "Second", "item_type": "SERVICE", "sku": "OTHER"})
	got := h.refused(t, "update_item", map[string]any{"item_id": second["item_id"], "changes": map[string]any{"sku": "TAKEN"}})
	if got != "sku already exists" {
		t.Fatalf("update_item sentence %q", got)
	}
	got = h.refused(t, "create_bill", map[string]any{"vendor_id": uuid.New().String(), "currency": "USD", "line_items": lines("AWS", "1", "200")})
	if got != "vendor_id: vendor not found" {
		t.Fatalf("create_bill sentence %q", got)
	}
	for _, driver := range []string{"violates", "SQLSTATE", "constraint"} {
		if strings.Contains(got, driver) {
			t.Fatalf("sentence %q holds driver text", got)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM bills`); n != 0 {
		t.Fatalf("%d bills stored", n)
	}
}

// ---- bills ----

func TestCreateBillMakesDraft(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	out := h.done(t, "create_bill", map[string]any{"vendor_id": v.String(), "currency": "USD", "line_items": lines("AWS", "1", "200")})
	if out["status"] != "DRAFT" || out["amount"] != "200.0000" {
		t.Fatalf("bill %v", out)
	}
}

func TestUpdateBillEditsOpenBill(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	b := h.bill(t, h.vendor(t, "Acme Vendor Pte Ltd"), "BILL-0001", "OPEN", "500.0000")
	out := h.done(t, "update_bill", map[string]any{"bill_id": b.String(), "changes": map[string]any{"notes": "please settle"}})
	if out["bill_id"] != b.String() {
		t.Fatalf("bill %v", out)
	}
	if got := h.cell(t, `SELECT notes FROM bills WHERE bill_id = $1`, b); got != "please settle" {
		t.Fatalf("notes %v", got)
	}
}

// update_bill opens, re-posts and voids as PATCH /bills does (PORT-NOTES 164).
func TestMCPUpdateBillOpenPosts(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16000, '2026-01-01', 'TEST')`)
	created := h.done(t, "create_bill", map[string]any{
		"vendor_id": h.vendor(t, "Acme Vendor Pte Ltd").String(), "currency": "USD",
		"issue_date": "2026-01-10", "line_items": lines("AWS", "1", "200"),
	})
	id := created["bill_id"]
	h.done(t, "update_bill", map[string]any{"bill_id": id, "changes": map[string]any{"status": "OPEN"}})
	lineSQL := `SELECT string_agg(a.code || ' ' || l.debit::text || '/' || l.credit::text || ' ' || l.base_amount_debit::text || '/' || l.base_amount_credit::text, ', ' ORDER BY a.code)
FROM journal_lines l JOIN journal_entries e ON e.entry_id = l.entry_id JOIN chart_of_accounts a ON a.account_id = l.account_id
WHERE e.source_type = 'BILL' AND e.source_id = $1::uuid AND e.reversed_by_entry_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM journal_entries r WHERE r.reversed_by_entry_id = e.entry_id)`
	if got, want := h.cell(t, lineSQL, id), "2000 0.0000/200.0000 0.0000/3200000.0000, 6000 200.0000/0.0000 3200000.0000/0.0000"; got != want {
		t.Errorf("issue entry %v, want %s", got, want)
	}
	// Refusals come back as the tool's error sentence.
	if msg := h.refused(t, "update_bill", map[string]any{"bill_id": id, "changes": map[string]any{"status": "PARTIAL"}}); msg != "PARTIAL comes from recording a payment" {
		t.Errorf("refusal %q", msg)
	}
	h.done(t, "update_bill", map[string]any{"bill_id": id, "changes": map[string]any{"status": "VOID"}})
	if n := h.count(t, `SELECT count(*) FROM journal_entries WHERE source_type = 'BILL' AND source_id = $1::uuid AND reversed_by_entry_id IS NOT NULL`, id); n != 1 {
		t.Errorf("%d reversed entries, want 1", n)
	}
	other := h.done(t, "create_bill", map[string]any{
		"vendor_id": h.vendor(t, "Other Vendor").String(), "currency": "EUR",
		"issue_date": "2026-01-15", "line_items": lines("x", "1", "10"),
	})
	if msg := h.refused(t, "update_bill", map[string]any{"bill_id": other["bill_id"], "changes": map[string]any{"status": "OPEN"}}); msg != "no fx rate for EUR->IDR on or before 2026-01-15" {
		t.Errorf("refusal %q", msg)
	}
}

func TestUpdateBillNotFoundReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	id := uuid.New()
	if got := h.refused(t, "update_bill", map[string]any{"bill_id": id.String(), "changes": map[string]any{"notes": "x"}}); got != fmt.Sprintf("bill %s not found", id) {
		t.Fatalf("sentence %q", got)
	}
}

// The not-found sentence echoes the id as Python did: the tools that pass
// UUID(id) to a service print the canonical lowercase form; update_bill
// formats the argument string it was given.
func TestNotFoundSentencesEchoTheIDAsPythonDid(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	id := uuid.New()
	upper := strings.ToUpper(id.String())
	notes := map[string]any{"notes": "x"}
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"update_customer", map[string]any{"customer_id": upper, "changes": notes}, "customer " + id.String() + " not found"},
		{"update_item", map[string]any{"item_id": upper, "changes": map[string]any{"active": false}}, "item " + id.String() + " not found"},
		{"update_vendor", map[string]any{"vendor_id": upper, "changes": notes}, "vendor " + id.String() + " not found"},
		{"update_invoice", map[string]any{"invoice_id": upper, "changes": notes}, "invoice " + id.String() + " not found"},
		{"finalize_invoice", map[string]any{"invoice_id": upper}, "invoice " + id.String() + " not found"},
		{"void_invoice", map[string]any{"invoice_id": upper}, "invoice " + id.String() + " not found"},
		{"update_project", map[string]any{"project_id": upper, "changes": notes}, "project " + id.String() + " not found"},
		{"update_quotation", map[string]any{"quotation_id": upper, "changes": notes}, "quotation " + id.String() + " not found"},
		{"update_bill", map[string]any{"bill_id": upper, "changes": notes}, "bill " + upper + " not found"},
	} {
		if got := h.refused(t, tc.tool, tc.args); got != tc.want {
			t.Errorf("%s: sentence %q, want %q", tc.tool, got, tc.want)
		}
	}
}

func TestUpdateBillRejectsUnknownFieldNames(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	b := h.bill(t, h.vendor(t, "Acme Vendor Pte Ltd"), "BILL-0001", "OPEN", "500.0000")
	got := h.refused(t, "update_bill", map[string]any{"bill_id": b.String(), "changes": map[string]any{"note": "please settle", "terms": "NET30"}})
	// The sorted JSON names of bills.BillUpdate.
	want := "unknown field(s): note, terms. Valid fields: bill_number, currency, discount_type, discount_value, due_date, issue_date, line_items, notes, payment_terms, po_number, project_id, status, vendor_id"
	if got != want {
		t.Fatalf("sentence %q, want %q", got, want)
	}
	if got := h.cell(t, `SELECT concat_ws('|', notes, payment_terms) FROM bills WHERE bill_id = $1`, b); got != "" {
		t.Fatalf("row changed: %v", got)
	}
}

// The unknown-key guard runs before any lookup, so a bad key on a missing
// bill reports the bad key, not the missing bill.
func TestUpdateBillUnknownFieldCheckPrecedesNotFound(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	got := h.refused(t, "update_bill", map[string]any{"bill_id": uuid.New().String(), "changes": map[string]any{"note": "x"}})
	if !strings.Contains(got, "note") || strings.Contains(got, "not found") {
		t.Fatalf("sentence %q, want the unknown key, not the missing bill", got)
	}
	// And before the id is read: a bad id with a bad key is the bad key too.
	got = h.refused(t, "update_bill", map[string]any{"bill_id": "nope", "changes": map[string]any{"note": "x"}})
	if !strings.HasPrefix(got, "unknown field(s): note.") {
		t.Fatalf("sentence %q, want the unknown key before the bad id", got)
	}
}

func TestUpdateBillEchoesNotes(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	b := h.bill(t, h.vendor(t, "Acme Vendor Pte Ltd"), "BILL-0001", "OPEN", "500.0000")
	out := h.done(t, "update_bill", map[string]any{"bill_id": b.String(), "changes": map[string]any{"notes": "please settle"}})
	if out["notes"] != "please settle" {
		t.Fatalf("bill %v", out)
	}
}

// ---- quotations ----

func TestCreateQuotationMakesDraft(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	out := h.done(t, "create_quotation", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": lines("Design", "10", "100")})
	if out["status"] != "DRAFT" || out["amount"] != "1000.0000" {
		t.Fatalf("quotation %v", out)
	}
}

func TestUpdateQuotationEditsDraft(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	q := h.quotation(t, h.customer(t, "Acme Pte Ltd"), "QUO-0001", "DRAFT", "750.0000", "USD")
	out := h.done(t, "update_quotation", map[string]any{"quotation_id": q.String(), "changes": map[string]any{"notes": "revised terms"}})
	if out["quotation_id"] != q.String() {
		t.Fatalf("quotation %v", out)
	}
	if got := h.cell(t, `SELECT notes FROM quotations WHERE quotation_id = $1`, q); got != "revised terms" {
		t.Fatalf("notes %v", got)
	}
}

func TestCreateQuotationWithoutLineItemsReturnsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	got := h.refused(t, "create_quotation", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": []any{}})
	if got != "quotation must have at least one line item" {
		t.Fatalf("sentence %q", got)
	}
}

func TestUpdateQuotationRejectsUnknownFieldNames(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	q := h.quotation(t, h.customer(t, "Acme Pte Ltd"), "QUO-0001", "DRAFT", "750.0000", "USD")
	got := h.refused(t, "update_quotation", map[string]any{"quotation_id": q.String(), "changes": map[string]any{"note": "revised terms", "valid_till": "2026-12-31"}})
	if !strings.Contains(got, "note") || !strings.Contains(got, "valid_till") || !strings.Contains(got, "valid_until") {
		t.Fatalf("sentence %q", got)
	}
	if got := h.cell(t, `SELECT notes FROM quotations WHERE quotation_id = $1`, q); got != nil {
		t.Fatalf("notes %v", got)
	}
}

func TestUpdateQuotationEchoesNotes(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	q := h.quotation(t, h.customer(t, "Acme Pte Ltd"), "QUO-0001", "DRAFT", "750.0000", "USD")
	out := h.done(t, "update_quotation", map[string]any{"quotation_id": q.String(), "changes": map[string]any{"notes": "revised terms"}})
	if out["notes"] != "revised terms" {
		t.Fatalf("quotation %v", out)
	}
}

// ---- invoices ----

func TestUpdateInvoiceRejectsUnknownFieldNames(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	inv := h.invoice(t, h.customer(t, "Acme Pte Ltd"), "INV-0003", "MILESTONE", "DRAFT", "1000.0000", false)
	got := h.refused(t, "update_invoice", map[string]any{"invoice_id": inv.String(), "changes": map[string]any{"note": "pay promptly", "terms": "NET30"}})
	if !strings.HasPrefix(got, "unknown field(s): note, terms. Valid fields: ") || !strings.Contains(got, "payment_terms") {
		t.Fatalf("sentence %q", got)
	}
	if got := h.cell(t, `SELECT concat_ws('|', notes, payment_terms) FROM invoices WHERE invoice_id = $1`, inv); got != "" {
		t.Fatalf("row changed: %v", got)
	}
}

func TestUpdateInvoiceEchoesNotes(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	inv := h.invoice(t, h.customer(t, "Acme Pte Ltd"), "INV-0003", "MILESTONE", "DRAFT", "1000.0000", false)
	out := h.done(t, "update_invoice", map[string]any{"invoice_id": inv.String(), "changes": map[string]any{"notes": "pay promptly"}})
	if out["notes"] != "pay promptly" {
		t.Fatalf("invoice %v", out)
	}
}

// ---- reconciliation (safe-stop) ----

// recon inserts an invoice and a payment for resolve_reconciliation_match.
// The invoice gets its issue entry (PORT-NOTES 163) when the test stored a
// rate to issue it at; without one, a refusal comes before the posting (or
// the payment's own rate is missing first).
func (h *writeHarness) recon(t *testing.T, payCurrency, payAmount, status, invCurrency, invAmount string) (payment, invoice uuid.UUID) {
	t.Helper()
	c := h.customer(t, "Acme Pte Ltd")
	invoice, payment = uuid.New(), uuid.New()
	h.exec(t, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, invoice_number, currency, amount, balance_due, status, issue_date, due_date)
VALUES ($1, $2, 'MILESTONE', 'INV-' || $5::text, $3, $4::text::numeric, $4::text::numeric, 'SENT', '2026-06-01', '2026-06-15')`, invoice, c, invCurrency, invAmount, invoice.String()[:8])
	h.exec(t, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, status)
VALUES ($1, $2::text::numeric, $3, 'Ambiguous LLC', '2026-06-05', 'EMAIL', $4)`, payment, payAmount, payCurrency, status)
	if h.count(t, `SELECT count(*) FROM fx_rates WHERE from_currency = $1 AND to_currency = 'IDR' AND as_of_date <= '2026-06-01'`, invCurrency) > 0 {
		tx, err := h.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		ledgertest.IssueFixture(t, tx, invoice)
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return payment, invoice
}

func (h *writeHarness) paymentStatus(t *testing.T, id uuid.UUID) string {
	t.Helper()
	return fmt.Sprint(h.cell(t, `SELECT status FROM payments WHERE payment_id = $1`, id))
}

func TestResolveReconciliationMatchExactMatchClears(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16000, '2026-01-01', 'TEST')`)
	payment, invoice := h.recon(t, "USD", "500.0000", "PENDING_MANUAL_REVIEW", "USD", "500.0000")
	out := h.done(t, "resolve_reconciliation_match", map[string]any{"payment_id": payment.String(), "invoice_id": invoice.String()})
	if out["status"] != "CLEARED" || out["payment_id"] != payment.String() || out["invoice_id"] != invoice.String() {
		t.Fatalf("payment %v", out)
	}
	if got := h.paymentStatus(t, payment); got != "CLEARED" {
		t.Fatalf("status %s", got)
	}
	// The actor is the token's subject.
	if got := h.cell(t, `SELECT actor_user_id::text FROM reconciliation_log WHERE payment_id = $1 AND action = 'OVERRIDDEN'`, payment); got != h.adminID.String() {
		t.Fatalf("actor %v, want the token's subject %s", got, h.adminID)
	}
}

func TestResolveReconciliationMatchCurrencyMismatchRefuses(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	payment, invoice := h.recon(t, "GBP", "500.0000", "PENDING_MANUAL_REVIEW", "USD", "500.0000")
	got := h.refused(t, "resolve_reconciliation_match", map[string]any{"payment_id": payment.String(), "invoice_id": invoice.String()})
	if got != "currency mismatch: payment GBP vs invoice USD — refusing autonomous resolution, route to manual review" {
		t.Fatalf("sentence %q", got)
	}
	if st := h.paymentStatus(t, payment); st != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("safe-stop violated: currency mismatch changed the status to %s", st)
	}
}

func TestResolveReconciliationMatchAmountMismatchRefuses(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	payment, invoice := h.recon(t, "USD", "450.0000", "PENDING_MANUAL_REVIEW", "USD", "500.0000")
	got := h.refused(t, "resolve_reconciliation_match", map[string]any{"payment_id": payment.String(), "invoice_id": invoice.String()})
	// Python Decimal str: the payment's amount and the expected cash keep
	// their column scale.
	if got != "amount mismatch: payment 450.0000 vs invoice expected cash 500.0000 — refusing autonomous resolution, route to manual review" {
		t.Fatalf("sentence %q", got)
	}
	if st := h.paymentStatus(t, payment); st != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("safe-stop violated: amount mismatch changed the status to %s", st)
	}
}

// ---- Go change 14, and the tools' own refusals ----

// A payment that is not awaiting review is refused, however well it matches
// (Python approved a FLAGGED one).
func TestResolveFlaggedPaymentRefused(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	payment, invoice := h.recon(t, "USD", "500.0000", "FLAGGED", "USD", "500.0000")
	got := h.refused(t, "resolve_reconciliation_match", map[string]any{"payment_id": payment.String(), "invoice_id": invoice.String()})
	if got != fmt.Sprintf("payment %s is FLAGGED, not awaiting review", payment) {
		t.Fatalf("sentence %q", got)
	}
	if st := h.paymentStatus(t, payment); st != "FLAGGED" {
		t.Fatalf("status %s", st)
	}
	if n := h.count(t, `SELECT count(*) FROM reconciliation_log WHERE payment_id = $1`, payment); n != 0 {
		t.Fatalf("%d log rows", n)
	}
	h.paid.mu.Lock()
	defer h.paid.mu.Unlock()
	if len(h.paid.got) != 0 {
		t.Fatalf("the hook ran %d time(s) for a refused call", len(h.paid.got))
	}
}

// A DRAFT invoice is refused by the service (plan 7's refusal, inherited):
// the payment stays in the queue, nothing is logged, the hook never runs.
func TestResolveAgainstDraftInvoiceRefused(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16000, '2026-01-01', 'TEST')`)
	payment, invoice := h.recon(t, "USD", "500.0000", "PENDING_MANUAL_REVIEW", "USD", "500.0000")
	h.exec(t, `UPDATE invoices SET status = 'DRAFT' WHERE invoice_id = $1`, invoice)
	got := h.refused(t, "resolve_reconciliation_match", map[string]any{"payment_id": payment.String(), "invoice_id": invoice.String()})
	if got != "cannot approve payment against DRAFT invoice" {
		t.Fatalf("sentence %q", got)
	}
	if st := h.paymentStatus(t, payment); st != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s", st)
	}
	if n := h.count(t, `SELECT count(*) FROM reconciliation_log WHERE payment_id = $1`, payment); n != 0 {
		t.Fatalf("%d log rows", n)
	}
	h.paid.mu.Lock()
	defer h.paid.mu.Unlock()
	if len(h.paid.got) != 0 {
		t.Fatalf("the hook ran %d time(s) for a refused call", len(h.paid.got))
	}
}

// A ledger refusal (no rate to the base currency) is {"error"}, not isError,
// and nothing is written.
func TestResolveLedgerRefusalIsError(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	payment, invoice := h.recon(t, "EUR", "500.0000", "PENDING_MANUAL_REVIEW", "EUR", "500.0000")
	got := h.refused(t, "resolve_reconciliation_match", map[string]any{"payment_id": payment.String(), "invoice_id": invoice.String()})
	if !strings.Contains(got, "EUR") || !strings.Contains(got, "IDR") {
		t.Fatalf("sentence %q, want the missing-rate sentence", got)
	}
	if st := h.paymentStatus(t, payment); st != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s", st)
	}
	if n := h.count(t, `SELECT count(*) FROM journal_entries`); n != 0 {
		t.Fatalf("%d journal entries", n)
	}
}

func TestResolveReconciliationUnknownPaymentAndInvoice(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	payment, invoice := h.recon(t, "USD", "500.0000", "PENDING_MANUAL_REVIEW", "USD", "500.0000")
	missing := uuid.New()
	if got := h.refused(t, "resolve_reconciliation_match", map[string]any{"payment_id": missing.String(), "invoice_id": invoice.String()}); got != fmt.Sprintf("payment %s not found", missing) {
		t.Fatalf("sentence %q", got)
	}
	if got := h.refused(t, "resolve_reconciliation_match", map[string]any{"payment_id": payment.String(), "invoice_id": missing.String()}); got != fmt.Sprintf("invoice %s not found", missing) {
		t.Fatalf("sentence %q", got)
	}
}

// The after-commit seam runs once the payment is committed (a second
// connection sees it CLEARED) and not at all for a refusal.
func TestResolveRunsAfterPaymentOnlyAfterCommit(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	h.exec(t, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', 16000, '2026-01-01', 'TEST')`)
	refusedPay, refusedInv := h.recon(t, "USD", "450.0000", "PENDING_MANUAL_REVIEW", "USD", "500.0000")
	h.refused(t, "resolve_reconciliation_match", map[string]any{"payment_id": refusedPay.String(), "invoice_id": refusedInv.String()})
	h.paid.mu.Lock()
	n := len(h.paid.got)
	h.paid.mu.Unlock()
	if n != 0 {
		t.Fatalf("the hook ran %d time(s) for a refused call", n)
	}
	payment, invoice := h.recon(t, "USD", "500.0000", "PENDING_MANUAL_REVIEW", "USD", "500.0000")
	h.done(t, "resolve_reconciliation_match", map[string]any{"payment_id": payment.String(), "invoice_id": invoice.String()})
	h.paid.mu.Lock()
	defer h.paid.mu.Unlock()
	if len(h.paid.got) != 1 || h.paid.got[0].PaymentID != payment || h.paid.visible[0] != "CLEARED" {
		t.Fatalf("hook calls %v, seen as %v, want one call for %s seen CLEARED (committed)", h.paid.got, h.paid.visible, payment)
	}
}

// trigger_recurring tells the notifier once, after the draft is committed,
// and not again for an idempotent repeat.
func TestTriggerRecurringNotifiesOnceAfterCommit(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	template := h.done(t, "create_recurring_template", map[string]any{
		"customer_id": c.String(), "currency": "USD", "line_items": lines("Hosting", "1", "50"),
		"schedule":      map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-08-01", "end_mode": "NEVER"},
		"payment_terms": "Net 14",
	})
	h.drafts.mu.Lock()
	n := len(h.drafts.calls)
	h.drafts.mu.Unlock()
	if n != 0 {
		t.Fatalf("creating a template notified %d time(s)", n)
	}
	args := map[string]any{"template_id": template["invoice_id"], "cycle_key": "2026-08-01"}
	first := h.done(t, "trigger_recurring", args)
	h.done(t, "trigger_recurring", args)
	h.drafts.mu.Lock()
	defer h.drafts.mu.Unlock()
	if len(h.drafts.calls) != 1 || len(h.drafts.calls[0]) != 1 {
		t.Fatalf("notifications %v, want exactly one call with one draft", h.drafts.calls)
	}
	d := h.drafts.calls[0][0]
	if d.InvoiceID.String() != first["invoice_id"] || h.drafts.visible[0] != "DRAFT" {
		t.Fatalf("notified %+v, seen as %q, want the new draft %v, committed", d, h.drafts.visible[0], first["invoice_id"])
	}
	if d.CycleDate != "2026-08-01" {
		t.Fatalf("cycle date %q", d.CycleDate)
	}
}

// A refused trigger notifies nobody.
func TestTriggerRecurringRefusalDoesNotNotify(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	inv := h.invoice(t, c, "INV-0001", "MILESTONE", "SENT", "1000.0000", false)
	h.refused(t, "trigger_recurring", map[string]any{"template_id": inv.String(), "cycle_key": "2026-08-01"})
	h.drafts.mu.Lock()
	defer h.drafts.mu.Unlock()
	if len(h.drafts.calls) != 0 {
		t.Fatalf("notified %v", h.drafts.calls)
	}
}

// Go change 11: a bad id or decimal is {"error": sentence} in every write
// tool, not an isError result (Python raised in all but two of them).
func TestBadUuidIsErrorResult(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	cases := []struct {
		tool string
		args map[string]any
		arg  string
	}{
		{"create_bill", map[string]any{"vendor_id": "nope", "currency": "USD", "line_items": []any{}}, "vendor_id"},
		{"update_bill", map[string]any{"bill_id": "nope", "changes": map[string]any{}}, "bill_id"},
		{"update_item", map[string]any{"item_id": "nope", "changes": map[string]any{}}, "item_id"},
		{"update_vendor", map[string]any{"vendor_id": "nope", "changes": map[string]any{}}, "vendor_id"},
		{"update_customer", map[string]any{"customer_id": "nope", "changes": map[string]any{}}, "customer_id"},
		{"create_invoice", map[string]any{"customer_id": "nope", "currency": "USD", "line_items": []any{}}, "customer_id"},
		{"update_invoice", map[string]any{"invoice_id": "nope", "changes": map[string]any{}}, "invoice_id"},
		{"finalize_invoice", map[string]any{"invoice_id": "nope"}, "invoice_id"},
		{"void_invoice", map[string]any{"invoice_id": "nope"}, "invoice_id"},
		{"create_recurring_template", map[string]any{"customer_id": "nope", "currency": "USD", "line_items": []any{}, "schedule": map[string]any{}, "payment_terms": ""}, "customer_id"},
		{"trigger_recurring", map[string]any{"template_id": "nope", "cycle_key": "2026-08-01"}, "template_id"},
		{"create_project", map[string]any{"customer_id": "nope", "code": "A", "name": "A", "currency": "USD"}, "customer_id"},
		{"update_project", map[string]any{"project_id": "nope", "changes": map[string]any{}}, "project_id"},
		{"create_quotation", map[string]any{"customer_id": "nope", "currency": "USD", "line_items": []any{}}, "customer_id"},
		{"update_quotation", map[string]any{"quotation_id": "nope", "changes": map[string]any{}}, "quotation_id"},
		{"resolve_reconciliation_match", map[string]any{"payment_id": "nope", "invoice_id": uuid.New().String()}, "payment_id"},
		{"resolve_reconciliation_match", map[string]any{"payment_id": uuid.New().String(), "invoice_id": "nope"}, "invoice_id"},
	}
	for _, c := range cases {
		t.Run(c.tool+"/"+c.arg, func(t *testing.T) {
			t.Parallel()
			want := fmt.Sprintf("invalid %s: nope is not a valid UUID", c.arg)
			if got := h.refused(t, c.tool, c.args); got != want {
				t.Fatalf("sentence %q, want %q", got, want)
			}
		})
	}
}

// A bad decimal, date, enum or id inside a body is a refusal that names the
// field.
func TestBadValuesInBodiesAreRefusals(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	cases := []struct {
		name, tool string
		args       map[string]any
		want       string
	}{
		{"decimal", "create_invoice", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": lines("x", "1", "1.23456")}, "line_items.0.unit_price"},
		{"missing line key", "create_bill", map[string]any{"vendor_id": v.String(), "currency": "USD", "line_items": []any{map[string]any{"description": "x"}}}, "line_items.0"},
		{"date", "create_quotation", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": lines("x", "1", "1"), "valid_until": "soon"}, "valid_until"},
		{"enum", "create_item", map[string]any{"name": "x", "item_type": "GADGET"}, "item_type"},
		{"line item id", "create_invoice", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": []any{map[string]any{"description": "x", "quantity": "1", "unit_price": "1", "item_id": "bad"}}}, "line_items.0.item_id"},
		{"unknown line item", "create_invoice", map[string]any{"customer_id": c.String(), "currency": "USD", "line_items": []any{map[string]any{"description": "x", "quantity": "1", "unit_price": "1", "item_id": uuid.New().String()}}}, "line_items.0.item_id"},
		{"update body", "update_item", map[string]any{"item_id": h.item(t, "I").String(), "changes": map[string]any{"item_type": "GADGET"}}, "item_type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := h.refused(t, tc.tool, tc.args)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("sentence %q, want it to name %q", got, tc.want)
			}
		})
	}
}

// A line's item_id and expense_account_id that are empty or null are no
// value (Python: `if ln.get("item_id")`), and the id spellings Python takes
// work in a body.
func TestLineItemIDsFollowPython(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	c := h.customer(t, "Acme Pte Ltd")
	item := h.item(t, "Web Hosting")
	out := h.done(t, "create_invoice", map[string]any{"customer_id": strings.ToUpper(c.String()), "currency": "USD", "line_items": []any{
		map[string]any{"description": "a", "quantity": 1, "unit_price": 10, "item_id": ""},
		map[string]any{"description": "b", "quantity": "2", "unit_price": "5.5", "item_id": "urn:uuid:" + item.String(), "position": 3},
		map[string]any{"description": "c", "quantity": "1", "unit_price": "1", "item_id": nil},
	}})
	if out["amount"] != "22.0000" {
		t.Fatalf("invoice %v", out)
	}
	if n := h.count(t, `SELECT count(*) FROM invoice_line_items WHERE invoice_id = $1 AND item_id = $2`, out["invoice_id"], item); n != 1 {
		t.Fatalf("%d lines name the item", n)
	}
}

// create_bill takes the expense account of a line.
func TestCreateBillLineExpenseAccount(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	v := h.vendor(t, "Acme Vendor Pte Ltd")
	out := h.done(t, "create_bill", map[string]any{"vendor_id": v.String(), "currency": "USD", "line_items": []any{
		map[string]any{"description": "AWS", "quantity": "1", "unit_price": "200", "expense_account_id": 13},
	}})
	if got := h.cell(t, `SELECT expense_account_id FROM bill_line_items WHERE bill_id = $1`, out["bill_id"]); fmt.Sprint(got) != "13" {
		t.Fatalf("expense account %v", got)
	}
}

// Every update tool names the sorted JSON fields of its package's update
// struct, and checks the keys before anything else.
func TestUpdateToolsListTheirValidFields(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	id := uuid.New().String()
	cases := []struct{ tool, arg, mustHave string }{
		{"update_bill", "bill_id", "payment_terms"},
		{"update_item", "item_id", "default_unit_price"},
		{"update_vendor", "vendor_id", "tax_id"},
		{"update_customer", "customer_id", "contact_phone"},
		{"update_invoice", "invoice_id", "wht_rate"},
		{"update_project", "project_id", "contract_value"},
		{"update_quotation", "quotation_id", "valid_until"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			t.Parallel()
			got := h.refused(t, c.tool, map[string]any{c.arg: id, "changes": map[string]any{"zzz": 1, "aaa": 2}})
			prefix := "unknown field(s): aaa, zzz. Valid fields: "
			if !strings.HasPrefix(got, prefix) || !strings.Contains(got, c.mustHave) {
				t.Fatalf("sentence %q", got)
			}
			list := strings.Split(strings.TrimPrefix(got, prefix), ", ")
			for i := 1; i < len(list); i++ {
				if list[i-1] >= list[i] {
					t.Fatalf("valid fields not sorted: %v", list)
				}
			}
		})
	}
}

// The helper's afterCommit hooks run only after a commit, and a panic in one
// does not fail the tool.
func TestAfterCommitHooks(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var ran []string
	h := newFix1Harness(t, func(s *mcp.Server, d Deps) {
		record := func(label string) func(context.Context) {
			return func(context.Context) { mu.Lock(); ran = append(ran, label); mu.Unlock() }
		}
		addTool(s, d, &mcp.Tool{Name: "hooks_ok", Description: "hooks"}, func(ctx context.Context, tx pgx.Tx, _ principalRef, _ fix1In) (Object, error) {
			afterCommit(ctx, func(ctx context.Context) {
				var n int
				// The hook runs after the commit: its own connection sees the row.
				if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = 'hook-row@example.com'`).Scan(&n); err != nil || n != 1 {
					record("ok-saw-row-missing")(ctx)
					return
				}
				record("ok")(ctx)
			})
			afterCommit(ctx, func(context.Context) { panic("hook boom") })
			afterCommit(ctx, record("after-panic"))
			if _, err := tx.Exec(ctx, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'hook-row@example.com', 'x', 'admin')`, uuid.New()); err != nil {
				return Object{}, err
			}
			return Object{}, nil
		})
		addTool(s, d, &mcp.Tool{Name: "hooks_refuse", Description: "hooks"}, func(ctx context.Context, _ pgx.Tx, _ principalRef, _ fix1In) (Object, error) {
			afterCommit(ctx, record("refused"))
			return Object{}, Refuse("no")
		})
		addTool(s, d, &mcp.Tool{Name: "hooks_fail", Description: "hooks"}, func(ctx context.Context, _ pgx.Tx, _ principalRef, _ fix1In) (Object, error) {
			afterCommit(ctx, record("failed"))
			return Object{}, fmt.Errorf("driver text")
		})
	})
	if res := h.callTool(t, "hooks_ok"); res.IsError {
		t.Fatalf("hooks_ok isError: %s", resultText(res))
	}
	h.callTool(t, "hooks_refuse")
	h.callTool(t, "hooks_fail")
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ran, ",") != "ok,after-panic" {
		t.Fatalf("hooks ran %v, want ok then after-panic only", ran)
	}
}

// unmarshalable is a tool output that json cannot encode: an injected
// marshalOut failure.
type unmarshalable struct {
	C chan int `json:"c"`
}

// A withoutTx tool commits its own work before it registers a hook, so the
// hook runs even when building the result fails afterwards: an output that
// does not marshal, or an error from the body after the hook (a failed
// pick). The client still gets the generic isError.
func TestNoTxHooksRunWhenTheResultFails(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var ran []string
	h := newFix1Harness(t, func(s *mcp.Server, d Deps) {
		record := func(label string) func(context.Context) {
			return func(context.Context) { mu.Lock(); ran = append(ran, label); mu.Unlock() }
		}
		addTool(s, d, &mcp.Tool{Name: "notx_bad_out", Description: "hooks"}, func(ctx context.Context, _ pgx.Tx, _ principalRef, _ fix1In) (unmarshalable, error) {
			afterCommit(ctx, record("marshal"))
			return unmarshalable{C: make(chan int)}, nil
		}, withoutTx())
		addTool(s, d, &mcp.Tool{Name: "notx_pick_fails", Description: "hooks"}, func(ctx context.Context, _ pgx.Tx, _ principalRef, _ fix1In) (Object, error) {
			afterCommit(ctx, record("pick"))
			return Object{}, fmt.Errorf("pick: no such field")
		}, withoutTx())
	})
	for _, name := range []string{"notx_bad_out", "notx_pick_fails"} {
		if res := h.callTool(t, name); !res.IsError {
			t.Errorf("%s: not isError: %s", name, resultText(res))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ran, ",") != "marshal,pick" {
		t.Fatalf("hooks ran %v, want marshal then pick", ran)
	}
}

// A bad argument from a dict tool is a refusal ({"error"}), not isError.
func TestBadArgFromDictToolIsRefusal(t *testing.T) {
	t.Parallel()
	h := newFix1Harness(t, func(s *mcp.Server, d Deps) {
		addTool(s, d, &mcp.Tool{Name: "dict_bad_arg", Description: "rejects"}, func(context.Context, pgx.Tx, principalRef, fix1In) (Object, error) {
			return Object{}, BadArg("invalid x: %s", "y")
		})
	})
	res := h.callTool(t, "dict_bad_arg")
	if res.IsError || strings.TrimSpace(resultText(res)) != `{"error":"invalid x: y"}` {
		t.Fatalf("IsError=%v text=%q", res.IsError, resultText(res))
	}
}

// A result holding < > & is written as Python writes it, not as <.
func TestResultsKeepHTMLCharactersLiteral(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	res := h.call(t, "create_vendor", map[string]any{"name": "A<B> & Co", "notes": "<ok>"})
	text := resultText(res)
	if !strings.Contains(text, `"name":"A<B> & Co"`) || !strings.Contains(text, `"notes":"<ok>"`) || strings.Contains(text, `\u00`) {
		t.Fatalf("text %s", text)
	}
	if strings.HasSuffix(strings.TrimSuffix(text, "\n"), "\n") {
		t.Fatalf("text %q has a trailing newline", text)
	}
}

// parseID is Python's UUID(x): hyphens anywhere, repeated urn:/uuid:
// prefixes and braces are taken; anything but 32 hex digits after that is
// not.
func TestParseIDMirrorsPython(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("12345678-1234-5678-1234-567812345678")
	s := id.String()
	bare := strings.ReplaceAll(s, "-", "")
	for _, ok := range []string{
		s, strings.ToUpper(s), bare, "{" + s + "}", "{{" + s + "}}", "urn:uuid:" + s, "uuid:" + s, "urn:" + s,
		"1234-5678-12345678-1234-56781234-5678", "12345678123456781234567812345678",
	} {
		got, err := parseID("x", ok)
		if err != nil || got != id {
			t.Errorf("parseID(%q) = %v, %v, want %v", ok, got, err, id)
		}
	}
	for _, bad := range []string{
		"", "nope", bare[:31], bare + "0", "0x" + bare[:30], " " + bare[:31], "+" + bare[:31], bare[:31] + "g", bare[:30] + "\u00e9\u00e9",
	} {
		if _, err := parseID("x", bad); err == nil || err.Error() != "invalid x: "+bad+" is not a valid UUID" {
			t.Errorf("parseID(%q) err = %v", bad, err)
		}
	}
}

// refuseDomain turns what a client's input causes into a refusal and leaves
// the rest alone; a database error's constraint and column names and its text
// never reach the sentence.
func TestRefuseDomainMapsConstraintViolations(t *testing.T) {
	t.Parallel()
	for code, want := range map[string]string{
		"23505": "a record with the same value already exists",
		"23503": "a referenced record does not exist",
		"23502": "a required value is missing",
		"23514": "a value is not allowed",
		"22001": "a value is too long, out of range or malformed",
		"22P02": "a value is too long, out of range or malformed",
	} {
		pg := &pgconn.PgError{Code: code, ConstraintName: "secret_constraint_key", Message: `duplicate key value violates unique constraint "secret_constraint_key"`, Detail: "Key (name)=(x) already exists."}
		err := refuseDomain(fmt.Errorf("insert: %w", pg))
		var ref *Refusal
		if !errors.As(err, &ref) || ref.Msg != want {
			t.Errorf("%s: err %v, want a refusal saying %q", code, err, want)
		}
	}
	for _, other := range []error{
		&pgconn.PgError{Code: "40P01", Message: "deadlock detected"}, // not caused by input
		pgx.ErrTxClosed,
		errors.New("boom"),
	} {
		if err := refuseDomain(other); !errors.Is(err, other) {
			t.Errorf("refuseDomain(%v) = %v, want it unchanged", other, err)
		}
		var ref *Refusal
		if errors.As(refuseDomain(other), &ref) {
			t.Errorf("refuseDomain(%v) is a refusal", other)
		}
	}
	if refuseDomain(nil) != nil {
		t.Error("refuseDomain(nil) is not nil")
	}
}
