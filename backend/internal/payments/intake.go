package payments

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ErrIntake is an intake body the parsers refuse: Msg is Python's 400
// sentence (or Go's, where inventory section 5 changes it) and Code is the
// stable snake_case code of that sentence.
type ErrIntake struct{ Code, Msg string }

func (e ErrIntake) Error() string { return e.Msg }

func intakeErr(code, format string, args ...any) error {
	return ErrIntake{Code: code, Msg: fmt.Sprintf(format, args...)}
}

const (
	sourceWebhook = "WEBHOOK"
	sourceEmail   = "EMAIL_PARSER"

	maxIntakeText = 255 // payer_name, payer_reference and external_ref are varchar(255)

	ocrSentence = "Attach an OCR backend (tesseract/textract/etc) before calling"
)

// The email regexes of services/intake/email_ocr.py:18-19, verbatim. (?i)
// makes [A-Z]{3} any three letters, and REF_RE has no word boundaries.
var (
	amountRE = regexp.MustCompile(`(?i)(?:amount|total|paid)\s*[:=]?\s*([A-Z]{3})?\s*([\d,]+\.\d{2})`)
	refRE    = regexp.MustCompile(`(?i)(?:ref|reference|txn|id)\s*[:#]?\s*([A-Za-z0-9\-]{4,})`)
)

// webhookKeys are the required keys of the generic webhook, in Python's order.
var webhookKeys = []string{"amount", "currency", "payer_name", "payment_date", "external_ref"}

// decodeObject reads body as one JSON object, numbers kept as written. The
// errors are 400 sentences: invalid JSON (Go's text, not Python's) or a body
// that is not an object.
func decodeObject(body []byte, notObject string) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, intakeErr("invalid_json", "invalid JSON: %s", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, intakeErr("invalid_json", "invalid JSON: unexpected data after the top-level value")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, intakeErr("body_not_object", "%s", notObject)
	}
	return obj, nil
}

// repr is Python's repr of a decoded JSON value, for the sentences that
// print the refused value; arrays and objects print as their JSON.
func repr(v any) string {
	switch v.(type) {
	case []any, map[string]any:
		b, _ := json.Marshal(v)
		return string(b)
	}
	return platform.PyValueRepr(v)
}

// ParseWebhook is parse_generic_webhook (services/intake/webhook.py) for the
// raw body: the keys amount, currency, payer_name, payment_date and
// external_ref are required (payer_reference is optional); every refusal is
// an ErrIntake. Go changes from Python: invalid JSON prints Go's text, and a
// bad value is a 400 sentence where Python answered 500 (inventory 5.7).
func ParseWebhook(body []byte) (IntakePayload, error) {
	obj, err := decodeObject(body, "webhook payload must be a JSON object")
	if err != nil {
		return IntakePayload{}, err
	}
	var missing []string
	for _, k := range webhookKeys {
		if _, ok := obj[k]; !ok {
			missing = append(missing, "'"+k+"'")
		}
	}
	if len(missing) > 0 {
		return IntakePayload{}, intakeErr("webhook_missing_fields", "webhook payload missing fields: [%s]", strings.Join(missing, ", "))
	}

	amount, err := amountValue(obj["amount"])
	if err != nil {
		return IntakePayload{}, err
	}
	currency, err := currencyValue(obj["currency"])
	if err != nil {
		return IntakePayload{}, err
	}
	payer, err := textValue("payer_name", obj["payer_name"])
	if err != nil {
		return IntakePayload{}, err
	}
	var payerRef *string
	if v := obj["payer_reference"]; v != nil {
		s, err := textValue("payer_reference", v)
		if err != nil {
			return IntakePayload{}, err
		}
		payerRef = &s
	}
	date, err := dateValue(obj["payment_date"])
	if err != nil {
		return IntakePayload{}, err
	}
	ref, err := textValue("external_ref", obj["external_ref"])
	if err != nil {
		return IntakePayload{}, err
	}
	return IntakePayload{
		Amount: amount, Currency: currency, PayerName: payer, PayerReference: payerRef,
		PaymentDate: date, IntakeSource: sourceWebhook, ExternalRef: ref,
	}, nil
}

// amountValue is a JSON string or number as an amount: through
// platform.ParseDec4 (a number by its literal text), greater than 0.
func amountValue(v any) (decimal.Decimal, error) {
	var text string
	switch x := v.(type) {
	case string:
		text = x
	case json.Number:
		text = x.String()
	default:
		return decimal.Zero, intakeErr("invalid_amount", "invalid amount: %s", repr(v))
	}
	return positiveAmount(text, repr(v))
}

func positiveAmount(text, shown string) (decimal.Decimal, error) {
	d, err := platform.ParseDec4(text)
	if err != nil {
		return decimal.Zero, intakeErr("invalid_amount", "invalid amount: %s", shown)
	}
	if d.Sign() <= 0 {
		return decimal.Zero, intakeErr("amount_not_positive", "amount must be greater than 0")
	}
	return d, nil
}

// currencyValue is a string of three letters, upper-cased.
func currencyValue(v any) (string, error) {
	s, ok := v.(string)
	if ok {
		s = platform.PyUpper(s)
		ok = len(s) == 3
		for _, r := range s {
			ok = ok && r >= 'A' && r <= 'Z'
		}
	}
	if !ok {
		return "", intakeErr("invalid_currency", "invalid currency: %s", repr(v))
	}
	return s, nil
}

// textValue is a string of at most 255 characters with no NUL (which
// Postgres cannot store).
func textValue(field string, v any) (string, error) {
	s, ok := v.(string)
	if !ok || strings.ContainsRune(s, 0) {
		return "", intakeErr("invalid_"+field, "invalid %s: %s", field, repr(v))
	}
	return s, checkLength(field, s)
}

func checkLength(field, s string) error {
	if utf8.RuneCountInString(s) > maxIntakeText {
		return intakeErr(field+"_too_long", "%s is too long (max %d)", field, maxIntakeText)
	}
	return nil
}

// dateValue is a YYYY-MM-DD string. A bad string is Python's
// date.fromisoformat sentence.
func dateValue(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, intakeErr("invalid_payment_date", "invalid payment_date: %s", repr(v))
	}
	return isoDate(s)
}

func isoDate(s string) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil || len(s) != len(time.DateOnly) {
		return time.Time{}, intakeErr("invalid_payment_date", "Invalid isoformat string: %s", platform.PyRepr(s))
	}
	return d, nil
}

// ParseEmail is parse_inbound_email (services/intake/email_ocr.py) for the
// decoded body: the amount and currency come from the first AMOUNT_RE match
// in the subject, text and html, the reference from the first REF_RE match.
// An attachment with content needs an OCR backend, so it is refused. today
// (a calendar date) is the payment date when the body has none.
func ParseEmail(body map[string]any, today time.Time) (IntakePayload, error) {
	var parts []string
	for _, k := range []string{"subject", "text", "html"} {
		if s, ok := truthyString(body[k]); ok {
			parts = append(parts, s)
		}
	}
	corpus := strings.Join(parts, " ")
	if err := checkAttachments(body["attachments"]); err != nil {
		return IntakePayload{}, err
	}

	m := amountRE.FindStringSubmatch(corpus)
	if m == nil {
		return IntakePayload{}, intakeErr("amount_not_found", "could not locate amount in email body or attachments")
	}
	currency := "IDR"
	if m[1] != "" {
		currency = strings.ToUpper(m[1])
	}
	shown := strings.ReplaceAll(m[2], ",", "")
	amount, err := positiveAmount(shown, platform.PyRepr(shown))
	if err != nil {
		return IntakePayload{}, err
	}

	var ref *string
	if r := refRE.FindStringSubmatch(corpus); r != nil {
		ref = &r[1]
		if err := checkLength("payer_reference", r[1]); err != nil {
			return IntakePayload{}, err
		}
	}
	payer, ok := truthyString(body["from"])
	if !ok {
		if payer, ok = truthyString(body["payer_name"]); !ok {
			payer = "UNKNOWN"
		}
	}
	if strings.ContainsRune(payer, 0) {
		return IntakePayload{}, intakeErr("invalid_payer_name", "invalid payer_name: %s", platform.PyRepr(payer))
	}
	if err := checkLength("payer_name", payer); err != nil {
		return IntakePayload{}, err
	}

	date := today
	if raw := body["payment_date"]; truthy(raw) {
		if date, err = dateValue(raw); err != nil {
			return IntakePayload{}, err
		}
	}

	extRef, ok := truthyString(body["message_id"])
	if !ok && ref != nil {
		extRef = *ref
	}
	if strings.ContainsRune(extRef, 0) {
		return IntakePayload{}, intakeErr("invalid_external_ref", "invalid external_ref: %s", platform.PyRepr(extRef))
	}
	if err := checkLength("external_ref", extRef); err != nil {
		return IntakePayload{}, err
	}
	return IntakePayload{
		Amount: amount, Currency: currency, PayerName: payer, PayerReference: ref,
		PaymentDate: date, IntakeSource: sourceEmail, ExternalRef: extRef,
	}, nil
}

// checkAttachments refuses an attachment that has content: Python's OCR stub
// raised for it. Attachments that are absent or falsy are none.
func checkAttachments(v any) error {
	if !truthy(v) {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return intakeErr("invalid_attachments", "invalid attachments: %s", repr(v))
	}
	for _, a := range list {
		att, ok := a.(map[string]any)
		if !ok {
			return intakeErr("invalid_attachments", "invalid attachments: %s", repr(v))
		}
		if att["content"] != nil {
			return intakeErr("ocr_not_configured", ocrSentence)
		}
	}
	return nil
}

// truthy is Python's truthiness of a decoded JSON value.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		d, err := decimal.NewFromString(x.String())
		return err != nil || !d.IsZero()
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// truthyString is str(v) of a truthy string, number or true; anything else
// is not one.
func truthyString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, x != ""
	case json.Number:
		return x.String(), truthy(x)
	case bool:
		return "True", x
	}
	return "", false
}
