package platform

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
)

func (h scopedProfileRouter) quotationConvert(w http.ResponseWriter, r *http.Request) {
	converter := quotationConverter{
		pool:   candidateTransactions{db: h.pool, scope: h.scope},
		admin:  h.quotationWriter().admin,
		scoped: true,
		queries: func(tx pgx.Tx) quotationConversionQueries {
			return scopedQuotationConversionQueries{scopedQuotationWriteQueries{tx: tx, scope: h.scope}}
		},
	}
	converter.convert(w, r)
}

type scopedQuotationConversionQueries struct {
	scopedQuotationWriteQueries
}

func (q scopedQuotationConversionQueries) lockConversionQuote(ctx context.Context, id pgtype.UUID) (dbquery.Quotation, error) {
	var locked pgtype.UUID
	err := q.tx.QueryRow(ctx, `SELECT quotation_id FROM quotations
 WHERE business_id=$1 AND quotation_id=$2 FOR NO KEY UPDATE`, q.scope.UUID(), id).Scan(&locked)
	if err != nil {
		return dbquery.Quotation{}, err
	}
	quote, err := q.GetQuotation(ctx, locked)
	if err != nil {
		return dbquery.Quotation{}, err
	}
	if quote.ConvertedInvoiceID.Valid {
		if _, err := q.GetInvoice(ctx, quote.ConvertedInvoiceID); err != nil {
			return dbquery.Quotation{}, err
		}
	}
	return quote, nil
}
func (q scopedQuotationConversionQueries) LockMilestoneCustomer(ctx context.Context, id pgtype.UUID) (dbquery.LockMilestoneCustomerRow, error) {
	row, err := scopedinvoices.LockMilestoneCustomer(ctx, q.tx, q.scope, dbqueryv8.LockMilestoneCustomerParams{CustomerID: id})
	return dbquery.LockMilestoneCustomerRow(row), err
}
func (q scopedQuotationConversionQueries) lockProjectReference(ctx context.Context, id pgtype.UUID) error {
	var locked pgtype.UUID
	return q.tx.QueryRow(ctx, `SELECT project_id FROM projects
 WHERE business_id=$1 AND project_id=$2 FOR KEY SHARE`, q.scope.UUID(), id).Scan(&locked)
}
func (q scopedQuotationConversionQueries) lockItemReference(ctx context.Context, id pgtype.UUID) error {
	var locked pgtype.UUID
	return q.tx.QueryRow(ctx, `SELECT item_id FROM items
 WHERE business_id=$1 AND item_id=$2 FOR KEY SHARE`, q.scope.UUID(), id).Scan(&locked)
}
func (q scopedQuotationConversionQueries) LockInvoiceNumberAllocation(ctx context.Context) error {
	return scopedinvoices.LockInvoiceNumberAllocation(ctx, q.tx, q.scope)
}
func (q scopedQuotationConversionQueries) InvoiceNumbersForPrefix(ctx context.Context, prefix pgtype.Text) ([]pgtype.Text, error) {
	return scopedinvoices.InvoiceNumbersForPrefix(ctx, q.tx, q.scope, dbqueryv8.InvoiceNumbersForPrefixParams{InvoiceNumber: prefix})
}
func (q scopedQuotationConversionQueries) InsertDraftInvoice(ctx context.Context, p dbquery.InsertDraftInvoiceParams) error {
	return scopedinvoices.InsertDraftInvoice(ctx, q.tx, q.scope, dbqueryv8.InsertDraftInvoiceParams{
		InvoiceID: p.InvoiceID, CustomerID: p.CustomerID, ProjectID: p.ProjectID, InvoiceType: p.InvoiceType,
		InvoiceNumber: p.InvoiceNumber, PoSoNumber: p.PoSoNumber, Currency: p.Currency, Subtotal: p.Subtotal,
		DiscountType: p.DiscountType, DiscountValue: p.DiscountValue, WhtType: p.WhtType, WhtRate: p.WhtRate,
		WhtGrossUp: p.WhtGrossUp, WhtBase: p.WhtBase, WhtAmount: p.WhtAmount, Amount: p.Amount,
		IssueDate: p.IssueDate, DueDate: p.DueDate, PaymentTerms: p.PaymentTerms, Notes: p.Notes, Footer: p.Footer,
		InternalNote: p.InternalNote,
	})
}
func (q scopedQuotationConversionQueries) InsertDraftInvoiceLine(ctx context.Context, p dbquery.InsertDraftInvoiceLineParams) error {
	return scopedinvoices.InsertDraftInvoiceLine(ctx, q.tx, q.scope, dbqueryv8.InsertDraftInvoiceLineParams{
		LineItemID: p.LineItemID, InvoiceID: p.InvoiceID, ItemID: p.ItemID, Position: p.Position,
		Description: p.Description, Quantity: p.Quantity, UnitPrice: p.UnitPrice, Amount: p.Amount,
	})
}
func (q scopedQuotationConversionQueries) setInvoiceSource(ctx context.Context, invoice, quote pgtype.UUID) error {
	tag, err := q.tx.Exec(ctx, `UPDATE invoices SET source_quote_id=$3 WHERE business_id=$1 AND invoice_id=$2`, q.scope.UUID(), invoice, quote)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("expected one row when linking quotation and invoice")
	}
	return err
}
func (q scopedQuotationConversionQueries) markQuoteConverted(ctx context.Context, quote, invoice pgtype.UUID) error {
	tag, err := q.tx.Exec(ctx, `UPDATE quotations SET status='INVOICED',converted_invoice_id=$3 WHERE business_id=$1 AND quotation_id=$2`, q.scope.UUID(), quote, invoice)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("expected one row when linking quotation and invoice")
	}
	return err
}
func (q scopedQuotationConversionQueries) GetInvoice(ctx context.Context, id pgtype.UUID) (dbquery.Invoice, error) {
	row, err := scopedinvoices.GetInvoice(ctx, q.tx, q.scope, dbqueryv8.GetInvoiceParams{InvoiceID: id})
	return legacyInvoice(row), err
}
func (q scopedQuotationConversionQueries) InvoiceLinesByIDs(ctx context.Context, ids []pgtype.UUID) ([]dbquery.InvoiceLineItem, error) {
	rows, err := scopedinvoices.InvoiceLinesByIDs(ctx, q.tx, q.scope, dbqueryv8.InvoiceLinesByIDsParams{InvoiceIds: ids})
	if err != nil {
		return nil, err
	}
	lines := make([]dbquery.InvoiceLineItem, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, legacyInvoiceLine(row))
	}
	return lines, nil
}
