package mcp

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
)

// The tests of the 5 gated tools. They port test_mcp_gated_delete.py (11),
// test_mcp_gated_email.py (9) and test_payment_reminder_quotes_expected_cash
// with Python's seeds, driving each tool through the SDK's client over the
// transport, and add the tests of Go change 4: single use, a subject or
// message change invalidating the token, and a stale reminder.

// mailRecorder is the email.Sender of the harness: it records every
// message, or fails every send while fail is set.
type mailRecorder struct {
	mu   sync.Mutex
	sent []email.Message
	fail error
	// after, when set, runs with the tool's context once a send is recorded
	// (outside the lock), as a provider that accepted the email and then
	// took its time answering.
	after func(ctx context.Context)
}

func (m *mailRecorder) Send(ctx context.Context, msg email.Message) error {
	m.mu.Lock()
	if m.fail != nil {
		err := m.fail
		m.mu.Unlock()
		return err
	}
	m.sent = append(m.sent, msg)
	after := m.after
	m.mu.Unlock()
	if after != nil {
		after(ctx)
	}
	return nil
}

func (m *mailRecorder) setAfter(fn func(ctx context.Context)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.after = fn
}

func (m *mailRecorder) setFail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = err
}

func (m *mailRecorder) messages() []email.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sent)
}

type gatedHarness struct {
	*writeHarness
	mail *mailRecorder
}

func newGatedHarness(t *testing.T) *gatedHarness {
	t.Helper()
	g := &gatedHarness{mail: &mailRecorder{}}
	g.writeHarness = &writeHarness{drafts: &draftRecorder{}, paid: &paymentRecorder{}}
	g.readHarness = &readHarness{newFix1Harness(t, func(s *mcp.Server, d Deps) {
		d.Sender = g.mail
		registerGatedTools(s, d)
	})}
	return g
}

// The Python seeds (tests/conftest.py).

// seedCustomer is seed_customer: "Acme Pte Ltd", no contact email.
func (g *gatedHarness) seedCustomer(t *testing.T) uuid.UUID {
	t.Helper()
	return g.customer(t, "Acme Pte Ltd")
}

// seedSentInvoice is seed_sent_invoice: INV-0002, SENT, USD 1000.0000.
func (g *gatedHarness) seedSentInvoice(t *testing.T) uuid.UUID {
	t.Helper()
	return g.invoice(t, g.seedCustomer(t), "INV-0002", "MILESTONE", "SENT", "1000.0000", false)
}

// seedDraftInvoice is seed_draft_invoice: INV-0003, DRAFT, USD 1000.0000.
func (g *gatedHarness) seedDraftInvoice(t *testing.T) uuid.UUID {
	t.Helper()
	return g.invoice(t, g.seedCustomer(t), "INV-0003", "MILESTONE", "DRAFT", "1000.0000", false)
}

// seedOpenInvoice is a finalized invoice that was never sent: INV-0003, OPEN.
func (g *gatedHarness) seedOpenInvoice(t *testing.T) uuid.UUID {
	t.Helper()
	return g.invoice(t, g.seedCustomer(t), "INV-0003", "MILESTONE", "OPEN", "1000.0000", false)
}

func (g *gatedHarness) invoiceStatus(t *testing.T, id uuid.UUID) string {
	t.Helper()
	s, _ := g.cell(t, `SELECT status FROM invoices WHERE invoice_id = $1`, id).(string)
	return s
}

func (g *gatedHarness) spentTokens(t *testing.T) int {
	t.Helper()
	return g.count(t, `SELECT count(*) FROM mcp_spent_confirm_tokens`)
}

// preview calls a gated tool without a token and returns its confirm token,
// checking the preview's requires_confirmation and action.
func (g *gatedHarness) preview(t *testing.T, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	out := g.done(t, tool, args)
	if out["requires_confirmation"] != true || out["action"] != tool {
		t.Fatalf("%s preview %v, want requires_confirmation and action %q", tool, out, tool)
	}
	tok, ok := out["confirm_token"].(string)
	if !ok || tok == "" {
		t.Fatalf("%s preview %v: no confirm_token", tool, out)
	}
	return out, tok
}

func with(args map[string]any, kv ...any) map[string]any {
	out := make(map[string]any, len(args)+len(kv)/2)
	for k, v := range args {
		out[k] = v
	}
	for i := 1; i < len(kv); i += 2 {
		k, _ := kv[i-1].(string)
		out[k] = kv[i]
	}
	return out
}

// --- delete_invoice (test_mcp_gated_delete.py) ---

func TestDeleteInvoicePreviewDoesNotDelete(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedDraftInvoice(t)
	res := g.call(t, "delete_invoice", map[string]any{"invoice_id": id.String()})
	if got, want := textKeys(t, res), []string{"requires_confirmation", "action", "invoice_id", "invoice_number", "status", "confirm_token"}; !slices.Equal(got, want) {
		t.Fatalf("preview keys %v, want %v", got, want)
	}
	out, _ := g.preview(t, "delete_invoice", map[string]any{"invoice_id": id.String()})
	if out["invoice_id"] != id.String() || out["invoice_number"] != "INV-0003" || out["status"] != "DRAFT" {
		t.Fatalf("preview %v", out)
	}
	if n := g.count(t, `SELECT count(*) FROM invoices WHERE invoice_id = $1`, id); n != 1 {
		t.Fatalf("invoice rows %d after preview, want 1", n)
	}
	if n := g.spentTokens(t); n != 0 {
		t.Fatalf("spent tokens %d after preview, want 0", n)
	}
}

func TestDeleteInvoicePreviewsThenDeletes(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedDraftInvoice(t)
	args := map[string]any{"invoice_id": id.String()}
	_, tok := g.preview(t, "delete_invoice", args)
	res := g.call(t, "delete_invoice", with(args, "confirm_token", tok))
	if got := strings.TrimSpace(resultText(res)); got != `{"deleted":true,"invoice_id":"`+id.String()+`"}` {
		t.Fatalf("result %s", got)
	}
	if n := g.count(t, `SELECT count(*) FROM invoices WHERE invoice_id = $1`, id); n != 0 {
		t.Fatalf("invoice rows %d after delete, want 0", n)
	}
}

func TestDeleteNonDraftInvoiceErrors(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	args := map[string]any{"invoice_id": id.String()}
	_, tok := g.preview(t, "delete_invoice", args)
	if msg := g.refused(t, "delete_invoice", with(args, "confirm_token", tok)); msg != "only DRAFT invoices can be deleted (got SENT)" {
		t.Fatalf("refusal %q", msg)
	}
	if n := g.count(t, `SELECT count(*) FROM invoices WHERE invoice_id = $1`, id); n != 1 {
		t.Fatalf("invoice rows %d, want 1", n)
	}
	// The refusal rolled the spend back.
	if n := g.spentTokens(t); n != 0 {
		t.Fatalf("spent tokens %d after a refused delete, want 0", n)
	}
}

func TestDeleteInvoiceTamperedTokenRejected(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedDraftInvoice(t)
	other := g.seedSentInvoice(t)
	_, tok := g.preview(t, "delete_invoice", map[string]any{"invoice_id": other.String()})
	if msg := g.refused(t, "delete_invoice", map[string]any{"invoice_id": id.String(), "confirm_token": tok}); msg != "confirm_token does not match this action/payload" {
		t.Fatalf("refusal %q", msg)
	}
	if n := g.count(t, `SELECT count(*) FROM invoices WHERE invoice_id = $1`, id); n != 1 {
		t.Fatalf("invoice rows %d, want 1", n)
	}
}

// --- delete_bill ---

func TestDeleteBillPreviewDoesNotDelete(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.bill(t, g.vendor(t, "Vendor"), "BILL-0002", "DRAFT", "500.0000")
	res := g.call(t, "delete_bill", map[string]any{"bill_id": id.String()})
	if got, want := textKeys(t, res), []string{"requires_confirmation", "action", "bill_id", "bill_number", "status", "confirm_token"}; !slices.Equal(got, want) {
		t.Fatalf("preview keys %v, want %v", got, want)
	}
	out, _ := g.preview(t, "delete_bill", map[string]any{"bill_id": id.String()})
	if out["bill_number"] != "BILL-0002" || out["status"] != "DRAFT" {
		t.Fatalf("preview %v", out)
	}
	if n := g.count(t, `SELECT count(*) FROM bills WHERE bill_id = $1`, id); n != 1 {
		t.Fatalf("bill rows %d after preview, want 1", n)
	}
}

func TestDeleteBillPreviewsThenDeletes(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.bill(t, g.vendor(t, "Vendor"), "BILL-0002", "DRAFT", "500.0000")
	args := map[string]any{"bill_id": id.String()}
	_, tok := g.preview(t, "delete_bill", args)
	out := g.done(t, "delete_bill", with(args, "confirm_token", tok))
	if out["deleted"] != true || out["bill_id"] != id.String() {
		t.Fatalf("result %v", out)
	}
	if n := g.count(t, `SELECT count(*) FROM bills WHERE bill_id = $1`, id); n != 0 {
		t.Fatalf("bill rows %d after delete, want 0", n)
	}
}

func TestDeleteNonDraftBillErrors(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.bill(t, g.vendor(t, "Vendor"), "BILL-0001", "OPEN", "500.0000")
	args := map[string]any{"bill_id": id.String()}
	_, tok := g.preview(t, "delete_bill", args)
	if msg := g.refused(t, "delete_bill", with(args, "confirm_token", tok)); msg != "only DRAFT bills can be deleted (got OPEN)" {
		t.Fatalf("refusal %q", msg)
	}
	if n := g.count(t, `SELECT count(*) FROM bills WHERE bill_id = $1`, id); n != 1 {
		t.Fatalf("bill rows %d, want 1", n)
	}
}

// A DRAFT bill that still has (reversed) payment history is refused with a
// clean sentence, not a foreign-key error, and stays.
func TestDeleteBillWithHistoryErrors(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.bill(t, g.vendor(t, "Vendor"), "BILL-0003", "DRAFT", "500.0000")
	g.exec(t, `INSERT INTO bill_payments (bill_payment_id, bill_id, amount, currency, payment_date, status, source, reversed_at, reversal_reason)
VALUES ($1, $2, 100, 'USD', CURRENT_DATE, 'REVERSED', 'MANUAL', now(), 'typo')`, uuid.New(), id)
	args := map[string]any{"bill_id": id.String()}
	_, tok := g.preview(t, "delete_bill", args)
	if msg := g.refused(t, "delete_bill", with(args, "confirm_token", tok)); msg != "bill BILL-0003 has payment or ledger history and cannot be deleted; void it instead" {
		t.Fatalf("refusal %q", msg)
	}
	if n := g.count(t, `SELECT count(*) FROM bills WHERE bill_id = $1`, id); n != 1 {
		t.Fatalf("bill rows %d, want 1", n)
	}
}

func TestDeleteBillTamperedTokenRejected(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	vendor := g.vendor(t, "Vendor")
	open := g.bill(t, vendor, "BILL-0001", "OPEN", "500.0000")
	draft := g.bill(t, vendor, "BILL-0002", "DRAFT", "500.0000")
	_, tok := g.preview(t, "delete_bill", map[string]any{"bill_id": open.String()})
	if msg := g.refused(t, "delete_bill", map[string]any{"bill_id": draft.String(), "confirm_token": tok}); msg != "confirm_token does not match this action/payload" {
		t.Fatalf("refusal %q", msg)
	}
	if n := g.count(t, `SELECT count(*) FROM bills WHERE bill_id = $1`, draft); n != 1 {
		t.Fatalf("bill rows %d, want 1", n)
	}
}

// --- delete_customer ---

func TestDeleteCustomerPreviewDoesNotDeactivate(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedCustomer(t)
	res := g.call(t, "delete_customer", map[string]any{"customer_id": id.String()})
	if got, want := textKeys(t, res), []string{"requires_confirmation", "action", "customer_id", "name", "confirm_token"}; !slices.Equal(got, want) {
		t.Fatalf("preview keys %v, want %v", got, want)
	}
	out, _ := g.preview(t, "delete_customer", map[string]any{"customer_id": id.String()})
	if out["name"] != "Acme Pte Ltd" {
		t.Fatalf("preview %v", out)
	}
	if active := g.cell(t, `SELECT active FROM customers WHERE customer_id = $1`, id); active != true {
		t.Fatalf("active %v after preview, want true", active)
	}
}

func TestDeleteCustomerPreviewsThenDeactivates(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedCustomer(t)
	args := map[string]any{"customer_id": id.String()}
	_, tok := g.preview(t, "delete_customer", args)
	out := g.done(t, "delete_customer", with(args, "confirm_token", tok))
	if out["deleted"] != true || out["customer_id"] != id.String() {
		t.Fatalf("result %v", out)
	}
	if active := g.cell(t, `SELECT active FROM customers WHERE customer_id = $1`, id); active != false {
		t.Fatalf("active %v, want false (the row stays)", active)
	}
}

func TestDeleteCustomerTamperedTokenRejected(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedCustomer(t)
	other := g.customer(t, "Acme Global Inc")
	_, tok := g.preview(t, "delete_customer", map[string]any{"customer_id": other.String()})
	if msg := g.refused(t, "delete_customer", map[string]any{"customer_id": id.String(), "confirm_token": tok}); msg != "confirm_token does not match this action/payload" {
		t.Fatalf("refusal %q", msg)
	}
	if active := g.cell(t, `SELECT active FROM customers WHERE customer_id = $1`, id); active != true {
		t.Fatalf("active %v, want true", active)
	}
}

// The not-found and bad-id refusals of every gated tool.
func TestGatedToolsRefuseUnknownAndBadIDs(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	unknown := uuid.NewString()
	for _, c := range []struct{ tool, arg, want string }{
		{"delete_invoice", "invoice_id", "invoice not found"},
		{"delete_bill", "bill_id", "bill not found"},
		{"delete_customer", "customer_id", "customer not found"},
		{"send_invoice_email", "invoice_id", "invoice not found"},
		{"send_payment_reminder", "invoice_id", "invoice not found"},
	} {
		if msg := g.refused(t, c.tool, map[string]any{c.arg: unknown}); msg != c.want {
			t.Errorf("%s unknown: %q, want %q", c.tool, msg, c.want)
		}
		if msg := g.refused(t, c.tool, map[string]any{c.arg: unknown, "confirm_token": "1.ab"}); msg != c.want {
			t.Errorf("%s unknown with token: %q, want %q", c.tool, msg, c.want)
		}
		want := "invalid " + c.arg + ": nope is not a valid UUID"
		if msg := g.refused(t, c.tool, map[string]any{c.arg: "nope"}); msg != want {
			t.Errorf("%s bad id: %q, want %q", c.tool, msg, want)
		}
	}
}

// --- send_invoice_email (test_mcp_gated_email.py) ---

func TestFirstCallReturnsPreviewAndSendsNothing(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	res := g.call(t, "send_invoice_email", args)
	if got, want := textKeys(t, res), []string{"requires_confirmation", "action", "invoice_id", "invoice_number", "recipient", "amount", "currency", "confirm_token"}; !slices.Equal(got, want) {
		t.Fatalf("preview keys %v, want %v", got, want)
	}
	out, _ := g.preview(t, "send_invoice_email", args)
	if out["recipient"] != "a@b.com" || out["amount"] != "1000.0000" || out["currency"] != "USD" || out["invoice_number"] != "INV-0002" {
		t.Fatalf("preview %v", out)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails sent by a preview, want 0", n)
	}
}

func TestSecondCallWithTokenSends(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	_, tok := g.preview(t, "send_invoice_email", args)
	res := g.call(t, "send_invoice_email", with(args, "confirm_token", tok))
	if got := strings.TrimSpace(resultText(res)); got != `{"sent":true,"invoice_id":"`+id.String()+`","recipient":"a@b.com"}` {
		t.Fatalf("result %s", got)
	}
	sent := g.mail.messages()
	if len(sent) != 1 {
		t.Fatalf("%d emails, want 1", len(sent))
	}
	m := sent[0]
	if m.To != "a@b.com" || m.Cc != nil || m.Subject != "Invoice INV-0002" || m.Text != "Please find attached invoice INV-0002." {
		t.Fatalf("message to=%q cc=%v subject=%q text=%q", m.To, m.Cc, m.Subject, m.Text)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Filename != "invoice-INV-0002.pdf" || m.Attachments[0].ContentType != "application/pdf" ||
		!bytes.HasPrefix(m.Attachments[0].Content, []byte("%PDF")) {
		t.Fatalf("attachments %+v, want one invoice PDF", m.Attachments)
	}
	if m.IdempotencyKey == "" || !strings.HasPrefix(m.IdempotencyKey, id.String()+":") {
		t.Fatalf("idempotency key %q, want the send route's", m.IdempotencyKey)
	}
}

func TestSendInvoiceEmailUsesSubjectMessageAndContactEmail(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	cust := g.seedCustomer(t)
	g.exec(t, `UPDATE customers SET contact_email = 'billing@acme.example' WHERE customer_id = $1`, cust)
	id := g.invoice(t, cust, "", "MILESTONE", "OPEN", "10.0000", false)
	args := map[string]any{"invoice_id": id.String(), "subject": "Your bill", "message": "See attached."}
	out, tok := g.preview(t, "send_invoice_email", args)
	if out["recipient"] != "billing@acme.example" || out["invoice_number"] != nil {
		t.Fatalf("preview %v", out)
	}
	g.done(t, "send_invoice_email", with(args, "confirm_token", tok))
	sent := g.mail.messages()
	if len(sent) != 1 || sent[0].To != "billing@acme.example" || sent[0].Subject != "Your bill" || sent[0].Text != "See attached." ||
		sent[0].Attachments[0].Filename != "invoice-"+id.String()[:8]+".pdf" {
		t.Fatalf("messages %+v", sent)
	}
	if s := g.invoiceStatus(t, id); s != "SENT" {
		t.Fatalf("status %s, want SENT (OPEN moves too)", s)
	}
}

func TestTamperedTokenRefuses(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	_, tok := g.preview(t, "send_invoice_email", map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"})
	msg := g.refused(t, "send_invoice_email", map[string]any{"invoice_id": id.String(), "to_email": "DIFFERENT@x.com", "confirm_token": tok})
	if msg != "confirm_token does not match this action/payload" {
		t.Fatalf("refusal %q", msg)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
}

func TestOpenInvoicePromotedToSentAfterSend(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedOpenInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	_, tok := g.preview(t, "send_invoice_email", args)
	if s := g.invoiceStatus(t, id); s != "OPEN" {
		t.Fatalf("status %s after preview, want OPEN", s)
	}
	out := g.done(t, "send_invoice_email", with(args, "confirm_token", tok))
	if out["sent"] != true || len(g.mail.messages()) != 1 {
		t.Fatalf("result %v, %d emails", out, len(g.mail.messages()))
	}
	if s := g.invoiceStatus(t, id); s != "SENT" {
		t.Fatalf("status %s, want SENT", s)
	}
}

// Owner decision 2026-11-10: only a finalized invoice can be sent. The
// preview refuses too, so a refused invoice never gets a token.
func TestSendDraftInvoiceRefusedOnPreviewAndConfirm(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedDraftInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	const want = "finalize the invoice before sending it"
	if msg := g.refused(t, "send_invoice_email", args); msg != want {
		t.Fatalf("preview refusal %q, want %q", msg, want)
	}
	// A token minted while the invoice was OPEN no longer works once it is DRAFT again.
	g.exec(t, `UPDATE invoices SET status = 'OPEN' WHERE invoice_id = $1`, id)
	_, tok := g.preview(t, "send_invoice_email", args)
	g.exec(t, `UPDATE invoices SET status = 'DRAFT' WHERE invoice_id = $1`, id)
	if msg := g.refused(t, "send_invoice_email", with(args, "confirm_token", tok)); msg != want {
		t.Fatalf("confirm refusal %q, want %q", msg, want)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
	if n := g.spentTokens(t); n != 0 {
		t.Fatalf("spent tokens %d after a refused send, want 0", n)
	}
	if s := g.invoiceStatus(t, id); s != "DRAFT" {
		t.Fatalf("status %s, want DRAFT", s)
	}
}

func TestSendTemplateRefusedOnPreviewAndConfirm(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.invoice(t, g.seedCustomer(t), "INV-TPL-EMAIL", "RECURRING", "DRAFT", "100.0000", true)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	const want = "cannot send a recurring template"
	if msg := g.refused(t, "send_invoice_email", args); msg != want {
		t.Fatalf("preview refusal %q, want %q", msg, want)
	}
	g.exec(t, `UPDATE invoices SET is_template = false, status = 'OPEN' WHERE invoice_id = $1`, id)
	_, tok := g.preview(t, "send_invoice_email", args)
	g.exec(t, `UPDATE invoices SET is_template = true WHERE invoice_id = $1`, id)
	if msg := g.refused(t, "send_invoice_email", with(args, "confirm_token", tok)); msg != want {
		t.Fatalf("confirm refusal %q, want %q", msg, want)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
	if n := g.spentTokens(t); n != 0 {
		t.Fatalf("spent tokens %d after a refused send, want 0", n)
	}
}

func TestReminderDraftRefusedOnPreviewAndConfirm(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedDraftInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	const want = "finalize the invoice before sending it"
	if msg := g.refused(t, "send_payment_reminder", args); msg != want {
		t.Fatalf("preview refusal %q, want %q", msg, want)
	}
	g.exec(t, `UPDATE invoices SET status = 'OPEN' WHERE invoice_id = $1`, id)
	_, tok := g.preview(t, "send_payment_reminder", args)
	g.exec(t, `UPDATE invoices SET status = 'DRAFT' WHERE invoice_id = $1`, id)
	if msg := g.refused(t, "send_payment_reminder", with(args, "confirm_token", tok)); msg != want {
		t.Fatalf("confirm refusal %q, want %q", msg, want)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
	if n := g.spentTokens(t); n != 0 {
		t.Fatalf("spent tokens %d after a refused reminder, want 0", n)
	}
}

func TestReminderTemplateRefusedOnPreviewAndConfirm(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.invoice(t, g.seedCustomer(t), "INV-TPL-REMIND", "RECURRING", "DRAFT", "100.0000", true)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	const want = "cannot send a recurring template"
	if msg := g.refused(t, "send_payment_reminder", args); msg != want {
		t.Fatalf("preview refusal %q, want %q", msg, want)
	}
	g.exec(t, `UPDATE invoices SET is_template = false, status = 'OPEN' WHERE invoice_id = $1`, id)
	_, tok := g.preview(t, "send_payment_reminder", args)
	g.exec(t, `UPDATE invoices SET is_template = true WHERE invoice_id = $1`, id)
	if msg := g.refused(t, "send_payment_reminder", with(args, "confirm_token", tok)); msg != want {
		t.Fatalf("confirm refusal %q, want %q", msg, want)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
	if n := g.spentTokens(t); n != 0 {
		t.Fatalf("spent tokens %d after a refused reminder, want 0", n)
	}
}

func TestVoidInvoiceRefuses(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.invoice(t, g.seedCustomer(t), "INV-VOID-EMAIL", "MILESTONE", "VOID", "100.0000", false)
	if msg := g.refused(t, "send_invoice_email", map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}); msg != "cannot send a voided invoice" {
		t.Fatalf("refusal %q", msg)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
}

func TestNoRecipientRefuses(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t) // seed_customer has no contact_email
	for _, tool := range []string{"send_invoice_email", "send_payment_reminder"} {
		if msg := g.refused(t, tool, map[string]any{"invoice_id": id.String()}); msg != "no recipient: pass to_email or set customer contact_email" {
			t.Fatalf("%s refusal %q", tool, msg)
		}
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
}

// A send the provider refuses answers Python's sentence and changes
// nothing: the status stays and the token is not spent, so it still works.
func TestSendInvoiceEmailFailureChangesNothing(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedOpenInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	_, tok := g.preview(t, "send_invoice_email", args)
	g.mail.setFail(errors.New("resend: 500 internal"))
	if msg := g.refused(t, "send_invoice_email", with(args, "confirm_token", tok)); msg != "failed to send invoice email" {
		t.Fatalf("refusal %q", msg)
	}
	if s := g.invoiceStatus(t, id); s != "OPEN" {
		t.Fatalf("status %s after a failed send, want OPEN", s)
	}
	if n := g.spentTokens(t); n != 0 {
		t.Fatalf("spent tokens %d after a failed send, want 0", n)
	}
	g.mail.setFail(nil)
	g.done(t, "send_invoice_email", with(args, "confirm_token", tok))
	if s := g.invoiceStatus(t, id); s != "SENT" || len(g.mail.messages()) != 1 {
		t.Fatalf("retry: status %s, %d emails", s, len(g.mail.messages()))
	}
}

// --- send_payment_reminder ---

func TestReminderFirstCallReturnsPreviewAndSendsNothing(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	res := g.call(t, "send_payment_reminder", args)
	if got, want := textKeys(t, res), []string{"requires_confirmation", "action", "invoice_id", "invoice_number", "recipient", "amount", "currency", "confirm_token"}; !slices.Equal(got, want) {
		t.Fatalf("preview keys %v, want %v", got, want)
	}
	out, _ := g.preview(t, "send_payment_reminder", args)
	if out["recipient"] != "a@b.com" || out["amount"] != "1000.0000" {
		t.Fatalf("preview %v", out)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
}

func TestReminderSecondCallWithTokenSends(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	_, tok := g.preview(t, "send_payment_reminder", args)
	out := g.done(t, "send_payment_reminder", with(args, "confirm_token", tok))
	if out["sent"] != true || out["recipient"] != "a@b.com" {
		t.Fatalf("result %v", out)
	}
	sent := g.mail.messages()
	if len(sent) != 1 {
		t.Fatalf("%d emails, want 1", len(sent))
	}
	m := sent[0]
	if m.To != "a@b.com" || m.Subject != "Payment reminder: invoice INV-0002" ||
		m.Text != "This is a reminder that invoice INV-0002 has an outstanding balance of 1000.0000 USD." || m.Attachments != nil {
		t.Fatalf("message %+v", m)
	}
	if s := g.invoiceStatus(t, id); s != "SENT" {
		t.Fatalf("status %s, want SENT (a reminder changes nothing)", s)
	}
}

func TestReminderTamperedTokenRefuses(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	_, tok := g.preview(t, "send_payment_reminder", map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"})
	msg := g.refused(t, "send_payment_reminder", map[string]any{"invoice_id": id.String(), "to_email": "DIFFERENT@x.com", "confirm_token": tok})
	if msg != "confirm_token does not match this action/payload" {
		t.Fatalf("refusal %q", msg)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
}

func TestReminderRefusesVoidAndPaid(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	cust := g.seedCustomer(t)
	for _, status := range []string{"VOID", "PAID"} {
		id := g.invoice(t, cust, "INV-"+status, "MILESTONE", status, "100.0000", false)
		if msg := g.refused(t, "send_payment_reminder", map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}); msg != "cannot remind on a "+status+" invoice" {
			t.Fatalf("%s: refusal %q", status, msg)
		}
	}
}

// test_payment_reminder_quotes_expected_cash
// (test_record_payment_withholding.py): a grossed-up PPh 23 invoice of
// 10204082 IDR with 204082 withheld expects 10000000 in cash.
func TestPaymentReminderQuotesExpectedCash(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := uuid.New()
	g.exec(t, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, invoice_number, currency, amount, balance_due, status,
		wht_type, wht_rate, wht_gross_up, wht_base, wht_amount, wht_booked)
		VALUES ($1, $2, 'MILESTONE', 'INV-WHT', 'IDR', 10204082.0000, 10204082.0000, 'OPEN', 'PPH23', 2, true, 10000000.0000, 204082.0000, 0)`,
		id, g.seedCustomer(t))
	args := map[string]any{"invoice_id": id.String(), "to_email": "ap@acme.example"}
	out, tok := g.preview(t, "send_payment_reminder", args)
	if out["amount"] != "10000000.0000" {
		t.Fatalf("preview amount %v, want 10000000.0000", out["amount"])
	}
	if res := g.done(t, "send_payment_reminder", with(args, "confirm_token", tok)); res["sent"] != true {
		t.Fatalf("result %v", res)
	}
	sent := g.mail.messages()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "outstanding balance of 10000000") {
		t.Fatalf("messages %+v", sent)
	}
}

// --- Go change 4 ---

// A confirm token acts once: a second confirm, sequential or concurrent,
// is refused and sends nothing more.
func TestConfirmTokenSingleUse(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	_, tok := g.preview(t, "send_invoice_email", args)
	g.done(t, "send_invoice_email", with(args, "confirm_token", tok))
	if msg := g.refused(t, "send_invoice_email", with(args, "confirm_token", tok)); msg != "confirm_token already used — re-request the action" {
		t.Fatalf("replay: %q", msg)
	}
	if n := len(g.mail.messages()); n != 1 {
		t.Fatalf("%d emails, want 1", n)
	}
	if n := g.spentTokens(t); n != 1 {
		t.Fatalf("spent tokens %d, want 1", n)
	}

	// Concurrent confirms of one fresh token: exactly one sends.
	_, tok2 := g.preview(t, "send_payment_reminder", args)
	const n = 6
	var wg sync.WaitGroup
	results := make([]*mcp.CallToolResult, n)
	errs := make([]error, n)
	for i := range n {
		sess := g.connect(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "send_payment_reminder", Arguments: with(args, "confirm_token", tok2)})
		}()
	}
	wg.Wait()
	sentCount := 0
	for i := range n {
		if errs[i] != nil || results[i].IsError {
			t.Fatalf("call %d: %v %v", i, errs[i], results[i])
		}
		m, _ := results[i].StructuredContent.(map[string]any)
		switch {
		case m["sent"] == true:
			sentCount++
		case m["error"] != "confirm_token already used — re-request the action":
			t.Fatalf("call %d: %v", i, m)
		}
	}
	if sentCount != 1 || len(g.mail.messages()) != 2 {
		t.Fatalf("%d concurrent confirms sent, %d emails in all; want 1 and 2", sentCount, len(g.mail.messages()))
	}
}

// The spent record keys on the token's canonical form: the same token
// respelled (a leading zero or plus sign on the expiry, which still
// verifies, or its MAC in upper case) is refused after it was used.
func TestConfirmTokenRespelledReplayRefused(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedCustomer(t)
	args := map[string]any{"customer_id": id.String()}
	_, tok := g.preview(t, "delete_customer", args)
	g.done(t, "delete_customer", with(args, "confirm_token", tok))
	expiry, sig, _ := strings.Cut(tok, ".")
	const used = "confirm_token already used — re-request the action"
	for _, c := range []struct{ variant, want string }{
		{"0" + tok, used},
		{"+" + tok, used},
		{"00" + tok, used},
		{expiry + "." + strings.ToUpper(sig), "confirm_token does not match this action/payload"},
	} {
		if msg := g.refused(t, "delete_customer", with(args, "confirm_token", c.variant)); msg != c.want {
			t.Errorf("replay %q: %q, want %q", c.variant, msg, c.want)
		}
	}
	if n := g.spentTokens(t); n != 1 {
		t.Fatalf("spent tokens %d, want 1", n)
	}
}

// Go change 4: the subject and message are in the MAC, so the confirmed
// email is the previewed one.
func TestSubjectChangeInvalidatesToken(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com", "subject": "Invoice for March", "message": "Thanks!"}
	_, tok := g.preview(t, "send_invoice_email", args)
	for _, changed := range []map[string]any{
		with(args, "subject", "Invoice for April", "confirm_token", tok),
		with(args, "message", "Pay now!", "confirm_token", tok),
		with(args, "subject", nil, "confirm_token", tok),
	} {
		if msg := g.refused(t, "send_invoice_email", changed); msg != "confirm_token does not match this action/payload" {
			t.Fatalf("changed %v: %q", changed, msg)
		}
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
	g.done(t, "send_invoice_email", with(args, "confirm_token", tok))
	if sent := g.mail.messages(); len(sent) != 1 || sent[0].Subject != "Invoice for March" || sent[0].Text != "Thanks!" {
		t.Fatalf("messages %+v", sent)
	}
}

// A partial payment between the preview and the confirm changes the cash
// still expected, so the reminder's token is stale and nothing is sent.
func TestReminderStaleAfterPartialPayment(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedSentInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	_, tok := g.preview(t, "send_payment_reminder", args)
	g.exec(t, `UPDATE invoices SET balance_due = 600.0000, status = 'PARTIAL' WHERE invoice_id = $1`, id)
	if msg := g.refused(t, "send_payment_reminder", with(args, "confirm_token", tok)); msg != "confirm_token does not match this action/payload" {
		t.Fatalf("refusal %q", msg)
	}
	if n := len(g.mail.messages()); n != 0 {
		t.Fatalf("%d emails, want 0", n)
	}
	out, _ := g.preview(t, "send_payment_reminder", args)
	if out["amount"] != "600.0000" {
		t.Fatalf("new preview amount %v, want 600.0000", out["amount"])
	}
}

// An expired token is refused with Python's sentence, and an empty one is
// malformed (not a preview: only a missing token previews).
func TestGatedExpiredAndEmptyTokens(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedCustomer(t)
	args := map[string]any{"customer_id": id.String()}
	if msg := g.refused(t, "delete_customer", with(args, "confirm_token", "")); msg != "malformed confirm_token" {
		t.Fatalf("empty token: %q", msg)
	}
	_, tok := g.preview(t, "delete_customer", args)
	g.clock.Advance(confirmTTL + 1e9)
	if msg := g.refused(t, "delete_customer", with(args, "confirm_token", tok)); msg != "confirm_token expired — re-request the action" {
		t.Fatalf("expired token: %q", msg)
	}
	if active := g.cell(t, `SELECT active FROM customers WHERE customer_id = $1`, id); active != true {
		t.Fatalf("active %v, want true", active)
	}
}

// Spent rows go once their token is past expiry and the grace.
func TestSpentConfirmTokensPruned(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	now := g.clock.Now()
	g.exec(t, `INSERT INTO mcp_spent_confirm_tokens (token_hash, expires_at) VALUES ('old', $1), ('edge', $2), ('live', $3)`,
		now.Add(-spentConfirmGrace-1e9), now.Add(-spentConfirmGrace), now.Add(confirmTTL))
	id := g.seedCustomer(t)
	args := map[string]any{"customer_id": id.String()}
	_, tok := g.preview(t, "delete_customer", args)
	g.done(t, "delete_customer", with(args, "confirm_token", tok))
	rows, err := g.pool.Query(t.Context(), `SELECT token_hash FROM mcp_spent_confirm_tokens WHERE token_hash IN ('old', 'edge', 'live') ORDER BY token_hash`)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		left = append(left, s)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if want := []string{"edge", "live"}; !slices.Equal(left, want) {
		t.Fatalf("left %v, want %v", left, want)
	}
}

// A request cancelled after the provider accepted the email does not roll
// the call back: the invoice is SENT, the token stays spent, and a replay of
// the token is refused. The body runs through runTool, as the SDK runs it,
// with a context the sender cancels once it has recorded the email (the
// transport does not hand a client's hang-up to the handler, so the test
// cancels the handler's context itself).
func TestSendInvoiceEmailCancelAfterSendKeepsStatusAndSpend(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	id := g.seedOpenInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	_, tok := g.preview(t, "send_invoice_email", args)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	g.mail.setAfter(func(context.Context) { cancel() })
	gt := gate{pool: g.pool, confirm: NewConfirmer(g.cfg.MCPConfirmSecret), clock: g.clock, mail: g.mail}
	schema, err := resolveSchema(openObjectSchema())
	if err != nil {
		t.Fatal(err)
	}
	to := "a@b.com"
	in := sendInvoiceIn{InvoiceID: id.String(), ToEmail: &to, ConfirmToken: &tok}
	d := Deps{Pool: g.pool, Clock: g.clock, Cfg: g.cfg}
	if _, err := runTool(ctx, d, toolConfig{name: actSendInvoice}, schema, principalRef{UserID: g.adminID}, in, gt.sendInvoiceEmail); err != nil {
		t.Fatalf("send_invoice_email cancelled after the send: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("the context was never cancelled, so this test proves nothing")
	}
	g.mail.setAfter(nil)

	if s := g.invoiceStatus(t, id); s != "SENT" {
		t.Fatalf("status %s after a send the client hung up on, want SENT", s)
	}
	if n := g.spentTokens(t); n != 1 {
		t.Fatalf("spent tokens %d, want 1", n)
	}
	if msg := g.refused(t, "send_invoice_email", with(args, "confirm_token", tok)); msg != errConfirmSpent {
		t.Fatalf("replay: %q, want %q", msg, errConfirmSpent)
	}
	if n := len(g.mail.messages()); n != 1 {
		t.Fatalf("%d emails, want 1", n)
	}
}

// The spent-token prune runs outside the action's transaction, so a confirm
// does not wait behind another confirm whose send is still in flight (that
// one's prune would otherwise hold the expired rows it deleted until its
// commit). Expired rows are still pruned.
func TestConfirmPruneDoesNotWaitBehindASend(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	g.exec(t, `INSERT INTO mcp_spent_confirm_tokens (token_hash, expires_at) VALUES ('old', $1)`,
		g.clock.Now().Add(-spentConfirmGrace-time.Second))
	inv := g.seedSentInvoice(t)
	sendArgs := map[string]any{"invoice_id": inv.String(), "to_email": "a@b.com"}
	_, sendTok := g.preview(t, "send_invoice_email", sendArgs)
	cust := g.customer(t, "Other Pte Ltd")
	delArgs := map[string]any{"customer_id": cust.String()}
	_, delTok := g.preview(t, "delete_customer", delArgs)

	entered := make(chan struct{})
	release := make(chan struct{})
	g.mail.setAfter(func(context.Context) {
		close(entered)
		<-release
	})
	var once sync.Once
	var wg sync.WaitGroup
	free := func() {
		once.Do(func() { close(release) })
		wg.Wait()
	}
	defer free()
	sendSess := g.connect(t)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = sendSess.CallTool(t.Context(), &mcp.CallToolParams{Name: "send_invoice_email", Arguments: with(sendArgs, "confirm_token", sendTok)})
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the send never reached the mail provider")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	res, err := g.connect(t).CallTool(ctx, &mcp.CallToolParams{Name: "delete_customer", Arguments: with(delArgs, "confirm_token", delTok)})
	if err != nil {
		t.Fatalf("delete_customer confirm waited behind the in-flight send: %v", err)
	}
	if m, _ := res.StructuredContent.(map[string]any); res.IsError || m["error"] != nil {
		t.Fatalf("delete_customer: %v", m)
	}
	if active := g.cell(t, `SELECT active FROM customers WHERE customer_id = $1`, cust); active != false {
		t.Fatalf("active %v, want false", active)
	}
	if n := g.count(t, `SELECT count(*) FROM mcp_spent_confirm_tokens WHERE token_hash = 'old'`); n != 0 {
		t.Fatalf("expired spent row still there (%d)", n)
	}

	free()
	if s := g.invoiceStatus(t, inv); s != "SENT" || g.spentTokens(t) != 2 {
		t.Fatalf("send after release: status %s, %d spent tokens; want SENT and 2", s, g.spentTokens(t))
	}
}

// A confirm holds one connection at a time: on a pool of a single
// connection it still spends its token, acts, commits and prunes expired
// spent rows (after the commit has released the connection). A deadline
// turns a confirm that waits for a second connection into a failure.
func TestConfirmSucceedsOnSingleConnectionPool(t *testing.T) {
	t.Parallel()
	g := newGatedHarness(t)
	g.exec(t, `INSERT INTO mcp_spent_confirm_tokens (token_hash, expires_at) VALUES ('old', $1)`,
		g.clock.Now().Add(-spentConfirmGrace-time.Second))
	id := g.seedOpenInvoice(t)
	args := map[string]any{"invoice_id": id.String(), "to_email": "a@b.com"}
	_, tok := g.preview(t, "send_invoice_email", args)

	cfg := g.pool.Config().Copy()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	one, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(one.Close)
	gt := gate{pool: one, confirm: NewConfirmer(g.cfg.MCPConfirmSecret), clock: g.clock, mail: g.mail}
	schema, err := resolveSchema(openObjectSchema())
	if err != nil {
		t.Fatal(err)
	}
	to := "a@b.com"
	in := sendInvoiceIn{InvoiceID: id.String(), ToEmail: &to, ConfirmToken: &tok}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	d := Deps{Pool: one, Clock: g.clock, Cfg: g.cfg}
	if _, err := runTool(ctx, d, toolConfig{name: actSendInvoice}, schema, principalRef{UserID: g.adminID}, in, gt.sendInvoiceEmail); err != nil {
		t.Fatalf("send_invoice_email on a one-connection pool: %v", err)
	}
	if s := g.invoiceStatus(t, id); s != "SENT" || g.spentTokens(t) != 1 {
		t.Fatalf("status %s, %d spent tokens; want SENT and 1", s, g.spentTokens(t))
	}
	if n := g.count(t, `SELECT count(*) FROM mcp_spent_confirm_tokens WHERE token_hash = 'old'`); n != 0 {
		t.Fatalf("expired spent row still there (%d)", n)
	}
}
