// Package matching provides payment matching primitives. ExactMatch alone is
// insufficient to authorize automatic reconciliation: callers must also check
// confidence and ensure there is no ambiguity among candidates.
package matching

import (
	"strings"

	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

var amountEpsilon = decimal.RequireFromString("0.0001")

// ExactMatch compares the payment with expected cash after unbooked withholding.
// It retains the Python epsilon for parity and identifying plausible candidates;
// Match requires strict amount equality before authorizing automatic clearing.
// All decimals must already have bounded coefficients and exponents; untrusted
// external input must be bounded before decimal parsing and validated through
// Match before reconciliation.
// Only three ASCII letters are accepted as currency codes. Python uppercases
// some Unicode characters differently; unsupported values must go to review.
func ExactMatch(amount decimal.Decimal, paymentCurrency string, balance, withheld, booked decimal.Decimal, invoiceCurrency string) bool {
	expected := withholding.ExpectedCash(balance, withheld, booked)
	return currencyCode(paymentCurrency) && currencyCode(invoiceCurrency) && strings.ToUpper(paymentCurrency) == strings.ToUpper(invoiceCurrency) && amount.Sub(expected).Abs().LessThanOrEqual(amountEpsilon)
}

func currencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}
