package invoices

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DraftSummary is one draft a recurring cycle generated, as the
// notification lists it (draft_notifications.py).
type DraftSummary struct {
	InvoiceID     uuid.UUID
	InvoiceNumber string
	CustomerName  *string
	Currency      string
	Amount        decimal.Decimal
	CycleDate     string
}

// DraftNotifier tells the team about newly generated drafts. It is best
// effort: it never fails its caller, which has already committed.
type DraftNotifier interface {
	NotifyNewDrafts(ctx context.Context, drafts []DraftSummary)
}

// LogNotifier logs the drafts; it stands in until the email plan brings
// the real sender. A nil Logger is slog's default.
type LogNotifier struct{ Logger *slog.Logger }

// notifyDrafts hands the drafts to n once they are committed; none is no
// call (notify_users_of_new_drafts, draft_notifications.py:55-58). The
// notifier gets ctx's values without its cancellation: the drafts are
// committed, so a scan or request ending just after the commit must not
// silence them. Scan and the trigger route both notify through here. A
// notifier that panics breaks its promise: the panic is logged, never the
// caller's failure.
func notifyDrafts(ctx context.Context, n DraftNotifier, drafts []DraftSummary) {
	if len(drafts) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	defer func() {
		if p := recover(); p != nil {
			slog.ErrorContext(ctx, "draft notifier failed", "drafts", len(drafts), "panic", p)
		}
	}()
	n.NotifyNewDrafts(ctx, drafts)
}

// NotifyNewDrafts logs one line per draft; none is nothing.
func (n LogNotifier) NotifyNewDrafts(ctx context.Context, drafts []DraftSummary) {
	logger := n.Logger
	if logger == nil {
		logger = slog.Default()
	}
	for _, d := range drafts {
		logger.InfoContext(ctx, "new recurring draft",
			"invoice_id", d.InvoiceID, "invoice_number", d.InvoiceNumber, "currency", d.Currency,
			"amount", d.Amount.StringFixed(4), "cycle_date", d.CycleDate)
	}
}
