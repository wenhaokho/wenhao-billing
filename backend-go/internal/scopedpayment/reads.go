// Package scopedpayment provides development-only v8 payment reads.
package scopedpayment

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

type AuditFilter struct {
	Action, Payer    pgtype.Text
	FromDate, ToDate pgtype.Timestamp
	RowLimit         int32
}

func Get(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id pgtype.UUID) (dbqueryv8.Payment, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Payment{}, err
	}
	return dbqueryv8.New(db).GetPayment(ctx, dbqueryv8.GetPaymentParams{BusinessID: scope.UUID(), PaymentID: id})
}
func InvoicePayments(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id pgtype.UUID) ([]dbqueryv8.Payment, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).ListInvoicePayments(ctx, dbqueryv8.ListInvoicePaymentsParams{BusinessID: scope.UUID(), InvoiceID: id})
}
func AwaitingReview(ctx context.Context, db dbqueryv8.DBTX, scope business.ID) ([]dbqueryv8.Payment, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).ListPaymentsAwaitingReview(ctx, scope.UUID())
}
func Logs(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, id pgtype.UUID) ([]dbqueryv8.PaymentLogsRow, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).PaymentLogs(ctx, dbqueryv8.PaymentLogsParams{BusinessID: scope.UUID(), PaymentID: id})
}
func Audit(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, f AuditFilter) ([]dbqueryv8.PaymentAuditRow, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).PaymentAudit(ctx, dbqueryv8.PaymentAuditParams{BusinessID: scope.UUID(), Action: f.Action, Payer: f.Payer, FromDate: f.FromDate, ToDate: f.ToDate, RowLimit: f.RowLimit})
}

func LockInvoiceReceiptPayment(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, invoiceID, paymentID pgtype.UUID) (dbqueryv8.Payment, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Payment{}, err
	}
	return dbqueryv8.New(db).LockInvoiceReceiptPayment(ctx, dbqueryv8.LockInvoiceReceiptPaymentParams{
		BusinessID: scope.UUID(), InvoiceID: invoiceID, PaymentID: paymentID,
	})
}
