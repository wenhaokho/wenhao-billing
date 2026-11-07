// Package scopeditem provides development-only v8 item access.
package scopeditem

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func DeactivateItem(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.DeactivateItemParams) (dbqueryv8.Item, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Item{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeactivateItem(ctx, arg)
}
func GetItem(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.GetItemParams) (dbqueryv8.Item, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Item{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetItem(ctx, arg)
}
func GetItemTarget(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.GetItemTargetParams) (dbqueryv8.CloudflareTarget, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.CloudflareTarget{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetItemTarget(ctx, arg)
}
func InsertItem(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.InsertItemParams) (dbqueryv8.Item, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Item{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertItem(ctx, arg)
}
func ListItems(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ListItemsParams) ([]dbqueryv8.Item, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ListItems(ctx, arg)
}
func LockItem(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockItemParams) (dbqueryv8.Item, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Item{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockItem(ctx, arg)
}
func UpdateItem(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.UpdateItemParams) (dbqueryv8.Item, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Item{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).UpdateItem(ctx, arg)
}
func UpsertItemTarget(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.UpsertItemTargetParams) (dbqueryv8.CloudflareTarget, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.CloudflareTarget{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).UpsertItemTarget(ctx, arg)
}
