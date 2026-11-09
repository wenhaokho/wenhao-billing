package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// registerIntake adds the two routes external services call: Python's
// intake_webhook and intake_email. Neither takes a session (they are public to
// the session middleware); the webhook is guarded by an HMAC of the body and
// the email by a shared token.
func (r routes) registerIntake(api huma.API) {
	const prefix = "/api/v1/payments/intake"
	tags := []string{platform.PublicTag, "payments"}
	huma.Register(api, huma.Operation{
		OperationID: "payments-intake-webhook", Method: http.MethodPost, Path: prefix + "/webhook", Tags: tags,
		Summary: "Receive a payment from a gateway webhook",
		Description: "X-Signature is the lowercase hex HMAC-SHA256 of the raw body under WEBHOOK_HMAC_SECRET. " +
			"The body is a JSON object with amount, currency, payer_name, payment_date and external_ref (and optional payer_reference). " +
			"The payment is matched against open invoices of its currency: a confident match is cleared, anything else is held for manual review.",
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusConflict},
		RequestBody:   jsonObjectBody(), SkipValidateBody: true,
	}, r.intakeWebhook)
	huma.Register(api, huma.Operation{
		OperationID: "payments-intake-email", Method: http.MethodPost, Path: prefix + "/email", Tags: tags,
		Summary: "Receive a payment from an inbound email",
		Description: "X-Email-Token is EMAIL_WEBHOOK_TOKEN. The body is the inbound-mail JSON object (from, subject, text, html, " +
			"message_id, payment_date); the amount is read from the subject, text and html. An attachment with content is refused: no OCR backend is attached.",
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusConflict},
		RequestBody:   jsonObjectBody(), SkipValidateBody: true,
	}, r.intakeEmail)
}

// jsonObjectBody documents the body as a JSON object without making Huma
// validate it: the handlers check the signature or token first, and the
// parsers answer Python's sentences.
func jsonObjectBody() *huma.RequestBody {
	return &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: "object"}}}}
}

// The headers are optional parameters so that a missing one is Python's 400 or
// 401 sentence, not Huma's 422, and the body is raw so that the HMAC covers
// the bytes sent and the checks run before any validation of it.
type webhookIn struct {
	Signature platform.OptionalParam[string] `header:"X-Signature"`
	RawBody   []byte
}

type emailIn struct {
	Token   platform.OptionalParam[string] `header:"X-Email-Token"`
	RawBody []byte
}

func (r routes) intakeWebhook(ctx context.Context, in *webhookIn) (*paymentBody, error) {
	if !in.Signature.Set {
		return nil, platform.Err(http.StatusBadRequest, "missing_signature", "missing X-Signature header")
	}
	mac := hmac.New(sha256.New, []byte(r.webhookSecret))
	mac.Write(in.RawBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(strings.TrimSpace(in.Signature.Value))) {
		return nil, platform.Err(http.StatusUnauthorized, "invalid_signature", "invalid webhook signature")
	}
	payload, err := ParseWebhook(in.RawBody)
	if err != nil {
		return nil, intakeError(err)
	}
	return r.intake(ctx, payload, "duplicate (intake_source, external_ref)")
}

func (r routes) intakeEmail(ctx context.Context, in *emailIn) (*paymentBody, error) {
	if !in.Token.Set || subtle.ConstantTimeCompare([]byte(in.Token.Value), []byte(r.emailToken)) != 1 {
		return nil, platform.Err(http.StatusUnauthorized, "invalid_email_token", "invalid email webhook token")
	}
	body, err := decodeObject(in.RawBody, "")
	if err != nil {
		sentence := "Input should be a valid dictionary"
		var e ErrIntake
		if errors.As(err, &e) && e.Code == "invalid_json" {
			sentence = "JSON decode error"
		}
		return nil, platform.FieldError("body", sentence)
	}
	now := r.clock.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	payload, err := ParseEmail(body, today)
	if err != nil {
		return nil, intakeError(err)
	}
	return r.intake(ctx, payload, "duplicate email payment")
}

// intakeError is a parser refusal as its 400.
func intakeError(err error) error {
	var e ErrIntake
	if errors.As(err, &e) {
		return platform.Err(http.StatusBadRequest, e.Code, e.Msg)
	}
	return err
}

// intake reconciles payload in one transaction. A repeated (source, ref) is a
// 409 with duplicate as its sentence: the violation aborts the transaction,
// which is rolled back.
func (r routes) intake(ctx context.Context, payload IntakePayload, duplicate string) (*paymentBody, error) {
	var out PaymentOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		p, err := r.svc.ProcessIncoming(ctx, tx, payload)
		if errors.Is(err, ErrDuplicate) {
			return clientRefuse(http.StatusConflict, "duplicate_payment", "%s", duplicate)
		}
		if err != nil {
			return err
		}
		out = PaymentOutFrom(p)
		return nil
	})
	if err != nil {
		return nil, clientError(err)
	}
	return &paymentBody{Body: out}, nil
}
