// Package scopedbills provides development-only scoped query access.
package scopedbills

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func BillLines(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.BillLinesParams) ([]dbqueryv8.BillLineItem, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).BillLines(ctx, arg)
}
func DeleteBillLines(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.DeleteBillLinesParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeleteBillLines(ctx, arg)
}
func GetBill(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.GetBillParams) (dbqueryv8.Bill, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Bill{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetBill(ctx, arg)
}
func InsertBill(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertBillParams) (dbqueryv8.Bill, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Bill{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertBill(ctx, arg)
}
func InsertBillLine(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertBillLineParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertBillLine(ctx, arg)
}
func ListBills(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.ListBillsParams) ([]dbqueryv8.Bill, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ListBills(ctx, arg)
}
func LockBill(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockBillParams) (dbqueryv8.Bill, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Bill{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockBill(ctx, arg)
}
func UpdateBill(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.UpdateBillParams) (dbqueryv8.Bill, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Bill{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).UpdateBill(ctx, arg)
}
