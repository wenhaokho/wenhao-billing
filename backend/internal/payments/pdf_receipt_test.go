package payments

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	lpdf "github.com/ledongthuc/pdf"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// pdfText is the text of every page of a PDF.
func pdfText(t *testing.T, b []byte) string {
	t.Helper()
	r, err := lpdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a PDF: %v", err)
	}
	var out []string
	for i := 1; i <= r.NumPage(); i++ {
		s, err := r.Page(i).GetPlainText(nil)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func receiptData(paymentID, invoiceID uuid.UUID) ReceiptData {
	ref := "TRF-991"
	cust, biz := "Acme Pte Ltd", "Wenhao Development"
	return ReceiptData{
		Payment: PaymentOut{
			PaymentID: paymentID, InvoiceID: &invoiceID, Amount: platform.NewDec4(dec("400")), Currency: "USD",
			PayerName: "Acme payer", PayerReference: &ref, IntakeSource: "MANUAL", Status: "CLEARED",
			PaymentDate: platform.Date{Time: time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)},
		},
		Reference: ReceiptReference(paymentID), InvoiceNumber: "INV-0100", Currency: "USD", BalanceDue: dec("600"),
		CustomerName: &cust, BusinessName: &biz,
	}
}

func TestPDFReceiptRendererMapsReceiptData(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("1a2b3c4d-0000-4000-8000-000000000000")
	b, err := PDFReceiptRenderer{}.RenderReceipt(t.Context(), receiptData(id, uuid.New()))
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, b)
	for _, w := range []string{
		"RECEIPT", "RCT-1A2B3C4D", "07 Aug 2026", "INV-0100", "Payment for invoice INV-0100",
		"Manual entry · Ref: TRF-991", "400.00", "600.00 USD", "Remaining balance: 600.00 USD.", "Acme Pte Ltd", "Wenhao Development",
	} {
		if !strings.Contains(text, w) {
			t.Errorf("receipt has no %q:\n%s", w, text)
		}
	}
}

func TestPDFReceiptRendererReadsWhatReceiptDataLacks(t *testing.T) {
	t.Parallel()
	a := apitest.New(t, func(huma.API, apitest.Deps) {})
	ctx := t.Context()
	cust, inv, pay := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO customers (customer_id, name, contact_email, billing_address1) VALUES ($1, 'Acme Pte Ltd', 'ap@acme.example', '12 Raffles Place')`, []any{cust}},
		{`INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, invoice_number)
VALUES ($1, $2, 'MILESTONE', 'USD', 1000, 600, 'PARTIALLY_PAID', 'INV-0100')`, []any{inv, cust}},
		{`INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, status)
VALUES ($1, $2, 400, 'USD', 'Acme payer', '2026-08-07', 'MANUAL', 'CLEARED')`, []any{pay, inv}},
		{`UPDATE business_profile SET name = 'Wenhao Development', address = '1 Marina Boulevard' WHERE id = 1`, nil},
	} {
		if _, err := a.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	b, err := PDFReceiptRenderer{DB: a.Pool}.RenderReceipt(ctx, receiptData(pay, inv))
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, b)
	for _, w := range []string{"1,000.00 USD", "12 Raffles Place", "ap@acme.example", "1 Marina Boulevard"} {
		if !strings.Contains(text, w) {
			t.Errorf("receipt has no %q:\n%s", w, text)
		}
	}
}
