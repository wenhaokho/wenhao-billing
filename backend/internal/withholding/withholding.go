// Package withholding ports Indonesian withholding tax (PPh 21 / PPh 23)
// helpers from the Python backend (app/services/withholding.py).
//
// The customer withholds a percentage of the invoice base and remits it to
// the tax office. With gross-up the invoice total is increased so that
// total - amount == base.
package withholding

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Types are the accepted withholding types.
var Types = []string{"NONE", "PPH21", "PPH23"}

// DefaultRates are the rates (percent) used when a type is given without one.
var DefaultRates = map[string]decimal.Decimal{
	"PPH21": decimal.RequireFromString("2.5"),
	"PPH23": decimal.NewFromInt(2),
}

// Labels are the display names of the taxed types.
var Labels = map[string]string{"PPH21": "PPh 21", "PPH23": "PPh 23"}

var hundred = decimal.NewFromInt(100)

// divPlaces is the division precision: at least Python's 28 significant
// digits for any realistic amount. It is passed per call so the global
// decimal.DivisionPrecision is never touched.
const divPlaces = 28

// Result is the outcome of Compute.
type Result struct{ Total, Amount, Base decimal.Decimal }

// Compute returns the invoice total and withholding amount. base is the
// subtotal after discount.
func Compute(base decimal.Decimal, currency, whtType string, rate *decimal.Decimal, grossUp bool) Result {
	if whtType == "NONE" || rate == nil || base.IsZero() {
		return Result{Total: base, Amount: decimal.Zero, Base: base}
	}
	if grossUp {
		divisor := decimal.NewFromInt(1).Sub(rate.DivRound(hundred, divPlaces))
		total := platform.RoundWithholding(base.DivRound(divisor, divPlaces), currency)
		return Result{Total: total, Amount: total.Sub(base), Base: base}
	}
	amount := platform.RoundWithholding(base.Mul(*rate).DivRound(hundred, divPlaces), currency)
	return Result{Total: base, Amount: amount, Base: base}
}

// ExpectedCash is the cash still expected from the customer: the balance
// minus unbooked withholding, never negative.
func ExpectedCash(balanceDue, whtAmount, whtBooked decimal.Decimal) decimal.Decimal {
	v := balanceDue.Sub(whtAmount.Sub(whtBooked))
	if v.IsNegative() {
		return decimal.Zero
	}
	return v
}

// Normalize validates a withholding configuration. NONE clears the rate and
// gross-up; a nil rate takes the type's default. Error text is Python's.
func Normalize(whtType string, rate *decimal.Decimal, grossUp bool) (string, *decimal.Decimal, bool, error) {
	known := false
	for _, t := range Types {
		if t == whtType {
			known = true
		}
	}
	if !known {
		return "", nil, false, fmt.Errorf("Unknown withholding tax type: %s", whtType) //nolint:staticcheck // Python's exact sentence, surfaced as the 422 detail
	}
	if whtType == "NONE" {
		return "NONE", nil, false, nil
	}
	if rate == nil {
		r := DefaultRates[whtType]
		rate = &r
	}
	if !rate.IsPositive() || rate.GreaterThanOrEqual(hundred) {
		return "", nil, false, errors.New("Withholding tax rate must be greater than 0 and less than 100") //nolint:staticcheck // Python's exact sentence, surfaced as the 422 detail
	}
	return whtType, rate, grossUp, nil
}
