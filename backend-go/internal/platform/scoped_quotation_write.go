package platform

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedcustomer"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeditem"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedproject"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedquotations"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
)

func (h scopedProfileRouter) quotationWriter() quotationWriter {
	return quotationWriter{
		pool: h.pool,
		admin: func(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
			u, ok := h.admin(w, r)
			return u.UserID, ok
		},
		queries: func(tx pgx.Tx) quotationWriteQueries {
			return scopedQuotationWriteQueries{tx: tx, scope: h.scope}
		},
		scoped: true,
	}
}

type scopedQuotationWriteQueries struct {
	tx    pgx.Tx
	scope business.ID
}

func (q scopedQuotationWriteQueries) lockAdminRole(ctx context.Context, id pgtype.UUID) (string, error) {
	user, err := scopeduser.LockSessionUserByID(ctx, q.tx, q.scope, dbqueryv8.LockSessionUserByIDParams{UserID: id})
	return user.Role, err
}

func (q scopedQuotationWriteQueries) lockCustomer(ctx context.Context, id pgtype.UUID) error {
	_, err := scopedcustomer.LockCustomer(ctx, q.tx, q.scope, dbqueryv8.LockCustomerParams{CustomerID: id})
	return err
}

func (q scopedQuotationWriteQueries) lockProject(ctx context.Context, id pgtype.UUID) error {
	_, err := scopedproject.LockProject(ctx, q.tx, q.scope, dbqueryv8.LockProjectParams{ProjectID: id})
	return err
}

func (q scopedQuotationWriteQueries) lockItem(ctx context.Context, id pgtype.UUID) error {
	_, err := scopeditem.LockItem(ctx, q.tx, q.scope, dbqueryv8.LockItemParams{ItemID: id})
	return err
}

func (q scopedQuotationWriteQueries) LockQuotationNumberAllocation(ctx context.Context) error {
	return scopedquotations.LockQuotationNumberAllocation(ctx, q.tx, q.scope)
}

func (q scopedQuotationWriteQueries) QuotationNumbersForPrefix(ctx context.Context, prefix pgtype.Text) ([]pgtype.Text, error) {
	return scopedquotations.QuotationNumbersForPrefix(ctx, q.tx, q.scope, dbqueryv8.QuotationNumbersForPrefixParams{QuotationNumber: prefix})
}

func (q scopedQuotationWriteQueries) InsertQuotation(ctx context.Context, p dbquery.InsertQuotationParams) error {
	return scopedquotations.InsertQuotation(ctx, q.tx, q.scope, dbqueryv8.InsertQuotationParams{
		QuotationID: p.QuotationID, CustomerID: p.CustomerID, ProjectID: p.ProjectID,
		QuotationNumber: p.QuotationNumber, PoSoNumber: p.PoSoNumber, Currency: p.Currency,
		Subtotal: p.Subtotal, DiscountType: p.DiscountType, DiscountValue: p.DiscountValue, Amount: p.Amount,
		IssueDate: p.IssueDate, ValidUntil: p.ValidUntil, PaymentTerms: p.PaymentTerms, Notes: p.Notes, Footer: p.Footer,
	})
}

func (q scopedQuotationWriteQueries) InsertQuotationLine(ctx context.Context, p dbquery.InsertQuotationLineParams) error {
	return scopedquotations.InsertQuotationLine(ctx, q.tx, q.scope, dbqueryv8.InsertQuotationLineParams{
		LineItemID: p.LineItemID, QuotationID: p.QuotationID, ItemID: p.ItemID, Position: p.Position,
		Description: p.Description, Quantity: p.Quantity, UnitPrice: p.UnitPrice, Amount: p.Amount,
	})
}

func (q scopedQuotationWriteQueries) LockQuotation(ctx context.Context, id pgtype.UUID) (dbquery.Quotation, error) {
	row, err := scopedquotations.LockQuotation(ctx, q.tx, q.scope, dbqueryv8.LockQuotationParams{QuotationID: id})
	return legacyQuotation(row), err
}

func (q scopedQuotationWriteQueries) GetQuotation(ctx context.Context, id pgtype.UUID) (dbquery.Quotation, error) {
	row, err := scopedquotations.GetQuotation(ctx, q.tx, q.scope, dbqueryv8.GetQuotationParams{QuotationID: id})
	return legacyQuotation(row), err
}

func (q scopedQuotationWriteQueries) QuotationLines(ctx context.Context, ids []pgtype.UUID) ([]dbquery.QuotationLineItem, error) {
	rows, err := scopedquotations.QuotationLines(ctx, q.tx, q.scope, dbqueryv8.QuotationLinesParams{QuotationIds: ids})
	if err != nil {
		return nil, err
	}
	lines := make([]dbquery.QuotationLineItem, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, legacyQuotationLine(row))
	}
	return lines, nil
}

func (q scopedQuotationWriteQueries) DeleteQuotationLines(ctx context.Context, id pgtype.UUID) error {
	return scopedquotations.DeleteQuotationLines(ctx, q.tx, q.scope, dbqueryv8.DeleteQuotationLinesParams{QuotationID: id})
}

func (q scopedQuotationWriteQueries) UpdateQuotation(ctx context.Context, p dbquery.UpdateQuotationParams) error {
	return scopedquotations.UpdateQuotation(ctx, q.tx, q.scope, dbqueryv8.UpdateQuotationParams{
		QuotationID: p.QuotationID, CustomerID: p.CustomerID, ProjectID: p.ProjectID,
		QuotationNumber: p.QuotationNumber, PoSoNumber: p.PoSoNumber, Currency: p.Currency,
		Subtotal: p.Subtotal, DiscountType: p.DiscountType, DiscountValue: p.DiscountValue, Amount: p.Amount,
		IssueDate: p.IssueDate, ValidUntil: p.ValidUntil, PaymentTerms: p.PaymentTerms, Notes: p.Notes, Footer: p.Footer,
	})
}

func (q scopedQuotationWriteQueries) deleteQuotation(ctx context.Context, id pgtype.UUID) error {
	return scopedquotations.DeleteQuotation(ctx, q.tx, q.scope, dbqueryv8.DeleteQuotationParams{QuotationID: id})
}

func (q scopedQuotationWriteQueries) changeQuotationStatus(ctx context.Context, id pgtype.UUID, action string, acceptedBy *string) error {
	switch action {
	case "mark-sent":
		return scopedquotations.MarkQuotationSent(ctx, q.tx, q.scope, dbqueryv8.MarkQuotationSentParams{QuotationID: id})
	case "accept":
		return scopedquotations.AcceptQuotation(ctx, q.tx, q.scope, dbqueryv8.AcceptQuotationParams{
			QuotationID: id, AcceptedBy: textValue(acceptedBy),
		})
	case "decline":
		return scopedquotations.DeclineQuotation(ctx, q.tx, q.scope, dbqueryv8.DeclineQuotationParams{QuotationID: id})
	case "void":
		return scopedquotations.VoidQuotation(ctx, q.tx, q.scope, dbqueryv8.VoidQuotationParams{QuotationID: id})
	default:
		return fmt.Errorf("unsupported quotation transition %q", action)
	}
}

func (q scopedQuotationWriteQueries) recordQuotationSend(ctx context.Context, id pgtype.UUID) error {
	return scopedquotations.RecordQuotationSend(ctx, q.tx, q.scope, dbqueryv8.RecordQuotationSendParams{QuotationID: id})
}

func (q scopedQuotationWriteQueries) sendDetails(ctx context.Context, id pgtype.UUID) (quotationSendDetails, error) {
	rows, err := scopedquotations.QuotationSendDetails(ctx, q.tx, q.scope, dbqueryv8.QuotationSendDetailsParams{CustomerID: id})
	if err != nil {
		return quotationSendDetails{}, err
	}
	if len(rows) == 0 {
		return quotationSendDetails{}, pgx.ErrNoRows
	}
	first := rows[0]
	details := quotationSendDetails{
		customer: legacyCustomer(first.Customer),
		profile: dbquery.BusinessProfile{
			Name: first.ProfileName, Address: first.ProfileAddress, ContactEmail: first.ProfileEmail,
			ContactPhone: first.ProfilePhone, LogoUrl: first.LogoUrl, DefaultNotes: first.DefaultNotes,
		},
	}
	for _, row := range rows {
		if row.HasAccount {
			details.accounts = append(details.accounts, dbquery.PaymentAccount{
				Currency: row.Currency, Instructions: row.Instructions, IsDefault: row.IsDefault,
			})
		}
	}
	return details, nil
}
