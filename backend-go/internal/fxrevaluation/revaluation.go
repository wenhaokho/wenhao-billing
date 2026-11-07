// Package fxrevaluation calculates a report from authorized as-of balances.
package fxrevaluation

import (
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

type Balance struct {
	AccountID                  int64
	Code, Name, Type, Currency string
	// Debit and credit amounts are aggregate journal balances, not individual lines.
	Debit, Credit, BaseDebit, BaseCredit decimal.Decimal
}
type Rate struct {
	Value decimal.Decimal
	AsOf  time.Time
}
type Line struct {
	AccountID                              int64
	Code, Name, Type, Currency             string
	FaceBalance, OriginalBase              decimal.Decimal
	RevaluedBase, Rate, UnrealisedGainLoss *decimal.Decimal
	RateAsOf                               *time.Time
	RateMissing                            bool
}
type Report struct {
	AsOf                                     time.Time
	BaseCurrency                             string
	Lines                                    []Line
	TotalUnrealisedGain, TotalUnrealisedLoss decimal.Decimal
	MissingRates                             []string
}

func day(t time.Time) time.Time { y, m, d := t.Date(); return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
func currency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// bounded limits computational work and permits aggregate values up to 28 digits.
func bounded(v decimal.Decimal, scale int32) bool {
	return v.Exponent() >= -scale && v.Exponent() <= 27 && v.NumDigits() <= 28 && int64(v.NumDigits())+int64(v.Exponent()) <= int64(28-scale)
}

// Calculate performs no reads. The caller must authorize and scope the balances,
// attest they represent the requested cutoff, and supply one bucket per account
// and currency. Rates are direct currency-to-base effective rates, selected by
// the caller as the latest on or before asOf. Missing rates produce incomplete
// lines; invalid rates reject the report. Calendar dates use the supplied calendar fields; no timezone conversion occurs.
// Python accepts nonpositive rates; this implementation rejects them. Amounts
// and rates are bounded to keep Python Decimal precision and arithmetic safe.
// Face and original balances, gains, and totals use exact arithmetic: unlike
// Python Decimal with its default 28-digit context, they are not rounded after
// subtraction or addition. Multiplication and quantization retain Python
// behavior. Work and storage scale with the number of supplied buckets; each
// decimal operand is bounded before arithmetic.
func Calculate(asOf time.Time, base string, snapshotVerified bool, balances []Balance, rates map[string]Rate) (Report, error) {
	if asOf.IsZero() || !currency(base) || !snapshotVerified {
		return Report{}, fmt.Errorf("invalid report parameters")
	}
	asOf = day(asOf)
	out := Report{AsOf: asOf, BaseCurrency: base, Lines: []Line{}, MissingRates: []string{}}
	for c, r := range rates {
		if !currency(c) || c == base || r.AsOf.IsZero() || day(r.AsOf).After(asOf) || !r.Value.IsPositive() || !bounded(r.Value, 8) || r.Value.GreaterThan(decimal.RequireFromString("99999999999.99999999")) {
			return Report{}, fmt.Errorf("invalid rate for %s", c)
		}
	}
	seen := map[string]bool{}
	missing := map[string]bool{}
	accounts := map[int64]Balance{}
	for i, b := range balances {
		if b.AccountID <= 0 || b.Code == "" || b.Name == "" || (b.Type != "ASSET" && b.Type != "LIABILITY") || !currency(b.Currency) || b.Currency == base || !bounded(b.Debit, 4) || !bounded(b.Credit, 4) || !bounded(b.BaseDebit, 4) || !bounded(b.BaseCredit, 4) || b.Debit.IsNegative() || b.Credit.IsNegative() || b.BaseDebit.IsNegative() || b.BaseCredit.IsNegative() {
			return Report{}, fmt.Errorf("invalid balance %d", i)
		}
		k := fmt.Sprintf("%d:%s", b.AccountID, b.Currency)
		if seen[k] {
			return Report{}, fmt.Errorf("duplicate balance %s", k)
		}
		seen[k] = true
		if a, ok := accounts[b.AccountID]; ok && (a.Code != b.Code || a.Name != b.Name || a.Type != b.Type) {
			return Report{}, fmt.Errorf("inconsistent account %d", b.AccountID)
		}
		accounts[b.AccountID] = b
		face, original := b.Debit.Sub(b.Credit), b.BaseDebit.Sub(b.BaseCredit)
		if b.Type == "LIABILITY" {
			face = face.Neg()
			original = original.Neg()
		}
		if face.IsZero() && original.IsZero() {
			continue
		}
		l := Line{AccountID: b.AccountID, Code: b.Code, Name: b.Name, Type: b.Type, Currency: b.Currency, FaceBalance: face, OriginalBase: original}
		r, ok := rates[b.Currency]
		if !ok {
			l.RateMissing = true
			missing[b.Currency] = true
			out.Lines = append(out.Lines, l)
			continue
		}
		product := face.Mul(r.Value)
		if !product.IsZero() {
			product = product.RoundBank(int32(28-product.NumDigits()) - product.Exponent())
		}
		revalued := product.RoundBank(4)
		if revalued.NumDigits()+int(revalued.Exponent())+4 > 28 {
			return Report{}, fmt.Errorf("revaluation exceeds decimal precision")
		}
		gain := revalued.Sub(original)
		if b.Type == "LIABILITY" {
			gain = gain.Neg()
		}
		if gain.IsPositive() {
			out.TotalUnrealisedGain = out.TotalUnrealisedGain.Add(gain)
		} else if gain.IsNegative() {
			out.TotalUnrealisedLoss = out.TotalUnrealisedLoss.Sub(gain)
		}
		rate := r.Value
		date := day(r.AsOf)
		l.RevaluedBase = &revalued
		l.Rate = &rate
		l.RateAsOf = &date
		l.UnrealisedGainLoss = &gain
		out.Lines = append(out.Lines, l)
	}
	sort.Slice(out.Lines, func(i, j int) bool {
		a, b := out.Lines[i], out.Lines[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Currency != b.Currency {
			return a.Currency < b.Currency
		}
		return a.AccountID < b.AccountID
	})
	for c := range missing {
		out.MissingRates = append(out.MissingRates, c)
	}
	sort.Strings(out.MissingRates)
	return out, nil
}
