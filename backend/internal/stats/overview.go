package stats

import (
	"context"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// SeriesPoint is one chart bucket's amount in the base currency.
type SeriesPoint struct {
	Label  string        `json:"label"`
	Amount platform.Dec4 `json:"amount" doc:"Base-currency equivalent at today's rate; 0.0000 for a bucket with nothing."`
}

// KPIFlow is an amount over the window, the same over the previous window, and
// the per-bucket series, all in the base currency.
type KPIFlow struct {
	Amount   platform.Dec4 `json:"amount"`
	Previous platform.Dec4 `json:"previous"`
	Series   []SeriesPoint `json:"series" nullable:"false"`
}

// OverviewKPIs are the dashboard's headline figures in the base currency.
type OverviewKPIs struct {
	Invoiced  KPIFlow `json:"invoiced" doc:"Issued in the window; DRAFT, VOID and templates excluded."`
	Collected KPIFlow `json:"collected" doc:"CLEARED payments dated in the window, withholding tax included."`
	//nolint:forbidigo // a percentage is a JSON number, not money
	CollectedShareOfInvoiced *float64 `json:"collected_share_of_invoiced" doc:"Percent; null when nothing was invoiced."`

	OpenAR              platform.Dec4 `json:"open_ar"`
	OpenARInvoiceCount  int           `json:"open_ar_invoice_count"`
	OpenARCustomerCount int           `json:"open_ar_customer_count"`
	Overdue             platform.Dec4 `json:"overdue" doc:"The part of open_ar past its due date."`
	//nolint:forbidigo // a percentage is a JSON number, not money
	OverdueShareOfOpenAR *float64 `json:"overdue_share_of_open_ar" doc:"Percent; null when nothing is open."`

	MonthlyRecurring platform.Dec4 `json:"monthly_recurring"`
	//nolint:forbidigo // a percentage is a JSON number, not money
	RecurringForeignShare       *float64 `json:"recurring_foreign_share" doc:"Percent of monthly_recurring not in the base currency; null when there is none."`
	RecurringTopForeignCurrency *string  `json:"recurring_top_foreign_currency"`
}

// DaysToPay, ReconHealth and TopCustomers are filled in by the next change.
type (
	// DaysToPay is a placeholder.
	DaysToPay struct{}
	// ReconHealth is a placeholder.
	ReconHealth struct{}
	// TopCustomers is a placeholder.
	TopCustomers struct{}
)

// OverviewResponse is GET /stats/overview.
type OverviewResponse struct {
	Period         string        `json:"period" enum:"30d,90d,12m"`
	From           platform.Date `json:"from"`
	To             platform.Date `json:"to"`
	PreviousFrom   platform.Date `json:"previous_from"`
	PreviousTo     platform.Date `json:"previous_to"`
	BaseCurrency   string        `json:"base_currency"`
	Unconverted    []string      `json:"unconverted" nullable:"false" doc:"Sorted currencies with no FX rate, left out of every figure."`
	KPIs           OverviewKPIs  `json:"kpis"`
	DaysToPay      DaysToPay     `json:"days_to_pay"`
	Reconciliation ReconHealth   `json:"reconciliation"`
	TopCustomers   TopCustomers  `json:"top_customers"`
}

type betweenFn func(ctx context.Context, from, to time.Time) ([]invoices.CurrencyAmount, error)

// percent is part/whole as a percentage rounded to one place; nil when whole
// is not positive.
func percent(part, whole decimal.Decimal) *float64 { //nolint:forbidigo // a percentage is a JSON number, not money
	if !whole.IsPositive() {
		return nil
	}
	v, _ := part.Mul(decimal.NewFromInt(100)).DivRound(whole, 1).Float64()
	return &v
}

// Overview is the dashboard's KPI block for window w, as of today.
func Overview(ctx context.Context, db statsdb.DBTX, base string, today time.Time, w Window) (OverviewResponse, error) {
	q := statsdb.New(db)
	skipped := map[string]bool{}
	// conv converts native rows into base and remembers the skipped currencies.
	conv := func(rows []invoices.CurrencyAmount) (decimal.Decimal, error) {
		v, cs, err := toBase(ctx, db, base, today, rows)
		for _, c := range cs {
			skipped[c] = true
		}
		return v, err
	}
	invoiced := func(ctx context.Context, from, to time.Time) ([]invoices.CurrencyAmount, error) {
		rows, err := q.InvoicedByCurrencyBetween(ctx, statsdb.InvoicedByCurrencyBetweenParams{FromDate: pgDay(from), ToDate: pgDay(to)})
		if err != nil {
			return nil, err
		}
		out := make([]invoices.CurrencyAmount, 0, len(rows))
		for _, r := range rows {
			out = append(out, invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
		}
		return out, nil
	}
	collected := func(ctx context.Context, from, to time.Time) ([]invoices.CurrencyAmount, error) {
		rows, err := q.CollectedByCurrencyBetween(ctx, statsdb.CollectedByCurrencyBetweenParams{FromDate: pgDay(from), ToDate: pgDay(to)})
		if err != nil {
			return nil, err
		}
		out := make([]invoices.CurrencyAmount, 0, len(rows))
		for _, r := range rows {
			out = append(out, invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
		}
		return out, nil
	}
	flow := func(fn betweenFn) (KPIFlow, decimal.Decimal, error) {
		var f KPIFlow
		cur, err := fn(ctx, w.From, w.To)
		if err != nil {
			return f, decimal.Zero, err
		}
		amount, err := conv(cur)
		if err != nil {
			return f, decimal.Zero, err
		}
		prev, err := fn(ctx, w.PrevFrom, w.PrevTo)
		if err != nil {
			return f, decimal.Zero, err
		}
		previous, err := conv(prev)
		if err != nil {
			return f, decimal.Zero, err
		}
		f.Amount, f.Previous = platform.NewDec4(amount), platform.NewDec4(previous)
		f.Series = make([]SeriesPoint, 0, len(w.Buckets))
		for _, b := range w.Buckets {
			rows, err := fn(ctx, b.From, b.To)
			if err != nil {
				return f, decimal.Zero, err
			}
			v, err := conv(rows)
			if err != nil {
				return f, decimal.Zero, err
			}
			f.Series = append(f.Series, SeriesPoint{Label: b.Label, Amount: platform.NewDec4(v)})
		}
		return f, amount, nil
	}

	var out OverviewResponse
	var invAmount, colAmount decimal.Decimal
	var err error
	if out.KPIs.Invoiced, invAmount, err = flow(invoiced); err != nil {
		return OverviewResponse{}, err
	}
	if out.KPIs.Collected, colAmount, err = flow(collected); err != nil {
		return OverviewResponse{}, err
	}
	out.KPIs.CollectedShareOfInvoiced = percent(colAmount, invAmount)

	openRows, err := q.OpenARByCurrency(ctx)
	if err != nil {
		return OverviewResponse{}, err
	}
	open := make([]invoices.CurrencyAmount, 0, len(openRows))
	for _, r := range openRows {
		open = append(open, invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
	}
	openBase, err := conv(open)
	if err != nil {
		return OverviewResponse{}, err
	}
	overdueRows, err := q.OverdueARByCurrency(ctx, pgDay(today))
	if err != nil {
		return OverviewResponse{}, err
	}
	overdue := make([]invoices.CurrencyAmount, 0, len(overdueRows))
	for _, r := range overdueRows {
		overdue = append(overdue, invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
	}
	overdueBase, err := conv(overdue)
	if err != nil {
		return OverviewResponse{}, err
	}
	counts, err := q.OpenARCounts(ctx)
	if err != nil {
		return OverviewResponse{}, err
	}
	out.KPIs.OpenAR, out.KPIs.Overdue = platform.NewDec4(openBase), platform.NewDec4(overdueBase)
	out.KPIs.OpenARInvoiceCount, out.KPIs.OpenARCustomerCount = int(counts.InvoiceCount), int(counts.CustomerCount)
	out.KPIs.OverdueShareOfOpenAR = percent(overdueBase, openBase)

	// MRR: each currency converted on its own so the foreign share and the top
	// foreign currency can be read off.
	mrr, err := MRR(ctx, db, today)
	if err != nil {
		return OverviewResponse{}, err
	}
	total, foreign, topAmount := decimal.Zero, decimal.Zero, decimal.Zero
	var top string
	for _, m := range mrr {
		v, err := conv([]invoices.CurrencyAmount{m})
		if err != nil {
			return OverviewResponse{}, err
		}
		total = total.Add(v)
		if m.Currency != base {
			foreign = foreign.Add(v)
			if v.GreaterThan(topAmount) {
				top, topAmount = m.Currency, v
			}
		}
	}
	out.KPIs.MonthlyRecurring = platform.NewDec4(total)
	out.KPIs.RecurringForeignShare = percent(foreign, total)
	if top != "" {
		out.KPIs.RecurringTopForeignCurrency = &top
	}

	out.Period = string(w.Period)
	out.From, out.To = platform.Date{Time: w.From}, platform.Date{Time: w.To}
	out.PreviousFrom, out.PreviousTo = platform.Date{Time: w.PrevFrom}, platform.Date{Time: w.PrevTo}
	out.BaseCurrency = base
	out.Unconverted = make([]string, 0, len(skipped))
	for c := range skipped {
		out.Unconverted = append(out.Unconverted, c)
	}
	slices.Sort(out.Unconverted)
	return out, nil
}
