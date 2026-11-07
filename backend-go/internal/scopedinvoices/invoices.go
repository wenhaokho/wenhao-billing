// Package scopedinvoices provides development-only scoped invoice queries.
package scopedinvoices

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func ApplyInvoicePayment(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.ApplyInvoicePaymentParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ApplyInvoicePayment(ctx, arg)
}
func CustomerWithholdingDefaults(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.CustomerWithholdingDefaultsParams) (dbqueryv8.CustomerWithholdingDefaultsRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.CustomerWithholdingDefaultsRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CustomerWithholdingDefaults(ctx, arg)
}
func DeleteInvoice(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.DeleteInvoiceParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeleteInvoice(ctx, arg)
}
func DeleteInvoiceLines(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.DeleteInvoiceLinesParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeleteInvoiceLines(ctx, arg)
}
func FindRecurringCycle(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.FindRecurringCycleParams) (dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Invoice{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).FindRecurringCycle(ctx, arg)
}
func GetInvoice(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.GetInvoiceParams) (dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Invoice{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetInvoice(ctx, arg)
}
func InsertDraftInvoice(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertDraftInvoiceParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertDraftInvoice(ctx, arg)
}
func InsertDraftInvoiceLine(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertDraftInvoiceLineParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertDraftInvoiceLine(ctx, arg)
}
func InsertInvoicePayment(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertInvoicePaymentParams) (dbqueryv8.Payment, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Payment{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertInvoicePayment(ctx, arg)
}
func InsertInvoicePaymentLog(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertInvoicePaymentLogParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertInvoicePaymentLog(ctx, arg)
}
func InsertMilestoneInvoice(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InsertMilestoneInvoiceParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InsertMilestoneInvoice(ctx, arg)
}
func InvoiceLinesByIDs(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InvoiceLinesByIDsParams) ([]dbqueryv8.InvoiceLineItem, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InvoiceLinesByIDs(ctx, arg)
}
func InvoiceNumbersForPrefix(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.InvoiceNumbersForPrefixParams) ([]pgtype.Text, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).InvoiceNumbersForPrefix(ctx, arg)
}
func ItemIsHosting(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.ItemIsHostingParams) (bool, error) {
	if err := scope.Validate(); err != nil {
		return false, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ItemIsHosting(ctx, arg)
}
func LatestRecurringChild(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LatestRecurringChildParams) (dbqueryv8.LatestRecurringChildRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.LatestRecurringChildRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LatestRecurringChild(ctx, arg)
}
func ListInvoices(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.ListInvoicesParams) ([]dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ListInvoices(ctx, arg)
}
func ListRecurringCycleCandidates(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.ListRecurringCycleCandidatesParams) ([]dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ListRecurringCycleCandidates(ctx, arg)
}
func LockInvoice(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockInvoiceParams) (dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Invoice{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockInvoice(ctx, arg)
}
func LockInvoiceLines(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockInvoiceLinesParams) ([]dbqueryv8.InvoiceLineItem, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockInvoiceLines(ctx, arg)
}
func LockMilestoneCustomer(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockMilestoneCustomerParams) (dbqueryv8.LockMilestoneCustomerRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.LockMilestoneCustomerRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockMilestoneCustomer(ctx, arg)
}
func LockMilestoneProject(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockMilestoneProjectParams) (pgtype.UUID, error) {
	if err := scope.Validate(); err != nil {
		return pgtype.UUID{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockMilestoneProject(ctx, arg)
}
func LockUsageInvoice(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockUsageInvoiceParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockUsageInvoice(ctx, arg)
}
func MarkInvoiceTemplate(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.MarkInvoiceTemplateParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).MarkInvoiceTemplate(ctx, arg)
}
func SetRecurringChildFields(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.SetRecurringChildFieldsParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).SetRecurringChildFields(ctx, arg)
}
func TransitionInvoice(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.TransitionInvoiceParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).TransitionInvoice(ctx, arg)
}
func UpdateDraftInvoice(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.UpdateDraftInvoiceParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).UpdateDraftInvoice(ctx, arg)
}
func UpdateTemplateSchedule(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.UpdateTemplateScheduleParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).UpdateTemplateSchedule(ctx, arg)
}
func AwaitingFinalization(ctx context.Context, db pgx.Tx, scope business.ID) ([]dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).AwaitingFinalization(ctx, scope.UUID())
}
func ListRecurringTemplates(ctx context.Context, db pgx.Tx, scope business.ID) ([]dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).ListRecurringTemplates(ctx, scope.UUID())
}
func LockInvoiceNumberAllocation(ctx context.Context, db pgx.Tx, scope business.ID) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	return dbqueryv8.New(db).LockInvoiceNumberAllocation(ctx, scope.UUID())
}
func RecurringTemplateRows(ctx context.Context, db pgx.Tx, scope business.ID) ([]dbqueryv8.RecurringTemplateRowsRow, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	return dbqueryv8.New(db).RecurringTemplateRows(ctx, scope.UUID())
}

func RecordInvoiceSend(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.RecordInvoiceSendParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).RecordInvoiceSend(ctx, arg)
}
