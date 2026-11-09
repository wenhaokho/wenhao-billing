package accounts

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/accounts/accountsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// constraints are the violations a client causes, as the errors they become
// (Python's sentences).
var constraints = map[string]platform.Constraint{
	"chart_of_accounts_code_key": {Status: http.StatusConflict, Code: "account_code_exists", Detail: "account code already exists"},
}

type routes struct {
	pool *pgxpool.Pool
	q    *accountsdb.Queries
}

// Register adds the chart-of-accounts operations; every one is admin-only.
func Register(api huma.API, pool *pgxpool.Pool) {
	r := routes{pool: pool, q: accountsdb.New(pool)}
	const prefix = "/api/v1/accounting/chart-of-accounts"
	tags := []string{"accounting"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "accounts-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List the chart of accounts",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "accounts-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add an account", DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict},
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "accounts-get", Method: http.MethodGet, Path: prefix + "/{account_id}", Tags: tags,
		Summary: "An account", Errors: []int{http.StatusNotFound},
	}), r.get)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "accounts-update", Method: http.MethodPatch, Path: prefix + "/{account_id}", Tags: tags,
		Summary: "Change an account's fields", Errors: []int{http.StatusNotFound, http.StatusConflict},
	}), r.update)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "accounts-deactivate", Method: http.MethodDelete, Path: prefix + "/{account_id}", Tags: tags,
		Summary: "Deactivate an account (never deleted)", Errors: []int{http.StatusNotFound},
	}), r.deactivate)
}

// notFound is Python's 404 for a missing row; any other error is unchanged.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Err(http.StatusNotFound, "account_not_found", "account not found")
	}
	return err
}

type listIn struct {
	Active platform.OptionalParam[bool] `query:"active"`
}

type listOut struct{ Body []AccountOut }

func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	rows, err := r.q.ListAccounts(ctx, in.Active.Ptr())
	if err != nil {
		return nil, err
	}
	out := &listOut{Body: make([]AccountOut, 0, len(rows))}
	for _, a := range rows {
		out.Body = append(out.Body, accountOut(a))
	}
	return out, nil
}

type accountBody struct{ Body AccountOut }

type createIn struct{ Body AccountCreate }

func (r routes) create(ctx context.Context, in *createIn) (*accountBody, error) {
	b := in.Body
	a, err := r.q.CreateAccount(ctx, accountsdb.CreateAccountParams{
		Code: b.Code, Name: b.Name, Type: b.Type, Active: b.Active.Or(true),
	})
	if err != nil {
		return nil, platform.MapConstraint(err, constraints)
	}
	return &accountBody{Body: accountOut(a)}, nil
}

type idIn struct {
	AccountID int32 `path:"account_id"`
}

func (r routes) get(ctx context.Context, in *idIn) (*accountBody, error) {
	a, err := r.q.GetAccount(ctx, in.AccountID)
	if err != nil {
		return nil, notFound(err)
	}
	return &accountBody{Body: accountOut(a)}, nil
}

type updateIn struct {
	AccountID int32 `path:"account_id"`
	Body      AccountUpdate
}

// update is the PATCH pattern: read the row locked, overlay the fields sent
// (a null never gets here: NotNull refuses it), and write every column back.
func (r routes) update(ctx context.Context, in *updateIn) (*accountBody, error) {
	b := in.Body
	var a accountsdb.ChartOfAccount
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		cur, err := q.GetAccountForUpdate(ctx, in.AccountID)
		if err != nil {
			return notFound(err)
		}
		a, err = q.UpdateAccount(ctx, accountsdb.UpdateAccountParams{
			AccountID: cur.AccountID,
			Code:      b.Code.Or(cur.Code),
			Name:      b.Name.Or(cur.Name),
			Type:      b.Type.Or(cur.Type),
			Active:    b.Active.Or(cur.Active),
		})
		return platform.MapConstraint(err, constraints)
	})
	if err != nil {
		return nil, err
	}
	return &accountBody{Body: accountOut(a)}, nil
}

func (r routes) deactivate(ctx context.Context, in *idIn) (*accountBody, error) {
	a, err := r.q.DeactivateAccount(ctx, in.AccountID)
	if err != nil {
		return nil, notFound(err)
	}
	return &accountBody{Body: accountOut(a)}, nil
}
