// Package money is the one rounding rule and the one way money is written in
// text. Amounts are decimal.Decimal, never float64.
package money

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Scale is the number of decimal places money is kept at.
const Scale = 2

// Quantize rounds to Scale places, half away from zero: 0.125 -> 0.13 and
// -0.125 -> -0.13 (Python's ROUND_HALF_UP, which TAGARA used).
func Quantize(d decimal.Decimal) decimal.Decimal {
	return d.Round(Scale)
}

// Format writes an amount inside a sentence. Rupiah the Indonesian way:
// "Rp1.500.000", or "Rp933.336,50" when there are cents. Any other currency
// code-prefixed with exactly two decimals: "USD 1,500.00". A negative amount
// takes a leading "-". Rounded with Quantize first.
func Format(d decimal.Decimal, currency string) string {
	rounded := Quantize(d)
	sign := ""
	if rounded.IsNegative() {
		sign = "-"
	}
	whole, cents, _ := strings.Cut(rounded.Abs().StringFixed(Scale), ".")
	if currency != "IDR" {
		return sign + currency + " " + group(whole, ",") + "." + cents
	}
	if cents == "00" {
		return sign + "Rp" + group(whole, ".")
	}
	return sign + "Rp" + group(whole, ".") + "," + cents
}

// group puts sep between every three digits from the right.
func group(digits, sep string) string {
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	return b.String()
}
