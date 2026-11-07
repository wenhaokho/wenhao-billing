// Package scopedcustomer provides development-only v8 customer access.
package scopedcustomer

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func ApplyCustomerCreate(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ApplyCustomerCreateParams) (dbqueryv8.Customer, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Customer{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ApplyCustomerCreate(ctx, arg)
}
func ApplyCustomerUpdate(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ApplyCustomerUpdateParams) (dbqueryv8.Customer, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Customer{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ApplyCustomerUpdate(ctx, arg)
}
func CreateCustomer(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CreateCustomerParams) (dbqueryv8.Customer, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Customer{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CreateCustomer(ctx, arg)
}
func CustomerBalances(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CustomerBalancesParams) ([]dbqueryv8.CustomerBalancesRow, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CustomerBalances(ctx, arg)
}
func CustomerInvoiceLines(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CustomerInvoiceLinesParams) ([]dbqueryv8.InvoiceLineItem, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CustomerInvoiceLines(ctx, arg)
}
func CustomerInvoices(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CustomerInvoicesParams) ([]dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CustomerInvoices(ctx, arg)
}
func CustomerOpenInvoices(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CustomerOpenInvoicesParams) ([]dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CustomerOpenInvoices(ctx, arg)
}
func CustomerRecentPayments(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CustomerRecentPaymentsParams) ([]dbqueryv8.CustomerRecentPaymentsRow, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CustomerRecentPayments(ctx, arg)
}
func DeactivateCustomer(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.DeactivateCustomerParams) (dbqueryv8.Customer, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Customer{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeactivateCustomer(ctx, arg)
}
func GetCustomer(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.GetCustomerParams) (dbqueryv8.Customer, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Customer{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetCustomer(ctx, arg)
}
func ListCustomers(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ListCustomersParams) ([]dbqueryv8.Customer, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ListCustomers(ctx, arg)
}
func LockCustomer(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockCustomerParams) (dbqueryv8.Customer, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Customer{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockCustomer(ctx, arg)
}

func CustomerReportHasDamagedReferences(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.CustomerReportHasDamagedReferencesParams) (bool, error) {
	if err := scope.Validate(); err != nil {
		return false, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CustomerReportHasDamagedReferences(ctx, arg)
}
