// Package trialbalance aggregates journal lines that have already been loaded.
package trialbalance

import (
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

type Line struct {
	AccountID                  int64
	Code, Name, Type, Currency string
	// PostedAt represents the database's timestamp without time zone; its calendar
	// fields must be interpreted in the same reporting calendar as AsOf.
	PostedAt                             time.Time
	Debit, Credit, BaseDebit, BaseCredit decimal.Decimal
}

type Row struct {
	AccountID                                          int64
	Code, Name, Type, Currency                         string
	Debit, Credit, Net, BaseDebit, BaseCredit, BaseNet decimal.Decimal
}

type Report struct {
	AsOf                            time.Time
	BaseCurrency                    string
	Rows                            []Row
	TotalDebitBase, TotalCreditBase decimal.Decimal
}

func currencyValid(s string) bool {
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

// amountValid bounds coefficients and exponents before arithmetic can rescale
// them. Inputs use the journal schema's NUMERIC(19,4) representation.
func amountValid(v decimal.Decimal) bool {
	if v.Exponent() < -4 || v.Exponent() > 14 || v.IsNegative() {
		return false
	}
	coefficient := v.Coefficient()
	if coefficient.BitLen() > 64 {
		return false
	}
	digits := len(coefficient.String())
	return digits <= 19 && int64(digits)+int64(v.Exponent()) <= 15
}

// Aggregate has no database access. The caller MUST establish business identity
// and supply only journal lines and account metadata belonging to that business;
// this function cannot verify tenant isolation. The caller must supply valid,
// database decimals and posted timestamps in the reporting calendar. Amounts
// must have at most four fractional digits and fit NUMERIC(19,4); equivalent
// values with excessively scaled coefficients are rejected without rescaling.
// AsOf is a calendar date; its time and location are ignored. Currency codes
// must be canonical uppercase ASCII. No currency conversion is performed.
func Aggregate(asOf time.Time, baseCurrency string, lines []Line) (Report, error) {
	if asOf.IsZero() || !currencyValid(baseCurrency) {
		return Report{}, fmt.Errorf("invalid report date or base currency")
	}
	y, m, d := asOf.Date()
	date := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	report := Report{AsOf: date, BaseCurrency: baseCurrency, Rows: []Row{}}
	type key struct {
		id       int64
		currency string
	}
	groups := make(map[key]*Row)
	accounts := make(map[int64]Line)
	for i, l := range lines {
		if l.PostedAt.IsZero() {
			return Report{}, fmt.Errorf("invalid journal line %d", i)
		}
		py, pm, pd := l.PostedAt.Date()
		if time.Date(py, pm, pd, 0, 0, 0, 0, time.UTC).After(date) {
			continue
		}
		if l.AccountID <= 0 || l.Code == "" || l.Name == "" || l.Type == "" || !currencyValid(l.Currency) || !amountValid(l.Debit) || !amountValid(l.Credit) || !amountValid(l.BaseDebit) || !amountValid(l.BaseCredit) {
			return Report{}, fmt.Errorf("invalid journal line %d", i)
		}
		if prev, ok := accounts[l.AccountID]; ok && (prev.Code != l.Code || prev.Name != l.Name || prev.Type != l.Type) {
			return Report{}, fmt.Errorf("inconsistent account metadata for %d", l.AccountID)
		}
		accounts[l.AccountID] = l
		k := key{l.AccountID, l.Currency}
		r := groups[k]
		if r == nil {
			r = &Row{AccountID: l.AccountID, Code: l.Code, Name: l.Name, Type: l.Type, Currency: l.Currency}
			groups[k] = r
		}
		r.Debit = r.Debit.Add(l.Debit)
		r.Credit = r.Credit.Add(l.Credit)
		r.BaseDebit = r.BaseDebit.Add(l.BaseDebit)
		r.BaseCredit = r.BaseCredit.Add(l.BaseCredit)
	}
	for _, r := range groups {
		r.Net = r.Debit.Sub(r.Credit)
		r.BaseNet = r.BaseDebit.Sub(r.BaseCredit)
		report.TotalDebitBase = report.TotalDebitBase.Add(r.BaseDebit)
		report.TotalCreditBase = report.TotalCreditBase.Add(r.BaseCredit)
		report.Rows = append(report.Rows, *r)
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		a, b := report.Rows[i], report.Rows[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Currency != b.Currency {
			return a.Currency < b.Currency
		}
		return a.AccountID < b.AccountID
	})
	return report, nil
}
