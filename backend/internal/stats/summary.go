package stats

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// InvoicesSummaryResponse is the tiles of the invoices list (Python's
// InvoicesSummaryResponse).
type InvoicesSummaryResponse struct {
	Overdue []invoices.CurrencyAmount `json:"overdue" nullable:"false" doc:"Open balance per currency of invoices due before today, by currency."`
	Due30d  []invoices.CurrencyAmount `json:"due_30d" nullable:"false" doc:"Open balance per currency of invoices due from today to 30 days ahead, both inclusive, by currency."`
	//nolint:forbidigo // a mean of whole days is a JSON number, not money
	AvgDaysToPay *float64 `json:"avg_days_to_pay" doc:"Mean days from issue to payment over every CLEARED payment of a PAID invoice in the last 90 days; null when there is none."`
}

func pgDay(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

func currencyAmounts[R any](rows []R, get func(R) (string, platform.Dec4)) []invoices.CurrencyAmount {
	out := make([]invoices.CurrencyAmount, 0, len(rows))
	for _, r := range rows {
		c, a := get(r)
		out = append(out, invoices.CurrencyAmount{Currency: c, Amount: a})
	}
	return out
}

// InvoicesSummary is Python's compute_invoices_summary. Overdue and due-in-30
// are the open balance (OPEN, SENT and PARTIAL, never a template, a positive
// balance, a due date) per currency: overdue is due before today, due_30d is
// due from today through today+30. avg_days_to_pay averages issue-to-payment
// days per payment, not per invoice.
func InvoicesSummary(ctx context.Context, db statsdb.DBTX, today time.Time) (InvoicesSummaryResponse, error) {
	q := statsdb.New(db)
	overdue, err := q.OverdueByCurrency(ctx, pgDay(today))
	if err != nil {
		return InvoicesSummaryResponse{}, err
	}
	due, err := q.DueWithinByCurrency(ctx, statsdb.DueWithinByCurrencyParams{Today: pgDay(today), Until: pgDay(today.AddDate(0, 0, 30))})
	if err != nil {
		return InvoicesSummaryResponse{}, err
	}
	days, err := q.DaysToPay(ctx, pgDay(today.AddDate(0, 0, -90)))
	if err != nil {
		return InvoicesSummaryResponse{}, err
	}
	out := InvoicesSummaryResponse{
		Overdue: currencyAmounts(overdue, func(r statsdb.OverdueByCurrencyRow) (string, platform.Dec4) {
			return r.Currency, platform.NewDec4(r.Amount)
		}),
		Due30d: currencyAmounts(due, func(r statsdb.DueWithinByCurrencyRow) (string, platform.Dec4) {
			return r.Currency, platform.NewDec4(r.Amount)
		}),
	}
	if days.Payments > 0 {
		avg := float64(days.TotalDays) / float64(days.Payments) //nolint:forbidigo // see AvgDaysToPay
		out.AvgDaysToPay = &avg
	}
	return out, nil
}
