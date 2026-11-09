package quotations

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

type routes struct {
	pool  *pgxpool.Pool
	q     *quotationsdb.Queries
	clock platform.Clock
}

// Register adds the quotation operations; every one is admin-only.
func Register(api huma.API, pool *pgxpool.Pool, clock platform.Clock) {
	r := routes{pool: pool, q: quotationsdb.New(pool), clock: clock}
	const prefix = "/api/v1/quotations"
	tags := []string{"quotations"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add a DRAFT quotation", DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusBadRequest, http.StatusConflict},
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List quotations, newest first",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-get", Method: http.MethodGet, Path: prefix + "/{quotation_id}", Tags: tags,
		Summary: "A quotation", Errors: []int{http.StatusNotFound},
	}), r.get)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-update", Method: http.MethodPatch, Path: prefix + "/{quotation_id}", Tags: tags,
		Summary: "Change a DRAFT or SENT quotation's fields and reprice it",
		Errors:  []int{http.StatusBadRequest, http.StatusConflict},
	}), r.update)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "quotations-delete", Method: http.MethodDelete, Path: prefix + "/{quotation_id}", Tags: tags,
		Summary: "Delete a DRAFT quotation and its lines", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusBadRequest},
	}), r.delete)
	r.registerLifecycle(api, prefix, tags)
	r.registerConvert(api, prefix, tags)
}

// inTx runs write in one transaction and answers its quotation.
func (r routes) inTx(ctx context.Context, write func(pgx.Tx) (QuotationOut, error)) (*quotationBody, error) {
	var out QuotationOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = write(tx)
		return err
	})
	if err != nil {
		return nil, clientError(err)
	}
	return &quotationBody{Body: out}, nil
}

type quotationBody struct{ Body QuotationOut }

type createIn struct{ Body QuotationCreate }

func (r routes) create(ctx context.Context, in *createIn) (*quotationBody, error) {
	if err := checkFields(in.Body.QuotationFields, in.Body.LineItems); err != nil {
		return nil, err
	}
	today := platform.TodayUTC(r.clock)
	return r.inTx(ctx, func(tx pgx.Tx) (QuotationOut, error) { return createQuotation(ctx, tx, in.Body, today) })
}

type listIn struct {
	Status     []string                          `query:"status,explode" doc:"Repeat for several (status=DRAFT&status=SENT); any string."`
	CustomerID platform.OptionalParam[uuid.UUID] `query:"customer_id"`
	ProjectID  platform.OptionalParam[uuid.UUID] `query:"project_id"`
	Limit      int32                             `query:"limit" default:"200" minimum:"1" maximum:"500"`
}

type listOut struct{ Body []QuotationOut }

func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	var statuses []string
	if len(in.Status) > 0 {
		statuses = in.Status
	}
	rows, err := r.q.ListQuotations(ctx, quotationsdb.ListQuotationsParams{
		Statuses: statuses, CustomerID: pgUUID(in.CustomerID.Ptr()), ProjectID: pgUUID(in.ProjectID.Ptr()), RowLimit: in.Limit,
	})
	if err != nil {
		return nil, err
	}
	out, err := list(ctx, r.q, rows)
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

type idIn struct {
	QuotationID uuid.UUID `path:"quotation_id"`
}

func (r routes) get(ctx context.Context, in *idIn) (*quotationBody, error) {
	out, err := get(ctx, r.q, in.QuotationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, platform.Err(http.StatusNotFound, "quotation_not_found", "quotation not found")
	}
	if err != nil {
		return nil, err
	}
	return &quotationBody{Body: out}, nil
}

type updateIn struct {
	QuotationID uuid.UUID `path:"quotation_id"`
	Body        QuotationUpdate
}

func (r routes) update(ctx context.Context, in *updateIn) (*quotationBody, error) {
	var lines []QuotationLineItemIn
	if in.Body.LineItems != nil {
		lines = *in.Body.LineItems
	}
	if err := checkFields(in.Body.QuotationFields, lines); err != nil {
		return nil, err
	}
	return r.inTx(ctx, func(tx pgx.Tx) (QuotationOut, error) { return updateQuotation(ctx, tx, in.QuotationID, in.Body) })
}

func (r routes) delete(ctx context.Context, in *idIn) (*struct{}, error) {
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error { return deleteQuotation(ctx, tx, in.QuotationID) })
	if err != nil {
		return nil, clientError(err)
	}
	return nil, nil
}
