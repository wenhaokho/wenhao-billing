package platform

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedproject"
)

func (h scopedProfileRouter) projectReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "project_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "project_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	project, err := scopedproject.GetProject(ctx, tx, h.scope, dbqueryv8.GetProjectParams{ProjectID: id})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "project not found")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	invoiceList := strings.HasSuffix(r.URL.Path, "/invoices")
	damaged, err := scopedproject.ProjectReportHasDamagedReferences(ctx, tx, h.scope, dbqueryv8.ProjectReportHasDamagedReferencesParams{
		ProjectID: id, InvoiceList: invoiceList,
	})
	if err != nil || damaged {
		authError(w, 500, "internal server error")
		return
	}
	if invoiceList {
		rows, err := scopedproject.ProjectInvoices(ctx, tx, h.scope, dbqueryv8.ProjectInvoicesParams{ProjectID: id})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		lines, err := scopedproject.ProjectInvoiceLines(ctx, tx, h.scope, dbqueryv8.ProjectInvoiceLinesParams{ProjectID: id})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		legacyLines := make([]dbquery.InvoiceLineItem, 0, len(lines))
		for _, line := range lines {
			legacyLines = append(legacyLines, legacyInvoiceLine(line))
		}
		legacyRows := legacyCustomerInvoices(rows)
		writeCustomerInvoices(w, legacyRows, legacyLines)
		return
	}
	stats, err := scopedproject.ProjectInvoiceStats(ctx, tx, h.scope, dbqueryv8.ProjectInvoiceStatsParams{ProjectID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	bill, err := scopedproject.ProjectBillStats(ctx, tx, h.scope, dbqueryv8.ProjectBillStatsParams{ProjectID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	writeProjectSummary(w, id, project.ContractValue, dbquery.ProjectInvoiceStatsRow(stats), bill)
}
