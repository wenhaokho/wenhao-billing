package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Go's own route tests of the two intake routes: no Python test covers their
// signature and token checks (inventory section 3). Every request is sent
// without a session cookie.

const (
	testWebhookSecret = "test-webhook-secret"
	testEmailToken    = "test-email-token"
	webhookPath       = "/api/v1/payments/intake/webhook"
	emailPath         = "/api/v1/payments/intake/email"
)

// intakeConfig is cfg with the test secrets.
func intakeConfig(cfg platform.Config) platform.Config {
	cfg.WebhookHMACSecret = testWebhookSecret
	cfg.EmailWebhookToken = testEmailToken
	return cfg
}

func sign(body string) string {
	m := hmac.New(sha256.New, []byte(testWebhookSecret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

// post sends body to path with headers and no session.
func (a *api) post(t *testing.T, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.app.Handler.ServeHTTP(rec, req)
	return rec
}

// webhook sends body signed correctly.
func (a *api) webhook(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return a.post(t, webhookPath, body, map[string]string{"X-Signature": sign(body)})
}

func (a *api) postEmail(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return a.post(t, emailPath, body, map[string]string{"X-Email-Token": testEmailToken})
}

// hook is a webhook body for the sent invoice's Acme Corp payment of amount.
func hook(amount, ref string) string {
	return `{"amount":"` + amount + `","currency":"usd","payer_name":"Acme Corp","payment_date":"2026-04-15","external_ref":"` + ref + `"}`
}

// wantNoPayments fails the test, once it and its subtests are done, when a
// payment was stored.
func (a *api) wantNoPayments(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if n := a.paymentCount(t); n != 0 {
			t.Errorf("%d payments stored", n)
		}
	})
}

func (a *api) paymentCount(t *testing.T) int {
	t.Helper()
	return a.count(t, `SELECT count(*) FROM payments`)
}

func TestWebhookMissingSignature400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.wantNoPayments(t)
	wantProblem(t, a.post(t, webhookPath, hook("1000.00", "E1"), nil), http.StatusBadRequest, "missing_signature", "missing X-Signature header")
	// Checked before the body: an unreadable body does not change it.
	wantProblem(t, a.post(t, webhookPath, `{`, nil), http.StatusBadRequest, "missing_signature", "missing X-Signature header")
}

func TestWebhookBadSignature401(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.wantNoPayments(t)
	a.sentInvoice(t)
	body := hook("1000.00", "E1")
	other := hmac.New(sha256.New, []byte("another secret"))
	other.Write([]byte(body))
	for name, sig := range map[string]string{
		"wrong":         "0000",
		"empty":         "",
		"wrong secret":  hex.EncodeToString(other.Sum(nil)),
		"signs another": sign(body + " "),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := a.post(t, webhookPath, body, map[string]string{"X-Signature": sig})
			wantProblem(t, rec, http.StatusUnauthorized, "invalid_signature", "invalid webhook signature")
		})
	}
}

func TestWebhookUppercaseHexIs401(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	body := hook("1000.00", "E1")
	rec := a.post(t, webhookPath, body, map[string]string{"X-Signature": strings.ToUpper(sign(body))})
	wantProblem(t, rec, http.StatusUnauthorized, "invalid_signature", "invalid webhook signature")
	// Surrounding blanks are trimmed, as Python's strip().
	rec = a.post(t, webhookPath, body, map[string]string{"X-Signature": "  " + sign(body) + "\t"})
	if rec.Code != http.StatusCreated {
		t.Errorf("trimmed signature: status %d: %s", rec.Code, rec.Body)
	}
}

func TestWebhookClearsAndPosts(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	invID := a.sentInvoice(t)
	rec := a.webhook(t, hook("1000.00", "E1"))
	p := apitest.Decode[PaymentOut](t, rec, http.StatusCreated)
	if p.Status != "CLEARED" || p.IntakeSource != "WEBHOOK" || p.Currency != "USD" || p.InvoiceID == nil || *p.InvoiceID != invID {
		t.Errorf("payment = %+v", p)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if got := apitest.Decode[map[string]string](t, rec, http.StatusCreated); got["amount"] != "1000.0000" {
		t.Errorf("amount %q, want 1000.0000", got["amount"])
	}
	checkInvoice(t, a.env, invID, "0", "0", "PAID")
	if es := a.entries(t, p.PaymentID); len(es) != 1 {
		t.Errorf("%d journal entries, want 1", len(es))
	}
	if ls := a.logs(t, p.PaymentID); len(ls) != 1 || ls[0].action != "CLEARED" || ls[0].actor != nil {
		t.Errorf("logs = %+v", ls)
	}
}

func TestWebhookAmbiguousIsHeld(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	c := a.customer(t, "Acme Corp", "Acme")
	var ids []uuid.UUID
	for range 2 {
		ids = append(ids, a.invoice(t, inv{
			customer: c, currency: "USD", status: "SENT", balance: "1000.0000", issue: day(2026, 4, 10), due: day(2026, 4, 24),
		}))
	}
	p := apitest.Decode[PaymentOut](t, a.webhook(t, hook("1000", "E1")), http.StatusCreated)
	if p.Status != "PENDING_MANUAL_REVIEW" {
		t.Errorf("status %s, want PENDING_MANUAL_REVIEW", p.Status)
	}
	for _, id := range ids {
		checkInvoice(t, a.env, id, "1000.0000", "0", "SENT")
	}
	ls := a.logs(t, p.PaymentID)
	if len(ls) != 1 || ls[0].action != "HELD" || !slices.ContainsFunc(ls[0].reasons, func(r string) bool { return strings.HasPrefix(r, "ambiguous match") }) {
		t.Errorf("logs = %+v", ls)
	}
}

func TestWebhookDuplicate409(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.sentInvoice(t)
	apitest.Decode[PaymentOut](t, a.webhook(t, hook("999.00", "SAME")), http.StatusCreated)
	wantProblem(t, a.webhook(t, hook("999.00", "SAME")), http.StatusConflict, "duplicate_payment", "duplicate (intake_source, external_ref)")
	if n := a.paymentCount(t); n != 1 {
		t.Errorf("%d payments, want 1", n)
	}
	// The same ref from another source is no duplicate.
	apitest.Decode[PaymentOut](t, a.postEmail(t, `{"text":"Total USD 999.00","message_id":"SAME"}`), http.StatusCreated)
}

func TestWebhookBadBodiesAre400(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.wantNoPayments(t)
	for name, tc := range map[string]struct{ body, code, detail string }{
		"not json":      {`{`, "invalid_json", ""},
		"array":         {`[]`, "body_not_object", "webhook payload must be a JSON object"},
		"missing":       {`{"currency":"USD","payer_name":"A","payment_date":"2026-04-15"}`, "webhook_missing_fields", "webhook payload missing fields: ['amount', 'external_ref']"},
		"bad amount":    {hook("abc", "E1"), "invalid_amount", "invalid amount: 'abc'"},
		"zero amount":   {hook("0", "E1"), "amount_not_positive", "amount must be greater than 0"},
		"long currency": {strings.Replace(hook("5", "E1"), `"usd"`, `"USDX"`, 1), "invalid_currency", "invalid currency: 'USDX'"},
		"null name":     {strings.Replace(hook("5", "E1"), `"Acme Corp"`, `null`, 1), "invalid_payer_name", "invalid payer_name: None"},
		"bad date":      {strings.Replace(hook("5", "E1"), `2026-04-15`, `x`, 1), "invalid_payment_date", "Invalid isoformat string: 'x'"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := a.webhook(t, tc.body)
			p := apitest.Decode[problemBody](t, rec, http.StatusBadRequest)
			if p.Code != tc.code || (tc.detail != "" && p.Detail != tc.detail) || p.Detail == "" {
				t.Errorf("problem = %+v, want %s %q", p, tc.code, tc.detail)
			}
		})
	}
}

func TestEmailTokenRequired401(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.wantNoPayments(t)
	const body = `{"text":"Total 10.00"}`
	for name, headers := range map[string]map[string]string{
		"missing":       nil,
		"wrong":         {"X-Email-Token": "nope"},
		"empty":         {"X-Email-Token": ""},
		"prefix":        {"X-Email-Token": testEmailToken[:5]},
		"longer":        {"X-Email-Token": testEmailToken + "x"},
		"the signature": {"X-Signature": sign(body)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := a.post(t, emailPath, body, headers)
			wantProblem(t, rec, http.StatusUnauthorized, "invalid_email_token", "invalid email webhook token")
		})
	}
	// Dependency errors come before body validation.
	wantProblem(t, a.post(t, emailPath, `[]`, nil), http.StatusUnauthorized, "invalid_email_token", "invalid email webhook token")
}

func TestEmailBadBodies(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.wantNoPayments(t)
	for name, body := range map[string]string{"array": `[]`, "scalar": `"x"`, "null": `null`, "not json": `{`} {
		t.Run("422 "+name, func(t *testing.T) {
			t.Parallel()
			rec := a.postEmail(t, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
	for name, tc := range map[string]struct{ body, detail string }{
		"attachment": {`{"text":"Total 10.00","attachments":[{"filename":"a.pdf","content":"QUJD"}]}`, "Attach an OCR backend (tesseract/textract/etc) before calling"},
		"no amount":  {`{"text":"hello"}`, "could not locate amount in email body or attachments"},
		"bad date":   {`{"text":"Total 10.00","payment_date":"soon"}`, "Invalid isoformat string: 'soon'"},
	} {
		t.Run("400 "+name, func(t *testing.T) {
			t.Parallel()
			rec := a.postEmail(t, tc.body)
			if p := apitest.Decode[problemBody](t, rec, http.StatusBadRequest); p.Detail != tc.detail {
				t.Errorf("detail %q, want %q", p.Detail, tc.detail)
			}
		})
	}
}

func TestEmailHeldWithoutCandidates(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	rec := a.postEmail(t, `{"from":"Pat","subject":"Payment ref: ABCD-77","text":"Total paid: USD 1,234.56"}`)
	p := apitest.Decode[PaymentOut](t, rec, http.StatusCreated)
	if p.Status != "PENDING_MANUAL_REVIEW" || p.IntakeSource != "EMAIL_PARSER" || p.Currency != "USD" || p.PayerName != "Pat" ||
		p.InvoiceID != nil || p.PayerReference == nil || *p.PayerReference != "ABCD-77" ||
		p.ExternalRef == nil || *p.ExternalRef != "ABCD-77" {
		t.Errorf("payment = %+v", p)
	}
	// The clock is at 2026-01-15 09:00 UTC (apitest.Start).
	if p.PaymentDate.Format("2006-01-02") != "2026-01-15" {
		t.Errorf("payment_date %s, want the clock's date", p.PaymentDate.Format("2006-01-02"))
	}
	if ls := a.logs(t, p.PaymentID); len(ls) != 1 || ls[0].action != "HELD" {
		t.Errorf("logs = %+v", ls)
	}
	if got := apitest.Decode[map[string]string](t, rec, http.StatusCreated)["amount"]; got != "1234.5600" {
		t.Errorf("amount %q, want 1234.5600", got)
	}
}

func TestEmailEmptyRefSecondIs409(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	// No message_id and no reference: external_ref is "" and the second such
	// email is a duplicate (kept, inventory 5.10).
	const body = `{"text":"Amount: 10.00"}`
	p := apitest.Decode[PaymentOut](t, a.postEmail(t, body), http.StatusCreated)
	if p.ExternalRef == nil || *p.ExternalRef != "" {
		t.Errorf("external_ref %v, want an empty string", p.ExternalRef)
	}
	wantProblem(t, a.postEmail(t, body), http.StatusConflict, "duplicate_payment", "duplicate email payment")
	if n := a.paymentCount(t); n != 1 {
		t.Errorf("%d payments, want 1", n)
	}
}

// The checks run before Huma's body handling: an empty body must not turn
// them into Huma's 400 "request body is required".
func TestIntakeChecksComeBeforeBodyHandling(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	a.wantNoPayments(t)
	t.Run("webhook empty body bad signature", func(t *testing.T) {
		t.Parallel()
		rec := a.post(t, webhookPath, "", map[string]string{"X-Signature": "0000"})
		wantProblem(t, rec, http.StatusUnauthorized, "invalid_signature", "invalid webhook signature")
	})
	t.Run("webhook empty body no signature", func(t *testing.T) {
		t.Parallel()
		wantProblem(t, a.post(t, webhookPath, "", nil), http.StatusBadRequest, "missing_signature", "missing X-Signature header")
	})
	t.Run("webhook empty body empty signature", func(t *testing.T) {
		t.Parallel()
		rec := a.post(t, webhookPath, "", map[string]string{"X-Signature": ""})
		wantProblem(t, rec, http.StatusUnauthorized, "invalid_signature", "invalid webhook signature")
	})
	t.Run("webhook empty body signed is a 400 on the body", func(t *testing.T) {
		t.Parallel()
		rec := a.post(t, webhookPath, "", map[string]string{"X-Signature": sign("")})
		if p := apitest.Decode[problemBody](t, rec, http.StatusBadRequest); p.Code != "invalid_json" {
			t.Errorf("problem = %+v", p)
		}
	})
	t.Run("email empty body no token", func(t *testing.T) {
		t.Parallel()
		wantProblem(t, a.post(t, emailPath, "", nil), http.StatusUnauthorized, "invalid_email_token", "invalid email webhook token")
	})
	t.Run("email empty body wrong token", func(t *testing.T) {
		t.Parallel()
		rec := a.post(t, emailPath, "", map[string]string{"X-Email-Token": "nope"})
		wantProblem(t, rec, http.StatusUnauthorized, "invalid_email_token", "invalid email webhook token")
	})
}
