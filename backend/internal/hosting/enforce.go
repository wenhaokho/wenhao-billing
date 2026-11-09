package hosting

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/hosting/hostingdb"
)

// Result is one enforcement run's counts (Python's {"suspended",
// "restored", "errors"}).
type Result struct {
	Suspended, Restored, Errors int
}

// Enforce is Python's run_hosting_enforcement for today (a calendar date;
// the time of day is ignored): every hosting item with suspension on and
// status ACTIVE, SUSPEND_PENDING or SUSPENDED, oldest first, is suspended
// when one of its open invoices is overdue past its grace days, else
// restored when eligible. Each item runs in its own transaction; one item's
// failure is counted in Errors and logged, and the run goes on (a Cloudflare
// failure's ERROR and SUSPEND_PENDING stamps are kept, so the next run
// retries). The error is the listing's, or the context's when it ends the
// run early.
func Enforce(ctx context.Context, pool *pgxpool.Pool, cl *Client, today time.Time) (Result, error) {
	var res Result
	ids, err := hostingdb.New(pool).EnforcementItems(ctx)
	if err != nil {
		return res, fmt.Errorf("hosting: enforcement items: %w", err)
	}
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		var suspended bool
		restored, err := withItem(ctx, pool, id, func(q *hostingdb.Queries, it item) (bool, error) {
			// The item may have changed since the listing.
			if !it.IsHosting || !it.suspensionOn() || !slices.Contains([]string{statusActive, statusSuspendPending, statusSuspended}, it.status()) {
				return false, nil
			}
			overdue, err := isOverdue(ctx, q, it, day)
			if err != nil {
				return false, err
			}
			if overdue {
				suspended, err = suspend(ctx, q, cl, it)
				return false, err
			}
			return restore(ctx, q, cl, it)
		})
		switch {
		case err != nil:
			res.Errors++
			slog.WarnContext(ctx, "hosting enforcement: item failed", "item_id", id, "err", err)
		case suspended:
			res.Suspended++
		case restored:
			res.Restored++
		}
	}
	return res, nil
}

// isOverdue is Python's resolve_overdue_invoice != None: an open invoice
// for the item with a balance whose due date plus the grace days (NULL is
// 0) is strictly before day. An invoice with no due date is never overdue.
func isOverdue(ctx context.Context, q *hostingdb.Queries, it item, day time.Time) (bool, error) {
	grace := 0
	if it.HostingGraceDays != nil {
		grace = int(*it.HostingGraceDays)
	}
	rows, err := q.OpenInvoicesForItem(ctx, it.ItemID)
	if err != nil {
		return false, fmt.Errorf("hosting: open invoices: %w", err)
	}
	for _, r := range rows {
		if !r.DueDate.Valid {
			continue
		}
		if r.DueDate.Time.AddDate(0, 0, grace).Before(day) {
			return true, nil
		}
	}
	return false, nil
}
