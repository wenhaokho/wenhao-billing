// Package scopedfx provides development-only v8 FX access.
package scopedfx

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

type Rate struct {
	From, To string
	Value    pgtype.Numeric
	Date     pgtype.Date
	Source   string
}

func Create(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, r Rate) (dbqueryv8.FxRate, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.FxRate{}, err
	}
	return dbqueryv8.New(db).CreateFXRate(ctx, dbqueryv8.CreateFXRateParams{BusinessID: scope.UUID(), FromCurrency: r.From, ToCurrency: r.To, Rate: r.Value, AsOfDate: r.Date, Source: r.Source})
}
func Get(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, from, to string, date pgtype.Date) (pgtype.Numeric, error) {
	if err := scope.Validate(); err != nil {
		return pgtype.Numeric{}, err
	}
	return dbqueryv8.New(db).GetFXRate(ctx, dbqueryv8.GetFXRateParams{BusinessID: scope.UUID(), FromCurrency: from, ToCurrency: to, AsOfDate: date})
}
func Latest(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, from, to string, date pgtype.Date) (dbqueryv8.FxRate, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.FxRate{}, err
	}
	return dbqueryv8.New(db).LatestFXRate(ctx, dbqueryv8.LatestFXRateParams{BusinessID: scope.UUID(), FromCurrency: from, ToCurrency: to, OnDate: date})
}
func List(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, from, to pgtype.Text) ([]dbqueryv8.FxRate, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).ListFXRates(ctx, dbqueryv8.ListFXRatesParams{BusinessID: scope.UUID(), FromCurrency: from, ToCurrency: to})
}
func Delete(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id int64) (int64, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	return dbqueryv8.New(db).DeleteFXRate(ctx, dbqueryv8.DeleteFXRateParams{BusinessID: scope.UUID(), RateID: id})
}
func LockSync(ctx context.Context, db pgx.Tx, scope business.ID) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	return dbqueryv8.New(db).LockFXSync(ctx, scope.UUID())
}
func FindSync(ctx context.Context, db pgx.Tx, scope business.ID, from, to string, date pgtype.Date) (dbqueryv8.FxRate, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.FxRate{}, err
	}
	return dbqueryv8.New(db).FindFXSyncRate(ctx, dbqueryv8.FindFXSyncRateParams{BusinessID: scope.UUID(), FromCurrency: from, ToCurrency: to, AsOfDate: date})
}
func UpdateSync(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id int64, value pgtype.Numeric) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	return dbqueryv8.New(db).UpdateFXSyncRate(ctx, dbqueryv8.UpdateFXSyncRateParams{BusinessID: scope.UUID(), RateID: id, Rate: value})
}
