package platform

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeditem"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
)

func (h scopedProfileRouter) invoiceWriter() invoiceWriter {
	return invoiceWriter{
		pool: candidateTransactions{db: h.pool, scope: h.scope},
		admin: func(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
			u, ok := h.admin(w, r)
			return u.UserID, ok
		},
		queries: func(tx pgx.Tx) invoiceWriteQueries {
			return scopedInvoiceWriteQueries{tx: tx, scope: h.scope}
		},
		scoped: true,
	}
}

type scopedInvoiceWriteQueries struct {
	tx    pgx.Tx
	scope business.ID
}

func (q scopedInvoiceWriteQueries) lockAdminRole(ctx context.Context, id pgtype.UUID) (string, error) {
	user, err := scopeduser.LockSessionUserByID(ctx, q.tx, q.scope, dbqueryv8.LockSessionUserByIDParams{UserID: id})
	return user.Role, err
}

// SHARE keeps WHT defaults stable and permits a concurrent project update
// to check its customer foreign key.
func (q scopedInvoiceWriteQueries) lockCustomer(ctx context.Context, id pgtype.UUID) error {
	_, err := q.LockMilestoneCustomer(ctx, id)
	return err
}

func (q scopedInvoiceWriteQueries) lockProject(ctx context.Context, id pgtype.UUID) error {
	var locked pgtype.UUID
	return q.tx.QueryRow(ctx, `SELECT project_id FROM projects WHERE business_id=$1 AND project_id=$2 FOR KEY SHARE`, q.scope.UUID(), id).Scan(&locked)
}

// Draft invoice lines reference items. The item's account references are
// enforced by the scoped item-to-account foreign keys.
func (q scopedInvoiceWriteQueries) lockItem(ctx context.Context, id pgtype.UUID, template bool) error {
	if template {
		_, err := scopeditem.LockItem(ctx, q.tx, q.scope, dbqueryv8.LockItemParams{ItemID: id})
		return err
	}
	var locked pgtype.UUID
	return q.tx.QueryRow(ctx, `SELECT item_id FROM items WHERE business_id=$1 AND item_id=$2 FOR KEY SHARE`, q.scope.UUID(), id).Scan(&locked)
}
func (q scopedInvoiceWriteQueries) LockInvoiceNumberAllocation(ctx context.Context) error {
	return scopedinvoices.LockInvoiceNumberAllocation(ctx, q.tx, q.scope)
}
func (q scopedInvoiceWriteQueries) InvoiceNumbersForPrefix(ctx context.Context, prefix pgtype.Text) ([]pgtype.Text, error) {
	return scopedinvoices.InvoiceNumbersForPrefix(ctx, q.tx, q.scope, dbqueryv8.InvoiceNumbersForPrefixParams{InvoiceNumber: prefix})
}
func (q scopedInvoiceWriteQueries) InvoiceLinesByIDs(ctx context.Context, ids []pgtype.UUID) ([]dbquery.InvoiceLineItem, error) {
	rows, err := scopedinvoices.InvoiceLinesByIDs(ctx, q.tx, q.scope, dbqueryv8.InvoiceLinesByIDsParams{InvoiceIds: ids})
	return legacyInvoiceLines(rows), err
}
func legacyInvoiceLines(rows []dbqueryv8.InvoiceLineItem) []dbquery.InvoiceLineItem {
	lines := make([]dbquery.InvoiceLineItem, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, legacyInvoiceLine(row))
	}
	return lines
}

func (q scopedInvoiceWriteQueries) CustomerWithholdingDefaults(ctx context.Context, id pgtype.UUID) (dbquery.CustomerWithholdingDefaultsRow, error) {
	row, err := scopedinvoices.CustomerWithholdingDefaults(ctx, q.tx, q.scope, dbqueryv8.CustomerWithholdingDefaultsParams{CustomerID: id})
	return dbquery.CustomerWithholdingDefaultsRow(row), err
}

func (q scopedInvoiceWriteQueries) LockInvoice(ctx context.Context, id pgtype.UUID) (dbquery.Invoice, error) {
	row, err := scopedinvoices.LockInvoice(ctx, q.tx, q.scope, dbqueryv8.LockInvoiceParams{InvoiceID: id})
	return legacyInvoice(row), err
}

func (q scopedInvoiceWriteQueries) GetInvoice(ctx context.Context, id pgtype.UUID) (dbquery.Invoice, error) {
	row, err := scopedinvoices.GetInvoice(ctx, q.tx, q.scope, dbqueryv8.GetInvoiceParams{InvoiceID: id})
	return legacyInvoice(row), err
}

func (q scopedInvoiceWriteQueries) LockInvoiceLines(ctx context.Context, id pgtype.UUID) ([]dbquery.InvoiceLineItem, error) {
	row, err := scopedinvoices.LockInvoiceLines(ctx, q.tx, q.scope, dbqueryv8.LockInvoiceLinesParams{InvoiceID: id})
	return legacyInvoiceLines(row), err
}

func (q scopedInvoiceWriteQueries) DeleteInvoiceLines(ctx context.Context, id pgtype.UUID) error {
	return scopedinvoices.DeleteInvoiceLines(ctx, q.tx, q.scope, dbqueryv8.DeleteInvoiceLinesParams{InvoiceID: id})
}

func (q scopedInvoiceWriteQueries) LatestRecurringChild(ctx context.Context, id pgtype.UUID) (dbquery.LatestRecurringChildRow, error) {
	row, err := scopedinvoices.LatestRecurringChild(ctx, q.tx, q.scope, dbqueryv8.LatestRecurringChildParams{TemplateID: id})
	return dbquery.LatestRecurringChildRow(row), err
}

func (q scopedInvoiceWriteQueries) InsertDraftInvoice(ctx context.Context, p dbquery.InsertDraftInvoiceParams) error {
	return scopedinvoices.InsertDraftInvoice(ctx, q.tx, q.scope, dbqueryv8.InsertDraftInvoiceParams{
		InvoiceID:     p.InvoiceID,
		CustomerID:    p.CustomerID,
		ProjectID:     p.ProjectID,
		InvoiceType:   p.InvoiceType,
		InvoiceNumber: p.InvoiceNumber,
		PoSoNumber:    p.PoSoNumber,
		Currency:      p.Currency,
		Subtotal:      p.Subtotal,
		DiscountType:  p.DiscountType,
		DiscountValue: p.DiscountValue,
		WhtType:       p.WhtType,
		WhtRate:       p.WhtRate,
		WhtGrossUp:    p.WhtGrossUp,
		WhtBase:       p.WhtBase,
		WhtAmount:     p.WhtAmount,
		Amount:        p.Amount,
		IssueDate:     p.IssueDate,
		DueDate:       p.DueDate,
		PaymentTerms:  p.PaymentTerms,
		Notes:         p.Notes,
		Footer:        p.Footer,
		InternalNote:  p.InternalNote,
	})
}

func (q scopedInvoiceWriteQueries) InsertDraftInvoiceLine(ctx context.Context, p dbquery.InsertDraftInvoiceLineParams) error {
	return scopedinvoices.InsertDraftInvoiceLine(ctx, q.tx, q.scope, dbqueryv8.InsertDraftInvoiceLineParams{
		LineItemID:  p.LineItemID,
		InvoiceID:   p.InvoiceID,
		ItemID:      p.ItemID,
		Position:    p.Position,
		Description: p.Description,
		Quantity:    p.Quantity,
		UnitPrice:   p.UnitPrice,
		Amount:      p.Amount,
	})
}

func (q scopedInvoiceWriteQueries) MarkInvoiceTemplate(ctx context.Context, p dbquery.MarkInvoiceTemplateParams) error {
	return scopedinvoices.MarkInvoiceTemplate(ctx, q.tx, q.scope, dbqueryv8.MarkInvoiceTemplateParams{
		InvoiceID:       p.InvoiceID,
		BillingCycleRef: p.BillingCycleRef,
	})
}

func (q scopedInvoiceWriteQueries) UpdateDraftInvoice(ctx context.Context, p dbquery.UpdateDraftInvoiceParams) error {
	return scopedinvoices.UpdateDraftInvoice(ctx, q.tx, q.scope, dbqueryv8.UpdateDraftInvoiceParams{
		InvoiceID:     p.InvoiceID,
		CustomerID:    p.CustomerID,
		ProjectID:     p.ProjectID,
		InvoiceType:   p.InvoiceType,
		Currency:      p.Currency,
		InvoiceNumber: p.InvoiceNumber,
		PoSoNumber:    p.PoSoNumber,
		IssueDate:     p.IssueDate,
		DueDate:       p.DueDate,
		PaymentTerms:  p.PaymentTerms,
		Notes:         p.Notes,
		Footer:        p.Footer,
		InternalNote:  p.InternalNote,
		DiscountType:  p.DiscountType,
		DiscountValue: p.DiscountValue,
		WhtType:       p.WhtType,
		WhtRate:       p.WhtRate,
		WhtGrossUp:    p.WhtGrossUp,
		Subtotal:      p.Subtotal,
		WhtBase:       p.WhtBase,
		WhtAmount:     p.WhtAmount,
		Amount:        p.Amount,
	})
}

func (q scopedInvoiceWriteQueries) UpdateTemplateSchedule(ctx context.Context, p dbquery.UpdateTemplateScheduleParams) error {
	return scopedinvoices.UpdateTemplateSchedule(ctx, q.tx, q.scope, dbqueryv8.UpdateTemplateScheduleParams{
		InvoiceID:       p.InvoiceID,
		BillingCycleRef: p.BillingCycleRef,
	})
}

func (q scopedInvoiceWriteQueries) LockMilestoneCustomer(ctx context.Context, id pgtype.UUID) (dbquery.LockMilestoneCustomerRow, error) {
	row, err := scopedinvoices.LockMilestoneCustomer(ctx, q.tx, q.scope, dbqueryv8.LockMilestoneCustomerParams{CustomerID: id})
	return dbquery.LockMilestoneCustomerRow(row), err
}

func (q scopedInvoiceWriteQueries) LockMilestoneProject(ctx context.Context, id pgtype.UUID) (pgtype.UUID, error) {
	return scopedinvoices.LockMilestoneProject(ctx, q.tx, q.scope, dbqueryv8.LockMilestoneProjectParams{ProjectID: id})
}

func (q scopedInvoiceWriteQueries) InsertMilestoneInvoice(ctx context.Context, p dbquery.InsertMilestoneInvoiceParams) error {
	return scopedinvoices.InsertMilestoneInvoice(ctx, q.tx, q.scope, dbqueryv8.InsertMilestoneInvoiceParams{
		InvoiceID:       p.InvoiceID,
		CustomerID:      p.CustomerID,
		ProjectID:       p.ProjectID,
		Currency:        p.Currency,
		WhtType:         p.WhtType,
		WhtRate:         p.WhtRate,
		WhtGrossUp:      p.WhtGrossUp,
		WhtBase:         p.WhtBase,
		WhtAmount:       p.WhtAmount,
		Amount:          p.Amount,
		BillingCycleRef: p.BillingCycleRef,
		IssueDate:       p.IssueDate,
		DueDate:         p.DueDate,
	})
}

func (q scopedInvoiceWriteQueries) DeleteInvoice(ctx context.Context, id pgtype.UUID) error {
	return scopedinvoices.DeleteInvoice(ctx, q.tx, q.scope, dbqueryv8.DeleteInvoiceParams{InvoiceID: id})
}

func (q scopedInvoiceWriteQueries) TransitionInvoice(ctx context.Context, p dbquery.TransitionInvoiceParams) error {
	return scopedinvoices.TransitionInvoice(ctx, q.tx, q.scope, dbqueryv8.TransitionInvoiceParams{
		InvoiceID:  p.InvoiceID,
		Status:     p.Status,
		BalanceDue: p.BalanceDue,
	})
}

func (q scopedInvoiceWriteQueries) lockInvoiceHostingItems(ctx context.Context, id pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `
		SELECT i.item_id FROM items i
		WHERE i.business_id=$1 AND i.is_hosting AND EXISTS (
			SELECT 1 FROM invoice_line_items l
			WHERE l.business_id=i.business_id AND l.invoice_id=$2 AND l.item_id=i.item_id
		)
		ORDER BY i.item_id FOR UPDATE /* scoped invoice lifecycle item lock */`, q.scope.UUID(), id)
	return err
}
