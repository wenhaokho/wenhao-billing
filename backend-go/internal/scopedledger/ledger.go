// Package scopedledger provides development-only scoped query access.
package scopedledger

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func LedgerAccount(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LedgerAccountParams) (int32, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LedgerAccount(ctx, arg)
}
func LedgerCreateEntry(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LedgerCreateEntryParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LedgerCreateEntry(ctx, arg)
}
func LedgerCreateLine(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LedgerCreateLineParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LedgerCreateLine(ctx, arg)
}
func LedgerLines(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LedgerLinesParams) ([]dbqueryv8.JournalLine, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LedgerLines(ctx, arg)
}
func LedgerLinkReversal(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LedgerLinkReversalParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LedgerLinkReversal(ctx, arg)
}
func LedgerLockEntry(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LedgerLockEntryParams) (dbqueryv8.JournalEntry, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.JournalEntry{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LedgerLockEntry(ctx, arg)
}
