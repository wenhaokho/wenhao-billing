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
	rows, err := r.q.ListCustomers(ctx, in.Active.Ptr())
	if err != nil {
		return nil, err
	}
	out := &listOut{Body: make([]CustomerOut, 0, len(rows))}
	for _, c := range rows {
		out.Body = append(out.Body, customerOut(c))
	}
	return out, nil
}

type customerBody struct{ Body CustomerOut }

type createIn struct{ Body CustomerCreate }

// create checks the CC lists (pydantic's field validation), then normalises
// the withholding and writes the customer.
func (r routes) create(ctx context.Context, in *createIn) (*customerBody, error) {
	b := in.Body
	if err := cleanCC(&b.CustomerFields); err != nil {
		return nil, err
	}
	out, err := create(ctx, r.q, b)
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

// update checks the CC lists before looking the customer up, as Python's
// request validation came before its 404.
func (r routes) update(ctx context.Context, in *updateIn) (*customerBody, error) {
	b := in.Body
	if err := cleanCC(&b.CustomerFields); err != nil {
		return nil, err
	}
	var out CustomerOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = update(ctx, r.q.WithTx(tx), in.CustomerID, b)
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
