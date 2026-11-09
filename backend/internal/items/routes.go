package items

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/items/itemsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// constraints are the violations a client causes, as the errors they become.
// Python answered 500 for each (an unhandled IntegrityError).
var constraints = map[string]platform.Constraint{
	"items_sku_key":                 {Status: http.StatusConflict, Code: "sku_exists", Detail: "sku already exists"},
	"items_revenue_account_id_fkey": {Status: http.StatusUnprocessableEntity, Code: "account_not_found", Detail: "account not found"},
	"fk_items_expense_account":      {Status: http.StatusUnprocessableEntity, Code: "account_not_found", Detail: "account not found"},
}

type routes struct {
	pool *pgxpool.Pool
	q    *itemsdb.Queries
}

// Register adds the item operations; every one is admin-only.
func Register(api huma.API, pool *pgxpool.Pool) {
	r := routes{pool: pool, q: itemsdb.New(pool)}
	const prefix = "/api/v1/items"
	tags := []string{"items"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "items-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List items",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "items-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add an item", DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict},
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "items-get", Method: http.MethodGet, Path: prefix + "/{item_id}", Tags: tags,
		Summary: "An item", Errors: []int{http.StatusNotFound},
	}), r.get)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "items-update", Method: http.MethodPatch, Path: prefix + "/{item_id}", Tags: tags,
		Summary: "Change an item's fields", Errors: []int{http.StatusNotFound, http.StatusConflict},
	}), r.update)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "items-deactivate", Method: http.MethodDelete, Path: prefix + "/{item_id}", Tags: tags,
		Summary: "Deactivate an item (never deleted)", Errors: []int{http.StatusNotFound},
	}), r.deactivate)
}

// notFound is Python's 404 for a missing row; any other error is unchanged.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Err(http.StatusNotFound, "item_not_found", "item not found")
	}
	return err
}

type listIn struct {
	Active    platform.OptionalParam[bool] `query:"active"`
	ItemType  string                       `query:"item_type" doc:"Empty lists every type."`
	IsHosting platform.OptionalParam[bool] `query:"is_hosting"`
}

type listOut struct{ Body []ItemOut }

func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	out, err := list(ctx, r.q, itemsdb.ListItemsParams{Active: in.Active.Ptr(), ItemType: in.ItemType, IsHosting: in.IsHosting.Ptr()})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

type itemBody struct{ Body ItemOut }

type createIn struct{ Body ItemCreate }

// create checks the prices, then the hosting fields (pydantic's field
// errors come before its model validator), and writes the item and its
// target in one transaction.
func (r routes) create(ctx context.Context, in *createIn) (*itemBody, error) {
	b := in.Body
	if err := checkPrices(b.DefaultUnitPrice, b.DefaultPurchasePrice); err != nil {
		return nil, err
	}
	if err := checkHosting(b); err != nil {
		return nil, err
	}
	var out ItemOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = create(ctx, r.q.WithTx(tx), b)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &itemBody{Body: out}, nil
}

type idIn struct {
	ItemID uuid.UUID `path:"item_id"`
}

func (r routes) get(ctx context.Context, in *idIn) (*itemBody, error) {
	out, err := Get(ctx, r.q, in.ItemID)
	if err != nil {
		return nil, notFound(err)
	}
	return &itemBody{Body: out}, nil
}

type updateIn struct {
	ItemID uuid.UUID `path:"item_id"`
	Body   ItemUpdate
}

func (r routes) update(ctx context.Context, in *updateIn) (*itemBody, error) {
	b := in.Body
	if err := checkPrices(b.DefaultUnitPrice, b.DefaultPurchasePrice); err != nil {
		return nil, err
	}
	var out ItemOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = update(ctx, r.q.WithTx(tx), in.ItemID, b)
		return err
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &itemBody{Body: out}, nil
}

func (r routes) deactivate(ctx context.Context, in *idIn) (*itemBody, error) {
	var out ItemOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		it, err := q.DeactivateItem(ctx, in.ItemID)
		if err != nil {
			return err
		}
		t, err := target(ctx, q, it.ItemID)
		out = itemOut(it, t)
		return err
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &itemBody{Body: out}, nil
}
