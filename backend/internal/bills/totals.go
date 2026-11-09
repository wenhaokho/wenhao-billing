package bills

import (
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// lineAmount is billing_ap._compute_line_amount (billing_ap.py:23-24): qty ×
// price quantized to 4 places with Python's default rounding, ties to the
// even digit. Invoices and quotations round half-up; bills do not, so they
// cannot share invoices.ComputeTotals.
func lineAmount(qty, price decimal.Decimal) decimal.Decimal {
	return platform.RoundHalfEven4(qty.Mul(price))
}

// totals is billing_ap._apply_totals' arithmetic (billing_ap.py:27-40): the
// subtotal is the sum of the already rounded line amounts; the amount is the
// subtotal, less the discount when there is a type and a value (PERCENT
// subtotal × (1 − value/100), unrounded; AMOUNT subtotal − value), never
// below zero. Both are rounded half-even to 4 places.
func totals(amounts []decimal.Decimal, discountType *string, discountValue *decimal.Decimal) (subtotal, amount decimal.Decimal) {
	for _, a := range amounts {
		subtotal = subtotal.Add(a)
	}
	total := subtotal
	if discountType != nil && *discountType != "" && discountValue != nil {
		switch *discountType {
		case "PERCENT":
			total = subtotal.Mul(decimal.NewFromInt(1).Sub(discountValue.Div(decimal.NewFromInt(100))))
		case "AMOUNT":
			total = subtotal.Sub(*discountValue)
		}
		if total.IsNegative() {
			total = decimal.Zero
		}
	}
	return platform.RoundHalfEven4(subtotal), platform.RoundHalfEven4(total)
}
