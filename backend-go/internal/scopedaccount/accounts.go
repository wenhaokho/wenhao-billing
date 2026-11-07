// Package scopedaccount provides development-only v8 chart-of-accounts access.
package scopedaccount

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

type Fields struct {
	Code, Name, Type string
	Active           bool
}

func List(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, active pgtype.Bool) ([]dbqueryv8.ChartOfAccount, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).ListAccounts(ctx, dbqueryv8.ListAccountsParams{BusinessID: scope.UUID(), Active: active})
}
func Get(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id int32) (dbqueryv8.ChartOfAccount, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.ChartOfAccount{}, err
	}
	return dbqueryv8.New(db).GetAccount(ctx, dbqueryv8.GetAccountParams{BusinessID: scope.UUID(), AccountID: id})
}
func Lock(ctx context.Context, db pgx.Tx, scope business.ID, id int32) (dbqueryv8.ChartOfAccount, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.ChartOfAccount{}, err
	}
	return dbqueryv8.New(db).LockAccount(ctx, dbqueryv8.LockAccountParams{BusinessID: scope.UUID(), AccountID: id})
}
func Create(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, fields Fields) (dbqueryv8.ChartOfAccount, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.ChartOfAccount{}, err
	}
	return dbqueryv8.New(db).CreateAccount(ctx, dbqueryv8.CreateAccountParams{BusinessID: scope.UUID(), Code: fields.Code, Name: fields.Name, Type: fields.Type, Active: fields.Active})
}
func Update(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id int32, fields Fields) (dbqueryv8.ChartOfAccount, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.ChartOfAccount{}, err
	}
	return dbqueryv8.New(db).UpdateAccount(ctx, dbqueryv8.UpdateAccountParams{BusinessID: scope.UUID(), AccountID: id, Code: fields.Code, Name: fields.Name, Type: fields.Type, Active: fields.Active})
}
