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

// Register adds the dashboard, revenue-monthly and invoices-summary operations under /api/v1/stats; it is
// admin-only.
func Register(api huma.API, pool *pgxpool.Pool, cfg platform.Config, clock platform.Clock) {
	r := routes{pool: pool, cfg: cfg, clock: clock}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "stats-dashboard", Method: http.MethodGet, Path: "/api/v1/stats/dashboard", Tags: []string{"stats"},
		Summary: "Dashboard statistics, recent activity and upcoming invoices",
	}), r.dashboard)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "stats-revenue-monthly", Method: http.MethodGet, Path: "/api/v1/stats/revenue-monthly", Tags: []string{"stats"},
		Summary: "Invoiced revenue by calendar month for the last 12 months",
	}), r.revenueMonthly)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "stats-invoices-summary", Method: http.MethodGet, Path: "/api/v1/stats/invoices-summary", Tags: []string{"stats"},
		Summary: "Overdue and due-in-30-days totals and the average days to pay",
	}), r.invoicesSummary)
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

type revenueMonthlyOut struct{ Body MonthlyRevenueResponse }

func (r routes) revenueMonthly(ctx context.Context, _ *struct{}) (*revenueMonthlyOut, error) {
	body, err := MonthlyRevenue(ctx, r.pool, r.cfg.BaseCurrency, platform.TodayUTC(r.clock))
	if err != nil {
		return nil, err
	}
	return &revenueMonthlyOut{Body: body}, nil
}

type invoicesSummaryOut struct{ Body InvoicesSummaryResponse }

func (r routes) invoicesSummary(ctx context.Context, _ *struct{}) (*invoicesSummaryOut, error) {
	body, err := InvoicesSummary(ctx, r.pool, platform.TodayUTC(r.clock))
	if err != nil {
		return nil, err
	}
	return &invoicesSummaryOut{Body: body}, nil
}
