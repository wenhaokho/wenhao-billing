package invoices

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
)

// newApp serves this package's routes on a fresh migrated database, with
// apitest's clock and the default (logging) draft notifier.
func newApp(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) { Register(api, d.Pool, d.Clock, nil) })
}

// insertInvoice stores a minimal DRAFT invoice with the given number (nil
// for none) and returns its id.
func insertInvoice(t *testing.T, pool *pgxpool.Pool, number *string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO invoices (invoice_id, invoice_type, currency, amount, balance_due, status, invoice_number)
VALUES ($1, 'MILESTONE', 'IDR', 0, 0, 'DRAFT', $2)`, id, number); err != nil {
		t.Fatal(err)
	}
	return id
}

// jsonKeys are the top-level keys of the JSON object b, in order.
func jsonKeys(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %s", b)
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

// invoiceOutKeys are inventory §0's InvoiceOut fields, in order.
var invoiceOutKeys = []string{
	"invoice_id", "customer_id", "project_id", "source_quote_id", "invoice_type", "invoice_number",
	"po_so_number", "currency", "subtotal", "discount_type", "discount_value", "wht_type", "wht_rate",
	"wht_gross_up", "wht_base", "wht_amount", "wht_booked", "amount", "balance_due", "status",
	"billing_cycle_ref", "issue_date", "due_date", "coverage_start", "coverage_end", "payment_terms",
	"notes", "footer", "internal_note", "is_template", "created_at", "line_items", "expected_cash",
}

func TestInvoiceOutFieldOrder(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertInvoice(t, a.Pool, nil)
	out, err := Get(t.Context(), invoicesdb.New(a.Pool), id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonKeys(t, b); !slices.Equal(got, invoiceOutKeys) {
		t.Errorf("keys\n got %v\nwant %v", got, invoiceOutKeys)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"line_items": `[]`, "billing_cycle_ref": `null`, "expected_cash": `"0.0000"`,
		"wht_amount": `"0.0000"`, "subtotal": `null`, "wht_type": `"NONE"`,
	} {
		if string(m[key]) != want {
			t.Errorf("%s = %s, want %s", key, m[key], want)
		}
	}
}

func TestGetRendersLinesAndStoredValues(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	ctx := t.Context()
	id := insertInvoice(t, a.Pool, strPtr("WH20260001"))
	if _, err := a.Pool.Exec(ctx, `UPDATE invoices SET balance_due = 1000, amount = 1000, wht_type = 'PPH23',
wht_rate = 2, wht_amount = 20, wht_booked = 5, billing_cycle_ref = '{"milestone_ref": "M1", "a": 1}',
issue_date = '2026-01-15', subtotal = 1000 WHERE invoice_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	// Equal positions: line_item_id breaks the tie.
	first, second := uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002")
	for _, l := range []struct {
		id  uuid.UUID
		pos int
	}{{second, 0}, {uuid.New(), 1}, {first, 0}} {
		if _, err := a.Pool.Exec(ctx, `INSERT INTO invoice_line_items (line_item_id, invoice_id, position, description, quantity, unit_price, amount)
VALUES ($1, $2, $3, 'line', 1, 500, 500)`, l.id, id, l.pos); err != nil {
			t.Fatal(err)
		}
	}
	out, err := Get(ctx, invoicesdb.New(a.Pool), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.LineItems) != 3 || out.LineItems[0].LineItemID != first || out.LineItems[1].LineItemID != second || out.LineItems[2].Position != 1 {
		t.Errorf("line order = %+v", out.LineItems)
	}
	// Postgres's jsonb key order (shorter keys first), stored JSON as is.
	if got := string(out.BillingCycleRef); got != `{"a": 1, "milestone_ref": "M1"}` {
		t.Errorf("billing_cycle_ref = %s", got)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"expected_cash": `"985.0000"`, "wht_rate": `"2.0000"`, "issue_date": `"2026-01-15"`,
		"invoice_number": `"WH20260001"`, "due_date": `null`, "subtotal": `"1000.0000"`,
	} {
		if string(m[key]) != want {
			t.Errorf("%s = %s, want %s", key, m[key], want)
		}
	}
}

func TestGetMissingIsNoRows(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	if _, err := Get(t.Context(), invoicesdb.New(a.Pool), uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("err = %v, want pgx.ErrNoRows", err)
	}
}

// TestListByProject: the loader the project routes use lists a project's
// invoices as the list route renders them: newest first, lines present,
// templates left out on request, an empty (never nil) list for no match.
func TestListByProject(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a.Pool, "NONE", "", false)
	project, other := uuid.New(), uuid.New()
	for _, p := range []uuid.UUID{project, other} {
		if _, err := a.Pool.Exec(t.Context(), `INSERT INTO projects (project_id, customer_id, code, name, currency) VALUES ($1, $2, $3, 'P', 'IDR')`, p, c, p.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	first := post(t, a, createBody(c, "100", map[string]any{"project_id": project}))
	second := post(t, a, createBody(c, "200", map[string]any{"project_id": project}))
	post(t, a, createBody(c, "300", map[string]any{"project_id": other}))
	post(t, a, createBody(c, "400", nil))
	tplBody := templateBody(c, monthly("2026-01-01"))
	tplBody["project_id"] = project
	tpl := apitest.Decode[InvoiceOut](t, a.Do(t, http.MethodPost, invoicesPath+"/recurring-template", tplBody), http.StatusCreated)

	q := invoicesdb.New(a.Pool)
	ids := func(out []InvoiceOut) []uuid.UUID {
		got := []uuid.UUID{}
		for _, o := range out {
			got = append(got, o.InvoiceID)
		}
		return got
	}

	out, err := ListBy(t.Context(), q, ListFilter{ProjectID: &project, ExcludeTemplates: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(out), []uuid.UUID{second.InvoiceID, first.InvoiceID}; !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v (newest first, no template)", got, want)
	}
	for _, o := range out {
		if len(o.LineItems) != 1 {
			t.Errorf("invoice %s has %d lines, want 1", o.InvoiceID, len(o.LineItems))
		}
	}
	if want := get(t, a, second.InvoiceID); !out[0].Amount.Decimal().Equal(want.Amount.Decimal()) || !out[0].ExpectedCash.Decimal().Equal(want.ExpectedCash.Decimal()) {
		t.Errorf("amount/expected_cash = %s/%s, the route renders %s/%s", out[0].Amount.Decimal(), out[0].ExpectedCash.Decimal(), want.Amount.Decimal(), want.ExpectedCash.Decimal())
	}

	out, err = ListBy(t.Context(), q, ListFilter{ProjectID: &project})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(out), []uuid.UUID{tpl.InvoiceID, second.InvoiceID, first.InvoiceID}; !slices.Equal(got, want) {
		t.Fatalf("with templates: ids = %v, want %v", got, want)
	}

	none := uuid.New()
	out, err = ListBy(t.Context(), q, ListFilter{ProjectID: &none, ExcludeTemplates: true})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || len(out) != 0 {
		t.Errorf("no match = %#v, want an empty non-nil list", out)
	}
}
