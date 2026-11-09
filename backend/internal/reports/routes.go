package reports

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/reports/reportsdb"
)

type routes struct {
	q     *reportsdb.Queries
	cfg   platform.Config
	clock platform.Clock
}

// Register adds the report operations under /api/v1/accounting/reports; every
// one is admin-only.
func Register(api huma.API, pool *pgxpool.Pool, cfg platform.Config, clock platform.Clock) {
	r := routes{q: reportsdb.New(pool), cfg: cfg, clock: clock}
	const prefix = "/api/v1/accounting/reports"
	tags := []string{"accounting"}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "reports-trial-balance", Method: http.MethodGet, Path: prefix + "/trial-balance", Tags: tags,
		Summary: "Trial balance by account and currency",
	}), r.trialBalance)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "reports-balance-sheet", Method: http.MethodGet, Path: prefix + "/balance-sheet", Tags: tags,
		Summary: "Balance sheet in the base currency",
	}), r.balanceSheet)
}

// asOfIn is the report date: UTC today when left out.
type asOfIn struct {
	AsOf platform.OptionalParam[time.Time] `query:"as_of" timeFormat:"2006-01-02" doc:"Defaults to today (UTC)."`
}

func (r routes) asOf(in *asOfIn) time.Time {
	if in.AsOf.Set {
		return in.AsOf.Value
	}
	return platform.TodayUTC(r.clock)
}

type trialBalanceOut struct{ Body TrialBalanceReport }

func (r routes) trialBalance(ctx context.Context, in *asOfIn) (*trialBalanceOut, error) {
	asOf := r.asOf(in)
	rows, err := r.q.TrialBalanceRows(ctx, cutoff(asOf))
	if err != nil {
		return nil, err
	}
	return &trialBalanceOut{Body: trialBalance(asOf, r.cfg.BaseCurrency, rows)}, nil
}

type balanceSheetOut struct{ Body BalanceSheetReport }

func (r routes) balanceSheet(ctx context.Context, in *asOfIn) (*balanceSheetOut, error) {
	asOf := r.asOf(in)
	rows, err := r.q.BalanceSheetRows(ctx, cutoff(asOf))
	if err != nil {
		return nil, err
	}
	return &balanceSheetOut{Body: balanceSheet(asOf, r.cfg.BaseCurrency, rows)}, nil
}
