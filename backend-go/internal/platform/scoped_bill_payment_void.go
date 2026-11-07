package platform

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedbills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
)

func (h scopedProfileRouter) billPaymentVoid(void bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") == "" {
			r = r.Clone(r.Context())
			r.Header.Set("Content-Type", "application/json")
		}
		body, ok := readBodyWithSyntax(w, r)
		if !ok {
			return
		}
		current, ok := h.admin(w, r)
		if !ok {
			return
		}
		in, ok := parseBillLifecycleInput(w, r, body, void)
		if !ok {
			return
		}
		ctx := r.Context()
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			billLifecycleInternalError(w)
			return
		}
		defer tx.Rollback(ctx)
		user, err := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
			authError(w, 403, "admin required")
			return
		}
		if err != nil {
			billLifecycleInternalError(w)
			return
		}
		bill, err := scopedbills.LockBill(ctx, tx, h.scope, dbqueryv8.LockBillParams{BillID: in.id})
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "bill not found")
			return
		}
		if err != nil {
			billLifecycleInternalError(w)
			return
		}
		b := legacyBill(bill)
		if !applyBillLifecycle(w, &b, in, void) {
			return
		}
		// Preserve Python's balance-only mutation pending the bill accounting decision.
		bill, err = scopedbills.UpdateBill(ctx, tx, h.scope, dbqueryv8.UpdateBillParams{
			BillID: in.id, VendorID: b.VendorID, ProjectID: b.ProjectID,
			BillNumber: b.BillNumber, PoNumber: b.PoNumber, Currency: b.Currency,
			Subtotal: b.Subtotal, DiscountType: b.DiscountType, DiscountValue: b.DiscountValue,
			Amount: b.Amount, BalanceDue: b.BalanceDue, Status: b.Status,
			IssueDate: b.IssueDate, DueDate: b.DueDate, PaymentTerms: b.PaymentTerms, Notes: b.Notes,
		})
		if err != nil {
			billLifecycleInternalError(w)
			return
		}
		lines, err := scopedbills.BillLines(ctx, tx, h.scope, dbqueryv8.BillLinesParams{BillIds: []pgtype.UUID{in.id}})
		if err != nil {
			billLifecycleInternalError(w)
			return
		}
		legacyLines := make([]dbquery.BillLineItem, 0, len(lines))
		for _, line := range lines {
			legacyLines = append(legacyLines, legacyBillLine(line))
		}
		out, err := billJSON(legacyBill(bill), legacyLines)
		if err != nil {
			billLifecycleInternalError(w)
			return
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			billLifecycleInternalError(w)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			billLifecycleInternalError(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append(encoded, '\n'))
	}
}
