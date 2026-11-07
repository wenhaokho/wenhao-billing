// Package scopedvendor provides development-only v8 vendor access.
package scopedvendor

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

type Fields struct {
	Name                      string
	ContactEmail, ContactName pgtype.Text
	DefaultCurrency           string
	PaymentTermsDays          int32
	TaxID, Notes              pgtype.Text
	Active                    bool
}

func Create(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, v Fields) (dbqueryv8.Vendor, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Vendor{}, err
	}
	return dbqueryv8.New(db).CreateVendor(ctx, dbqueryv8.CreateVendorParams{BusinessID: scope.UUID(), Name: v.Name, ContactEmail: v.ContactEmail, ContactName: v.ContactName, DefaultCurrency: v.DefaultCurrency, PaymentTermsDays: v.PaymentTermsDays, TaxID: v.TaxID, Notes: v.Notes, Active: v.Active})
}
func List(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, active pgtype.Bool) ([]dbqueryv8.Vendor, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).ListVendors(ctx, dbqueryv8.ListVendorsParams{BusinessID: scope.UUID(), Active: active})
}
func Get(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id pgtype.UUID) (dbqueryv8.Vendor, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Vendor{}, err
	}
	return dbqueryv8.New(db).GetVendor(ctx, dbqueryv8.GetVendorParams{BusinessID: scope.UUID(), VendorID: id})
}
func Lock(ctx context.Context, db pgx.Tx, scope business.ID, id pgtype.UUID) (dbqueryv8.Vendor, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Vendor{}, err
	}
	return dbqueryv8.New(db).LockVendor(ctx, dbqueryv8.LockVendorParams{BusinessID: scope.UUID(), VendorID: id})
}
func Deactivate(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id pgtype.UUID) (dbqueryv8.Vendor, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Vendor{}, err
	}
	return dbqueryv8.New(db).DeactivateVendor(ctx, dbqueryv8.DeactivateVendorParams{BusinessID: scope.UUID(), VendorID: id})
}
func Update(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id pgtype.UUID, data []byte) (dbqueryv8.Vendor, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Vendor{}, err
	}
	return dbqueryv8.New(db).UpdateVendor(ctx, dbqueryv8.UpdateVendorParams{BusinessID: scope.UUID(), VendorID: id, Data: data})
}
