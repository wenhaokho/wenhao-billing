package quotations

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

// registerLifecycle adds the four status transitions (quotations.py:109-163).
func (r routes) registerLifecycle(api huma.API, prefix string, tags []string) {
	one := prefix + "/{quotation_id}/"
	errs := []int{http.StatusBadRequest}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-mark-sent", Method: http.MethodPost, Path: one + "mark-sent", Tags: tags,
		Summary: "Mark a DRAFT or SENT quotation SENT now", Errors: errs,
	}), r.markSent)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-accept", Method: http.MethodPost, Path: one + "accept", Tags: tags,
		Summary: "Accept a DRAFT or SENT quotation", Errors: errs,
	}), r.accept)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-decline", Method: http.MethodPost, Path: one + "decline", Tags: tags,
		Summary: "Decline a DRAFT or SENT quotation", Errors: errs,
	}), r.decline)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-void", Method: http.MethodPost, Path: one + "void", Tags: tags,
		Summary: "Void any quotation that is not INVOICED", Errors: errs,
	}), r.void)
}

// transition runs change on quotation id, read FOR UPDATE, when allowed says
// its status may move (else refusal is the 400), and answers the row after.
func (r routes) transition(
	ctx context.Context, id uuid.UUID, allowed func(status string) bool, refusal func(status string) error,
	change func(q *quotationsdb.Queries) error,
) (*quotationBody, error) {
	return r.inTx(ctx, func(tx pgx.Tx) (QuotationOut, error) {
		q := quotationsdb.New(tx)
		cur, err := lock(ctx, q, id)
		if err != nil {
			return QuotationOut{}, err
		}
		if !allowed(cur.Status) {
			return QuotationOut{}, refusal(cur.Status)
		}
		if err := change(q); err != nil {
			return QuotationOut{}, err
		}
		return get(ctx, q, id)
	})
}

func draftOrSent(status string) bool { return status == "DRAFT" || status == "SENT" }

// instant is the clock's UTC instant, as the timestamp-without-zone columns
// hold it (Python's datetime.utcnow()).
func instant(c platform.Clock) time.Time { return c.Now().UTC() }

func (r routes) markSent(ctx context.Context, in *idIn) (*quotationBody, error) {
	at := instant(r.clock)
	return r.transition(ctx, in.QuotationID, draftOrSent,
		func(s string) error { return refuse("quotation_not_sendable", "cannot mark-sent from status %s", s) },
		func(q *quotationsdb.Queries) error {
			return q.MarkQuotationSent(ctx, quotationsdb.MarkQuotationSentParams{QuotationID: in.QuotationID, LastSentAt: &at})
		})
}

// AcceptRequest is the body of an accept (schemas/quotation.py:81-82): the
// body is required though accepted_by is optional.
type AcceptRequest struct {
	AcceptedBy platform.Optional[string] `json:"accepted_by" maxLength:"255" doc:"Who accepted; stored as sent, null clears."`
}

type acceptIn struct {
	QuotationID uuid.UUID `path:"quotation_id"`
	Body        AcceptRequest
}

func (r routes) accept(ctx context.Context, in *acceptIn) (*quotationBody, error) {
	at := instant(r.clock)
	return r.transition(ctx, in.QuotationID, draftOrSent,
		func(s string) error { return refuse("quotation_not_acceptable", "cannot accept from status %s", s) },
		func(q *quotationsdb.Queries) error {
			return q.AcceptQuotation(ctx, quotationsdb.AcceptQuotationParams{
				QuotationID: in.QuotationID, AcceptedAt: &at, AcceptedBy: in.Body.AcceptedBy.Merge(nil),
			})
		})
}

func (r routes) decline(ctx context.Context, in *idIn) (*quotationBody, error) {
	return r.transition(ctx, in.QuotationID, draftOrSent,
		func(s string) error { return refuse("quotation_not_declinable", "cannot decline from status %s", s) },
		func(q *quotationsdb.Queries) error {
			return q.SetQuotationStatus(ctx, quotationsdb.SetQuotationStatusParams{QuotationID: in.QuotationID, Status: "DECLINED"})
		})
}

// void is permissive as Python's: any status but INVOICED, VOID included.
func (r routes) void(ctx context.Context, in *idIn) (*quotationBody, error) {
	return r.transition(ctx, in.QuotationID, func(s string) bool { return s != "INVOICED" },
		func(string) error { return refuse("quotation_invoiced", "cannot void an already-invoiced quotation") },
		func(q *quotationsdb.Queries) error {
			return q.SetQuotationStatus(ctx, quotationsdb.SetQuotationStatusParams{QuotationID: in.QuotationID, Status: "VOID"})
		})
}
