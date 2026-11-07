package platform

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
	"net/http"
	"strconv"
	"strings"
)

func legacyInvoice(v dbqueryv8.Invoice) dbquery.Invoice {
	return dbquery.Invoice{InvoiceID: v.InvoiceID, CustomerID: v.CustomerID, InvoiceType: v.InvoiceType, Currency: v.Currency, Amount: v.Amount, BalanceDue: v.BalanceDue, Status: v.Status, BillingCycleRef: v.BillingCycleRef, IssueDate: v.IssueDate, DueDate: v.DueDate, CreatedAt: v.CreatedAt, InvoiceNumber: v.InvoiceNumber, PoSoNumber: v.PoSoNumber, Subtotal: v.Subtotal, DiscountType: v.DiscountType, DiscountValue: v.DiscountValue, Notes: v.Notes, Footer: v.Footer, PaymentTerms: v.PaymentTerms, IsTemplate: v.IsTemplate, ProjectID: v.ProjectID, SourceQuoteID: v.SourceQuoteID, CoverageStart: v.CoverageStart, CoverageEnd: v.CoverageEnd, InternalNote: v.InternalNote, WhtType: v.WhtType, WhtRate: v.WhtRate, WhtGrossUp: v.WhtGrossUp, WhtBase: v.WhtBase, WhtAmount: v.WhtAmount, WhtBooked: v.WhtBooked}
}
func legacyInvoiceLine(v dbqueryv8.InvoiceLineItem) dbquery.InvoiceLineItem {
	return dbquery.InvoiceLineItem{LineItemID: v.LineItemID, InvoiceID: v.InvoiceID, ItemID: v.ItemID, Position: v.Position, Description: v.Description, Quantity: v.Quantity, UnitPrice: v.UnitPrice, Amount: v.Amount, CreatedAt: v.CreatedAt}
}
func (h scopedProfileRouter) invoiceRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	tx, beginErr := h.pool.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if beginErr != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	ctx := r.Context()
	raw := chi.URLParam(r, "invoice_id")
	template := strings.Contains(r.URL.Path, "/recurring-templates")
	pathName := "invoice_id"
	if template {
		raw = chi.URLParam(r, "template_id")
		pathName = "template_id"
	}
	var rows []dbqueryv8.Invoice
	var err error
	if raw != "" {
		var id pgtype.UUID
		var ok bool
		id, ok = parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", pathName}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		var row dbqueryv8.Invoice
		row, err = scopedinvoices.GetInvoice(ctx, tx, h.scope, dbqueryv8.GetInvoiceParams{InvoiceID: id})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && template && !row.IsTemplate) {
			message := "invoice not found"
			if template {
				message = "recurring template not found"
			}
			authError(w, 404, message)
			return
		}
		rows = []dbqueryv8.Invoice{row}
	} else if template {
		rows, err = scopedinvoices.ListRecurringTemplates(ctx, tx, h.scope)
	} else if strings.HasSuffix(r.URL.Path, "/awaiting-finalization") {
		rows, err = scopedinvoices.AwaitingFinalization(ctx, tx, h.scope)
	} else {
		values := r.URL.Query()
		var errs []validationError
		var id pgtype.UUID
		if v, ok := values["customer_id"]; ok {
			v0 := v[len(v)-1]
			var valid bool
			id, valid = parsePathUUID(v0)
			if !valid {
				d := uuidErrorDetail(v0)
				errs = append(errs, validationError{"uuid_parsing", []any{"query", "customer_id"}, "Input should be a valid UUID, " + d, v0, map[string]string{"error": d}})
			}
		}
		limit := int64(200)
		if v, ok := values["limit"]; ok {
			v0 := v[len(v)-1]
			normalized := strings.TrimSpace(v0)
			if !fxPathInteger.MatchString(normalized) {
				errs = append(errs, validationError{"int_parsing", []any{"query", "limit"}, "Input should be a valid integer, unable to parse string as an integer", v0, nil})
			} else {
				normalized = strings.ReplaceAll(normalized, "_", "")
				if dot := strings.IndexByte(normalized, '.'); dot >= 0 {
					normalized = normalized[:dot]
				}
				n, e := strconv.ParseInt(normalized, 10, 64)
				if e != nil {
					if strings.HasPrefix(normalized, "-") {
						n = -1
					} else {
						n = 501
					}
				}
				if n > 500 {
					errs = append(errs, validationError{"less_than_equal", []any{"query", "limit"}, "Input should be less than or equal to 500", v0, map[string]int{"le": 500}})
				} else {
					limit = n
				}
			}
		}
		if len(errs) > 0 {
			validationResponse(w, errs)
			return
		}
		if limit < 0 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))
			return
		}
		statuses, ok := values["status"]
		if !ok {
			statuses = []string{}
		}
		var typ pgtype.Text
		if v, ok := values["invoice_type"]; ok && v[len(v)-1] != "" {
			typ = pgtype.Text{String: v[len(v)-1], Valid: true}
		}
		rows, err = scopedinvoices.ListInvoices(ctx, tx, h.scope, dbqueryv8.ListInvoicesParams{AllStatus: !ok, Statuses: statuses, CustomerID: id, InvoiceType: typ, RowLimit: limit})
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.InvoiceID)
	}
	lines, err := scopedinvoices.InvoiceLinesByIDs(ctx, tx, h.scope, dbqueryv8.InvoiceLinesByIDsParams{InvoiceIds: ids})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	byID := map[pgtype.UUID][]dbquery.InvoiceLineItem{}
	for _, line := range lines {
		byID[line.InvoiceID] = append(byID[line.InvoiceID], legacyInvoiceLine(line))
	}
	out := make([]invoiceOutput, 0, len(rows))
	for _, row := range rows {
		v, e := invoiceJSON(legacyInvoice(row), byID[row.InvoiceID])
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if raw != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}
