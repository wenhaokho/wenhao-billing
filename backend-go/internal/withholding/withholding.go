// Package withholding calculates Indonesian invoice withholding amounts.
package withholding

import (
	"fmt"

	"github.com/shopspring/decimal"
)

type Type string

const (
	None  Type = "NONE"
	PPH21 Type = "PPH21"
	PPH23 Type = "PPH23"
)

type Result struct {
	Total       decimal.Decimal
	Withholding decimal.Decimal
	Base        decimal.Decimal
}

// Normalize validates the type and rate and applies the type's default rate.
func Normalize(kind Type, rate *decimal.Decimal, grossUp bool) (Type, *decimal.Decimal, bool, error) {
	if kind != None && kind != PPH21 && kind != PPH23 {
		return "", nil, false, fmt.Errorf("Unknown withholding tax type: %s", kind)
	}
	if kind == None {
		return None, nil, false, nil
	}
	if rate == nil {
		value := decimal.NewFromInt(2)
		if kind == PPH21 {
			value = decimal.RequireFromString("2.5")
		}
		rate = &value
	}
	if !rate.GreaterThan(decimal.Zero) || !rate.LessThan(decimal.NewFromInt(100)) {
		return "", nil, false, fmt.Errorf("Withholding tax rate must be greater than 0 and less than 100")
	}
	return kind, rate, grossUp, nil
}

func currencyPlaces(currency string) int32 {
	if currency == "IDR" {
		return 0
	}
	return 2
}

// RoundCurrency rounds ties away from zero, as Python's ROUND_HALF_UP does.
func RoundCurrency(value decimal.Decimal, currency string) decimal.Decimal {
	return value.Round(currencyPlaces(currency))
}

// contextRound applies Python Decimal's default 28-digit, half-even context.
func contextRound(value decimal.Decimal) decimal.Decimal {
	if value.IsZero() {
		return value
	}
	places := int32(28-value.NumDigits()) - value.Exponent()
	return value.RoundBank(places)
}

// contextDivide rounds an exact quotient to 28 significant digits, half-even.
func contextDivide(a, b decimal.Decimal) decimal.Decimal {
	if a.IsZero() {
		return decimal.Zero
	}
	adjusted := int32(a.NumDigits()) + a.Exponent() - int32(b.NumDigits()) - b.Exponent()
	if a.Abs().LessThan(b.Abs().Shift(adjusted)) {
		adjusted--
	}
	places := int32(27) - adjusted
	quotient, remainder := a.QuoRem(b, places)
	comparison := remainder.Abs().Mul(decimal.NewFromInt(2)).Cmp(b.Abs().Shift(-places))
	if comparison > 0 || (comparison == 0 && quotient.Coefficient().Bit(0) == 1) {
		unit := decimal.New(1, -places)
		if a.Sign() != b.Sign() {
			unit = unit.Neg()
		}
		quotient = quotient.Add(unit)
	}
	return quotient
}

// Compute calculates withholding using a previously normalized type and rate.
// Like the Python helper, a nil rate, NONE, or zero base bypasses calculation.
func Compute(base decimal.Decimal, currency string, kind Type, rate *decimal.Decimal, grossUp bool) Result {
	result := Result{Total: base, Withholding: decimal.Zero, Base: base}
	if kind == None || rate == nil || base.IsZero() {
		return result
	}
	if grossUp {
		fraction := contextDivide(*rate, decimal.NewFromInt(100))
		denominator := contextRound(decimal.NewFromInt(1).Sub(fraction))
		result.Total = RoundCurrency(contextDivide(base, denominator), currency)
		result.Withholding = contextRound(result.Total.Sub(base))
	} else {
		result.Withholding = RoundCurrency(contextDivide(contextRound(base.Mul(*rate)), decimal.NewFromInt(100)), currency)
	}
	return result
}

// ExpectedCash returns the nonnegative balance after unbooked withholding.
func ExpectedCash(balanceDue, withholding, booked decimal.Decimal) decimal.Decimal {
	value := contextRound(balanceDue.Sub(contextRound(withholding.Sub(booked))))
	if value.IsNegative() {
		return decimal.Zero
	}
	return value
}
