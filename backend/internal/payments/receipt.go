package payments

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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
func ReceiptReference(paymentID uuid.UUID) string {
	return "RCT-" + strings.ToUpper(strings.ReplaceAll(paymentID.String(), "-", "")[:8])
}

// BalanceNote is the one-line balance summary of the receipt email (Python
// balance_note): paid in full at a balance of 0 or less.
func BalanceNote(balance decimal.Decimal, currency string) string {
	if !balance.IsPositive() {
		return "This invoice is now paid in full."
	}
	return "Remaining balance: " + FormatAmount(balance, currency) + "."
}

// zeroDecimal are the currencies with no minor unit (ISO 4217), as Python's
// _ZERO_DECIMAL_CURRENCIES.
var zeroDecimal = map[string]bool{
	"IDR": true, "JPY": true, "KRW": true, "VND": true, "CLP": true, "ISK": true, "HUF": true,
	"PYG": true, "RWF": true, "UGX": true, "XAF": true, "XOF": true, "XPF": true,
}

// FormatAmount is Python's _fmt_amount (invoice_pdf.py): the amount rounded
// half-even to 0 places for a zero-decimal currency (compared upper-cased)
// and 2 otherwise, thousands grouped by commas, then a space and the currency
// as given. A negative amount keeps its minus even when it rounds to zero
// ("-0 IDR"), as Python's Decimal does.
func FormatAmount(amount decimal.Decimal, currency string) string {
	places := int32(2)
	if zeroDecimal[strings.ToUpper(currency)] {
		places = 0
	}
	s := amount.RoundBank(places).Abs().StringFixed(places)
	whole, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	if amount.IsNegative() {
		b.WriteByte('-')
	}
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if hasFrac {
		b.WriteString("." + frac)
	}
	return fmt.Sprintf("%s %s", b.String(), currency)
}
