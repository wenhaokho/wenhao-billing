package platform

import "github.com/shopspring/decimal"

// RoundHalfEven4 rounds to four places, ties to the even digit (Python's
// ROUND_HALF_EVEN, the ledger's quantize).
func RoundHalfEven4(d decimal.Decimal) decimal.Decimal { return d.RoundBank(4) }

// RoundHalfUp4 rounds to four places, ties away from zero (ROUND_HALF_UP).
func RoundHalfUp4(d decimal.Decimal) decimal.Decimal { return d.Round(4) }

// RoundWithholding rounds a withholding amount ROUND_HALF_UP: whole rupiah
// for IDR, two places for any other currency.
func RoundWithholding(d decimal.Decimal, currency string) decimal.Decimal {
	if currency == "IDR" {
		return d.Round(0)
	}
	return d.Round(2)
}
