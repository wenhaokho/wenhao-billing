// Package reports serves the accounting and dashboard reports: read-only
// views over the ledger and the invoice tables.
package reports

import "github.com/wenhaokho/wenhao-billing/backend/internal/platform"

// TrialBalanceRow is one account in one currency (Python's TrialBalanceRow).
// net and base_net are debit minus credit whatever the account's type.
type TrialBalanceRow struct {
	AccountID  int32         `json:"account_id"`
	Code       string        `json:"code"`
	Name       string        `json:"name"`
	Type       string        `json:"type"`
	Currency   string        `json:"currency"`
	Debit      platform.Dec4 `json:"debit"`
	Credit     platform.Dec4 `json:"credit"`
	Net        platform.Dec4 `json:"net"`
	BaseDebit  platform.Dec4 `json:"base_debit"`
	BaseCredit platform.Dec4 `json:"base_credit"`
	BaseNet    platform.Dec4 `json:"base_net"`
}

// TrialBalanceReport is the trial balance as of a date.
type TrialBalanceReport struct {
	AsOf            platform.Date     `json:"as_of"`
	BaseCurrency    string            `json:"base_currency"`
	Rows            []TrialBalanceRow `json:"rows"`
	TotalDebitBase  platform.Dec4     `json:"total_debit_base"`
	TotalCreditBase platform.Dec4     `json:"total_credit_base"`
}

// BalanceSheetLine is one account's balance in the base currency.
type BalanceSheetLine struct {
	AccountID   int32         `json:"account_id"`
	Code        string        `json:"code"`
	Name        string        `json:"name"`
	BaseBalance platform.Dec4 `json:"base_balance"`
}

// BalanceSheetGroup is one account type's lines and total.
type BalanceSheetGroup struct {
	Type      string             `json:"type"`
	TotalBase platform.Dec4      `json:"total_base"`
	Lines     []BalanceSheetLine `json:"lines"`
}

// BalanceSheetReport is the balance sheet as of a date. It lists the P&L
// groups too and has no result line, as Python's does.
type BalanceSheetReport struct {
	AsOf                 platform.Date       `json:"as_of"`
	BaseCurrency         string              `json:"base_currency"`
	Groups               []BalanceSheetGroup `json:"groups"`
	AssetsTotalBase      platform.Dec4       `json:"assets_total_base"`
	LiabilitiesTotalBase platform.Dec4       `json:"liabilities_total_base"`
	EquityTotalBase      platform.Dec4       `json:"equity_total_base"`
	IncomeTotalBase      platform.Dec4       `json:"income_total_base"`
	ExpenseTotalBase     platform.Dec4       `json:"expense_total_base"`
}
