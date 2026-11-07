// Package balancesheet aggregates already tenant-scoped journal lines.
package balancesheet

import (
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

type Line struct {
	AccountID        int64
	Code, Name, Type string
	// PostedAt uses the reporting calendar, as in the database timestamp.
	PostedAt              time.Time
	BaseDebit, BaseCredit decimal.Decimal
}
type AccountLine struct {
	AccountID   int64
	Code, Name  string
	BaseBalance decimal.Decimal
}
type Group struct {
	Type      string
	TotalBase decimal.Decimal
	Lines     []AccountLine
}
type Report struct {
	AsOf                                                                                      time.Time
	BaseCurrency                                                                              string
	Groups                                                                                    []Group
	AssetsTotalBase, LiabilitiesTotalBase, EquityTotalBase, IncomeTotalBase, ExpenseTotalBase decimal.Decimal
}

var types = []string{"ASSET", "LIABILITY", "EQUITY", "INCOME", "EXPENSE", "COGS"}

func validAmount(v decimal.Decimal) bool {
	if v.Exponent() < -4 || v.Exponent() > 14 || v.IsNegative() {
		return false
	}
	c := v.Coefficient()
	if c.BitLen() > 64 {
		return false
	}
	n := len(c.String())
	return n <= 19 && int64(n)+int64(v.Exponent()) <= 15
}
func validCurrency(s string) bool {
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

// Aggregate performs no database reads. Callers must supply only records and
// account metadata belonging to the authorized business. Amounts must fit
// NUMERIC(19,4). Dates use calendar fields; the entire as-of date is included.
func Aggregate(asOf time.Time, baseCurrency string, lines []Line) (Report, error) {
	if asOf.IsZero() || !validCurrency(baseCurrency) {
		return Report{}, fmt.Errorf("invalid report date or base currency")
	}
	y, m, d := asOf.Date()
	date := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	report := Report{AsOf: date, BaseCurrency: baseCurrency, Groups: []Group{}}
	type accum struct {
		line          Line
		debit, credit decimal.Decimal
	}
	accounts := map[int64]*accum{}
	for i, l := range lines {
		if l.PostedAt.IsZero() {
			return Report{}, fmt.Errorf("invalid journal line %d", i)
		}
		y, m, d := l.PostedAt.Date()
		if time.Date(y, m, d, 0, 0, 0, 0, time.UTC).After(date) {
			continue
		}
		validType := false
		for _, t := range types {
			if l.Type == t {
				validType = true
				break
			}
		}
		if l.AccountID <= 0 || l.Code == "" || l.Name == "" || !validType || !validAmount(l.BaseDebit) || !validAmount(l.BaseCredit) {
			return Report{}, fmt.Errorf("invalid journal line %d", i)
		}
		a := accounts[l.AccountID]
		if a == nil {
			a = &accum{line: l}
			accounts[l.AccountID] = a
		} else if a.line.Code != l.Code || a.line.Name != l.Name || a.line.Type != l.Type {
			return Report{}, fmt.Errorf("inconsistent account metadata for %d", l.AccountID)
		}
		a.debit = a.debit.Add(l.BaseDebit)
		a.credit = a.credit.Add(l.BaseCredit)
	}
	groups := map[string]*Group{}
	for _, a := range accounts {
		if a.debit.IsZero() && a.credit.IsZero() {
			continue
		}
		balance := a.credit.Sub(a.debit)
		if a.line.Type == "ASSET" || a.line.Type == "EXPENSE" || a.line.Type == "COGS" {
			balance = a.debit.Sub(a.credit)
		}
		g := groups[a.line.Type]
		if g == nil {
			g = &Group{Type: a.line.Type, Lines: []AccountLine{}}
			groups[a.line.Type] = g
		}
		g.Lines = append(g.Lines, AccountLine{a.line.AccountID, a.line.Code, a.line.Name, balance})
		g.TotalBase = g.TotalBase.Add(balance)
	}
	for _, t := range types {
		if g := groups[t]; g != nil {
			sort.Slice(g.Lines, func(i, j int) bool {
				if g.Lines[i].Code != g.Lines[j].Code {
					return g.Lines[i].Code < g.Lines[j].Code
				}
				return g.Lines[i].AccountID < g.Lines[j].AccountID
			})
			report.Groups = append(report.Groups, *g)
			switch t {
			case "ASSET":
				report.AssetsTotalBase = g.TotalBase
			case "LIABILITY":
				report.LiabilitiesTotalBase = g.TotalBase
			case "EQUITY":
				report.EquityTotalBase = g.TotalBase
			case "INCOME":
				report.IncomeTotalBase = g.TotalBase
			case "EXPENSE", "COGS":
				report.ExpenseTotalBase = report.ExpenseTotalBase.Add(g.TotalBase)
			}
		}
	}
	return report, nil
}
