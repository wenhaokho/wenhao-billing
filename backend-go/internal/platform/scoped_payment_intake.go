package platform

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/intake"
)

// PaymentIntakeConfig supplies credentials and an optional bounded OCR adapter
// to the staged router. Empty credentials disable the corresponding endpoint.
type PaymentIntakeConfig struct {
	WebhookSecret string
	EmailToken    string
	OCR           intake.OCR
}

func (h scopedProfileRouter) paymentIntake(email bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := h.cfg.PaymentIntake
		if email {
			tokens := r.Header.Values("X-Email-Token")
			if cfg.EmailToken == "" || len(tokens) != 1 || !hmac.Equal([]byte(tokens[0]), []byte(cfg.EmailToken)) {
				authError(w, 401, "invalid email webhook token")
				return
			}
		} else {
			signatures := r.Header.Values("X-Signature")
			if len(signatures) == 0 {
				authError(w, 400, "missing X-Signature header")
				return
			}
			if cfg.WebhookSecret == "" || len(signatures) != 1 {
				authError(w, 401, "invalid webhook signature")
				return
			}
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, intake.MaxInput))
		if err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				authError(w, 413, "payment intake body too large")
			} else {
				authError(w, 400, "invalid payment intake body")
			}
			return
		}

		var payload intake.Payload
		if email {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			payload, err = intake.ParseEmailWebhook(ctx, body, cfg.OCR)
			cancel()
		} else {
			mac := hmac.New(sha256.New, []byte(cfg.WebhookSecret))
			_, _ = mac.Write(body)
			signature := strings.TrimSpace(r.Header.Get("X-Signature"))
			if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature)) {
				authError(w, 401, "invalid webhook signature")
				return
			}
			payload, err = intake.ParseGenericWebhook(body)
		}
		if err != nil {
			if errors.Is(err, intake.ErrInvalid) {
				authError(w, 400, "invalid or ambiguous payment data")
			} else {
				authError(w, 503, "payment proof extraction unavailable")
			}
			return
		}
		h.holdIntakePayment(w, r, payload, email)
	}
}

func (h scopedProfileRouter) holdIntakePayment(w http.ResponseWriter, r *http.Request, p intake.Payload, email bool) {
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbqueryv8.New(tx)
	var payerReference pgtype.Text
	if p.PayerReference != nil {
		payerReference = pgtype.Text{String: *p.PayerReference, Valid: true}
	}
	row, err := q.CreateHeldIntakePayment(ctx, dbqueryv8.CreateHeldIntakePaymentParams{
		BusinessID: h.scope.UUID(),
		Amount:     pgtype.Numeric{Int: p.Amount.Coefficient(), Exp: p.Amount.Exponent(), Valid: true},
		Currency:   p.Currency, PayerName: p.PayerName, PayerReference: payerReference,
		PaymentDate: pgtype.Date{Time: p.PaymentDate, Valid: true}, IntakeSource: p.IntakeSource,
		ExternalRef: pgtype.Text{String: p.ExternalRef, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		detail := "duplicate (intake_source, external_ref)"
		if email {
			detail = "duplicate email payment"
		}
		authError(w, 409, detail)
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	err = q.LogHeldIntakePayment(ctx, dbqueryv8.LogHeldIntakePaymentParams{
		BusinessID: h.scope.UUID(), PaymentID: row.PaymentID,
		Reasons: []string{"Automated reconciliation is unavailable. Operator review is required."},
	})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := paymentJSON(legacyPayment(row))
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	body, err := json.Marshal(out)
	if err != nil || tx.Commit(ctx) != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	_, _ = w.Write(body)
}
