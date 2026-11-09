package payments

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
)

// Ports of tests/integration/test_send_receipt.py and the two receipt tests
// of test_customer_cc.py (send_email there is monkeypatched; here a recording
// or failing email.Sender stands in), then Go's own.

type failingSender struct{}

func (failingSender) Send(context.Context, email.Message) error { return errors.New("resend down") }

type receiptEnv struct {
	*api
	mail *apitest.Mail // records, even when the sender fails
}

func newReceiptAPI(t *testing.T, failing bool) *receiptEnv {
	t.Helper()
	svc := Service{BaseCurrency: "IDR", Hosting: NoHosting{}}
	a := apitest.New(t, func(h huma.API, d apitest.Deps) {
		var sender email.Sender = d.Mail
		if failing {
			sender = failingSender{}
		}
		Register(h, d.Pool, svc, intakeConfig(d.Config), d.Clock, ReceiptDeps{Sender: sender, Renderer: StubRenderer{}})
	})
	return &receiptEnv{api: &api{env: &env{pool: a.Pool, svc: svc, admin: a.AdminID}, app: a}, mail: a.Mail}
}

func receiptPath(invoice, payment uuid.UUID) string {
	return "/api/v1/invoices/" + invoice.String() + "/payments/" + payment.String() + "/send-receipt"
}

// seed is Python's seed_invoice: Acme Pte Ltd and a SENT USD 1000 invoice
// INV-0001; a 400 payment dated 2026-08-07 is recorded against it.
func (r *receiptEnv) seed(t *testing.T) (invoice, customer uuid.UUID, payment PaymentOut) {
	t.Helper()
	customer = r.customer(t, "Acme Pte Ltd", "Acme")
	invoice = r.invoice(t, inv{customer: customer, currency: "USD", status: "SENT", balance: "1000.0000"})
	r.exec(t, `UPDATE invoices SET invoice_number = 'INV-0001' WHERE invoice_id = $1`, invoice)
	payment = r.mustRecord(t, invoice, map[string]any{"amount": "400", "payment_date": "2026-08-07"})
	return invoice, customer, payment
}

func (r *receiptEnv) send(t *testing.T, invoice, payment uuid.UUID, body map[string]any) int {
	t.Helper()
	return r.app.Do(t, http.MethodPost, receiptPath(invoice, payment), body).Code
}

func TestSendReceiptHappyPath(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	invoice, _, pay := r.seed(t)

	rec := r.app.Do(t, http.MethodPost, receiptPath(invoice, pay.PaymentID), map[string]any{"to_email": "acme@example.com", "cc_email": "me@example.com"})
	if got := apitest.Decode[SentTo](t, rec, http.StatusOK); got.SentTo != "acme@example.com" {
		t.Errorf("sent_to %q", got.SentTo)
	}
	msgs := r.mail.Messages()
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	m := msgs[0]
	if m.To != "acme@example.com" || len(m.Cc) != 1 || m.Cc[0] != "me@example.com" {
		t.Errorf("to %q cc %q", m.To, m.Cc)
	}
	if len(m.Attachments) != 1 {
		t.Fatalf("%d attachments", len(m.Attachments))
	}
	a := m.Attachments[0]
	if !strings.HasPrefix(a.Filename, "receipt-RCT-") || !strings.HasSuffix(a.Filename, ".pdf") || a.Filename != "receipt-"+ReceiptReference(pay.PaymentID)+".pdf" {
		t.Errorf("filename %q", a.Filename)
	}
	if !strings.HasPrefix(string(a.Content), "%PDF-") || a.ContentType != "application/pdf" {
		t.Errorf("attachment %q %.8q", a.ContentType, a.Content)
	}
	// 1000 - 400 leaves 600 remaining; both figures appear in the body.
	if !strings.Contains(m.Text, "400") || !strings.Contains(m.Text, "600") {
		t.Errorf("body %q", m.Text)
	}
}

func TestSendReceiptDefaultsToCustomerContactEmail(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	invoice, customer, pay := r.seed(t)
	r.exec(t, `UPDATE customers SET contact_email = 'billing@acme.example' WHERE customer_id = $1`, customer)

	for _, body := range []map[string]any{{}, {"to_email": ""}, {"to_email": nil}} {
		rec := r.app.Do(t, http.MethodPost, receiptPath(invoice, pay.PaymentID), body)
		if got := apitest.Decode[SentTo](t, rec, http.StatusOK); got.SentTo != "billing@acme.example" {
			t.Errorf("body %v: sent_to %q", body, got.SentTo)
		}
	}
	if msgs := r.mail.Messages(); len(msgs) != 3 || msgs[0].To != "billing@acme.example" {
		t.Errorf("messages %v", msgs)
	}
}

func TestSendReceiptNoRecipientIs400(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	invoice, _, pay := r.seed(t) // the customer has no contact email
	wantProblem(t, r.app.Do(t, http.MethodPost, receiptPath(invoice, pay.PaymentID), map[string]any{}), http.StatusBadRequest,
		"no_recipient", "no recipient: provide to_email or set a contact_email on the customer")
	if n := len(r.mail.Messages()); n != 0 {
		t.Errorf("%d messages", n)
	}
}

func TestSendReceiptPaymentNotOnInvoiceIs404(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	_, customer, pay := r.seed(t)
	other := r.invoice(t, inv{customer: customer, currency: "USD", status: "SENT", balance: "500.0000"})

	wantProblem(t, r.app.Do(t, http.MethodPost, receiptPath(other, pay.PaymentID), map[string]any{"to_email": "acme@example.com"}),
		http.StatusNotFound, "payment_not_on_invoice", "payment not found on this invoice")
	if n := len(r.mail.Messages()); n != 0 {
		t.Errorf("%d messages", n)
	}
}

func TestSendReceiptUnknownPaymentIs404(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	invoice, _, _ := r.seed(t)
	wantProblem(t, r.app.Do(t, http.MethodPost, receiptPath(invoice, uuid.New()), map[string]any{"to_email": "acme@example.com"}),
		http.StatusNotFound, "payment_not_on_invoice", "payment not found on this invoice")
}

func TestSendReceiptUnknownInvoiceIs404(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	_, _, pay := r.seed(t)
	wantProblem(t, r.app.Do(t, http.MethodPost, receiptPath(uuid.New(), pay.PaymentID), map[string]any{"to_email": "acme@example.com"}),
		http.StatusNotFound, "invoice_not_found", "invoice not found")
}

func TestSendReceiptEmailFailureIs502(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, true)
	invoice, _, pay := r.seed(t)
	wantProblem(t, r.app.Do(t, http.MethodPost, receiptPath(invoice, pay.PaymentID), map[string]any{"to_email": "acme@example.com"}),
		http.StatusBadGateway, "receipt_send_failed", "failed to send receipt email")
}

func TestSendReceiptMultipleCCNormalised(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	invoice, _, pay := r.seed(t)
	code := r.send(t, invoice, pay.PaymentID, map[string]any{"to_email": "acme@example.com", "cc_email": " a@example.com;b@example.com,, A@example.com "})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if cc := r.mail.Messages()[0].Cc; len(cc) != 2 || cc[0] != "a@example.com" || cc[1] != "b@example.com" {
		t.Errorf("cc %q", cc)
	}
}

func TestSendReceiptInvalidCCIs422(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	invoice, _, pay := r.seed(t)
	rec := r.app.Do(t, http.MethodPost, receiptPath(invoice, pay.PaymentID), map[string]any{"to_email": "acme@example.com", "cc_email": "a@example.com, nope"})
	p := apitest.Decode[problemBody](t, rec, http.StatusUnprocessableEntity)
	if p.Detail != "invalid email address: nope" || len(p.Errors) != 1 || p.Errors[0].Location != "cc_email" {
		t.Errorf("problem %+v", p)
	}
	if n := len(r.mail.Messages()); n != 0 {
		t.Errorf("%d messages", n)
	}
}

// Go change 7: a payment that is not CLEARED gets no receipt, though it
// carries the invoice of its best match.
func TestSendReceiptPendingPaymentIs404(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	invoice, _, pay := r.seed(t)
	for _, status := range []string{"PENDING_MANUAL_REVIEW", "FLAGGED"} {
		r.exec(t, `UPDATE payments SET status = $2 WHERE payment_id = $1`, pay.PaymentID, status)
		wantProblem(t, r.app.Do(t, http.MethodPost, receiptPath(invoice, pay.PaymentID), map[string]any{"to_email": "acme@example.com"}),
			http.StatusNotFound, "payment_not_on_invoice", "payment not found on this invoice")
	}
	if n := len(r.mail.Messages()); n != 0 {
		t.Errorf("%d messages", n)
	}
}

// TestSendReceiptBodyExact pins subject and text as inventory section 1 gives
// them, and that nothing is written.
func TestSendReceiptBodyExact(t *testing.T) {
	t.Parallel()
	r := newReceiptAPI(t, false)
	invoice, _, pay := r.seed(t)
	count := func() (n int) {
		t.Helper()
		const q = `SELECT (SELECT count(*) FROM payments) + (SELECT count(*) FROM reconciliation_log) + (SELECT count(*) FROM journal_entries)`
		if err := r.pool.QueryRow(t.Context(), q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()

	if r.send(t, invoice, pay.PaymentID, map[string]any{"to_email": "acme@example.com"}) != http.StatusOK {
		t.Fatal("not sent")
	}
	m := r.mail.Messages()[0]
	wantText := "Hi Acme Pte Ltd,\n\nWe have received your payment of 400.00 USD against invoice INV-0001 on 2026-08-07.\n" +
		"Remaining balance: 600.00 USD.\n\nA PDF receipt is attached for your records.\n\nThank you."
	if m.Subject != "Payment receipt for invoice INV-0001" || m.Text != wantText {
		t.Errorf("subject %q text %q", m.Subject, m.Text)
	}
	if after := count(); after != before {
		t.Errorf("rows %d, want %d: send-receipt wrote", after, before)
	}

	// Paid in full, no invoice number, an IDR amount: the other branches.
	r.exec(t, `UPDATE invoices SET invoice_number = NULL, balance_due = 0, currency = 'IDR' WHERE invoice_id = $1`, invoice)
	r.exec(t, `UPDATE payments SET currency = 'IDR', amount = 1234567.5 WHERE payment_id = $1`, pay.PaymentID)
	if r.send(t, invoice, pay.PaymentID, map[string]any{"to_email": "acme@example.com"}) != http.StatusOK {
		t.Fatal("not sent")
	}
	m = r.mail.Messages()[1]
	wantText = "Hi Acme Pte Ltd,\n\nWe have received your payment of 1,234,568 IDR against your invoice on 2026-08-07.\n" +
		"This invoice is now paid in full.\n\nA PDF receipt is attached for your records.\n\nThank you."
	if m.Subject != "Payment receipt" || m.Text != wantText {
		t.Errorf("subject %q text %q", m.Subject, m.Text)
	}
}
