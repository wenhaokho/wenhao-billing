package platform

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedcustomer"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedpayment"
)

func (h scopedProfileRouter) invoicePaymentWriter() invoicePaymentWriter {
	return invoicePaymentWriter{
		pool:  candidateTransactions{db: h.pool, scope: h.scope},
		admin: h.invoiceWriter().admin,
		queries: func(tx pgx.Tx) invoicePaymentQueries {
			return scopedInvoicePaymentQueries{scopedInvoiceWriteQueries{tx: tx, scope: h.scope}}
		},
	}
}

type scopedInvoicePaymentQueries struct {
	scopedInvoiceWriteQueries
}

func (q scopedInvoicePaymentQueries) LockInvoice(ctx context.Context, id pgtype.UUID) (dbquery.Invoice, error) {
	invoice, err := q.scopedInvoiceWriteQueries.LockInvoice(ctx, id)
	if err != nil || !invoice.CustomerID.Valid {
		return invoice, err
	}
	// Lock the scoped customer reference before taking hosting locks or inserting the payment.
	var customer pgtype.UUID
	err = q.tx.QueryRow(ctx, `SELECT customer_id FROM customers WHERE business_id=$1 AND customer_id=$2 FOR KEY SHARE`,
		q.scope.UUID(), invoice.CustomerID).Scan(&customer)
	return invoice, err
}

func (q scopedInvoicePaymentQueries) queueHostingRestore(ctx context.Context, invoiceID pgtype.UUID) error {
	var unresolved bool
	err := q.tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM invoice_line_items l
			WHERE l.business_id=$1 AND l.invoice_id=$2 AND l.item_id IS NOT NULL AND NOT EXISTS (
				SELECT 1 FROM items i WHERE i.business_id=l.business_id AND i.item_id=l.item_id
			)
		)`, q.scope.UUID(), invoiceID).Scan(&unresolved)
	if err != nil {
		return err
	}
	if unresolved {
		return errors.New("unresolved invoice line item")
	}
	// Match invoice lifecycle lock order and deduplicate items appearing on multiple lines.
	rows, err := q.tx.Query(ctx, `
		SELECT i.item_id FROM items i
		WHERE i.business_id=$1 AND i.is_hosting AND EXISTS (
			SELECT 1 FROM invoice_line_items l
			WHERE l.business_id=i.business_id AND l.invoice_id=$2 AND l.item_id=i.item_id
		)
		ORDER BY i.item_id FOR UPDATE`, q.scope.UUID(), invoiceID)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowTo[pgtype.UUID])
	if err != nil {
		return err
	}
	for _, itemID := range items {
		if _, err := q.tx.Exec(ctx, `SELECT target_id FROM cloudflare_targets WHERE business_id=$1 AND item_id=$2 FOR UPDATE`, q.scope.UUID(), itemID); err != nil {
			return err
		}
		if _, err := q.tx.Exec(ctx, `UPDATE items SET hosting_last_paid_at=date_trunc('second', timezone('UTC', now())) WHERE business_id=$1 AND item_id=$2`, q.scope.UUID(), itemID); err != nil {
			return err
		}
		if _, err := q.tx.Exec(ctx, `INSERT INTO hosting_restore_intents(business_id,invoice_id,item_id) VALUES ($1,$2,$3)
			ON CONFLICT (business_id,invoice_id,item_id) DO NOTHING`, q.scope.UUID(), invoiceID, itemID); err != nil {
			return err
		}
	}
	return nil
}

func (q scopedInvoicePaymentQueries) InsertInvoicePayment(ctx context.Context, p dbquery.InsertInvoicePaymentParams) (dbquery.Payment, error) {
	payment, err := scopedinvoices.InsertInvoicePayment(ctx, q.tx, q.scope, dbqueryv8.InsertInvoicePaymentParams{
		PaymentID: p.PaymentID, InvoiceID: p.InvoiceID, CustomerID: p.CustomerID, Amount: p.Amount,
		WhtAmount: p.WhtAmount, Currency: p.Currency, PayerName: p.PayerName, PayerReference: p.PayerReference,
		PaymentDate: p.PaymentDate, ExternalRef: p.ExternalRef,
	})
	return legacyPayment(payment), err
}

func (q scopedInvoicePaymentQueries) ApplyInvoicePayment(ctx context.Context, p dbquery.ApplyInvoicePaymentParams) error {
	return scopedinvoices.ApplyInvoicePayment(ctx, q.tx, q.scope, dbqueryv8.ApplyInvoicePaymentParams{
		InvoiceID: p.InvoiceID, BalanceDue: p.BalanceDue, WhtBooked: p.WhtBooked, Status: p.Status,
	})
}

func (q scopedInvoicePaymentQueries) InsertInvoicePaymentLog(ctx context.Context, p dbquery.InsertInvoicePaymentLogParams) error {
	return scopedinvoices.InsertInvoicePaymentLog(ctx, q.tx, q.scope, dbqueryv8.InsertInvoicePaymentLogParams{
		LogID: p.LogID, PaymentID: p.PaymentID, Reasons: p.Reasons, ActorUserID: p.ActorUserID,
	})
}

func (h scopedProfileRouter) invoicePayments(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "invoice_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "invoice_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	fail := func() { authError(w, 500, "internal server error") }
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		fail()
		return
	}
	defer tx.Rollback(ctx)
	invoice, err := scopedinvoices.GetInvoice(ctx, tx, h.scope, dbqueryv8.GetInvoiceParams{InvoiceID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "invoice not found")
		return
	} else if err != nil {
		fail()
		return
	}
	if invoice.CustomerID.Valid {
		_, err := scopedcustomer.GetCustomer(ctx, tx, h.scope, dbqueryv8.GetCustomerParams{CustomerID: invoice.CustomerID})
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "invoice not found")
			return
		}
		if err != nil {
			fail()
			return
		}
	}
	rows, err := scopedpayment.InvoicePayments(ctx, tx, h.scope, id)
	if err != nil {
		fail()
		return
	}
	out := make([]paymentOutput, 0, len(rows))
	for _, p := range rows {
		payment, err := paymentJSON(legacyPayment(p))
		if err != nil {
			fail()
			return
		}
		out = append(out, payment)
	}
	if err := tx.Commit(ctx); err != nil {
		fail()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}
