package invoices

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
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Ports of the two send-body tests of
// tests/integration/test_record_payment_withholding.py, then Go's own for the
// PDF and send routes (inventory section 1).

type failingMail struct{}

func (failingMail) Send(context.Context, email.Message) error { return errors.New("resend down") }

// newDocApp serves the invoice routes and the document routes; failing makes
// every send fail.
func newDocApp(t *testing.T, failing bool) *apitest.App {
	t.Helper()
	return newDocAppWith(t, func(d apitest.Deps) email.Sender {
		if failing {
			return failingMail{}
		}
		return d.Mail
	})
}

func newDocAppWith(t *testing.T, mail func(d apitest.Deps) email.Sender) *apitest.App {
	t.Helper()
	return apitest.New(t, func(api huma.API, d apitest.Deps) {
		Register(api, d.Pool, d.Clock, nil)
		RegisterDocuments(api, d.Pool, d.Clock, mail(d))
	})
}

// voidingMail voids every invoice as it "sends", as a concurrent void would
// while the email is out.
type voidingMail struct{ d apitest.Deps }

func (m voidingMail) Send(ctx context.Context, _ email.Message) error {
	_, err := m.d.Pool.Exec(ctx, `UPDATE invoices SET status = 'VOID', balance_due = 0`)
	return err
}

type docInv struct {
	id       uuid.UUID // zero: a new one
	number   *string
	status   string
	amount   string
	balance  string
	wht      string // wht_type, "" for NONE
	whtAmt   string
	customer *uuid.UUID
	due      string
	template bool
}

func (a docInv) insert(t *testing.T, app *apitest.App) uuid.UUID {
	t.Helper()
	id := a.id
	if id == uuid.Nil {
		id = uuid.New()
	}
	if a.status == "" {
		a.status = "SENT"
	}
	if a.amount == "" {
		a.amount = "1000000"
	}
	if a.balance == "" {
		a.balance = a.amount
	}
	if a.wht == "" {
		a.wht = "NONE"
	}
	if a.whtAmt == "" {
		a.whtAmt = "0"
	}
	var due *string
	if a.due != "" {
		due = &a.due
	}
	if _, err := app.Pool.Exec(t.Context(), `INSERT INTO invoices (invoice_id, customer_id, invoice_type, invoice_number, currency, amount,
balance_due, status, wht_type, wht_amount, due_date, is_template) VALUES ($1, $2, 'MILESTONE', $3, 'IDR', $4, $5, $6, $7, $8, $9, $10)`,
		id, a.customer, a.number, a.amount, a.balance, a.status, a.wht, a.whtAmt, due, a.template); err != nil {
		t.Fatal(err)
	}
	return id
}

func docCustomer(t *testing.T, app *apitest.App, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var e *string
	if email != "" {
		e = &email
	}
	if _, err := app.Pool.Exec(t.Context(), `INSERT INTO customers (customer_id, name, contact_email) VALUES ($1, 'Acme Pte Ltd', $2)`, id, e); err != nil {
		t.Fatal(err)
	}
	return id
}

func str(s string) *string { return &s }

func sendPath(id uuid.UUID) string { return "/api/v1/invoices/" + id.String() + "/send" }
func pdfPath(id uuid.UUID) string  { return "/api/v1/invoices/" + id.String() + "/pdf" }

func status(t *testing.T, app *apitest.App, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := app.Pool.QueryRow(t.Context(), `SELECT status FROM invoices WHERE invoice_id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func onlyMessage(t *testing.T, app *apitest.App) email.Message {
	t.Helper()
	msgs := app.Mail.Messages()
	if len(msgs) != 1 {
		t.Fatalf("%d messages, want 1", len(msgs))
	}
	return msgs[0]
}

func TestSendInvoiceBodyQuotesAmountToTransfer(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{number: str("WH20260001"), amount: "10204082", wht: "PPH23", whtAmt: "204082"}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "ap@acme.example"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := onlyMessage(t, app).Text
	for _, want := range []string{"for 10,204,082 IDR.", "Amount to transfer (after withholding tax): 10,000,000 IDR."} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q: %q", want, body)
		}
	}
}

func TestSendInvoiceBodyWithoutWHTUnchanged(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{id: uuid.MustParse("11111111-2222-3333-4444-555555555555"), number: str("WH20260002"), due: "2026-12-31"}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "ap@acme.example"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	m := onlyMessage(t, app)
	if strings.Contains(m.Text, "Amount to transfer") {
		t.Errorf("body mentions the transfer: %q", m.Text)
	}
	want := "Hi there,\n\nPlease find attached invoice WH20260002 for 1,000,000 IDR.\nDue date: 2026-12-31.\n\nThank you."
	if m.Text != want {
		t.Errorf("body %q, want %q", m.Text, want)
	}
	if m.Subject != "Invoice WH20260002" {
		t.Errorf("subject %q", m.Subject)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Filename != "invoice-WH20260002.pdf" ||
		m.Attachments[0].ContentType != "application/pdf" || !strings.HasPrefix(string(m.Attachments[0].Content), "%PDF-") {
		t.Errorf("attachments %+v", m.Attachments)
	}
	// The key is the document id, the 10-minute window of the clock and a
	// hash of everything sent, the PDF included.
	plain := m
	plain.IdempotencyKey = ""
	if want := email.IdempotencyKey("11111111-2222-3333-4444-555555555555", plain, app.Clock.Now()); m.IdempotencyKey != want {
		t.Errorf("idempotency key %q, want %q", m.IdempotencyKey, want)
	}
	if !strings.HasPrefix(m.IdempotencyKey, "11111111-2222-3333-4444-555555555555:") {
		t.Errorf("idempotency key %q lacks the document id", m.IdempotencyKey)
	}
}

func TestInvoicePdfHeadersAndBytes(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	for name, c := range map[string]struct {
		number *string
		status string
		want   func(id uuid.UUID) string
	}{
		"number":    {str("WH20260001"), "OPEN", func(uuid.UUID) string { return `inline; filename="invoice-WH20260001.pdf"` }},
		"no number": {nil, "DRAFT", func(id uuid.UUID) string { return `inline; filename="invoice-` + id.String()[:8] + `.pdf"` }},
		"void":      {str("WH20260003"), "VOID", func(uuid.UUID) string { return `inline; filename="invoice-WH20260003.pdf"` }},
		"non-ascii": {str("WH-é\"1"), "OPEN", func(uuid.UUID) string {
			return `inline; filename="invoice-WH-__1.pdf"; filename*=UTF-8''invoice-WH-%C3%A9%221.pdf`
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			id := docInv{number: c.number, status: c.status}.insert(t, app)
			rec := app.Do(t, http.MethodGet, pdfPath(id), nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
				t.Errorf("content type %q", got)
			}
			if got, want := rec.Header().Get("Content-Disposition"), c.want(id); got != want {
				t.Errorf("disposition %q, want %q", got, want)
			}
			if !strings.HasPrefix(rec.Body.String(), "%PDF-") {
				t.Errorf("body %.10q", rec.Body)
			}
		})
	}
}

func TestPdf404(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	rec := app.Do(t, http.MethodGet, pdfPath(uuid.New()), nil)
	p := apitest.Decode[struct{ Detail string }](t, rec, http.StatusNotFound)
	if p.Detail != "invoice not found" {
		t.Errorf("detail %q", p.Detail)
	}
	rec = app.Do(t, http.MethodPost, sendPath(uuid.New()), map[string]any{"to_email": "a@acme.example"})
	if p := apitest.Decode[struct{ Detail string }](t, rec, http.StatusNotFound); p.Detail != "invoice not found" {
		t.Errorf("send detail %q", p.Detail)
	}
}

func TestSendVoidInvoice400(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{status: "VOID"}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "a@acme.example"})
	if p := apitest.Decode[struct{ Detail string }](t, rec, http.StatusBadRequest); p.Detail != "cannot send a voided invoice" {
		t.Errorf("detail %q", p.Detail)
	}
	if n := len(app.Mail.Messages()); n != 0 {
		t.Errorf("%d messages sent", n)
	}
}

func TestSendNoRecipient400(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	c := docCustomer(t, app, "")
	for name, body := range map[string]map[string]any{"none": {}, "empty": {"to_email": ""}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			id := docInv{customer: &c}.insert(t, app)
			rec := app.Do(t, http.MethodPost, sendPath(id), body)
			p := apitest.Decode[struct{ Detail string }](t, rec, http.StatusBadRequest)
			if p.Detail != "no recipient: provide to_email or set a contact_email on the customer" {
				t.Errorf("detail %q", p.Detail)
			}
		})
	}
	// The customer's contact email is the fallback.
	withEmail := docCustomer(t, app, "billing@acme.example")
	id := docInv{customer: &withEmail}.insert(t, app)
	if rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": ""}); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	found := false
	for _, m := range app.Mail.Messages() {
		if m.To == "billing@acme.example" && strings.HasPrefix(m.Text, "Hi Acme Pte Ltd,") {
			found = true
		}
	}
	if !found {
		t.Errorf("no message to the contact email: %+v", app.Mail.Messages())
	}
}

func TestSendFailure502LeavesStatus(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, true)
	id := docInv{status: "OPEN"}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "a@acme.example"})
	if p := apitest.Decode[struct{ Detail string }](t, rec, http.StatusBadGateway); p.Detail != "failed to send invoice email" {
		t.Errorf("detail %q", p.Detail)
	}
	if got := status(t, app, id); got != "OPEN" {
		t.Errorf("status %s after a failed send", got)
	}
}

func TestSendOpenBecomesSent(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	for from, want := range map[string]string{"OPEN": "SENT", "PARTIAL": "PARTIAL", "PAID": "PAID", "SENT": "SENT"} {
		t.Run(from, func(t *testing.T) {
			t.Parallel()
			id := docInv{status: from}.insert(t, app)
			rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "a@acme.example"})
			out := apitest.Decode[InvoiceOut](t, rec, http.StatusOK)
			if out.Status != want || status(t, app, id) != want {
				t.Errorf("%s -> %s (stored %s), want %s", from, out.Status, status(t, app, id), want)
			}
		})
	}
}

// Owner decision 2026-11-10: only a finalized invoice can be sent.
func TestSendDraftInvoiceRefused(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{status: "DRAFT"}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "a@acme.example"})
	wantProblem(t, rec, http.StatusBadRequest, "invoice_not_finalized", "finalize the invoice before sending it", "", "")
	if n := len(app.Mail.Messages()); n != 0 {
		t.Errorf("%d emails for a refused send, want 0", n)
	}
	if got := status(t, app, id); got != "DRAFT" {
		t.Errorf("status %s after a refused send, want DRAFT", got)
	}
}

func TestSendTemplateRefused(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	for _, st := range []string{"DRAFT", "OPEN"} {
		t.Run(st, func(t *testing.T) {
			t.Parallel()
			id := docInv{status: st, template: true}.insert(t, app)
			rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "a@acme.example"})
			wantProblem(t, rec, http.StatusBadRequest, "invoice_template", "cannot send a recurring template", "", "")
			if got := status(t, app, id); got != st {
				t.Errorf("status %s after a refused send, want %s", got, st)
			}
		})
	}
}

func TestBadToEmail422(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "not-an-email"})
	p := apitest.Decode[struct {
		Errors []struct{ Location, Message string }
	}](t, rec, http.StatusUnprocessableEntity)
	if len(p.Errors) != 1 || p.Errors[0].Location != "to_email" || p.Errors[0].Message != "invalid email address: not-an-email" {
		t.Errorf("errors %+v", p.Errors)
	}
	if n := len(app.Mail.Messages()); n != 0 {
		t.Errorf("%d messages sent", n)
	}
}

func TestDocumentRoutesRequireAdmin(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	staffID := uuid.New()
	if _, err := app.Pool.Exec(t.Context(), `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'placeholder: never signs in', 'staff')`, staffID); err != nil {
		t.Fatal(err)
	}
	token, _, err := app.Sessions.Issue(staffID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	staff := *app
	staff.Cookie = platform.SessionCookie + "=" + token
	id := docInv{}.insert(t, app)
	for _, c := range []struct{ method, p string }{
		{http.MethodGet, pdfPath(id)},
		{http.MethodPost, sendPath(id)},
	} {
		if rec := app.Anon(t, c.method, c.p, map[string]any{"to_email": "a@acme.example"}); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", c.p, rec.Code)
		}
		if rec := staff.Do(t, c.method, c.p, map[string]any{"to_email": "a@acme.example"}); rec.Code != http.StatusForbidden {
			t.Errorf("%s staff: %d", c.p, rec.Code)
		}
	}
	if n := len(app.Mail.Messages()); n != 0 {
		t.Errorf("%d messages sent", n)
	}
}

func TestSendInvoiceCcSubjectAndMessage(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{number: str("WH20260004")}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{
		"to_email": "ap@acme.example", "cc_email": "me@acme.example; ME@acme.example, b@acme.example",
		"subject": "Hello", "message": "Custom body",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	m := onlyMessage(t, app)
	if len(m.Cc) != 2 || m.Cc[0] != "me@acme.example" || m.Cc[1] != "b@acme.example" {
		t.Errorf("cc %q", m.Cc)
	}
	if m.Subject != "Hello" || m.Text != "Custom body" {
		t.Errorf("subject %q body %q", m.Subject, m.Text)
	}
}

func TestSendInvoiceNoNumberDefaults(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "ap@acme.example", "subject": "", "message": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	m := onlyMessage(t, app)
	if m.Subject != "Invoice" || !strings.Contains(m.Text, "attached invoice  for 1,000,000 IDR.") {
		t.Errorf("subject %q body %q", m.Subject, m.Text)
	}
	if m.Attachments[0].Filename != "invoice-"+id.String()[:8]+".pdf" {
		t.Errorf("filename %q", m.Attachments[0].Filename)
	}
}

func TestSendBadCc422(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "ap@acme.example", "cc_email": "ok@acme.example, bad"})
	p := apitest.Decode[struct {
		Errors []struct{ Location, Message string }
	}](t, rec, http.StatusUnprocessableEntity)
	if len(p.Errors) != 1 || p.Errors[0].Location != "cc_email" || p.Errors[0].Message != "invalid email address: bad" {
		t.Errorf("errors %+v", p.Errors)
	}
	if n := len(app.Mail.Messages()); n != 0 {
		t.Errorf("%d messages sent", n)
	}
}

func TestSendToNormalisedAddress(t *testing.T) {
	t.Parallel()
	app := newDocApp(t, false)
	id := docInv{}.insert(t, app)
	if rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": " a@B.com"}); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if m := onlyMessage(t, app); m.To != "a@b.com" {
		t.Errorf("sent to %q, want a@b.com", m.To)
	}
}

// A void that lands while the email is out must stay VOID.
func TestSendConcurrentVoidStaysVoid(t *testing.T) {
	t.Parallel()
	app := newDocAppWith(t, func(d apitest.Deps) email.Sender { return voidingMail{d} })
	id := docInv{status: "OPEN"}.insert(t, app)
	rec := app.Do(t, http.MethodPost, sendPath(id), map[string]any{"to_email": "a@acme.example"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := status(t, app, id); got != "VOID" {
		t.Errorf("status %s, want VOID", got)
	}
}
