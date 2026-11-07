package platform

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedcustomer"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedprofile"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedquotations"
)

type scopedPDFDocument struct {
	customerID pgtype.UUID
	number     string
	render     func(context.Context, dbquery.Customer, dbquery.BusinessProfile, []dbquery.PaymentAccount) ([]byte, error)
}

func (h scopedProfileRouter) loadQuotationPDF(ctx context.Context, tx pgx.Tx, id pgtype.UUID) (scopedPDFDocument, error) {
	quote, err := scopedquotations.GetQuotation(ctx, tx, h.scope, dbqueryv8.GetQuotationParams{QuotationID: id})
	if err != nil {
		return scopedPDFDocument{}, err
	}
	rows, err := scopedquotations.QuotationLines(ctx, tx, h.scope, dbqueryv8.QuotationLinesParams{QuotationIds: []pgtype.UUID{id}})
	if err != nil {
		return scopedPDFDocument{}, err
	}
	lines := make([]dbquery.QuotationLineItem, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, legacyQuotationLine(row))
	}
	return scopedPDFDocument{
		customerID: quote.CustomerID,
		number:     quote.QuotationNumber.String,
		render: func(ctx context.Context, customer dbquery.Customer, profile dbquery.BusinessProfile, accounts []dbquery.PaymentAccount) ([]byte, error) {
			return renderQuotationPDF(ctx, legacyQuotation(quote), customer, profile, lines, accounts)
		},
	}, nil
}

func (h scopedProfileRouter) loadInvoicePDF(ctx context.Context, tx pgx.Tx, id pgtype.UUID) (scopedPDFDocument, error) {
	invoice, err := scopedinvoices.GetInvoice(ctx, tx, h.scope, dbqueryv8.GetInvoiceParams{InvoiceID: id})
	if err != nil {
		return scopedPDFDocument{}, err
	}
	rows, err := scopedinvoices.InvoiceLinesByIDs(ctx, tx, h.scope, dbqueryv8.InvoiceLinesByIDsParams{InvoiceIds: []pgtype.UUID{id}})
	if err != nil {
		return scopedPDFDocument{}, err
	}
	lines := make([]dbquery.InvoiceLineItem, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, legacyInvoiceLine(row))
	}
	return scopedPDFDocument{
		customerID: invoice.CustomerID,
		number:     invoice.InvoiceNumber.String,
		render: func(ctx context.Context, customer dbquery.Customer, profile dbquery.BusinessProfile, accounts []dbquery.PaymentAccount) ([]byte, error) {
			return renderInvoicePDF(ctx, legacyInvoice(invoice), customer, profile, lines, accounts)
		},
	}, nil
}

func (h scopedProfileRouter) documentPDF(kind string, load func(context.Context, pgx.Tx, pgtype.UUID) (scopedPDFDocument, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.admin(w, r); !ok {
			return
		}
		param := kind + "_id"
		raw := chi.URLParam(r, param)
		id, ok := parsePathUUID(raw)
		if !ok {
			detail := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", param}, "Input should be a valid UUID, " + detail, raw, map[string]string{"error": detail}}})
			return
		}
		ctx := r.Context()
		tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		defer tx.Rollback(ctx)

		document, err := load(ctx, tx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, kind+" not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		q := scopedQuotationWriteQueries{tx: tx, scope: h.scope}
		customer, err := q.GetCustomer(ctx, document.customerID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			authError(w, 500, "internal server error")
			return
		}
		profile, err := q.GetBusinessProfile(ctx)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			authError(w, 500, "internal server error")
			return
		}
		accounts, err := q.ListBusinessPaymentAccounts(ctx)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		if err = tx.Commit(ctx); err != nil {
			authError(w, 500, "internal server error")
			return
		}

		data, err := document.render(ctx, customer, profile, accounts)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		number := document.number
		if number == "" {
			number = fmt.Sprintf("%x", id.Bytes[:4])
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": kind + "-" + number + ".pdf"}))
		w.Write(data)
	}
}

func (q scopedQuotationWriteQueries) GetCustomer(ctx context.Context, id pgtype.UUID) (dbquery.Customer, error) {
	customer, err := scopedcustomer.GetCustomer(ctx, q.tx, q.scope, dbqueryv8.GetCustomerParams{CustomerID: id})
	return legacyCustomer(customer), err
}

func (q scopedQuotationWriteQueries) GetBusinessProfile(ctx context.Context) (dbquery.BusinessProfile, error) {
	profile, err := scopedprofile.Get(ctx, q.tx, q.scope)
	return dbquery.BusinessProfile{
		ID: profile.ID, Name: profile.Name, Address: profile.Address, ContactEmail: profile.ContactEmail,
		ContactPhone: profile.ContactPhone, InvoiceTitle: profile.InvoiceTitle, InvoiceSummary: profile.InvoiceSummary,
		LogoUrl: profile.LogoUrl, UpdatedAt: profile.UpdatedAt, DefaultNotes: profile.DefaultNotes,
		Npwp: profile.Npwp, NpwpName: profile.NpwpName,
	}, err
}

func (q scopedQuotationWriteQueries) ListBusinessPaymentAccounts(ctx context.Context) ([]dbquery.PaymentAccount, error) {
	rows, err := scopedprofile.ListAccounts(ctx, q.tx, q.scope)
	if err != nil {
		return nil, err
	}
	accounts := make([]dbquery.PaymentAccount, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, dbquery.PaymentAccount{ID: row.ID, BusinessProfileID: row.BusinessProfileID, Currency: row.Currency, Instructions: row.Instructions, IsDefault: row.IsDefault, UpdatedAt: row.UpdatedAt})
	}
	return accounts, nil
}
