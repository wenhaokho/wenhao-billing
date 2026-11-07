package platform

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedquotations"
	"net/http"
	"strconv"
	"strings"
)

func legacyQuotation(v dbqueryv8.Quotation) dbquery.Quotation {
	return dbquery.Quotation{QuotationID: v.QuotationID, CustomerID: v.CustomerID, ProjectID: v.ProjectID, QuotationNumber: v.QuotationNumber, PoSoNumber: v.PoSoNumber, Currency: v.Currency, Subtotal: v.Subtotal, DiscountType: v.DiscountType, DiscountValue: v.DiscountValue, Amount: v.Amount, Status: v.Status, IssueDate: v.IssueDate, ValidUntil: v.ValidUntil, PaymentTerms: v.PaymentTerms, Notes: v.Notes, Footer: v.Footer, LastSentAt: v.LastSentAt, AcceptedAt: v.AcceptedAt, AcceptedBy: v.AcceptedBy, ConvertedInvoiceID: v.ConvertedInvoiceID, CreatedAt: v.CreatedAt}
}
func legacyQuotationLine(v dbqueryv8.QuotationLineItem) dbquery.QuotationLineItem {
	return dbquery.QuotationLineItem{LineItemID: v.LineItemID, QuotationID: v.QuotationID, ItemID: v.ItemID, Position: v.Position, Description: v.Description, Quantity: v.Quantity, UnitPrice: v.UnitPrice, Amount: v.Amount, CreatedAt: v.CreatedAt}
}
func (h scopedProfileRouter) quotationRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	tx, beginErr := h.pool.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if beginErr != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	raw := chi.URLParam(r, "quotation_id")
	var rows []dbqueryv8.Quotation
	if raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "quotation_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		b, e := scopedquotations.GetQuotation(r.Context(), tx, h.scope, dbqueryv8.GetQuotationParams{QuotationID: id})
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "quotation not found")
			return
		}
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbqueryv8.Quotation{b}
	} else {
		values := r.URL.Query()
		var errs []validationError
		var customer, project pgtype.UUID
		for _, field := range []struct {
			name string
			dst  *pgtype.UUID
		}{{"customer_id", &customer}, {"project_id", &project}} {
			if v, ok := values[field.name]; ok {
				raw = v[len(v)-1]
				id, valid := parsePathUUID(raw)
				if !valid {
					d := uuidErrorDetail(raw)
					errs = append(errs, validationError{"uuid_parsing", []any{"query", field.name}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}})
				} else {
					*field.dst = id
				}
			}
		}
		limit := int64(200)
		if v, ok := values["limit"]; ok {
			raw = v[len(v)-1]
			n := strings.TrimSpace(raw)
			if !fxPathInteger.MatchString(n) {
				errs = append(errs, validationError{"int_parsing", []any{"query", "limit"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil})
			} else {
				n = strings.ReplaceAll(n, "_", "")
				if i := strings.IndexByte(n, '.'); i >= 0 {
					n = n[:i]
				}
				x, e := strconv.ParseInt(n, 10, 64)
				if e != nil {
					if strings.HasPrefix(n, "-") {
						x = -1
					} else {
						x = 501
					}
				}
				if x > 500 {
					errs = append(errs, validationError{"less_than_equal", []any{"query", "limit"}, "Input should be less than or equal to 500", raw, map[string]int{"le": 500}})
				} else {
					limit = x
				}
			}
		}
		if len(errs) > 0 {
			validationResponse(w, errs)
			return
		}
		if limit < 0 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(500)
			w.Write([]byte("Internal Server Error"))
			return
		}
		statuses, ok := values["status"]
		if !ok {
			statuses = []string{}
		}
		var e error
		rows, e = scopedquotations.ListQuotations(r.Context(), tx, h.scope, dbqueryv8.ListQuotationsParams{CustomerID: customer, ProjectID: project, AllStatus: !ok, Statuses: statuses, RowLimit: int32(limit)})
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, b := range rows {
		ids = append(ids, b.QuotationID)
	}
	lines, e := scopedquotations.QuotationLines(r.Context(), tx, h.scope, dbqueryv8.QuotationLinesParams{QuotationIds: ids})
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	grouped := map[pgtype.UUID][]dbquery.QuotationLineItem{}
	for _, l := range lines {
		grouped[l.QuotationID] = append(grouped[l.QuotationID], legacyQuotationLine(l))
	}
	out := make([]quotationOutput, 0, len(rows))
	for _, b := range rows {
		v, e := quotationJSON(legacyQuotation(b), grouped[b.QuotationID])
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if chi.URLParam(r, "quotation_id") != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}
