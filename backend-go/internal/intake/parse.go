// Package intake extracts payment candidates without performing reconciliation.
package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const MaxInput = 1 << 20

var ErrInvalid = errors.New("invalid or ambiguous payment data")

type ParseError struct{ Field, Reason string }

func (e *ParseError) Error() string  { return fmt.Sprintf("%s: %s: %v", e.Field, e.Reason, ErrInvalid) }
func (e *ParseError) Unwrap() error  { return ErrInvalid }
func bad(field, reason string) error { return &ParseError{field, reason} }

type Payload struct {
	Amount                    decimal.Decimal
	Currency, PayerName       string
	PayerReference            *string
	PaymentDate               time.Time
	IntakeSource, ExternalRef string
}

var amountPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,14})(?:\.[0-9]{1,4})?$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func validateAmount(s string) (decimal.Decimal, error) {
	if !amountPattern.MatchString(s) {
		return decimal.Zero, bad("amount", "expected positive DECIMAL(19,4)")
	}
	d, err := decimal.NewFromString(s)
	if err != nil || !d.IsPositive() {
		return decimal.Zero, bad("amount", "expected positive DECIMAL(19,4)")
	}
	return d, nil
}
func validString(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func validateText(field, s string) error {
	if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 255 || !validString(s) {
		return bad(field, "missing or too long")
	}
	return nil
}
func build(amount, currency, payer, date, ref, source string, payerRef *string) (Payload, error) {
	d, err := validateAmount(amount)
	if err != nil {
		return Payload{}, err
	}
	if !regexp.MustCompile(`^[A-Za-z]{3}$`).MatchString(currency) {
		return Payload{}, bad("currency", "expected three ASCII letters")
	}
	currency = strings.ToUpper(currency)
	if !currencyPattern.MatchString(currency) {
		return Payload{}, bad("currency", "expected three letters")
	}
	for _, v := range []struct{ k, v string }{{"payer_name", payer}, {"external_ref", ref}} {
		if err := validateText(v.k, v.v); err != nil {
			return Payload{}, err
		}
	}
	if payerRef != nil {
		if err := validateText("payer_reference", *payerRef); err != nil {
			return Payload{}, err
		}
	}
	dt, err := time.Parse("2006-01-02", date)
	if err != nil || dt.Year() == 0 || dt.Format("2006-01-02") != date {
		return Payload{}, bad("payment_date", "expected ISO date")
	}
	return Payload{d, currency, payer, payerRef, dt, source, ref}, nil
}

// validSurrogates checks JSON string escapes before encoding/json can replace lone surrogates.
func validSurrogates(b []byte) bool {
	inString := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return false
		}
		if b[i] != 'u' {
			continue
		}
		if i+4 >= len(b) {
			return false
		}
		n, err := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		}
	}
	return true
}

func jsonObject(data []byte) (map[string]json.RawMessage, error) {
	if len(data) > MaxInput {
		return nil, bad("body", "too large")
	}
	if !utf8.Valid(data) || !validSurrogates(data) {
		return nil, bad("body", "invalid Unicode")
	}
	// Reject duplicate keys: encoding/json otherwise silently keeps the last value.
	dec := json.NewDecoder(strings.NewReader(string(data)))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, bad("body", "expected object")
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		t, e := dec.Token()
		if e != nil {
			return nil, bad("body", "invalid JSON")
		}
		k, ok := t.(string)
		if !ok {
			return nil, bad("body", "invalid key")
		}
		if _, exists := fields[k]; exists {
			return nil, bad(k, "duplicate field")
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return nil, bad(k, "invalid JSON")
		}
		fields[k] = raw
	}
	if _, err = dec.Token(); err != nil {
		return nil, bad("body", "invalid JSON")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, bad("body", "trailing JSON")
	}
	return fields, nil
}

// ParseGenericWebhook accepts a JSON object; numeric amounts are decoded exactly.
func ParseGenericWebhook(data []byte) (Payload, error) {
	fields, err := jsonObject(data)
	if err != nil {
		return Payload{}, err
	}
	get := func(k string, numeric bool) (string, error) {
		raw, ok := fields[k]
		if !ok {
			return "", bad(k, "missing")
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s, nil
		}
		if numeric {
			var n json.Number
			if json.Unmarshal(raw, &n) == nil {
				return n.String(), nil
			}
		}
		return "", bad(k, "invalid type")
	}
	vals := make([]string, 5)
	for i, k := range []string{"amount", "currency", "payer_name", "payment_date", "external_ref"} {
		vals[i], err = get(k, i == 0)
		if err != nil {
			return Payload{}, err
		}
	}
	var pref *string
	if raw, ok := fields["payer_reference"]; ok && string(raw) != "null" {
		s, e := get("payer_reference", false)
		if e != nil {
			return Payload{}, e
		}
		pref = &s
	}
	return build(vals[0], vals[1], vals[2], vals[3], vals[4], "WEBHOOK", pref)
}

type Email struct {
	From, PayerName, Subject, Text, HTML, PaymentDate, MessageID string
	Attachments                                                  [][]byte
}

// ParseEmailWebhook decodes the inbound provider's JSON envelope. Attachment
// content is passed unchanged to OCR, matching the Python adapter's contract.
func ParseEmailWebhook(ctx context.Context, data []byte, ocr OCR) (Payload, error) {
	fields, err := jsonObject(data)
	if err != nil {
		return Payload{}, err
	}
	var email Email
	for _, field := range []struct {
		name string
		dst  *string
	}{
		{"from", &email.From}, {"payer_name", &email.PayerName}, {"subject", &email.Subject},
		{"text", &email.Text}, {"html", &email.HTML}, {"payment_date", &email.PaymentDate},
		{"message_id", &email.MessageID},
	} {
		if raw, present := fields[field.name]; present && json.Unmarshal(raw, field.dst) != nil {
			return Payload{}, bad(field.name, "expected string")
		}
	}
	if raw, present := fields["attachments"]; present {
		var attachments []json.RawMessage
		if json.Unmarshal(raw, &attachments) != nil || len(attachments) > 100 {
			return Payload{}, bad("attachments", "expected up to 100 attachments")
		}
		for _, attachment := range attachments {
			fields, err := jsonObject(attachment)
			if err != nil {
				return Payload{}, err
			}
			raw, present := fields["content"]
			if !present || string(raw) == "null" {
				continue
			}
			var content string
			if json.Unmarshal(raw, &content) != nil {
				return Payload{}, bad("attachments", "expected string content")
			}
			email.Attachments = append(email.Attachments, []byte(content))
		}
	}
	return ParseInboundEmail(ctx, email, ocr)
}

// OCR must honor context cancellation and must not return more than maxOutput bytes.
type OCR func(ctx context.Context, input []byte, maxOutput int) (string, error)

var amountRE = regexp.MustCompile(`(?i)\b(?:amount|total|paid)\s*[:=]?\s*([A-Z]{3})?\s*([0-9,]+\.[0-9]{2})\b`)
var amountLabelRE = regexp.MustCompile(`(?i)\b(?:amount|total|paid)\b`)
var refRE = regexp.MustCompile(`(?i)\b(?:reference|ref|txn|id)\s*[:#]\s*([A-Za-z0-9-]{4,})\b`)
var refLabelRE = regexp.MustCompile(`(?i)\b(?:reference|ref|txn|id)\b`)
var grouped = regexp.MustCompile(`^(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)\.[0-9]{2}$`)

// ParseInboundEmail extracts only unambiguous explicit data. OCR is injected by the caller.
func ParseInboundEmail(ctx context.Context, e Email, ocr OCR) (Payload, error) {
	size := len(e.Subject) + len(e.Text) + 1 + len(e.HTML) + len(e.From) + len(e.PayerName) + len(e.PaymentDate) + len(e.MessageID)
	if size > MaxInput {
		return Payload{}, bad("body", "too large")
	}
	if len(e.Attachments) > 100 {
		return Payload{}, bad("attachments", "too many")
	}
	for _, a := range e.Attachments {
		size += len(a) + 1 // Account for the separator before each OCR result.
		if size > MaxInput {
			return Payload{}, bad("body", "too large")
		}
	}
	for _, v := range []string{e.From, e.PayerName, e.Subject, e.Text, e.HTML, e.PaymentDate, e.MessageID} {
		if !validString(v) {
			return Payload{}, bad("body", "invalid text")
		}
	}
	corpus := strings.Join([]string{e.Subject, e.Text}, "\x00")
	for _, a := range e.Attachments {
		if size > MaxInput {
			return Payload{}, bad("body", "too large")
		}
		if ocr == nil {
			return Payload{}, bad("attachments", "OCR not configured")
		}
		if err := ctx.Err(); err != nil {
			return Payload{}, err
		}
		text, err := ocr(ctx, a, MaxInput-size)
		if err != nil {
			return Payload{}, fmt.Errorf("OCR: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Payload{}, err
		}
		if !validString(text) {
			return Payload{}, bad("body", "invalid OCR text")
		}
		size += len(text)
		if size > MaxInput {
			return Payload{}, bad("body", "too large")
		}
		corpus += "\x00" + text
	}
	if size > MaxInput {
		return Payload{}, bad("body", "too large")
	}
	// Accept payment amounts only as complete structured lines, optionally followed by a reference.
	lineGrammar := regexp.MustCompile(`(?i)^[\t ]*(?:amount|total|paid)[\t ]*[:=]?[\t ]*[A-Z]{3}[\t ]*[0-9,]+\.[0-9]{2}(?:[\t ]+(?:reference|ref|txn|id)[\t ]*[:#][\t ]*[A-Za-z0-9-]{4,})?[\t ]*$`)
	refLine := regexp.MustCompile(`(?i)^[\t ]*(?:reference|ref|txn|id)[\t ]*[:#][\t ]*[A-Za-z0-9-]{4,}[\t ]*$`)
	for _, line := range strings.FieldsFunc(corpus, func(r rune) bool { return r == '\n' || r == '\r' || r == 0 }) {
		if refLabelRE.MatchString(line) && !refLine.MatchString(line) && !lineGrammar.MatchString(line) {
			return Payload{}, bad("reference", "expected structured reference line")
		}
		if amountLabelRE.MatchString(line) && !lineGrammar.MatchString(line) {
			return Payload{}, bad("amount", "expected structured payment line")
		}
	}
	matches := amountRE.FindAllStringSubmatch(corpus, 2)
	if len(matches) != 1 || len(amountLabelRE.FindAllStringIndex(corpus, 2)) != 1 {
		return Payload{}, bad("amount", "expected exactly one amount")
	}
	m := matches[0]
	if m[1] == "" || !grouped.MatchString(m[2]) {
		return Payload{}, bad("amount", "currency or number is ambiguous")
	}
	refs := refRE.FindAllStringSubmatch(corpus, 2)
	if len(refs) > 1 || len(refLabelRE.FindAllStringIndex(corpus, 2)) != len(refs) {
		return Payload{}, bad("reference", "multiple references")
	}
	var pref *string
	if len(refs) == 1 {
		ri := refRE.FindStringIndex(corpus)
		if ri[1] < len(corpus) && strings.ContainsRune("/.-_", rune(corpus[ri[1]])) {
			return Payload{}, bad("reference", "ambiguous token")
		}
		pref = &refs[0][1]
	}
	payer := e.From
	if payer == "" {
		payer = e.PayerName
	}
	ref := e.MessageID
	if ref == "" && pref != nil {
		ref = *pref
	}
	return build(strings.ReplaceAll(m[2], ",", ""), m[1], payer, e.PaymentDate, ref, "EMAIL_PARSER", pref)
}
