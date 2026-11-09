package invoices

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/pdf"
)

// EmailNotifier mails the recurring-drafts digest to every user
// (notify_users_of_new_drafts, draft_notifications.py). It is best effort:
// a failure is logged and the next recipient is still tried.
type EmailNotifier struct {
	Q          *invoicesdb.Queries
	Sender     email.Sender
	AppBaseURL string
	Logger     *slog.Logger
}

// NotifyNewDrafts sends one digest per user; none is no mail.
func (n EmailNotifier) NotifyNewDrafts(ctx context.Context, drafts []DraftSummary) {
	if len(drafts) == 0 {
		return
	}
	logger := n.Logger
	if logger == nil {
		logger = slog.Default()
	}
	emails, err := n.Q.ListUserEmails(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "failed to list users for the new-drafts digest", "drafts", len(drafts), "err", err)
		return
	}
	subject, text, htmlBody := BuildDraftsDigest(drafts, n.AppBaseURL)
	for _, to := range emails {
		if err := n.Sender.Send(ctx, email.Message{To: to, Subject: subject, Text: text, HTML: htmlBody}); err != nil {
			logger.ErrorContext(ctx, fmt.Sprintf("failed to notify %s about %d new drafts", to, len(drafts)), "err", err)
		}
	}
}

// BuildDraftsDigest is build_recurring_drafts_email (email.py:152-193). The
// amount is pdf.FmtAmount (Go change 3: IDR has no decimals) and the
// HTML escapes every value (Go change 7).
func BuildDraftsDigest(drafts []DraftSummary, appBaseURL string) (subject, text, htmlBody string) {
	n := len(drafts)
	s := "s"
	if n == 1 {
		s = ""
	}
	subject = fmt.Sprintf("%d draft invoice%s awaiting your approval", n, s)
	intro := fmt.Sprintf("The recurring billing scan generated %d draft invoice%s that need your review and approval:", n, s)
	const outro = "Each invoice stays in the Awaiting Finalization queue until you approve and issue it."
	base := strings.TrimRight(appBaseURL, "/")

	lines := []string{"Hi,", "", intro, ""}
	var rows strings.Builder
	for _, d := range drafts {
		name := "(unknown customer)"
		if d.CustomerName != nil {
			name = *d.CustomerName
		}
		link := fmt.Sprintf("%s/invoices/%s/edit", base, d.InvoiceID)
		amount := pdf.FmtAmount(d.Amount, d.Currency)
		lines = append(lines, fmt.Sprintf("- %s · %s · %s · cycle %s", d.InvoiceNumber, name, amount, d.CycleDate), "  "+link)
		fmt.Fprintf(&rows, `<li><a href="%s">%s</a> — %s — %s — cycle %s</li>`,
			html.EscapeString(link), html.EscapeString(d.InvoiceNumber), html.EscapeString(name),
			html.EscapeString(amount), html.EscapeString(d.CycleDate))
	}
	lines = append(lines, "", outro)
	text = strings.Join(lines, "\n") + "\n"
	htmlBody = fmt.Sprintf("<p>Hi,</p><p>%s</p><ul>%s</ul><p>%s</p>", intro, rows.String(), outro)
	return subject, text, htmlBody
}
