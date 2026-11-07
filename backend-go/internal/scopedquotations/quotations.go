// Package scopedquotations provides development-only scoped query access.
package scopedquotations

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func DeleteQuotationLines(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.DeleteQuotationLinesParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeleteQuotationLines(ctx, arg)
}
func GetQuotation(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.GetQuotationParams) (dbqueryv8.Quotation, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Quotation{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetQuotation(ctx, arg)
}
func InsertQuotation(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertQuotationParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertQuotation(ctx, arg)
}
func InsertQuotationLine(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertQuotationLineParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertQuotationLine(ctx, arg)
}
func ListQuotations(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.ListQuotationsParams) ([]dbqueryv8.Quotation, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ListQuotations(ctx, arg)
}
func LockQuotation(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockQuotationParams) (dbqueryv8.Quotation, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Quotation{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockQuotation(ctx, arg)
}
func QuotationLines(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.QuotationLinesParams) ([]dbqueryv8.QuotationLineItem, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).QuotationLines(ctx, arg)
}
func QuotationNumbersForPrefix(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.QuotationNumbersForPrefixParams) ([]pgtype.Text, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).QuotationNumbersForPrefix(ctx, arg)
}
func UpdateQuotation(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.UpdateQuotationParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).UpdateQuotation(ctx, arg)
}
func LockQuotationNumberAllocation(ctx context.Context, db pgx.Tx, scope business.ID) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	return dbqueryv8.New(db).LockQuotationNumberAllocation(ctx, scope.UUID())
}

func DeleteQuotation(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.DeleteQuotationParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeleteQuotation(ctx, arg)
}

func MarkQuotationSent(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.MarkQuotationSentParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).MarkQuotationSent(ctx, arg)
}

func AcceptQuotation(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.AcceptQuotationParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).AcceptQuotation(ctx, arg)
}

func DeclineQuotation(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.DeclineQuotationParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeclineQuotation(ctx, arg)
}

func VoidQuotation(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.VoidQuotationParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).VoidQuotation(ctx, arg)
}

func RecordQuotationSend(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.RecordQuotationSendParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	n, err := dbqueryv8.New(db).RecordQuotationSend(ctx, arg)
	if err == nil && n != 1 {
		return pgx.ErrNoRows
	}
	return err
}

func QuotationSendDetails(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.QuotationSendDetailsParams) ([]dbqueryv8.QuotationSendDetailsRow, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).QuotationSendDetails(ctx, arg)
}
