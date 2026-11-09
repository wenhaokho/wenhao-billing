package invoices

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
)

// newApp is a fresh migrated database (no routes: this package has none
// yet), with apitest's clock.
func newApp(t *testing.T) *apitest.App {
	t.Helper()
	return apitest.New(t, func(huma.API, apitest.Deps) {})
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
