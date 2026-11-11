package stats

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// DaysToPay is the mean days from issue to payment over the window and over
// the previous window.
type DaysToPay struct {
	//nolint:forbidigo // a mean of whole days is a JSON number, not money
	Avg *float64 `json:"avg" doc:"Mean days from issue to payment over CLEARED payments of PAID invoices dated in the window; null when there is none."`
	//nolint:forbidigo // a mean of whole days is a JSON number, not money
	PreviousAvg *float64 `json:"previous_avg" doc:"The same over the previous window; null when there is none."`
}

// ReconHealth is how the window's payments were reconciled.
type ReconHealth struct {
	AutoCleared int `json:"auto_cleared" doc:"CLEARED payments created in the window whose CLEARED log row was written by the matching engine (no actor); manual entry and manual override write an actor."`
	Manual      int `json:"manual" doc:"CLEARED payments created in the window with no engine-written CLEARED log row, i.e. cleared by an admin."`
	Held        int `json:"held" doc:"Payments created in the window that are PENDING_MANUAL_REVIEW or FLAGGED."`
	//nolint:forbidigo // a confidence score is a JSON number, not money
	AvgConfidence        *float64 `json:"avg_confidence" doc:"Mean confidence score of the window's scored, non-manual payments; null when there is none."`
	AliasesInUse         int      `json:"aliases_in_use" doc:"Matching aliases across all customers (not windowed)."`
	CustomersWithAliases int      `json:"customers_with_aliases" doc:"Customers with at least one matching alias (not windowed)."`
}

// TopCustomer is one customer's invoiced amount in the window.
type TopCustomer struct {
	CustomerID uuid.UUID     `json:"customer_id"`
	Name       string        `json:"name"`
	Amount     platform.Dec4 `json:"amount" doc:"Invoiced in the window, base-currency equivalent."`
	//nolint:forbidigo // a percentage is a JSON number, not money
	Share   float64       `json:"share" doc:"Percent (0-100) of the window's invoiced total over all customers."`
	Overdue platform.Dec4 `json:"overdue" doc:"The customer's overdue open balance now, base-currency equivalent."`
}

// TopOthers is everyone outside the top ten.
type TopOthers struct {
	Count  int           `json:"count"`
	Amount platform.Dec4 `json:"amount"`
	//nolint:forbidigo // a percentage is a JSON number, not money
	Share float64 `json:"share" doc:"Percent (0-100)."`
}

// TopCustomers are the ten biggest customers by invoiced amount in the window.
type TopCustomers struct {
	Rows   []TopCustomer `json:"rows" nullable:"false" doc:"Up to ten, biggest first. A currency with no FX rate is left out of a customer's amount."`
	Others TopOthers     `json:"others"`
	//nolint:forbidigo // a percentage is a JSON number, not money
	Top3Share *float64 `json:"top3_share" doc:"Percent (0-100) of the invoiced total in the top three; null when nothing was invoiced."`
}

func meanDays(ctx context.Context, q *statsdb.Queries, from, to time.Time) (*float64, error) { //nolint:forbidigo // a mean of whole days is a JSON number, not money
	r, err := q.DaysToPayBetween(ctx, statsdb.DaysToPayBetweenParams{FromDate: pgDay(from), ToDate: pgDay(to)})
	if err != nil {
		return nil, err
	}
	if r.Payments == 0 {
		return nil, nil //nolint:nilnil // no payments is a null mean, not an error
	}
	avg := float64(r.TotalDays) / float64(r.Payments) //nolint:forbidigo // see DaysToPay.Avg
	return &avg, nil
}

func daysToPay(ctx context.Context, q *statsdb.Queries, w Window) (DaysToPay, error) {
	var out DaysToPay
	var err error
	if out.Avg, err = meanDays(ctx, q, w.From, w.To); err != nil {
		return out, err
	}
	out.PreviousAvg, err = meanDays(ctx, q, w.PrevFrom, w.PrevTo)
	return out, err
}

func reconHealth(ctx context.Context, q *statsdb.Queries, w Window) (ReconHealth, error) {
	counts, err := q.ReconCountsBetween(ctx, statsdb.ReconCountsBetweenParams{FromDate: pgDay(w.From), ToDate: pgDay(w.To)})
	if err != nil {
		return ReconHealth{}, err
	}
	conf, err := q.AvgConfidenceBetween(ctx, statsdb.AvgConfidenceBetweenParams{FromDate: pgDay(w.From), ToDate: pgDay(w.To)})
	if err != nil {
		return ReconHealth{}, err
	}
	aliases, err := q.AliasStats(ctx)
	if err != nil {
		return ReconHealth{}, err
	}
	out := ReconHealth{
		AutoCleared: int(counts.AutoCleared), Manual: int(counts.Manual), Held: int(counts.Held),
		AliasesInUse: int(aliases.Aliases), CustomersWithAliases: int(aliases.Customers),
	}
	if conf.Scored > 0 {
		out.AvgConfidence = &conf.AvgConfidence
	}
	return out, nil
}

const topN = 10

// share is part/whole as a percent to four places; 0 when whole is not positive.
func share(part, whole decimal.Decimal) float64 { //nolint:forbidigo // a percentage is a JSON number, not money
	if !whole.IsPositive() {
		return 0
	}
	v, _ := part.Mul(decimal.NewFromInt(100)).DivRound(whole, 4).Float64()
	return v
}

func topCustomers(ctx context.Context, q *statsdb.Queries, today time.Time, w Window, conv func([]invoices.CurrencyAmount) (decimal.Decimal, error)) (TopCustomers, error) {
	type cust struct {
		id            uuid.UUID
		name          string
		rows, overdue []invoices.CurrencyAmount
		amount, owed  decimal.Decimal
	}
	byID := map[uuid.UUID]*cust{}
	invRows, err := q.InvoicedByCustomerCurrencyBetween(ctx, statsdb.InvoicedByCustomerCurrencyBetweenParams{FromDate: pgDay(w.From), ToDate: pgDay(w.To)})
	if err != nil {
		return TopCustomers{}, err
	}
	for _, r := range invRows {
		id := uuid.UUID(r.CustomerID.Bytes)
		c := byID[id]
		if c == nil {
			c = &cust{id: id, name: r.Name}
			byID[id] = c
		}
		c.rows = append(c.rows, invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
	}
	odRows, err := q.OverdueByCustomerCurrency(ctx, pgDay(today))
	if err != nil {
		return TopCustomers{}, err
	}
	for _, r := range odRows {
		if c := byID[uuid.UUID(r.CustomerID.Bytes)]; c != nil {
			c.overdue = append(c.overdue, invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
		}
	}
	all := make([]*cust, 0, len(byID))
	total := decimal.Zero
	for _, c := range byID {
		if c.amount, err = conv(c.rows); err != nil {
			return TopCustomers{}, err
		}
		if !c.amount.IsPositive() {
			continue // nothing convertible
		}
		if c.owed, err = conv(c.overdue); err != nil {
			return TopCustomers{}, err
		}
		total = total.Add(c.amount)
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].amount.Equal(all[j].amount) {
			return all[i].amount.GreaterThan(all[j].amount)
		}
		return all[i].name < all[j].name
	})
	out := TopCustomers{Rows: make([]TopCustomer, 0, topN)}
	top3, others := decimal.Zero, decimal.Zero
	for i, c := range all {
		if i >= topN {
			out.Others.Count++
			others = others.Add(c.amount)
			continue
		}
		if i < 3 {
			top3 = top3.Add(c.amount)
		}
		out.Rows = append(out.Rows, TopCustomer{
			CustomerID: c.id, Name: c.name, Amount: platform.NewDec4(c.amount),
			Share: share(c.amount, total), Overdue: platform.NewDec4(c.owed),
		})
	}
	out.Others.Amount, out.Others.Share = platform.NewDec4(others), share(others, total)
	if total.IsPositive() {
		v := share(top3, total)
		out.Top3Share = &v
	}
	return out, nil
}
