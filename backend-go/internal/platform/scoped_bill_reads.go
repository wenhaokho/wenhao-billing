package platform

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedbills"
	"net/http"
)

func legacyBill(v dbqueryv8.Bill) dbquery.Bill {
	return dbquery.Bill{BillID: v.BillID, VendorID: v.VendorID, BillNumber: v.BillNumber, PoNumber: v.PoNumber, Currency: v.Currency, Subtotal: v.Subtotal, DiscountType: v.DiscountType, DiscountValue: v.DiscountValue, Amount: v.Amount, BalanceDue: v.BalanceDue, Status: v.Status, IssueDate: v.IssueDate, DueDate: v.DueDate, PaymentTerms: v.PaymentTerms, Notes: v.Notes, CreatedAt: v.CreatedAt, ProjectID: v.ProjectID}
}
func legacyBillLine(v dbqueryv8.BillLineItem) dbquery.BillLineItem {
	return dbquery.BillLineItem{LineItemID: v.LineItemID, BillID: v.BillID, ItemID: v.ItemID, ExpenseAccountID: v.ExpenseAccountID, Position: v.Position, Description: v.Description, Quantity: v.Quantity, UnitPrice: v.UnitPrice, Amount: v.Amount, CreatedAt: v.CreatedAt}
}
func (h scopedProfileRouter) billRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	tx, beginErr := h.pool.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if beginErr != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	raw := chi.URLParam(r, "bill_id")
	var rows []dbqueryv8.Bill
	if raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "bill_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		b, e := scopedbills.GetBill(r.Context(), tx, h.scope, dbqueryv8.GetBillParams{BillID: id})
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "bill not found")
			return
		}
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbqueryv8.Bill{b}
	} else {
		values := r.URL.Query()
		var id pgtype.UUID
		if v, ok := values["vendor_id"]; ok {
			raw = v[len(v)-1]
			var valid bool
			id, valid = parsePathUUID(raw)
			if !valid {
				d := uuidErrorDetail(raw)
				validationResponse(w, []validationError{{"uuid_parsing", []any{"query", "vendor_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
				return
			}
		}
		statuses, ok := values["status"]
		if !ok {
			statuses = []string{}
		}
		var e error
		rows, e = scopedbills.ListBills(r.Context(), tx, h.scope, dbqueryv8.ListBillsParams{VendorID: id, AllStatus: !ok, Statuses: statuses})
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, b := range rows {
		ids = append(ids, b.BillID)
	}
	lines, e := scopedbills.BillLines(r.Context(), tx, h.scope, dbqueryv8.BillLinesParams{BillIds: ids})
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	grouped := map[pgtype.UUID][]dbquery.BillLineItem{}
	for _, l := range lines {
		grouped[l.BillID] = append(grouped[l.BillID], legacyBillLine(l))
	}
	out := make([]billOutput, 0, len(rows))
	for _, b := range rows {
		v, e := billJSON(legacyBill(b), grouped[b.BillID])
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if chi.URLParam(r, "bill_id") != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}
