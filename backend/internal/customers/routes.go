package customers

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

type routes struct {
	pool *pgxpool.Pool
	q    *customersdb.Queries
}

// Register adds the customer operations; every one is admin-only.
func Register(api huma.API, pool *pgxpool.Pool) {
	r := routes{pool: pool, q: customersdb.New(pool)}
	const prefix = "/api/v1/customers"
	tags := []string{"customers"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "customers-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List customers",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "customers-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add a customer", DefaultStatus: http.StatusCreated,
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "customers-get", Method: http.MethodGet, Path: prefix + "/{customer_id}", Tags: tags,
		Summary: "A customer", Errors: []int{http.StatusNotFound},
	}), r.get)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "customers-update", Method: http.MethodPatch, Path: prefix + "/{customer_id}", Tags: tags,
		Summary: "Change a customer's fields", Errors: []int{http.StatusNotFound},
	}), r.update)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "customers-deactivate", Method: http.MethodDelete, Path: prefix + "/{customer_id}", Tags: tags,
		Summary: "Deactivate a customer (never deleted)", Errors: []int{http.StatusNotFound},
	}), r.deactivate)
}

// notFound is Python's 404 for a missing row; any other error is unchanged.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Err(http.StatusNotFound, "customer_not_found", "customer not found")
	}
	return err
}

type listIn struct {
	Active platform.OptionalParam[bool] `query:"active"`
}

type listOut struct{ Body []CustomerOut }

func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	out, err := List(ctx, r.q, ListFilter{Active: in.Active.Ptr()})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

type customerBody struct{ Body CustomerOut }

type createIn struct{ Body CustomerCreate }

// create writes the customer.
func (r routes) create(ctx context.Context, in *createIn) (*customerBody, error) {
	out, err := Create(ctx, r.q, in.Body)
	if err != nil {
		return nil, err
	}
	return &customerBody{Body: out}, nil
}

type idIn struct {
	CustomerID uuid.UUID `path:"customer_id"`
}

func (r routes) get(ctx context.Context, in *idIn) (*customerBody, error) {
	out, err := Get(ctx, r.q, in.CustomerID)
	if err != nil {
		return nil, notFound(err)
	}
	return &customerBody{Body: out}, nil
}

type updateIn struct {
	CustomerID uuid.UUID `path:"customer_id"`
	Body       CustomerUpdate
}

// update updates the customer in one transaction.
func (r routes) update(ctx context.Context, in *updateIn) (*customerBody, error) {
	var out CustomerOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = Update(ctx, r.q.WithTx(tx), in.CustomerID, in.Body)
		return err
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &customerBody{Body: out}, nil
}

func (r routes) deactivate(ctx context.Context, in *idIn) (*customerBody, error) {
	c, err := r.q.DeactivateCustomer(ctx, in.CustomerID)
	if err != nil {
		return nil, notFound(err)
	}
	return &customerBody{Body: customerOut(c)}, nil
}
