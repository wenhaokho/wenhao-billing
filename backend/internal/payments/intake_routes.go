package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
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
		Middlewares: huma.Middlewares{r.webhookGuard(api)},
	}, r.intakeWebhook)
	huma.Register(api, huma.Operation{
		OperationID: "payments-intake-email", Method: http.MethodPost, Path: prefix + "/email", Tags: tags,
		Summary: "Receive a payment from an inbound email",
		Description: "X-Email-Token is EMAIL_WEBHOOK_TOKEN. The body is the inbound-mail JSON object (from, subject, text, html, " +
			"message_id, payment_date); the amount is read from the subject, text and html. An attachment with content is refused: no OCR backend is attached.",
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusConflict},
		RequestBody:   jsonObjectBody(), SkipValidateBody: true,
		Middlewares: huma.Middlewares{r.emailGuard(api)},
	}, r.intakeEmail)
}

// jsonObjectBody documents the body as a JSON object without making Huma
// validate it: the handlers check the signature or token first, and the
// parsers answer Python's sentences.
func jsonObjectBody() *huma.RequestBody {
	return &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: "object"}}}}
}

// maxIntakeBody is Huma's default body limit (1 MB), which these routes keep.
const maxIntakeBody = 1024 * 1024

type bodyKey struct{}

// intakeBody is the request body the guard middleware read and verified.
func intakeBody(ctx context.Context) []byte {
	b, _ := ctx.Value(bodyKey{}).([]byte)
	return b
}

// header is the first value of the request header name; present tells an
// empty header from an absent one (Huma's parameters cannot).
func header(ctx huma.Context, name string) (value string, present bool) {
	ctx.EachHeader(func(n, v string) {
		if !present && strings.EqualFold(n, name) {
			value, present = v, true
		}
	})
	return value, present
}

// guard is a middleware: the secret check and the body read run before Huma's
// own body handling, which would refuse an empty body with its own 400 before
// Python's 400 or 401 could answer. pre runs before the body is read, post
// after; each refuses with Python's sentence. The
// body bytes, read with the 1 MB limit, travel to the handler in the context.
func guard(api huma.API, pre func(ctx huma.Context) error, post func(ctx huma.Context, body []byte) error) func(huma.Context, func(huma.Context)) {
	refuse := func(ctx huma.Context, err error) {
		status := http.StatusInternalServerError
		var e interface{ GetStatus() int }
		if errors.As(err, &e) {
			status = e.GetStatus()
		}
		_ = huma.WriteErr(api, ctx, status, "", err)
	}
	return func(ctx huma.Context, next func(huma.Context)) {
		if pre != nil {
			if err := pre(ctx); err != nil {
				refuse(ctx, err)
				return
			}
		}
		var body []byte
		if r := ctx.BodyReader(); r != nil {
			var err error
			body, err = io.ReadAll(http.MaxBytesReader(nil, io.NopCloser(r), maxIntakeBody))
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				refuse(ctx, platform.Err(http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large"))
				return
			}
			if err != nil {
				refuse(ctx, platform.Err(http.StatusBadRequest, "unreadable_body", "cannot read request body"))
				return
			}
		}
		if post != nil {
			if err := post(ctx, body); err != nil {
				refuse(ctx, err)
				return
			}
		}
		next(huma.WithValue(ctx, bodyKey{}, body))
	}
}

// webhookGuard: X-Signature must be present (400), then equal the lowercase
// hex HMAC-SHA256 of the body (401). An empty header is present, so 401.
func (r routes) webhookGuard(api huma.API) func(huma.Context, func(huma.Context)) {
	return guard(api, func(ctx huma.Context) error {
		if _, ok := header(ctx, "X-Signature"); !ok {
			return platform.Err(http.StatusBadRequest, "missing_signature", "missing X-Signature header")
		}
		return nil
	}, func(ctx huma.Context, body []byte) error {
		sig, _ := header(ctx, "X-Signature")
		mac := hmac.New(sha256.New, []byte(r.webhookSecret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(strings.TrimSpace(sig))) {
			return platform.Err(http.StatusUnauthorized, "invalid_signature", "invalid webhook signature")
		}
		return nil
	})
}

// emailGuard: X-Email-Token must equal the configured token (401), in
// constant time.
func (r routes) emailGuard(api huma.API) func(huma.Context, func(huma.Context)) {
	return guard(api, func(ctx huma.Context) error {
		tok, _ := header(ctx, "X-Email-Token")
		if subtle.ConstantTimeCompare([]byte(tok), []byte(r.emailToken)) != 1 {
			return platform.Err(http.StatusUnauthorized, "invalid_email_token", "invalid email webhook token")
		}
		return nil
	}, nil)
}

func (r routes) intakeWebhook(ctx context.Context, _ *struct{}) (*paymentBody, error) {
	payload, err := ParseWebhook(intakeBody(ctx))
	if err != nil {
		return nil, intakeError(err)
	}
	return r.intake(ctx, payload, "duplicate (intake_source, external_ref)")
}

func (r routes) intakeEmail(ctx context.Context, _ *struct{}) (*paymentBody, error) {
	body, err := decodeObject(intakeBody(ctx), "")
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
