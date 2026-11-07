// Package scopedprofile provides the development-only v8 profile adapter.
package scopedprofile

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

type Account struct {
	Currency     string
	Instructions string
	IsDefault    bool
}

// ReplaceAccounts replaces one business's accounts within the caller's transaction.
// The caller must roll back the transaction on error.
func ReplaceAccounts(ctx context.Context, tx pgx.Tx, scope business.ID, accounts []Account) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	q := dbqueryv8.New(tx)
	if err := q.EnsureBusinessProfile(ctx, scope.UUID()); err != nil {
		return err
	}
	if _, err := q.LockBusinessProfile(ctx, scope.UUID()); err != nil {
		return err
	}
	if err := q.DeleteBusinessPaymentAccounts(ctx, scope.UUID()); err != nil {
		return err
	}
	for _, account := range accounts {
		if err := q.InsertBusinessPaymentAccount(ctx, dbqueryv8.InsertBusinessPaymentAccountParams{BusinessID: scope.UUID(), Currency: account.Currency, Instructions: account.Instructions, IsDefault: account.IsDefault}); err != nil {
			return err
		}
	}
	return nil
}

// Get loads only the configured business's profile.
func Get(ctx context.Context, db dbqueryv8.DBTX, scope business.ID) (dbqueryv8.BusinessProfile, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.BusinessProfile{}, err
	}
	return dbqueryv8.New(db).GetBusinessProfile(ctx, scope.UUID())
}

// ListAccounts lists only the configured business's payment accounts.
func ListAccounts(ctx context.Context, db dbqueryv8.DBTX, scope business.ID) ([]dbqueryv8.PaymentAccount, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).ListBusinessPaymentAccounts(ctx, scope.UUID())
}

// Update applies only the named profile fields inside the caller's transaction.
func Update(ctx context.Context, tx pgx.Tx, scope business.ID, data []byte) (dbqueryv8.BusinessProfile, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.BusinessProfile{}, err
	}
	q := dbqueryv8.New(tx)
	if err := q.EnsureBusinessProfile(ctx, scope.UUID()); err != nil {
		return dbqueryv8.BusinessProfile{}, err
	}
	return q.ApplyBusinessProfileUpdate(ctx, dbqueryv8.ApplyBusinessProfileUpdateParams{BusinessID: scope.UUID(), Data: data})
}
