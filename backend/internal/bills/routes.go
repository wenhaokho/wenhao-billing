package bills

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

type routes struct {
	pool  *pgxpool.Pool
	q     *billsdb.Queries
	clock platform.Clock
	// base is the ledger's base currency: opening, editing and voiding a
	// bill post in it.
	base string
}

// Register adds the bill operations; every one is admin-only. base is the
// ledger's base currency (a bill posts to the ledger at open).
func Register(api huma.API, pool *pgxpool.Pool, clock platform.Clock, base string) {
	r := routes{pool: pool, q: billsdb.New(pool), clock: clock, base: base}
	const prefix = "/api/v1/bills"
	tags := []string{"bills"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "bills-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add a DRAFT bill", DefaultStatus: http.StatusCreated,
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "bills-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List bills, newest first",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "bills-get", Method: http.MethodGet, Path: prefix + "/{bill_id}", Tags: tags,
		Summary: "A bill", Errors: []int{http.StatusNotFound},
	}), r.get)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "bills-update", Method: http.MethodPatch, Path: prefix + "/{bill_id}", Tags: tags,
		Summary: "Change a DRAFT or OPEN bill's fields; a list of lines reprices it",
		Errors:  []int{http.StatusBadRequest, http.StatusNotFound},
	}), r.update)
	r.registerPayments(api, prefix, tags)
}

// inTx runs write in one transaction and answers its bill.
func (r routes) inTx(ctx context.Context, write func(pgx.Tx) (BillOut, error)) (*billBody, error) {
	var out BillOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = write(tx)
		return err
	})
	if err != nil {
		return nil, clientError(err)
	}
	return &billBody{Body: out}, nil
}

type billBody struct{ Body BillOut }

type createIn struct{ Body BillCreate }

func (r routes) create(ctx context.Context, in *createIn) (*billBody, error) {
	return r.inTx(ctx, func(tx pgx.Tx) (BillOut, error) { return CreateBill(ctx, tx, in.Body) })
}

type listIn struct {
	Status   []string                          `query:"status,explode" doc:"Repeat for several (status=OPEN&status=PARTIAL); any string."`
	VendorID platform.OptionalParam[uuid.UUID] `query:"vendor_id"`
}

type listOut struct{ Body []BillOut }

func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	out, err := List(ctx, r.q, ListFilter{Statuses: in.Status, VendorID: in.VendorID.Ptr()})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

type idIn struct {
	BillID uuid.UUID `path:"bill_id"`
}

func (r routes) get(ctx context.Context, in *idIn) (*billBody, error) {
	out, err := get(ctx, r.q, in.BillID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	return &billBody{Body: out}, nil
}

type updateIn struct {
	BillID uuid.UUID `path:"bill_id"`
	Body   BillUpdate
}

func (r routes) update(ctx context.Context, in *updateIn) (*billBody, error) {
	return r.inTx(ctx, func(tx pgx.Tx) (BillOut, error) { return UpdateBill(ctx, tx, r.base, in.BillID, in.Body) })
}
