package jobs

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// NextBatchOut is when the recurring scan next generates drafts for the
// Awaiting Finalization queue.
type NextBatchOut struct {
	SchedulerEnabled bool           `json:"scheduler_enabled" doc:"Whether this API runs the scheduled jobs (SCHEDULER_ENABLED)."`
	NextBatchAt      *platform.Time `json:"next_batch_at" doc:"The recurring scan's next daily slot (02:00 UTC) strictly after now; null when the scheduler is off."`
}

type nextBatchBody struct{ Body NextBatchOut }

// Register adds GET /api/v1/invoices/next-batch (admin-only). It lives here,
// not in invoices, because jobs imports invoices (the recurring scan): the
// slot is the scheduler's own (DailySlot, RecurringScanHour), and invoices
// importing jobs would be a cycle. chi matches the literal next-batch segment
// before invoices' {invoice_id}, whatever the registration order.
func Register(api huma.API, cfg platform.Config, clock platform.Clock) {
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-next-batch", Method: http.MethodGet, Path: "/api/v1/invoices/next-batch",
		Tags: []string{"invoices"}, Summary: "When the scheduler next generates recurring drafts",
	}), func(_ context.Context, _ *struct{}) (*nextBatchBody, error) {
		out := NextBatchOut{SchedulerEnabled: cfg.SchedulerEnabled}
		if cfg.SchedulerEnabled {
			// DailySlot is the latest slot at or before now, so the next one
			// is a day later: strictly after now, even at 02:00 sharp.
			next := DailySlot(RecurringScanHour)(clock.Now()).AddDate(0, 0, 1)
			out.NextBatchAt = &platform.Time{Time: next}
		}
		return &nextBatchBody{Body: out}, nil
	})
}
