package payments

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Go's own tests of the two intake parsers: no Python test covers them
// (inventory section 3).

type absentKey struct{}

// absent in webhookJSON's set removes the key.
var absent = absentKey{}

// webhookJSON is a valid webhook body changed by set.
func webhookJSON(t *testing.T, set map[string]any) []byte {
	t.Helper()
	m := map[string]any{
		"amount": "1500000", "currency": "idr", "payer_name": "Acme", "payment_date": "2026-04-15", "external_ref": "EXT-1",
	}
	for k, v := range set {
		if v == absent {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// refused is err's 400 sentence, failing the test when it is not an ErrIntake.
func refused(t *testing.T, err error) string {
	t.Helper()
	var e ErrIntake
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want an ErrIntake", err)
	}
	return e.Msg
}

func wantRefusal(t *testing.T, err error, sentence string) {
	t.Helper()
	if got := refused(t, err); got != sentence {
		t.Errorf("sentence %q, want %q", got, sentence)
	}
}

func TestParseWebhookValid(t *testing.T) {
	t.Parallel()
	p, err := ParseWebhook(webhookJSON(t, map[string]any{"payer_reference": "INV-9"}))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Amount.Equal(dec("1500000")) || p.Currency != "IDR" || p.PayerName != "Acme" || p.IntakeSource != "WEBHOOK" ||
		p.ExternalRef != "EXT-1" || p.PayerReference == nil || *p.PayerReference != "INV-9" ||
		!p.PaymentDate.Equal(day(2026, 4, 15)) {
		t.Errorf("payload = %+v", p)
	}
}

func TestParseWebhookMissingFieldsSentence(t *testing.T) {
	t.Parallel()
	_, err := ParseWebhook(webhookJSON(t, map[string]any{"external_ref": absent, "amount": absent}))
	wantRefusal(t, err, "webhook payload missing fields: ['amount', 'external_ref']")
	_, err = ParseWebhook([]byte(`{}`))
	wantRefusal(t, err, "webhook payload missing fields: ['amount', 'currency', 'payer_name', 'payment_date', 'external_ref']")
}

func TestParseWebhookInvalidBodies(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ body, prefix string }{
		"not json": {`{"amount":`, "invalid JSON: "},
		"empty":    {``, "invalid JSON: "},
		"trailing": {`{} {}`, "invalid JSON: "},
		"array":    {`[]`, "webhook payload must be a JSON object"},
		"scalar":   {`5`, "webhook payload must be a JSON object"},
		"null":     {`null`, "webhook payload must be a JSON object"},
		"array of keys": {
			`["amount","currency","payer_name","payment_date","external_ref"]`,
			"webhook payload must be a JSON object",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWebhook([]byte(tc.body))
			if got := refused(t, err); !strings.HasPrefix(got, tc.prefix) {
				t.Errorf("sentence %q, want prefix %q", got, tc.prefix)
			}
		})
	}
}

func TestParseWebhookAmountForms(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		amount string // JSON text
		want   string // the amount, or "" for a refusal
		err    string
	}{
		"string":            {`"100.5"`, "100.5", ""},
		"number":            {`1500000`, "1500000", ""},
		"number fraction":   {`1500000.25`, "1500000.25", ""},
		"four places":       {`"0.0001"`, "0.0001", ""},
		"five places":       {`"1.23456"`, "", "invalid amount: '1.23456'"},
		"number 5 places":   {`1.23456`, "", "invalid amount: 1.23456"},
		"letters":           {`"abc"`, "", "invalid amount: 'abc'"},
		"empty":             {`""`, "", "invalid amount: ''"},
		"null":              {`null`, "", "invalid amount: None"},
		"true":              {`true`, "", "invalid amount: True"},
		"array":             {`[1]`, "", "invalid amount: [1]"},
		"zero":              {`0`, "", "amount must be greater than 0"},
		"zero string":       {`"0.00"`, "", "amount must be greater than 0"},
		"negative":          {`-5`, "", "amount must be greater than 0"},
		"negative string":   {`"-0.5"`, "", "amount must be greater than 0"},
		"16 integer digits": {`"1234567890123456"`, "", "invalid amount: '1234567890123456'"},
		"15 integer digits": {`"123456789012345"`, "123456789012345", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := strings.Replace(string(webhookJSON(t, nil)), `"amount":"1500000"`, `"amount":`+tc.amount, 1)
			p, err := ParseWebhook([]byte(body))
			if tc.err != "" {
				wantRefusal(t, err, tc.err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !p.Amount.Equal(dec(tc.want)) {
				t.Errorf("amount %s, want %s", p.Amount, tc.want)
			}
		})
	}
}

func TestParseWebhookNullFieldsAre400(t *testing.T) {
	t.Parallel()
	for field, want := range map[string]string{
		"currency":     "invalid currency: None",
		"payer_name":   "invalid payer_name: None",
		"payment_date": "invalid payment_date: None",
		"external_ref": "invalid external_ref: None",
	} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWebhook(webhookJSON(t, map[string]any{field: nil}))
			wantRefusal(t, err, want)
		})
	}
	for field, tc := range map[string]struct {
		value any
		want  string
	}{
		"payer_name":      {5, "invalid payer_name: 5"},
		"external_ref":    {true, "invalid external_ref: True"},
		"payer_reference": {[]string{"x"}, `invalid payer_reference: ["x"]`},
	} {
		t.Run("non-string "+field, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWebhook(webhookJSON(t, map[string]any{field: tc.value}))
			wantRefusal(t, err, tc.want)
		})
	}
	t.Run("null payer_reference is none", func(t *testing.T) {
		t.Parallel()
		p, err := ParseWebhook(webhookJSON(t, map[string]any{"payer_reference": nil}))
		if err != nil || p.PayerReference != nil {
			t.Errorf("payload %+v, err %v", p, err)
		}
	})
}

func TestParseWebhookTooLongTexts(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 256) // runes, not bytes
	for field, want := range map[string]string{
		"payer_name":      "payer_name is too long (max 255)",
		"payer_reference": "payer_reference is too long (max 255)",
		"external_ref":    "external_ref is too long (max 255)",
	} {
		_, err := ParseWebhook(webhookJSON(t, map[string]any{field: long}))
		wantRefusal(t, err, want)
	}
	if _, err := ParseWebhook(webhookJSON(t, map[string]any{"payer_name": long[:2*255]})); err != nil {
		t.Errorf("255 characters refused: %v", err)
	}
}

func TestParseWebhookBadDateIs400(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"x":          "Invalid isoformat string: 'x'",
		"2026-13-45": "Invalid isoformat string: '2026-13-45'",
		"2026-4-5":   "Invalid isoformat string: '2026-4-5'",
		"":           "Invalid isoformat string: ''",
	} {
		_, err := ParseWebhook(webhookJSON(t, map[string]any{"payment_date": in}))
		wantRefusal(t, err, want)
	}
	_, err := ParseWebhook(webhookJSON(t, map[string]any{"payment_date": 20260415}))
	wantRefusal(t, err, "invalid payment_date: 20260415")
}

func TestParseWebhookLongCurrencyIs400(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"USDX": "invalid currency: 'USDX'",
		"US":   "invalid currency: 'US'",
		"A1B":  "invalid currency: 'A1B'",
		"ß":    "invalid currency: 'ß'",
	} {
		_, err := ParseWebhook(webhookJSON(t, map[string]any{"currency": in}))
		wantRefusal(t, err, want)
	}
	_, err := ParseWebhook(webhookJSON(t, map[string]any{"currency": 123}))
	wantRefusal(t, err, "invalid currency: 123")
	p, err := ParseWebhook(webhookJSON(t, map[string]any{"currency": "usd"}))
	if err != nil || p.Currency != "USD" {
		t.Errorf("usd -> %q, %v", p.Currency, err)
	}
}

var emailToday = day(2026, 5, 2)

func TestParseEmailAmountRegex(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		text, currency, amount string
	}{
		"currency and thousands": {"Total paid: USD 1,234.56", "USD", "1234.56"},
		"equals sign":            {"amount=100.00", "IDR", "100.00"},
		"lower-case currency":    {"total sgd 5.50", "SGD", "5.50"},
		// (?i) lets any three letters be a currency: kept (inventory 5.10).
		"the is a currency": {"Amount: the 100.00", "THE", "100.00"},
		"first match wins":  {"Paid 10.00 and total 20.00", "IDR", "10.00"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := ParseEmail(map[string]any{"text": tc.text}, emailToday)
			if err != nil {
				t.Fatal(err)
			}
			if p.Currency != tc.currency || !p.Amount.Equal(dec(tc.amount)) {
				t.Errorf("%s %s, want %s %s", p.Currency, p.Amount, tc.currency, tc.amount)
			}
		})
	}
	t.Run("subject text html", func(t *testing.T) {
		t.Parallel()
		p, err := ParseEmail(map[string]any{"subject": "Payment", "text": "no figures", "html": "<b>Total 7.25</b>"}, emailToday)
		if err != nil || !p.Amount.Equal(dec("7.25")) {
			t.Errorf("html: %+v, %v", p, err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		for name, text := range map[string]string{"none": "hello", "no decimals": "Total 100", "one decimal": "Total 100.5"} {
			_, err := ParseEmail(map[string]any{"text": text}, emailToday)
			if err == nil {
				t.Errorf("%s: no error", name)
				continue
			}
			wantRefusal(t, err, "could not locate amount in email body or attachments")
		}
	})
	t.Run("bad amounts", func(t *testing.T) {
		t.Parallel()
		_, err := ParseEmail(map[string]any{"text": "Total 0.00"}, emailToday)
		wantRefusal(t, err, "amount must be greater than 0")
		_, err = ParseEmail(map[string]any{"text": "Total 1234567890123456.00"}, emailToday)
		wantRefusal(t, err, "invalid amount: '1234567890123456.00'")
	})
}

func TestParseEmailRefRegex(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ text, ref string }{
		"ref":       {"Payment ref: ABCD-1234 Total 10.00", "ABCD-1234"},
		"hash":      {"txn #98765 Total 10.00", "98765"},
		"too short": {"ref: ab Total 10.00", ""},
		// REF_RE has no word boundaries: "id" matches inside "said" (kept).
		"said thanks": {"He said thanks. Total 10.00", "thanks"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := ParseEmail(map[string]any{"text": tc.text}, emailToday)
			if err != nil {
				t.Fatal(err)
			}
			if tc.ref == "" {
				if p.PayerReference != nil {
					t.Errorf("payer_reference %q, want none", *p.PayerReference)
				}
				return
			}
			if p.PayerReference == nil || *p.PayerReference != tc.ref || p.ExternalRef != tc.ref {
				t.Errorf("payer_reference %v external_ref %q, want %q", p.PayerReference, p.ExternalRef, tc.ref)
			}
		})
	}
}

func TestParseEmailDefaults(t *testing.T) {
	t.Parallel()
	p, err := ParseEmail(map[string]any{"text": "Total 10.00"}, emailToday)
	if err != nil {
		t.Fatal(err)
	}
	if p.PayerName != "UNKNOWN" || p.ExternalRef != "" || p.PayerReference != nil || p.IntakeSource != "EMAIL_PARSER" ||
		!p.PaymentDate.Equal(emailToday) || p.Currency != "IDR" {
		t.Errorf("payload = %+v", p)
	}
	t.Run("given values", func(t *testing.T) {
		t.Parallel()
		// from beats payer_name; message_id beats the reference; payment_date is used.
		p, err := ParseEmail(map[string]any{
			"text": "ref: ABCD-1 Total 10.00", "from": "Pat <pat@x.test>", "payer_name": "Other", "message_id": "<m1@x>", "payment_date": "2026-03-01",
		}, emailToday)
		if err != nil {
			t.Fatal(err)
		}
		if p.PayerName != "Pat <pat@x.test>" || p.ExternalRef != "<m1@x>" || !p.PaymentDate.Equal(day(2026, 3, 1)) ||
			p.PayerReference == nil || *p.PayerReference != "ABCD-1" {
			t.Errorf("payload = %+v", p)
		}
	})
	t.Run("payer_name fallback", func(t *testing.T) {
		t.Parallel()
		p, err := ParseEmail(map[string]any{"text": "Total 10.00", "from": "", "payer_name": "Fallback"}, emailToday)
		if err != nil || p.PayerName != "Fallback" {
			t.Errorf("payload %+v, err %v", p, err)
		}
	})
	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		_, err := ParseEmail(map[string]any{"text": "Total 10.00", "payment_date": "soon"}, emailToday)
		wantRefusal(t, err, "Invalid isoformat string: 'soon'")
		_, err = ParseEmail(map[string]any{"text": "Total 10.00", "from": strings.Repeat("a", 256)}, emailToday)
		wantRefusal(t, err, "payer_name is too long (max 255)")
	})
}

func TestParseEmailAttachmentIs400(t *testing.T) {
	t.Parallel()
	const ocr = "Attach an OCR backend (tesseract/textract/etc) before calling"
	t.Run("with content", func(t *testing.T) {
		t.Parallel()
		// The OCR refusal comes before the amount search, as in Python.
		_, err := ParseEmail(map[string]any{
			"text": "Total 10.00", "attachments": []any{map[string]any{"filename": "a.pdf", "content": "QUJD"}},
		}, emailToday)
		wantRefusal(t, err, ocr)
		_, err = ParseEmail(map[string]any{"attachments": []any{map[string]any{"content": ""}}}, emailToday)
		wantRefusal(t, err, ocr)
	})
	t.Run("without content", func(t *testing.T) {
		t.Parallel()
		for _, a := range []any{nil, []any{}, []any{map[string]any{"filename": "a.pdf", "content": nil}}, []any{map[string]any{"filename": "a.pdf"}}} {
			if _, err := ParseEmail(map[string]any{"text": "Total 10.00", "attachments": a}, emailToday); err != nil {
				t.Errorf("attachments %v: %v", a, err)
			}
		}
	})
	t.Run("not a list", func(t *testing.T) {
		t.Parallel()
		_, err := ParseEmail(map[string]any{"text": "Total 10.00", "attachments": "x"}, emailToday)
		wantRefusal(t, err, "invalid attachments: 'x'")
	})
}
