package stats

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

type routes struct {
	pool  *pgxpool.Pool
	cfg   platform.Config
	clock platform.Clock
}

// Register adds the dashboard operation under /api/v1/stats; it is
// admin-only.
func Register(api huma.API, pool *pgxpool.Pool, cfg platform.Config, clock platform.Clock) {
	r := routes{pool: pool, cfg: cfg, clock: clock}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "stats-dashboard", Method: http.MethodGet, Path: "/api/v1/stats/dashboard", Tags: []string{"stats"},
		Summary: "Dashboard statistics, recent activity and upcoming invoices",
	}), r.dashboard)
}

type dashboardOut struct{ Body DashboardStats }

func (r routes) dashboard(ctx context.Context, _ *struct{}) (*dashboardOut, error) {
	today := platform.TodayUTC(r.clock)
	core, err := Dashboard(ctx, r.pool, r.cfg.BaseCurrency, today, r.clock.Now())
	if err != nil {
		return nil, err
	}
	activity, err := RecentActivity(ctx, r.pool)
	if err != nil {
		return nil, err
	}
	upcoming, err := Upcoming(ctx, r.pool, today)
	if err != nil {
		return nil, err
	}
	return &dashboardOut{Body: newDashboardStats(core, activity, upcoming)}, nil
}
