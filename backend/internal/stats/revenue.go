package stats

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// MonthlyRevenueBucket is one calendar month's invoiced revenue (Python's
// MonthlyRevenueBucket). Each dict is a JSON object whose keys encoding/json
// writes sorted, which is currency order; none is null.
type MonthlyRevenueBucket struct {
	Month              string                   `json:"month" doc:"YYYY-MM."`
	Totals             map[string]platform.Dec4 `json:"totals" doc:"Invoiced amount in the base currency, per source currency, at today's rate. Currencies with no rate are left out."`
	NativeTotals       map[string]platform.Dec4 `json:"native_totals" doc:"Invoiced amount in each invoice currency."`
	UnpaidTotals       map[string]platform.Dec4 `json:"unpaid_totals" doc:"The part of totals still owed, in the base currency; a currency with nothing owed is left out."`
	NativeUnpaidTotals map[string]platform.Dec4 `json:"native_unpaid_totals" doc:"The part of native_totals still owed; a currency with nothing owed is left out."`
}

// MonthlyRevenueResponse is the last 12 calendar months of invoiced revenue.
type MonthlyRevenueResponse struct {
	BaseCurrency string                 `json:"base_currency"`
	Currencies   []string               `json:"currencies" nullable:"false" doc:"Sorted currencies that have a base total in some month."`
	Unconverted  []string               `json:"unconverted" nullable:"false" doc:"Sorted currencies with no FX rate to the base currency."`
	Months       []MonthlyRevenueBucket `json:"months" nullable:"false" doc:"Twelve months, oldest first, ending with the current month."`
	Footer       RevenueFooter          `json:"footer"`
}

// RevenueBestMonth is the month with the largest base total.
type RevenueBestMonth struct {
	Month  string        `json:"month" doc:"YYYY-MM."`
	Amount platform.Dec4 `json:"amount" doc:"The month's invoiced total in the base currency."`
}

// RevenueFooter summarises the twelve months for the revenue chart's footer.
type RevenueFooter struct {
	InvoicedBase platform.Dec4     `json:"invoiced_base" doc:"Sum of every month's base totals."`
	ForeignShare *float64          `json:"foreign_share" doc:"Percent of invoiced_base from non-base currencies; null when invoiced_base is zero."` //nolint:forbidigo // a percentage is a JSON number, not money
	BestMonth    *RevenueBestMonth `json:"best_month" doc:"The month with the largest base total (the most recent on a tie); null when every month is zero."`
	FXExposure   platform.Dec4     `json:"fx_exposure" doc:"Today's open receivables in non-base currencies, converted to the base currency at today's rate; currencies with no rate are left out."`
}

// monthLabels is the 12 YYYY-MM labels ending with today's month, oldest first.
func monthLabels(today time.Time) []string {
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := make([]string, 12)
	for i := range out {
		out[i] = first.AddDate(0, i-11, 0).Format("2006-01")
	}
	return out
}

// MonthlyRevenue is Python's compute_monthly_revenue_base behind the route:
// invoiced revenue (OPEN, SENT, PARTIAL and PAID, never a template) by issue
// month for the 12 months ending with today's, per source currency. Every
// month is converted at today's rate, each currency's sum rounded half-even to
// four places; a currency with no rate is kept in the native dicts only and
// listed in Unconverted. The unpaid dicts leave out a zero balance.
func MonthlyRevenue(ctx context.Context, db statsdb.DBTX, base string, today time.Time) (MonthlyRevenueResponse, error) {
	labels := monthLabels(today)
	// The window starts on the first day of the oldest label, so the SQL and
	// the labels cannot drift apart.
	windowStart, err := time.Parse("2006-01", labels[0])
	if err != nil {
		return MonthlyRevenueResponse{}, err
	}
	rows, err := statsdb.New(db).MonthlyRevenue(ctx, pgDay(windowStart))
	if err != nil {
		return MonthlyRevenueResponse{}, err
	}

	months := make(map[string]*MonthlyRevenueBucket, len(labels))
	type native struct{ amount, unpaid decimal.Decimal }
	nativeOf := make(map[string]map[string]native, len(labels))
	for _, l := range labels {
		months[l] = &MonthlyRevenueBucket{
			Month: l, Totals: map[string]platform.Dec4{}, NativeTotals: map[string]platform.Dec4{},
			UnpaidTotals: map[string]platform.Dec4{}, NativeUnpaidTotals: map[string]platform.Dec4{},
		}
		nativeOf[l] = map[string]native{}
	}
	for _, r := range rows {
		b, ok := months[r.Month]
		if !ok {
			continue // a future month
		}
		b.NativeTotals[r.Currency] = platform.NewDec4(r.Amount)
		if !r.Unpaid.IsZero() {
			b.NativeUnpaidTotals[r.Currency] = platform.NewDec4(r.Unpaid)
		}
		nativeOf[r.Month][r.Currency] = native{r.Amount, r.Unpaid}
	}

	fq := fxdb.New(db)
	rates := map[string]*decimal.Decimal{}
	unconverted := map[string]bool{}
	rate := func(cur string) (*decimal.Decimal, error) {
		if r, ok := rates[cur]; ok {
			return r, nil
		}
		v, err := fx.GetRate(ctx, fq, cur, today, base)
		if errors.Is(err, fx.ErrRateMissing) {
			rates[cur], unconverted[cur] = nil, true
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		rates[cur] = &v
		return &v, nil
	}

	haveBase := map[string]bool{}
	out := MonthlyRevenueResponse{BaseCurrency: base, Months: make([]MonthlyRevenueBucket, 0, len(labels))}
	for _, l := range labels {
		b := months[l]
		for cur, n := range nativeOf[l] {
			rt, err := rate(cur)
			if err != nil {
				return MonthlyRevenueResponse{}, err
			}
			if rt == nil {
				continue
			}
			haveBase[cur] = true
			b.Totals[cur] = platform.NewDec4(platform.RoundHalfEven4(n.amount.Mul(*rt)))
			if !n.unpaid.IsZero() {
				b.UnpaidTotals[cur] = platform.NewDec4(platform.RoundHalfEven4(n.unpaid.Mul(*rt)))
			}
		}
		out.Months = append(out.Months, *b)
	}
	out.Currencies = make([]string, 0, len(haveBase))
	for c := range haveBase {
		out.Currencies = append(out.Currencies, c)
	}
	slices.Sort(out.Currencies)
	out.Unconverted = make([]string, 0, len(unconverted))
	for c := range unconverted {
		out.Unconverted = append(out.Unconverted, c)
	}
	slices.Sort(out.Unconverted)

	foot, err := revenueFooter(ctx, db, base, today, out)
	if err != nil {
		return MonthlyRevenueResponse{}, err
	}
	out.Footer = foot.RevenueFooter
	for _, c := range foot.skipped {
		if !slices.Contains(out.Unconverted, c) {
			out.Unconverted = append(out.Unconverted, c)
		}
	}
	slices.Sort(out.Unconverted)
	return out, nil
}

type footerResult struct {
	RevenueFooter
	skipped []string
}

// revenueFooter derives the footer from the converted months plus today's open
// receivables in foreign currencies.
func revenueFooter(ctx context.Context, db statsdb.DBTX, base string, today time.Time, r MonthlyRevenueResponse) (footerResult, error) {
	total, foreign := decimal.Zero, decimal.Zero
	var best *RevenueBestMonth
	bestAmt := decimal.Zero
	for _, m := range r.Months {
		monthTotal := decimal.Zero
		for cur, v := range m.Totals {
			monthTotal = monthTotal.Add(v.Decimal())
			if cur != base {
				foreign = foreign.Add(v.Decimal())
			}
		}
		total = total.Add(monthTotal)
		if monthTotal.IsPositive() && monthTotal.GreaterThanOrEqual(bestAmt) { // >= : a tie goes to the later month
			bestAmt = monthTotal
			best = &RevenueBestMonth{Month: m.Month, Amount: platform.NewDec4(monthTotal)}
		}
	}

	openRows, err := statsdb.New(db).OpenARByCurrency(ctx)
	if err != nil {
		return footerResult{}, err
	}
	open := currencyAmounts(openRows, func(row statsdb.OpenARByCurrencyRow) (string, platform.Dec4) {
		return row.Currency, platform.NewDec4(row.Amount)
	})
	foreignOpen := open[:0]
	for _, o := range open {
		if o.Currency != base {
			foreignOpen = append(foreignOpen, o)
		}
	}
	exposure, skipped, err := toBase(ctx, db, base, today, foreignOpen)
	if err != nil {
		return footerResult{}, err
	}
	return footerResult{
		RevenueFooter: RevenueFooter{
			InvoicedBase: platform.NewDec4(total), ForeignShare: percent(foreign, total),
			BestMonth: best, FXExposure: platform.NewDec4(exposure),
		},
		skipped: skipped,
	}, nil
}
