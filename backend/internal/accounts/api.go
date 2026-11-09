// Package accounts serves the chart of accounts at
// /api/v1/accounting/chart-of-accounts with the Python backend's contract:
// list (optionally by active), create, read, partial update, and deactivate.
// Accounts are never deleted.
package accounts

import (
	"github.com/wenhaokho/wenhao-billing/backend/internal/accounts/accountsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// AccountTypes are the account types, Python's AccountType. Every type field's
// enum tag lists the same values (TestAccountTypesAreTheEnum).
var AccountTypes = []string{"ASSET", "LIABILITY", "EQUITY", "INCOME", "EXPENSE", "COGS"}

// AccountOut is an account, as every route answers.
type AccountOut struct {
	AccountID int32  `json:"account_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Type      string `json:"type" enum:"ASSET,LIABILITY,EQUITY,INCOME,EXPENSE,COGS"`
	Active    bool   `json:"active"`
}

func accountOut(a accountsdb.ChartOfAccount) AccountOut {
	return AccountOut{AccountID: a.AccountID, Code: a.Code, Name: a.Name, Type: a.Type, Active: a.Active}
}

// AccountCreate adds an account. active may be left out (true) but not sent
// as null, as Python's `active: bool = True`.
type AccountCreate struct {
	Code   string                 `json:"code" minLength:"1" maxLength:"10"`
	Name   string                 `json:"name" minLength:"1" maxLength:"100"`
	Type   string                 `json:"type" enum:"ASSET,LIABILITY,EQUITY,INCOME,EXPENSE,COGS"`
	Active platform.NotNull[bool] `json:"active" doc:"Defaults to true."`
}

// AccountUpdate changes the fields sent and leaves the rest. Every column is
// NOT NULL, so a null is a 422 (Python answered 409 there).
type AccountUpdate struct {
	Code   platform.NotNull[string] `json:"code" minLength:"1" maxLength:"10"`
	Name   platform.NotNull[string] `json:"name" minLength:"1" maxLength:"100"`
	Type   platform.NotNull[string] `json:"type" enum:"ASSET,LIABILITY,EQUITY,INCOME,EXPENSE,COGS"`
	Active platform.NotNull[bool]   `json:"active"`
}
