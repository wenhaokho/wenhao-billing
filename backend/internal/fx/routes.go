package fx

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// constraints are the violations a client causes, as the errors they become
// (Python's sentences).
var constraints = map[string]platform.Constraint{
	"uq_fx_rates_triple": {Status: http.StatusConflict, Code: "fx_rate_exists", Detail: "fx rate for this currency pair and date already exists"},
}

type routes struct {
	pool   *pgxpool.Pool
	q      *fxdb.Queries
	cfg    platform.Config
	client *http.Client
}

// Register adds the base-currency and FX rate operations under
// /api/v1/accounting; every one is admin-only. client is the provider's
// HTTP client for "sync now".
func Register(api huma.API, pool *pgxpool.Pool, cfg platform.Config, client *http.Client) {
	r := routes{pool: pool, q: fxdb.New(pool), cfg: cfg, client: client}
	const prefix = "/api/v1/accounting"
	const rates = prefix + "/fx-rates"
	tags := []string{"accounting"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "fx-base-currency", Method: http.MethodGet, Path: prefix + "/base-currency", Tags: tags,
		Summary: "The ledger's base currency",
	}), r.baseCurrency)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "fx-rates-list", Method: http.MethodGet, Path: rates, Tags: tags,
		Summary: "List FX rates, newest first",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "fx-rates-create", Method: http.MethodPost, Path: rates, Tags: tags,
		Summary: "Add an FX rate", DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict},
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "fx-rates-sync", Method: http.MethodPost, Path: rates + "/sync", Tags: tags,
		Summary: "Pull the latest provider rates now", Errors: []int{http.StatusBadGateway},
	}), r.sync)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "fx-rates-latest", Method: http.MethodGet, Path: rates + "/latest", Tags: tags,
		Summary: "The newest rate of a pair, optionally on or before a date", Errors: []int{http.StatusNotFound},
	}), r.latest)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "fx-rates-delete", Method: http.MethodDelete, Path: rates + "/{rate_id}", Tags: tags,
		Summary: "Delete an FX rate", DefaultStatus: http.StatusNoContent, Errors: []int{http.StatusNotFound},
	}), r.delete)
}

type baseCurrencyOut struct{ Body BaseCurrencyOut }

func (r routes) baseCurrency(context.Context, *struct{}) (*baseCurrencyOut, error) {
	return &baseCurrencyOut{Body: BaseCurrencyOut{BaseCurrency: r.cfg.BaseCurrency}}, nil
}

// upper is an optional currency filter, upper-cased, or nil when left out.
func upper(p platform.OptionalParam[string]) *string {
	if !p.Set {
		return nil
	}
	s := strings.ToUpper(p.Value)
	return &s
}

type listIn struct {
	FromCurrency platform.OptionalParam[string] `query:"from_currency" minLength:"3" maxLength:"3"`
	ToCurrency   platform.OptionalParam[string] `query:"to_currency" minLength:"3" maxLength:"3"`
}

type listOut struct{ Body []RateOut }

func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	rows, err := r.q.ListRates(ctx, fxdb.ListRatesParams{FromCurrency: upper(in.FromCurrency), ToCurrency: upper(in.ToCurrency)})
	if err != nil {
		return nil, err
	}
	out := &listOut{Body: make([]RateOut, 0, len(rows))}
	for _, row := range rows {
		out.Body = append(out.Body, rateOut(row))
	}
	return out, nil
}

type rateBody struct{ Body RateOut }

type createIn struct{ Body RateCreate }

func (r routes) create(ctx context.Context, in *createIn) (*rateBody, error) {
	b := in.Body
	rate := b.Rate.Decimal()
	if rate.Sign() <= 0 {
		return nil, platform.FieldError("rate", "Input should be greater than 0")
	}
	row, err := r.q.CreateRate(ctx, fxdb.CreateRateParams{
		FromCurrency: strings.ToUpper(b.FromCurrency), ToCurrency: strings.ToUpper(b.ToCurrency),
		Rate: rate, AsOfDate: pgtype.Date{Time: b.AsOfDate.Time, Valid: true}, Source: b.Source.Or("MANUAL"),
	})
	if err != nil {
		return nil, platform.MapConstraint(err, constraints)
	}
	return &rateBody{Body: rateOut(row)}, nil
}

type syncOut struct{ Body SyncResult }

// sync runs Sync; a provider failure is a 502 whose detail is its sentence.
func (r routes) sync(ctx context.Context, _ *struct{}) (*syncOut, error) {
	res, err := Sync(ctx, r.pool, r.cfg, r.client)
	if errors.Is(err, ErrProvider) {
		return nil, platform.Err(http.StatusBadGateway, "fx_provider_error", err.Error())
	}
	if err != nil {
		return nil, err
	}
	return &syncOut{Body: res}, nil
}

type latestIn struct {
	FromCurrency string                            `query:"from_currency" required:"true" minLength:"3" maxLength:"3"`
	ToCurrency   platform.OptionalParam[string]    `query:"to_currency" minLength:"3" maxLength:"3" doc:"Defaults to the base currency."`
	OnDate       platform.OptionalParam[time.Time] `query:"on_date" timeFormat:"2006-01-02" doc:"The newest rate on or before this date; the newest of all when left out."`
}

func (r routes) latest(ctx context.Context, in *latestIn) (*rateBody, error) {
	to := r.cfg.BaseCurrency
	if in.ToCurrency.Set {
		to = in.ToCurrency.Value
	}
	params := fxdb.LatestRateParams{FromCurrency: strings.ToUpper(in.FromCurrency), ToCurrency: strings.ToUpper(to)}
	if in.OnDate.Set {
		params.OnDate = pgtype.Date{Time: in.OnDate.Value, Valid: true}
	}
	row, err := r.q.LatestRate(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, platform.Err(http.StatusNotFound, "fx_rate_none_on_or_before", "no rate found")
	}
	if err != nil {
		return nil, err
	}
	return &rateBody{Body: rateOut(row)}, nil
}

type deleteIn struct {
	RateID int64 `path:"rate_id"`
}

func (r routes) delete(ctx context.Context, in *deleteIn) (*struct{}, error) {
	n, err := r.q.DeleteRate(ctx, in.RateID)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, platform.Err(http.StatusNotFound, "fx_rate_not_found", "rate not found")
	}
	return &struct{}{}, nil
}
