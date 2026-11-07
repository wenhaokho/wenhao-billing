package platform

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
)

func (h scopedProfileRouter) usageLock(w http.ResponseWriter, r *http.Request) {
	serveUsageLock(w, r, h.invoiceWriter(), func(tx pgx.Tx) usageLockQueries {
		return scopedInvoiceWriteQueries{tx: tx, scope: h.scope}
	})
}

func (q scopedInvoiceWriteQueries) LockUsageInvoice(ctx context.Context, p dbquery.LockUsageInvoiceParams) error {
	return scopedinvoices.LockUsageInvoice(ctx, q.tx, q.scope, dbqueryv8.LockUsageInvoiceParams{
		InvoiceID:       p.InvoiceID,
		WhtBase:         p.WhtBase,
		WhtAmount:       p.WhtAmount,
		Amount:          p.Amount,
		BillingCycleRef: p.BillingCycleRef,
	})
}
