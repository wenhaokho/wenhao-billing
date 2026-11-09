package pdf

import (
	"errors"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

// pool is a fresh migrated database.
func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return apitest.New(t, func(huma.API, apitest.Deps) {}).Pool
}

func exec(t *testing.T, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := p.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

type fixture struct{ customer, invoice, quotation, payment uuid.UUID }

// seed stores a customer, an invoice with three lines inserted out of order,
// a quotation with two, a payment on the invoice, a business profile with
// accounts in the wrong order.
func seed(t *testing.T, p *pgxpool.Pool) fixture {
	t.Helper()
	f := fixture{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	exec(t, p, `INSERT INTO customers (customer_id, name, contact_email) VALUES ($1, 'Acme Pte Ltd', 'ap@acme.example')`, f.customer)
	exec(t, p, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, invoice_number)
VALUES ($1, $2, 'MILESTONE', 'IDR', 3000, 3000, 'SENT', 'WH20260001')`, f.invoice, f.customer)
	for _, l := range []struct {
		pos  int
		desc string
	}{{3, "third"}, {1, "first"}, {2, "second"}} {
		exec(t, p, `INSERT INTO invoice_line_items (line_item_id, invoice_id, position, description, quantity, unit_price, amount) VALUES (gen_random_uuid(), $1, $2, $3, 1, 1000, 1000)`,
			f.invoice, l.pos, l.desc)
	}
	exec(t, p, `INSERT INTO quotations (quotation_id, customer_id, currency, amount, status, quotation_number) VALUES ($1, $2, 'SGD', 20, 'DRAFT', 'Q20260001')`,
		f.quotation, f.customer)
	exec(t, p, `INSERT INTO quotation_line_items (line_item_id, quotation_id, position, description, quantity, unit_price, amount) VALUES (gen_random_uuid(), $1, 2, 'b', 1, 10, 10), (gen_random_uuid(), $1, 1, 'a', 1, 10, 10)`, f.quotation)
	exec(t, p, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, status)
VALUES ($1, $2, 1000, 'IDR', 'Acme', '2026-01-20', 'MANUAL', 'CLEARED')`, f.payment, f.invoice)
	exec(t, p, `UPDATE business_profile SET name = 'Wenhao', logo_url = 'http://logo.test/l.png' WHERE id = 1`)
	exec(t, p, `INSERT INTO payment_account (business_profile_id, currency, instructions, is_default) VALUES (1, 'USD', 'usd', false), (1, 'IDR', 'idr', true), (1, 'SGD', 'sgd', false)`)
	return f
}

func TestLoadInvoice(t *testing.T) {
	t.Parallel()
	p := pool(t)
	f := seed(t, p)
	in, err := LoadInvoice(t.Context(), p, f.invoice)
	if err != nil {
		t.Fatal(err)
	}
	var descs []string
	for _, l := range in.Lines {
		descs = append(descs, l.Description)
	}
	eq(t, "lines by position", descs, []string{"first", "second", "third"})
	if in.Customer == nil || in.Customer.Name != "Acme Pte Ltd" {
		t.Errorf("customer = %+v", in.Customer)
	}
	if in.Business == nil || *in.Business.Name != "Wenhao" {
		t.Errorf("business = %+v", in.Business)
	}
	var cur []string
	for _, a := range in.Accounts {
		cur = append(cur, a.Currency)
	}
	eq(t, "accounts by currency", cur, []string{"IDR", "SGD", "USD"})
	d := InvoiceDocument(in)
	eq(t, "Ref", d.Ref, "WH20260001")
	eq(t, "PaymentInstructions", d.PaymentInstructions, "idr")
	eq(t, "LogoURL", d.LogoURL, "http://logo.test/l.png")
}

func TestLoadInvoiceWithoutCustomer(t *testing.T) {
	t.Parallel()
	p := pool(t)
	f := seed(t, p)
	exec(t, p, `UPDATE invoices SET customer_id = NULL WHERE invoice_id = $1`, f.invoice)
	in, err := LoadInvoice(t.Context(), p, f.invoice)
	if err != nil || in.Customer != nil {
		t.Errorf("customer %+v, err %v", in.Customer, err)
	}
	eq(t, "ToName", InvoiceDocument(in).ToName, "—")
}

// The profile GET creates row 1 when it is missing; a document must not (and
// still renders, as Python's db.get returned None).
func TestLoadersReadBusinessRowWithoutCreatingIt(t *testing.T) {
	t.Parallel()
	p := pool(t)
	f := seed(t, p)
	exec(t, p, `DELETE FROM payment_account`)
	exec(t, p, `DELETE FROM business_profile`)

	inv, err := LoadInvoice(t.Context(), p, f.invoice)
	if err != nil || inv.Business != nil || len(inv.Accounts) != 0 {
		t.Errorf("invoice: business %+v accounts %v err %v", inv.Business, inv.Accounts, err)
	}
	quo, err := LoadQuotation(t.Context(), p, f.quotation)
	if err != nil || quo.Business != nil {
		t.Errorf("quotation: business %+v err %v", quo.Business, err)
	}
	rcp, err := LoadReceipt(t.Context(), p, f.payment)
	if err != nil || rcp.Business != nil {
		t.Errorf("receipt: business %+v err %v", rcp.Business, err)
	}
	var n int
	if err := p.QueryRow(t.Context(), `SELECT count(*) FROM business_profile`).Scan(&n); err != nil || n != 0 {
		t.Errorf("business_profile rows = %d, err %v: a loader created row 1", n, err)
	}
	eq(t, "FromName", InvoiceDocument(inv).FromName, "—")
}

func TestLoadQuotation(t *testing.T) {
	t.Parallel()
	p := pool(t)
	f := seed(t, p)
	in, err := LoadQuotation(t.Context(), p, f.quotation)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "lines by position", []string{in.Lines[0].Description, in.Lines[1].Description}, []string{"a", "b"})
	if in.Customer == nil || in.Business == nil || len(in.Accounts) != 3 {
		t.Errorf("customer %+v business %+v accounts %d", in.Customer, in.Business, len(in.Accounts))
	}
	eq(t, "PaymentInstructions", QuotationDocument(in).PaymentInstructions, "sgd")
}

func TestLoadReceipt(t *testing.T) {
	t.Parallel()
	p := pool(t)
	f := seed(t, p)
	in, err := LoadReceipt(t.Context(), p, f.payment)
	if err != nil {
		t.Fatal(err)
	}
	if in.Payment.PaymentID != f.payment || in.Invoice.InvoiceID != f.invoice || in.Customer == nil || in.Business == nil {
		t.Errorf("receipt data = %+v", in)
	}
	eq(t, "Ref", ReceiptDocument(in).Ref, ReceiptReference(f.payment))
}

func TestLoadersRefuseWhatIsMissing(t *testing.T) {
	t.Parallel()
	p := pool(t)
	exec(t, p, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, status)
VALUES ($1, 1, 'IDR', 'x', '2026-01-20', 'MANUAL', 'PENDING_MANUAL_REVIEW')`, uuid.MustParse("00000000-0000-0000-0000-0000000000aa"))
	missing := uuid.New()
	if _, err := LoadInvoice(t.Context(), p, missing); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("invoice: %v", err)
	}
	if _, err := LoadQuotation(t.Context(), p, missing); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("quotation: %v", err)
	}
	if _, err := LoadReceipt(t.Context(), p, missing); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("receipt: %v", err)
	}
	if _, err := LoadReceipt(t.Context(), p, uuid.MustParse("00000000-0000-0000-0000-0000000000aa")); !errors.Is(err, ErrNoInvoice) {
		t.Errorf("receipt without an invoice: %v", err)
	}
}
