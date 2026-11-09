// Package stats is the dashboard statistics: the counts, open receivables,
// quotation pipeline, MRR and open AR in the base currency that the dashboard
// route and the MCP get_stats tool share (Python's services/stats.py).
package stats

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// CurrencyBaseAmount is a total converted into the base currency.
type CurrencyBaseAmount struct {
	Currency   string        `json:"currency"`
	BaseAmount platform.Dec4 `json:"base_amount"`
}

// DashboardCore is the dashboard's statistics that are plain queries, in the
// order the MCP get_stats tool returns them. Every list is non-nil.
type DashboardCore struct {
	AwaitingReviewCount   int                       `json:"awaiting_review_count"`
	DraftCount            int                       `json:"draft_count" doc:"DRAFT invoices that are not templates."`
	SentCount             int                       `json:"sent_count" doc:"OPEN and SENT invoices (templates and PARTIAL as Python counts them)."`
	CustomerCount         int                       `json:"customer_count" doc:"Every customer, inactive ones included."`
	AutoClearedLast30d    int                       `json:"auto_cleared_last_30d" doc:"Every CLEARED payment created in the last 30 days, manual ones included."`
	OpenARByCurrency      []invoices.CurrencyAmount `json:"open_ar_by_currency" nullable:"false"`
	OpenQuotationCount    int                       `json:"open_quotation_count"`
	OpenQuotationPipeline []invoices.CurrencyAmount `json:"open_quotation_pipeline" nullable:"false" doc:"Largest total first."`
	MRRByCurrency         []invoices.CurrencyAmount `json:"mrr_by_currency" nullable:"false"`
	BaseCurrency          string                    `json:"base_currency"`
	OpenARBaseByCurrency  []CurrencyBaseAmount      `json:"open_ar_base_by_currency" nullable:"false" doc:"Largest first."`
	OpenARUnconverted     []string                  `json:"open_ar_unconverted" nullable:"false" doc:"Currencies with no FX rate, left out of open_ar_base_by_currency."`
}

// Dashboard is the statistics of Python's compute_dashboard_stats. today is
// the UTC calendar date (MRR and the FX rates are read as of it) and now the
// instant the 30-day window counts back from; base is the base currency.
// db is a pool or a transaction.
func Dashboard(ctx context.Context, db statsdb.DBTX, base string, today, now time.Time) (DashboardCore, error) {
	q := statsdb.New(db)
	var (
		out DashboardCore
		err error
	)
	counts := []struct {
		dst *int
		run func() (int64, error)
	}{
		{&out.AwaitingReviewCount, func() (int64, error) { return q.CountAwaitingReview(ctx) }},
		{&out.DraftCount, func() (int64, error) { return q.CountDrafts(ctx) }},
		{&out.SentCount, func() (int64, error) { return q.CountSent(ctx) }},
		{&out.CustomerCount, func() (int64, error) { return q.CountCustomers(ctx) }},
		{&out.AutoClearedLast30d, func() (int64, error) { return q.CountAutoCleared(ctx, now.UTC().Add(-30*24*time.Hour)) }},
		{&out.OpenQuotationCount, func() (int64, error) { return q.CountOpenQuotations(ctx) }},
	}
	for _, c := range counts {
		n, cerr := c.run()
		if cerr != nil {
			return DashboardCore{}, cerr
		}
		*c.dst = int(n)
	}

	ar, err := q.OpenARByCurrency(ctx)
	if err != nil {
		return DashboardCore{}, err
	}
	out.OpenARByCurrency = make([]invoices.CurrencyAmount, 0, len(ar))
	for _, r := range ar {
		out.OpenARByCurrency = append(out.OpenARByCurrency, invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
	}

	pipeline, err := q.OpenQuotationPipeline(ctx)
	if err != nil {
		return DashboardCore{}, err
	}
	out.OpenQuotationPipeline = make([]invoices.CurrencyAmount, 0, len(pipeline))
	for _, r := range pipeline {
		out.OpenQuotationPipeline = append(out.OpenQuotationPipeline, invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
	}
	sortLargestFirst(out.OpenQuotationPipeline)

	if out.MRRByCurrency, err = MRR(ctx, db, today); err != nil {
		return DashboardCore{}, err
	}
	if out.OpenARBaseByCurrency, out.OpenARUnconverted, err = OpenARBase(ctx, db, base, today); err != nil {
		return DashboardCore{}, err
	}
	out.BaseCurrency = base
	return out, nil
}

// OpenARBase is the open receivables per currency converted into base as of
// today, largest first (equal totals in currency order), and the currencies
// with no rate, sorted, which are left out. The conversion is of each
// currency's total, rounded half-even to four places, not per invoice. Open
// is the one rule: OPEN, SENT and PARTIAL, never a template, a positive
// balance. Neither result is nil.
func OpenARBase(ctx context.Context, db statsdb.DBTX, base string, today time.Time) ([]CurrencyBaseAmount, []string, error) {
	rows, err := statsdb.New(db).OpenARByCurrency(ctx)
	if err != nil {
		return nil, nil, err
	}
	fq := fxdb.New(db)
	converted := make([]CurrencyBaseAmount, 0, len(rows))
	unconverted := []string{}
	for _, r := range rows {
		c, err := fx.Convert(ctx, fq, r.Amount, r.Currency, today, base)
		if errors.Is(err, fx.ErrRateMissing) {
			unconverted = append(unconverted, r.Currency)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		converted = append(converted, CurrencyBaseAmount{Currency: r.Currency, BaseAmount: platform.NewDec4(c.BaseAmount)})
	}
	slices.SortStableFunc(converted, func(a, b CurrencyBaseAmount) int {
		return b.BaseAmount.Decimal().Cmp(a.BaseAmount.Decimal())
	})
	slices.Sort(unconverted)
	return converted, unconverted, nil
}
