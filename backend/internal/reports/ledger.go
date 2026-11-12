package reports

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/reports/reportsdb"
)

// cutoff is the first instant after as_of's day: a posting is in the report
// when posted_at is before it.
func cutoff(asOf time.Time) time.Time { return asOf.AddDate(0, 0, 1) }

// trialBalance builds the trial balance from the query's rows, which arrive
// ordered by code and currency. The totals are the sums of the base columns.
func trialBalance(asOf time.Time, base string, rows []reportsdb.TrialBalanceRowsRow) TrialBalanceReport {
	out := TrialBalanceReport{AsOf: platform.Date{Time: asOf}, BaseCurrency: base, Rows: make([]TrialBalanceRow, 0, len(rows))}
	var totalDebit, totalCredit decimal.Decimal
	for _, r := range rows {
		out.Rows = append(out.Rows, TrialBalanceRow{
			AccountID: r.AccountID, Code: r.Code, Name: r.Name, Type: r.Type, Currency: r.Currency,
			Debit: platform.NewDec4(r.Debit), Credit: platform.NewDec4(r.Credit), Net: platform.NewDec4(r.Debit.Sub(r.Credit)),
			BaseDebit: platform.NewDec4(r.BaseDebit), BaseCredit: platform.NewDec4(r.BaseCredit),
			BaseNet: platform.NewDec4(r.BaseDebit.Sub(r.BaseCredit)),
		})
		totalDebit = totalDebit.Add(r.BaseDebit)
		totalCredit = totalCredit.Add(r.BaseCredit)
	}
	out.TotalDebitBase, out.TotalCreditBase = platform.NewDec4(totalDebit), platform.NewDec4(totalCredit)
	diff := totalDebit.Sub(totalCredit)
	out.Balanced, out.Difference = diff.IsZero(), platform.NewDec4(diff)
	return out
}

// groupOrder is the fixed order of the balance sheet's groups.
var groupOrder = []string{"ASSET", "LIABILITY", "EQUITY", "INCOME", "EXPENSE", "COGS"}

// debitNatural are the types whose balance is debit minus credit; every other
// type, an unknown one too, is credit minus debit.
func debitNatural(t string) bool { return t == "ASSET" || t == "EXPENSE" || t == "COGS" }

// balanceSheet builds the balance sheet from the query's rows (ordered by
// code). An account is left out only when its balance and both base columns
// are zero, so offsetting activity stays as a zero line. A type outside the
// six is summed but never shown.
func balanceSheet(asOf time.Time, base string, rows []reportsdb.BalanceSheetRowsRow) BalanceSheetReport {
	lines := map[string][]BalanceSheetLine{}
	totals := map[string]decimal.Decimal{}
	for _, r := range rows {
		balance := r.BaseCredit.Sub(r.BaseDebit)
		if debitNatural(r.Type) {
			balance = r.BaseDebit.Sub(r.BaseCredit)
		}
		if balance.IsZero() && r.BaseDebit.IsZero() && r.BaseCredit.IsZero() {
			continue
		}
		lines[r.Type] = append(lines[r.Type], BalanceSheetLine{
			AccountID: r.AccountID, Code: r.Code, Name: r.Name, BaseBalance: platform.NewDec4(balance),
		})
		totals[r.Type] = totals[r.Type].Add(balance)
	}
	groups := []BalanceSheetGroup{}
	for _, t := range groupOrder {
		if ls, ok := lines[t]; ok {
			groups = append(groups, BalanceSheetGroup{Type: t, TotalBase: platform.NewDec4(totals[t]), Lines: ls})
		}
	}
	return BalanceSheetReport{
		AsOf: platform.Date{Time: asOf}, BaseCurrency: base, Groups: groups,
		AssetsTotalBase:      platform.NewDec4(totals["ASSET"]),
		LiabilitiesTotalBase: platform.NewDec4(totals["LIABILITY"]),
		EquityTotalBase:      platform.NewDec4(totals["EQUITY"]),
		IncomeTotalBase:      platform.NewDec4(totals["INCOME"]),
		ExpenseTotalBase:     platform.NewDec4(totals["EXPENSE"].Add(totals["COGS"])),
	}
}
