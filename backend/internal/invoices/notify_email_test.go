package invoices

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
)

func acme() DraftSummary {
	name := "Acme Pte Ltd"
	return DraftSummary{
		InvoiceID:     uuid.MustParse("00000000-0000-0000-0000-00000000abc1"),
		InvoiceNumber: "INV-2026-0042", CustomerName: &name, Currency: "SGD",
		Amount: decimal.RequireFromString("1500"), CycleDate: "2026-08-01",
	}
}

// test_subject_counts_drafts
func TestDigestSubjectCountsDrafts(t *testing.T) {
	t.Parallel()
	subject, _, _ := BuildDraftsDigest([]DraftSummary{acme()}, "http://localhost:5173")
	if subject != "1 draft invoice awaiting your approval" {
		t.Errorf("subject = %q", subject)
	}
	subject, _, _ = BuildDraftsDigest([]DraftSummary{acme(), acme()}, "http://localhost:5173")
	if subject != "2 draft invoices awaiting your approval" {
		t.Errorf("subject = %q", subject)
	}
}

// test_text_body_lists_draft_details. The amount is FormatAmount's
// "1,500.00 SGD" (Go change 3), not Python's "SGD 1,500.00".
func TestDigestTextListsDraftDetails(t *testing.T) {
	t.Parallel()
	_, text, _ := BuildDraftsDigest([]DraftSummary{acme()}, "http://localhost:5173/")
	for _, want := range []string{
		"INV-2026-0042", "Acme Pte Ltd", "1,500.00 SGD", "cycle 2026-08-01",
		"http://localhost:5173/invoices/00000000-0000-0000-0000-00000000abc1/edit",
		"The recurring billing scan generated 1 draft invoice that need your review and approval:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(text, "Hi,\n\n") || !strings.HasSuffix(text, "approve and issue it.\n") {
		t.Errorf("text frame wrong:\n%q", text)
	}
}

// test_html_body_links_invoice_number
func TestDigestHTMLLinksInvoiceNumber(t *testing.T) {
	t.Parallel()
	_, _, html := BuildDraftsDigest([]DraftSummary{acme()}, "http://localhost:5173")
	want := `<a href="http://localhost:5173/invoices/00000000-0000-0000-0000-00000000abc1/edit">INV-2026-0042</a>`
	if !strings.Contains(html, want) {
		t.Errorf("html lacks %q:\n%s", want, html)
	}
	if !strings.Contains(html, "Awaiting Finalization") {
		t.Errorf("html lacks the queue name:\n%s", html)
	}
}

// Go change 7: a customer name or number with markup is escaped in the HTML,
// and left as is in the text. IDR has no decimals (Go change 3).
func TestDigestEscapesHtml(t *testing.T) {
	t.Parallel()
	d := acme()
	evil := `<script>alert("x")</script> & Co`
	d.CustomerName, d.InvoiceNumber, d.Currency = &evil, `INV<1>`, "IDR"
	d.Amount = decimal.RequireFromString("1500000")
	_, text, html := BuildDraftsDigest([]DraftSummary{d}, "http://localhost:5173")
	if strings.Contains(html, "<script>") || strings.Contains(html, "INV<1>") {
		t.Errorf("html not escaped:\n%s", html)
	}
	for _, want := range []string{"&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; Co", "INV&lt;1&gt;", "1,500,000 IDR"} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}
	if !strings.Contains(text, evil) {
		t.Errorf("text should keep the name as is:\n%s", text)
	}
}

// A draft with no customer reads "(unknown customer)".
func TestDigestUnknownCustomer(t *testing.T) {
	t.Parallel()
	d := acme()
	d.CustomerName = nil
	_, text, _ := BuildDraftsDigest([]DraftSummary{d}, "http://x.test")
	if !strings.Contains(text, "(unknown customer)") {
		t.Errorf("text = %s", text)
	}
}

// failingSender records each message and fails for the addresses in fail.
type failingSender struct {
	mu   sync.Mutex
	sent []email.Message
	fail map[string]bool
}

func (s *failingSender) Send(_ context.Context, m email.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m)
	if s.fail[m.To] {
		return errors.New("resend down")
	}
	return nil
}

// The digest goes to every user, once each; one failing recipient is logged
// and the rest still get it. No drafts is no mail.
func TestEmailNotifierSendsToEveryUserAndSkipsFailures(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, e := range []string{"staff@example.com", "viewer@example.com"} {
		if _, err := a.Pool.Exec(t.Context(), `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, $2, 'x', 'viewer')`, uuid.New(), e); err != nil {
			t.Fatal(err)
		}
	}
	s := &failingSender{fail: map[string]bool{"staff@example.com": true}}
	var logs bytes.Buffer
	n := EmailNotifier{Q: invoicesdb.New(a.Pool), Sender: s, AppBaseURL: "http://app.test", Logger: slog.New(slog.NewTextHandler(&logs, nil))}

	n.NotifyNewDrafts(t.Context(), nil)
	if len(s.sent) != 0 {
		t.Fatalf("no drafts sent %d mails", len(s.sent))
	}
	n.NotifyNewDrafts(t.Context(), []DraftSummary{acme()})
	got := map[string]bool{}
	for _, m := range s.sent {
		got[m.To] = true
		if m.Subject != "1 draft invoice awaiting your approval" || m.HTML == "" {
			t.Errorf("mail to %s = %+v", m.To, m)
		}
	}
	for _, want := range []string{apitest.AdminEmail, "staff@example.com", "viewer@example.com"} {
		if !got[want] {
			t.Errorf("no digest to %s (sent to %v)", want, got)
		}
	}
	if !strings.Contains(logs.String(), "failed to notify staff@example.com about 1 new drafts") {
		t.Errorf("failure not logged: %s", logs.String())
	}
	if len(s.sent) != 3 {
		t.Errorf("sent %d mails, want 3", len(s.sent))
	}
}
