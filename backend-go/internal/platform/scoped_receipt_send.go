package platform

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedpayment"
)

func (h scopedProfileRouter) receiptSender() receiptSender {
	return receiptSender{
		pool: h.pool,
		admin: func(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
			user, ok := h.admin(w, r)
			return user.UserID, ok
		},
		queries: func(tx pgx.Tx) receiptSendQueries {
			return scopedReceiptSendQueries{scopedInvoiceSendQueries{scopedQuotationWriteQueries{tx: tx, scope: h.scope}}}
		},
		sender: h.cfg.Sender,
	}
}

type scopedReceiptSendQueries struct {
	scopedInvoiceSendQueries
}

func (q scopedReceiptSendQueries) lockReceiptPayment(ctx context.Context, invoiceID, paymentID pgtype.UUID) (dbquery.Payment, error) {
	p, err := scopedpayment.LockInvoiceReceiptPayment(ctx, q.tx, q.scope, invoiceID, paymentID)
	return dbquery.Payment{
		PaymentID: p.PaymentID, Amount: p.Amount, Currency: p.Currency, PaymentDate: p.PaymentDate,
		PayerName: p.PayerName, PayerReference: p.PayerReference, IntakeSource: p.IntakeSource,
	}, err
}
