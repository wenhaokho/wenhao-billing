package vendors

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
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors/vendorsdb"
)

type routes struct {
	pool *pgxpool.Pool
	q    *vendorsdb.Queries
}

// Register adds the vendor operations; every one is admin-only.
func Register(api huma.API, pool *pgxpool.Pool) {
	r := routes{pool: pool, q: vendorsdb.New(pool)}
	const prefix = "/api/v1/vendors"
	tags := []string{"vendors"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "vendors-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List vendors",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "vendors-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add a vendor", DefaultStatus: http.StatusCreated,
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "vendors-get", Method: http.MethodGet, Path: prefix + "/{vendor_id}", Tags: tags,
		Summary: "A vendor", Errors: []int{http.StatusNotFound},
	}), r.get)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "vendors-update", Method: http.MethodPatch, Path: prefix + "/{vendor_id}", Tags: tags,
		Summary: "Change a vendor's fields", Errors: []int{http.StatusNotFound},
	}), r.update)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "vendors-deactivate", Method: http.MethodDelete, Path: prefix + "/{vendor_id}", Tags: tags,
		Summary: "Deactivate a vendor (never deleted)", Errors: []int{http.StatusNotFound},
	}), r.deactivate)
}

// notFound is Python's 404 for a missing row; any other error is unchanged.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Err(http.StatusNotFound, "vendor_not_found", "vendor not found")
	}
	return err
}

type listIn struct {
	Active platform.OptionalParam[bool] `query:"active"`
}

type listOut struct{ Body []VendorOut }

func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	out, err := List(ctx, r.q, ListFilter{Active: in.Active.Ptr()})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

type vendorBody struct{ Body VendorOut }

type createIn struct{ Body VendorCreate }

func (r routes) create(ctx context.Context, in *createIn) (*vendorBody, error) {
	out, err := Create(ctx, r.q, in.Body)
	if err != nil {
		return nil, err
	}
	return &vendorBody{Body: out}, nil
}

type idIn struct {
	VendorID uuid.UUID `path:"vendor_id"`
}

func (r routes) get(ctx context.Context, in *idIn) (*vendorBody, error) {
	out, err := Get(ctx, r.q, in.VendorID)
	if err != nil {
		return nil, notFound(err)
	}
	return &vendorBody{Body: out}, nil
}

type updateIn struct {
	VendorID uuid.UUID `path:"vendor_id"`
	Body     VendorUpdate
}

// update is the PATCH pattern: the row locked, overlaid and written back
// in one transaction (Update).
func (r routes) update(ctx context.Context, in *updateIn) (*vendorBody, error) {
	var v VendorOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		v, err = Update(ctx, r.q.WithTx(tx), in.VendorID, in.Body)
		return err
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &vendorBody{Body: v}, nil
}

func (r routes) deactivate(ctx context.Context, in *idIn) (*vendorBody, error) {
	v, err := r.q.DeactivateVendor(ctx, in.VendorID)
	if err != nil {
		return nil, notFound(err)
	}
	return &vendorBody{Body: vendorOut(v)}, nil
}
