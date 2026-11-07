package platform

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedcustomer"
)

func (h scopedProfileRouter) customerBalances(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	rows, err := scopedcustomer.CustomerBalances(r.Context(), h.pool, h.scope, dbqueryv8.CustomerBalancesParams{
		Today: pgtype.Date{Time: time.Now(), Valid: true},
	})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]dbquery.CustomerBalancesRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbquery.CustomerBalancesRow(row))
	}
	writeCustomerBalances(w, out)
}

func (h scopedProfileRouter) customerSummary(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "customer_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "customer_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	if _, err = scopedcustomer.GetCustomer(ctx, tx, h.scope, dbqueryv8.GetCustomerParams{CustomerID: id}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "customer not found")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	invoiceList := strings.HasSuffix(r.URL.Path, "/invoices")
	damaged, err := scopedcustomer.CustomerReportHasDamagedReferences(ctx, tx, h.scope, dbqueryv8.CustomerReportHasDamagedReferencesParams{
		CustomerID: id, AllStatuses: invoiceList,
	})
	if err != nil || damaged {
		authError(w, 500, "internal server error")
		return
	}
	rows, err := scopedcustomer.CustomerInvoices(ctx, tx, h.scope, dbqueryv8.CustomerInvoicesParams{CustomerID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	lines, err := scopedcustomer.CustomerInvoiceLines(ctx, tx, h.scope, dbqueryv8.CustomerInvoiceLinesParams{CustomerID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	legacyLines := make([]dbquery.InvoiceLineItem, 0, len(lines))
	for _, line := range lines {
		legacyLines = append(legacyLines, legacyInvoiceLine(line))
	}
	legacyRows := legacyCustomerInvoices(rows)
	if invoiceList {
		writeCustomerInvoices(w, legacyRows, legacyLines)
		return
	}
	open, err := scopedcustomer.CustomerOpenInvoices(ctx, tx, h.scope, dbqueryv8.CustomerOpenInvoicesParams{CustomerID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	payments, err := scopedcustomer.CustomerRecentPayments(ctx, tx, h.scope, dbqueryv8.CustomerRecentPaymentsParams{
		CustomerID:  id,
		PaymentDate: pgtype.Date{Time: time.Now().AddDate(0, 0, -365), Valid: true},
	})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	legacyPayments := make([]dbquery.CustomerRecentPaymentsRow, 0, len(payments))
	for _, payment := range payments {
		legacyPayments = append(legacyPayments, dbquery.CustomerRecentPaymentsRow(payment))
	}
	writeCustomerSummary(w, legacyRows, legacyCustomerInvoices(open), legacyLines, legacyPayments)
}

func legacyCustomerInvoices(rows []dbqueryv8.Invoice) []dbquery.Invoice {
	out := make([]dbquery.Invoice, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyInvoice(row))
	}
	return out
}
