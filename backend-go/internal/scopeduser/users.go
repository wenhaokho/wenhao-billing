// Package scopeduser provides development-only v8 user access.
package scopeduser

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func ClearResetToken(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ClearResetTokenParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ClearResetToken(ctx, arg)
}
func CreateUser(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CreateUserParams) (dbqueryv8.CreateUserRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.CreateUserRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CreateUser(ctx, arg)
}
func FindUserByEmail(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.FindUserByEmailParams) (dbqueryv8.FindUserByEmailRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.FindUserByEmailRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).FindUserByEmail(ctx, arg)
}
func GetSessionUserByEmail(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.GetSessionUserByEmailParams) (dbqueryv8.GetSessionUserByEmailRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.GetSessionUserByEmailRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetSessionUserByEmail(ctx, arg)
}
func GetSessionUserByID(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.GetSessionUserByIDParams) (dbqueryv8.GetSessionUserByIDRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.GetSessionUserByIDRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetSessionUserByID(ctx, arg)
}
func LockSessionUserByID(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockSessionUserByIDParams) (dbqueryv8.LockSessionUserByIDRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.LockSessionUserByIDRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockSessionUserByID(ctx, arg)
}
func LockUserByResetToken(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockUserByResetTokenParams) (dbqueryv8.LockUserByResetTokenRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.LockUserByResetTokenRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockUserByResetToken(ctx, arg)
}
func SetResetToken(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.SetResetTokenParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).SetResetToken(ctx, arg)
}
func SetUserPassword(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.SetUserPasswordParams) (pgtype.UUID, error) {
	if err := scope.Validate(); err != nil {
		return pgtype.UUID{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).SetUserPassword(ctx, arg)
}
func UpdateUserProfile(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.UpdateUserProfileParams) (dbqueryv8.UpdateUserProfileRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.UpdateUserProfileRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).UpdateUserProfile(ctx, arg)
}
func ListUsers(ctx context.Context, db dbqueryv8.DBTX, scope business.ID) ([]dbqueryv8.ListUsersRow, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).ListUsers(ctx, scope.UUID())
}
