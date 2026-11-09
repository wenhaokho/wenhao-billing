package invoices

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/schedule"
)

// Scan is scan_and_generate (workers/tasks/recurring.py:47-72): every
// recurring template with a cycle due today (not paused, schedule valid,
// today inside a cycle that has not ended) is triggered for that cycle, the
// templates oldest first. generated counts every invoice a trigger returned,
// idempotent replays included (as Python). notify hears once, after the
// commits, of the drafts this scan created; nil logs them. Only the cycle
// containing today is generated: missed cycles are never backfilled.
//
// Go changes this (port note): each trigger runs in its own transaction,
// and a failing one is logged and skipped, where Python rolled the whole
// run back. err is a failure to list the templates, or the context ending
// (the drafts committed before it, one committed just as it ended included,
// are still counted and notified). today is taken as its UTC calendar date.
func Scan(ctx context.Context, pool *pgxpool.Pool, today time.Time, notify DraftNotifier) (generated int, err error) {
	return scanWith(ctx, pool, today, notify, triggerAndSummarise)
}

// triggerFunc is triggerAndSummarise's shape: Scan's seam for tests.
type triggerFunc func(ctx context.Context, pool *pgxpool.Pool, templateID uuid.UUID, cycleKey string, today time.Time) (InvoiceOut, *DraftSummary, error)

// scanWith is Scan with trigger in place of triggerAndSummarise.
func scanWith(ctx context.Context, pool *pgxpool.Pool, today time.Time, notify DraftNotifier, trigger triggerFunc) (generated int, err error) {
	today = utcDate(today)
	if notify == nil {
		notify = LogNotifier{}
	}
	templates, err := invoicesdb.New(pool).ListScanTemplates(ctx)
	if err != nil {
		return 0, err
	}
	var drafts []DraftSummary
	for _, t := range templates {
		key, due := schedule.CycleKeyFor(storedConfig(t.BillingCycleRef), today)
		if !due {
			continue
		}
		_, summary, err := trigger(ctx, pool, t.InvoiceID, key, today)
		// A nil err means the trigger committed: count it before looking at
		// the context, which may have ended just after the commit.
		if err == nil {
			generated++
			if summary != nil {
				drafts = append(drafts, *summary)
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			notifyDrafts(ctx, notify, drafts)
			return generated, ctxErr
		}
		if err != nil {
			slog.ErrorContext(ctx, "recurring scan: trigger failed", "template_id", t.InvoiceID, "cycle_key", key, "error", err)
		}
	}
	notifyDrafts(ctx, notify, drafts)
	return generated, nil
}
