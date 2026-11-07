package intake

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWebhook(t *testing.T) {
	valid := `{"amount":"123.4500","currency":"usd","payer_name":"Alice","payment_date":"2025-02-28","external_ref":"abc"}`
	p, err := ParseGenericWebhook([]byte(valid))
	if err != nil || p.Amount.String() != "123.45" || p.Currency != "USD" {
		t.Fatalf("%+v %v", p, err)
	}
	cases := []string{
		`{"amount":1,"amount":2}`, strings.Replace(valid, `123.4500`, `9999999999999999`, 1), strings.Replace(valid, `123.4500`, `0.00001`, 1), strings.Replace(valid, `123.4500`, `0`, 1), strings.Replace(valid, `"usd"`, `null`, 1), strings.Replace(valid, `2025-02-28`, `2025-02-29`, 1), valid + ` garbage`, strings.Repeat("x", MaxInput+1),
	}
	for _, s := range cases {
		if _, err := ParseGenericWebhook([]byte(s)); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %q: %v", s[:min(len(s), 100)], err)
		}
	}
}
func TestEmail(t *testing.T) {
	e := Email{From: "Alice", Text: "Amount: USD 1,234.50 Ref: AB-123", PaymentDate: "2025-01-02", MessageID: "msg"}
	p, err := ParseInboundEmail(context.Background(), e, nil)
	if err != nil || p.Amount.String() != "1234.5" || p.PayerReference == nil || *p.PayerReference != "AB-123" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, text := range []string{"Amount: 1.00", "Amount: USD 1.00 Total: USD 2.00", "Amount: USD 1,23.00", "Amount: USD 1.00 Ref: ABCD Ref: EFGH"} {
		e.Text = text
		if _, err := ParseInboundEmail(context.Background(), e, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %q: %v", text, err)
		}
	}
	e.Text = ""
	e.Attachments = [][]byte{[]byte("image")}
	p, err = ParseInboundEmail(context.Background(), e, func(_ context.Context, b []byte, _ int) (string, error) { return "Paid: SGD 5.00", nil })
	if err != nil || p.Currency != "SGD" {
		t.Fatalf("OCR: %+v %v", p, err)
	}
	if _, err = ParseInboundEmail(context.Background(), e, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// Compare representative supported inputs directly with the existing Python parser.
func TestPythonParity(t *testing.T) {
	python := os.Getenv("INTAKE_PYTHON")
	if python == "" {
		python = "python3"
	}
	script := `import json, sys, types
sys.path.insert(0, "../../../backend")
m = types.ModuleType("app.services.reconciliation")
class IntakePayload:
 def __init__(self, **kwargs): self.__dict__.update(kwargs)
m.IntakePayload = IntakePayload
sys.modules[m.__name__] = m
from app.services.intake.webhook import parse_generic_webhook
from app.services.intake.email_ocr import parse_inbound_email
w=parse_generic_webhook({'amount':'123.45','currency':'usd','payer_name':'Alice','payment_date':'2025-01-02','external_ref':'msg'})
e=parse_inbound_email({'from':'Alice','text':'Amount: USD 123.45 Ref: ABCD','payment_date':'2025-01-02','message_id':'msg'})
print(json.dumps([[str(x.amount),x.currency,x.payer_name,str(x.payment_date),x.external_ref,x.payer_reference] for x in (w,e)]))`
	out, err := exec.Command(python, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("python: %s %v", out, err)
	}
	w, err := ParseGenericWebhook([]byte(`{"amount":"123.45","currency":"usd","payer_name":"Alice","payment_date":"2025-01-02","external_ref":"msg"}`))
	if err != nil {
		t.Fatal(err)
	}
	e, err := ParseInboundEmail(context.Background(), Email{From: "Alice", Text: "Amount: USD 123.45 Ref: ABCD", PaymentDate: "2025-01-02", MessageID: "msg"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `[["` + w.Amount.String() + `", "` + w.Currency + `", "` + w.PayerName + `", "` + w.PaymentDate.Format("2006-01-02") + `", "` + w.ExternalRef + `", null], ["` + e.Amount.String() + `", "` + e.Currency + `", "` + e.PayerName + `", "` + e.PaymentDate.Format("2006-01-02") + `", "` + e.ExternalRef + `", "` + *e.PayerReference + `"]]`
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("Python: %s Go: %s", out, want)
	}
}

func TestUnsafeInputs(t *testing.T) {
	base := Email{From: "Alice", PaymentDate: "2025-01-02", MessageID: "msg"}
	for _, text := range []string{"Amount: USD 1.00.99", "Amount: USD 1.00 SGD", "Not paid: USD 1.00", "Payment not yet paid: USD 1.00", "Amount: USD 1.00\x00", "Amount: USD 1.00\xff", "Amount: USD 1.00 Ref: ABCD/EFGH"} {
		e := base
		e.Text = text
		if _, err := ParseInboundEmail(context.Background(), e, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %q: %v", text, err)
		}
	}
	e := base
	e.HTML = "<!-- Amount: USD 1.00 -->"
	if _, err := ParseInboundEmail(context.Background(), e, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("HTML match: %v", err)
	}
	e = base
	e.Subject = "Amount: USD"
	e.Text = "12.34"
	if _, err := ParseInboundEmail(context.Background(), e, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-source match: %v", err)
	}
	e = base
	e.Text = "Amount: USD 1.00 Reference: ABCD"
	p, err := ParseInboundEmail(context.Background(), e, nil)
	if err != nil || p.PayerReference == nil || *p.PayerReference != "ABCD" {
		t.Fatalf("reference: %+v %v", p, err)
	}
	for _, body := range []string{
		`{"amount":"1","currency":"USD","payer_name":"Alice","payment_date":"0000-01-01","external_ref":"msg"}`,
		`{"amount":"1","currency":"USD","payer_name":"A\u0000lice","payment_date":"2025-01-01","external_ref":"msg"}`,
		`{"amount":"1","currency":"USD","payer_name":"` + strings.Repeat("a", 256) + `","payment_date":"2025-01-01","external_ref":"msg"}`,
	} {
		if _, err := ParseGenericWebhook([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %s: %v", body, err)
		}
	}
	e = base
	e.Attachments = [][]byte{[]byte("a"), make([]byte, MaxInput)}
	called := false
	_, err = ParseInboundEmail(context.Background(), e, func(context.Context, []byte, int) (string, error) { called = true; return "", nil })
	if !errors.Is(err, ErrInvalid) || called {
		t.Fatalf("preflight: called=%v err=%v", called, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Attachments = [][]byte{[]byte("a")}
	_, err = ParseInboundEmail(ctx, e, func(context.Context, []byte, int) (string, error) {
		t.Fatal("called after cancellation")
		return "", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReferenceLinesAndUnicode(t *testing.T) {
	base := Email{From: "Alice", Text: "Amount: USD 1.00\nVia Bank", PaymentDate: "2025-01-02", MessageID: "msg"}
	if _, err := ParseInboundEmail(context.Background(), base, nil); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"Ref: ABCD@x", "Ref: ABCD世界", "Ref:\nABCD", "Ref: ABCD/EFGH"} {
		e := base
		e.Text += "\n" + ref
		if _, err := ParseInboundEmail(context.Background(), e, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %q: %v", ref, err)
		}
	}
	e := base
	e.HTML = strings.Repeat("x", MaxInput)
	if _, err := ParseInboundEmail(context.Background(), e, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize HTML accepted")
	}
	for _, name := range []string{`Alice \uD83D\uDE00`, `Alice \\uD800`} {
		body := `{"amount":"1","currency":"USD","payer_name":"` + name + `","payment_date":"2025-01-01","external_ref":"msg"}`
		if _, err := ParseGenericWebhook([]byte(body)); err != nil {
			t.Errorf("valid JSON %s: %v", name, err)
		}
	}
	for _, name := range []string{`\uD800`, `\uDC00`} {
		body := `{"amount":"1","currency":"USD","payer_name":"` + name + `","payment_date":"2025-01-01","external_ref":"msg"}`
		if _, err := ParseGenericWebhook([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid JSON %s: %v", name, err)
		}
	}
}

func TestCurrencyAndAggregateBounds(t *testing.T) {
	body := `{"amount":"1","currency":"uſd","payer_name":"Alice","payment_date":"2025-01-01","external_ref":"msg"}`
	if _, err := ParseGenericWebhook([]byte(body)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-ASCII currency: %v", err)
	}
	body = `{"amount":"1","currency":"USD","payer_name":"Alice �","payment_date":"2025-01-01","external_ref":"msg"}`
	if _, err := ParseGenericWebhook([]byte(body)); err != nil {
		t.Fatalf("literal replacement character: %v", err)
	}
	e := Email{Subject: strings.Repeat("a", MaxInput), Text: strings.Repeat("b", MaxInput)}
	if _, err := ParseInboundEmail(context.Background(), e, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("aggregate bound: %v", err)
	}
}
