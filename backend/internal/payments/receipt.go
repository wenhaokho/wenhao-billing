package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/pdf"
)

// ReceiptData is what a receipt renderer is given: the payment, its invoice's
// number, currency and remaining balance, and the names Python's
// render_receipt_pdf printed. CustomerName and BusinessName are nil when the
// invoice has no customer or no business name is set.
type ReceiptData struct {
	Payment       PaymentOut
	Reference     string
	InvoiceNumber string
	Currency      string
	BalanceDue    decimal.Decimal
	CustomerName  *string
	BusinessName  *string
}

// ReceiptRenderer renders a payment receipt as PDF bytes. The documents plan
// brings the real one; StubRenderer stands in until then.
type ReceiptRenderer interface {
	RenderReceipt(ctx context.Context, d ReceiptData) ([]byte, error)
}

// StubRenderer returns a fixed, minimal, valid one-page PDF whatever it is
// given.
type StubRenderer struct{}

// RenderReceipt returns the stub PDF.
func (StubRenderer) RenderReceipt(context.Context, ReceiptData) ([]byte, error) {
	return append([]byte(nil), stubPDF...), nil
}

// stubPDF is a blank A4 page.
var stubPDF = []byte("%PDF-1.4\n" +
	"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
	"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
	"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] >>\nendobj\n" +
	"trailer\n<< /Root 1 0 R /Size 4 >>\n%%EOF\n")

// ReceiptReference is the receipt's number: RCT- and the first 8 hex
// characters of the payment id, upper-cased (Python receipt_reference).
func ReceiptReference(paymentID uuid.UUID) string { return pdf.ReceiptReference(paymentID) }

// BalanceNote is the one-line balance summary of the receipt email (Python
// balance_note): paid in full at a balance of 0 or less.
func BalanceNote(balance decimal.Decimal, currency string) string {
	return pdf.BalanceNote(balance, currency)
}

// FormatAmount is Python's _fmt_amount: pdf.FmtAmount, the one amount format
// of PDFs and email bodies.
func FormatAmount(amount decimal.Decimal, currency string) string {
	return pdf.FmtAmount(amount, currency)
}
