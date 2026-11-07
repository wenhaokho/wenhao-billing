package platform

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
)

func (h scopedProfileRouter) invoiceSender() invoiceSender {
	return invoiceSender{
		pool: candidateTransactions{db: h.pool, scope: h.scope},
		admin: func(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
			user, ok := h.admin(w, r)
			return user.UserID, ok
		},
		queries: func(tx pgx.Tx) invoiceSendQueries {
			return scopedInvoiceSendQueries{scopedQuotationWriteQueries{tx: tx, scope: h.scope}}
		},
		sender: h.cfg.Sender,
	}
}

type scopedInvoiceSendQueries struct {
	scopedQuotationWriteQueries
}

func (q scopedInvoiceSendQueries) GetCustomer(ctx context.Context, id pgtype.UUID) (dbquery.Customer, error) {
	customer, err := q.scopedQuotationWriteQueries.GetCustomer(ctx, id)
	if id.Valid && errors.Is(err, pgx.ErrNoRows) {
		return customer, errors.New("invoice customer reference is unavailable")
	}
	return customer, err
}

func (q scopedInvoiceSendQueries) LockInvoice(ctx context.Context, id pgtype.UUID) (dbquery.Invoice, error) {
	row, err := scopedinvoices.LockInvoice(ctx, q.tx, q.scope, dbqueryv8.LockInvoiceParams{InvoiceID: id})
	return legacyInvoice(row), err
}

func (q scopedInvoiceSendQueries) GetInvoice(ctx context.Context, id pgtype.UUID) (dbquery.Invoice, error) {
	row, err := scopedinvoices.GetInvoice(ctx, q.tx, q.scope, dbqueryv8.GetInvoiceParams{InvoiceID: id})
	return legacyInvoice(row), err
}

func (q scopedInvoiceSendQueries) InvoiceLinesByIDs(ctx context.Context, ids []pgtype.UUID) ([]dbquery.InvoiceLineItem, error) {
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

func (q scopedInvoiceSendQueries) recordInvoiceSend(ctx context.Context, id pgtype.UUID) error {
	return scopedinvoices.RecordInvoiceSend(ctx, q.tx, q.scope, dbqueryv8.RecordInvoiceSendParams{InvoiceID: id})
}
