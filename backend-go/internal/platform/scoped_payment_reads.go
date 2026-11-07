package platform

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedpayment"
	"net/http"
)

func (h scopedProfileRouter) paymentRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "payment_id")
	var rows []dbqueryv8.Payment
	if raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "payment_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		p, e := scopedpayment.Get(r.Context(), h.pool, h.scope, id)
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "payment not found")
			return
		}
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbqueryv8.Payment{p}
	} else {
		var e error
		rows, e = scopedpayment.AwaitingReview(r.Context(), h.pool, h.scope)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	out := make([]paymentOutput, 0, len(rows))
	for _, p := range rows {
		v, e := paymentJSON(legacyPayment(p))
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

func legacyPayment(p dbqueryv8.Payment) dbquery.Payment {
	return dbquery.Payment{PaymentID: p.PaymentID, InvoiceID: p.InvoiceID, CustomerID: p.CustomerID, Amount: p.Amount, Currency: p.Currency, PayerName: p.PayerName, PayerReference: p.PayerReference, PaymentDate: p.PaymentDate, IntakeSource: p.IntakeSource, ExternalRef: p.ExternalRef, Status: p.Status, AdjustmentType: p.AdjustmentType, ConfidenceScore: p.ConfidenceScore, CreatedAt: p.CreatedAt, WhtAmount: p.WhtAmount}
}
