package platform

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedpayment"
)

func (h scopedProfileRouter) paymentLog(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "payment_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "payment_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	rows, err := scopedpayment.Logs(r.Context(), h.pool, h.scope, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]logOutput, 0, len(rows))
	for _, row := range rows {
		if !row.Damaged.Valid || row.Damaged.Bool {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, logOutput{row.LogID, row.Action, row.Reasons, row.ActorUserID, pythonTimestamp(row.CreatedAt)})
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}

func (h scopedProfileRouter) paymentAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	args, ok := parsePaymentAudit(w, r)
	if !ok {
		return
	}
	rows, err := scopedpayment.Audit(r.Context(), h.pool, h.scope, scopedpayment.AuditFilter(args))
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]auditOutput, 0, len(rows))
	for _, row := range rows {
		if !row.Damaged.Valid || row.Damaged.Bool {
			authError(w, 500, "internal server error")
			return
		}
		amount, err := billNumeric(row.Amount, true)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		reasons := row.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		out = append(out, auditOutput{row.LogID, row.PaymentID, row.Action, reasons, row.ActorUserID, row.ActorEmail, pythonTimestamp(row.CreatedAt), row.PayerName, amount, row.Currency, row.InvoiceID, row.IntakeSource})
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}
