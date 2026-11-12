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

// KPICollected is the collected flow and its share of the invoiced amount.
type KPICollected struct {
	KPIFlow
	//nolint:forbidigo // a percentage is a JSON number, not money
	ShareOfInvoiced *float64 `json:"share_of_invoiced" doc:"Percent; null when nothing was invoiced."`
}

// KPIOpenAR is the open receivables and what is behind them.
type KPIOpenAR struct {
	Amount        platform.Dec4 `json:"amount"`
	InvoiceCount  int           `json:"invoice_count"`
	CustomerCount int           `json:"customer_count"`
}

// KPIOverdue is the part of open AR past its due date.
type KPIOverdue struct {
	Amount platform.Dec4 `json:"amount"`
	//nolint:forbidigo // a percentage is a JSON number, not money
	ShareOfOpenAR *float64 `json:"share_of_open_ar" doc:"Percent; null when nothing is open."`
}

// KPIRecurring is the monthly recurring revenue.
type KPIRecurring struct {
	Amount platform.Dec4 `json:"amount"`
	//nolint:forbidigo // a percentage is a JSON number, not money
	ForeignShare       *float64 `json:"foreign_share" doc:"Percent not in the base currency; null when there is no MRR or when an MRR currency has no FX rate (it is then in unconverted)."`
	TopForeignCurrency *string  `json:"top_foreign_currency"`
	Unconverted        []string `json:"unconverted" nullable:"false" doc:"Sorted MRR currencies with no FX rate, left out of amount (a subset of the overview's unconverted)."`
}

// OverviewKPIs are the dashboard's headline figures in the base currency.
type OverviewKPIs struct {
	Invoiced         KPIFlow      `json:"invoiced" doc:"Issued in the window; DRAFT, VOID and templates excluded."`
	Collected        KPICollected `json:"collected" doc:"CLEARED payments dated in the window, withholding tax included."`
	OpenAR           KPIOpenAR    `json:"open_ar"`
	Overdue          KPIOverdue   `json:"overdue"`
	MonthlyRecurring KPIRecurring `json:"monthly_recurring"`
}

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
		return currencyAmounts(rows, func(r statsdb.InvoicedByCurrencyBetweenRow) (string, platform.Dec4) {
			return r.Currency, platform.NewDec4(r.Amount)
		}), nil
	}
	collected := func(ctx context.Context, from, to time.Time) ([]invoices.CurrencyAmount, error) {
		rows, err := q.CollectedByCurrencyBetween(ctx, statsdb.CollectedByCurrencyBetweenParams{FromDate: pgDay(from), ToDate: pgDay(to)})
		if err != nil {
			return nil, err
		}
		return currencyAmounts(rows, func(r statsdb.CollectedByCurrencyBetweenRow) (string, platform.Dec4) {
			return r.Currency, platform.NewDec4(r.Amount)
		}), nil
	}
	// The series is one small aggregate query per bucket, by design.
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
	if out.KPIs.Collected.KPIFlow, colAmount, err = flow(collected); err != nil {
		return OverviewResponse{}, err
	}
	out.KPIs.Collected.ShareOfInvoiced = percent(colAmount, invAmount)

	openRows, err := q.OpenARByCurrency(ctx)
	if err != nil {
		return OverviewResponse{}, err
	}
	open := currencyAmounts(openRows, func(r statsdb.OpenARByCurrencyRow) (string, platform.Dec4) {
		return r.Currency, platform.NewDec4(r.Amount)
	})
	openBase, err := conv(open)
	if err != nil {
		return OverviewResponse{}, err
	}
	overdueRows, err := q.OverdueByCurrency(ctx, pgDay(today))
	if err != nil {
		return OverviewResponse{}, err
	}
	overdue := currencyAmounts(overdueRows, func(r statsdb.OverdueByCurrencyRow) (string, platform.Dec4) {
		return r.Currency, platform.NewDec4(r.Amount)
	})
	overdueBase, err := conv(overdue)
	if err != nil {
		return OverviewResponse{}, err
	}
	counts, err := q.OpenARCounts(ctx)
	if err != nil {
		return OverviewResponse{}, err
	}
	out.KPIs.OpenAR.Amount, out.KPIs.Overdue.Amount = platform.NewDec4(openBase), platform.NewDec4(overdueBase)
	out.KPIs.OpenAR.InvoiceCount, out.KPIs.OpenAR.CustomerCount = int(counts.InvoiceCount), int(counts.CustomerCount)
	out.KPIs.Overdue.ShareOfOpenAR = percent(overdueBase, openBase)

	// MRR: each currency converted on its own so the foreign share and the top
	// foreign currency can be read off.
	mrr, err := MRR(ctx, db, today)
	if err != nil {
		return OverviewResponse{}, err
	}
	total, foreign, topAmount := decimal.Zero, decimal.Zero, decimal.Zero
	var top string
	mrrSkipped := map[string]bool{}
	for _, m := range mrr {
		v, cs, err := toBase(ctx, db, base, today, []invoices.CurrencyAmount{m})
		if err != nil {
			return OverviewResponse{}, err
		}
		for _, c := range cs {
			skipped[c], mrrSkipped[c] = true, true
		}
		total = total.Add(v)
		if m.Currency != base {
			foreign = foreign.Add(v)
			if v.GreaterThan(topAmount) {
				top, topAmount = m.Currency, v
			}
		}
	}
	out.KPIs.MonthlyRecurring.Amount = platform.NewDec4(total)
	if len(mrrSkipped) == 0 {
		// With a currency left out the share would be of a partial total.
		out.KPIs.MonthlyRecurring.ForeignShare = percent(foreign, total)
	}
	if top != "" {
		out.KPIs.MonthlyRecurring.TopForeignCurrency = &top
	}
	out.KPIs.MonthlyRecurring.Unconverted = make([]string, 0, len(mrrSkipped))
	for c := range mrrSkipped {
		out.KPIs.MonthlyRecurring.Unconverted = append(out.KPIs.MonthlyRecurring.Unconverted, c)
	}
	slices.Sort(out.KPIs.MonthlyRecurring.Unconverted)

	if out.DaysToPay, err = daysToPay(ctx, q, w); err != nil {
		return OverviewResponse{}, err
	}
	if out.Reconciliation, err = reconHealth(ctx, q, w); err != nil {
		return OverviewResponse{}, err
	}
	if out.TopCustomers, err = topCustomers(ctx, q, today, w, conv); err != nil {
		return OverviewResponse{}, err
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
