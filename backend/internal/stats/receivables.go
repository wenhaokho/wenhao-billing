package stats

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// needsAttentionLimit is how many overdue invoices the dashboard lists.
const needsAttentionLimit = 6

// AgingBucket is the open balance that is this many days past due.
type AgingBucket struct {
	Key    string        `json:"key" enum:"not_due,d1_30,d31_60,d61_90,d90_plus" doc:"By today minus due date: not_due is due today or later or without a due date; d1_30 is 1-30 days late; d90_plus is over 90."`
	Amount platform.Dec4 `json:"amount" doc:"Base-currency equivalent."`
	Count  int           `json:"count" doc:"Invoices in the bucket, every currency, including those whose currency is in unconverted."`
}

// CashInBucket is the cash expected over a span of days ahead.
type CashInBucket struct {
	Key          string        `json:"key" enum:"d0_30,d31_60,d61_90" doc:"Days ahead of today: 0-30, 31-60, 61-90."`
	OpenInvoices platform.Dec4 `json:"open_invoices" doc:"Open balance not yet overdue and due in the span, base-currency equivalent."`
	Recurring    platform.Dec4 `json:"recurring" doc:"Every run of each recurring template generated in the span (on its generation date) plus each usage draft's next cut-off, base-currency equivalent."`
	Estimate     bool          `json:"estimate" doc:"True when a usage estimate is among the recurring runs."`
}

// AttentionRow is an overdue invoice the owner should chase.
type AttentionRow struct {
	InvoiceID      uuid.UUID      `json:"invoice_id"`
	InvoiceNumber  *string        `json:"invoice_number"`
	CustomerID     *uuid.UUID     `json:"customer_id" doc:"Null when the invoice has no customer."`
	CustomerName   *string        `json:"customer_name" doc:"Null when the invoice has no customer."`
	BalanceDue     platform.Dec4  `json:"balance_due" doc:"In the invoice's own currency."`
	ExpectedCash   platform.Dec4  `json:"expected_cash" doc:"The balance less withholding tax not yet booked, in the invoice's own currency."`
	Currency       string         `json:"currency"`
	DueDate        platform.Date  `json:"due_date"`
	DaysLate       int            `json:"days_late"`
	LastReminderAt *platform.Time `json:"last_reminder_at" doc:"The customer's latest reminder; null until reminders are logged."`
}

// ReceivablesResponse is GET /stats/receivables.
type ReceivablesResponse struct {
	BaseCurrency string        `json:"base_currency"`
	Unconverted  []string      `json:"unconverted" nullable:"false" doc:"Sorted currencies with no FX rate, left out of every amount."`
	OpenAR       platform.Dec4 `json:"open_ar" doc:"Base-currency equivalent."`
	//nolint:forbidigo // a percentage is a JSON number, not money
	PastDueShare    *float64       `json:"past_due_share" doc:"Percent of open AR that is past due; null when nothing is open."`
	InvoiceCount    int            `json:"invoice_count" doc:"Open invoices, including those whose currency is in unconverted."`
	CustomerCount   int            `json:"customer_count"`
	Aging           []AgingBucket  `json:"aging" nullable:"false" doc:"Always the five buckets, in order."`
	CashIn          []CashInBucket `json:"cash_in" nullable:"false" doc:"Always the three buckets, in order."`
	OverdueExcluded platform.Dec4  `json:"overdue_excluded" doc:"The overdue total, left out of the cash-in forecast until collected."`
	NeedsAttention  []AttentionRow `json:"needs_attention" nullable:"false" doc:"Overdue invoices, oldest due date first, at most six."`
}

var agingKeys = []string{"not_due", "d1_30", "d31_60", "d61_90", "d90_plus"}

// cashInSpans are the cash-in buckets' first and last day ahead of today.
var cashInSpans = []struct {
	key    string
	lo, hi int
}{{"d0_30", 0, 30}, {"d31_60", 31, 60}, {"d61_90", 61, 90}}

type convFn func([]invoices.CurrencyAmount) (decimal.Decimal, error)

// Receivables is the point-in-time receivables block as of today.
func Receivables(ctx context.Context, db statsdb.DBTX, base string, today time.Time) (ReceivablesResponse, error) {
	q := statsdb.New(db)
	skipped := map[string]bool{}
	conv := func(rows []invoices.CurrencyAmount) (decimal.Decimal, error) {
		v, cs, err := toBase(ctx, db, base, today, rows)
		for _, c := range cs {
			skipped[c] = true
		}
		return v, err
	}

	out := ReceivablesResponse{BaseCurrency: base}
	openRows, err := q.OpenARByCurrency(ctx)
	if err != nil {
		return out, err
	}
	openBase, err := conv(currencyAmounts(openRows, func(r statsdb.OpenARByCurrencyRow) (string, platform.Dec4) {
		return r.Currency, platform.NewDec4(r.Amount)
	}))
	if err != nil {
		return out, err
	}
	overdueRows, err := q.OverdueByCurrency(ctx, pgDay(today))
	if err != nil {
		return out, err
	}
	overdueBase, err := conv(currencyAmounts(overdueRows, func(r statsdb.OverdueByCurrencyRow) (string, platform.Dec4) {
		return r.Currency, platform.NewDec4(r.Amount)
	}))
	if err != nil {
		return out, err
	}
	counts, err := q.OpenARCounts(ctx)
	if err != nil {
		return out, err
	}
	out.OpenAR, out.OverdueExcluded = platform.NewDec4(openBase), platform.NewDec4(overdueBase)
	out.PastDueShare = percent(overdueBase, openBase)
	out.InvoiceCount, out.CustomerCount = int(counts.InvoiceCount), int(counts.CustomerCount)

	if out.Aging, err = aging(ctx, q, today, conv); err != nil {
		return out, err
	}
	if out.CashIn, err = cashIn(ctx, db, q, today, conv); err != nil {
		return out, err
	}
	if out.NeedsAttention, err = needsAttention(ctx, db, q, today); err != nil {
		return out, err
	}

	out.Unconverted = make([]string, 0, len(skipped))
	for c := range skipped {
		out.Unconverted = append(out.Unconverted, c)
	}
	slices.Sort(out.Unconverted)
	return out, nil
}

func aging(ctx context.Context, q *statsdb.Queries, today time.Time, conv convFn) ([]AgingBucket, error) {
	rows, err := q.AgingByCurrency(ctx, pgDay(today))
	if err != nil {
		return nil, err
	}
	byKey := map[string][]invoices.CurrencyAmount{}
	count := map[string]int{}
	for _, r := range rows {
		byKey[r.Bucket] = append(byKey[r.Bucket], invoices.CurrencyAmount{Currency: r.Currency, Amount: platform.NewDec4(r.Amount)})
		count[r.Bucket] += int(r.Invoices)
	}
	out := make([]AgingBucket, 0, len(agingKeys))
	for _, k := range agingKeys {
		v, err := conv(byKey[k])
		if err != nil {
			return nil, err
		}
		out = append(out, AgingBucket{Key: k, Amount: platform.NewDec4(v), Count: count[k]})
	}
	return out, nil
}

func cashIn(ctx context.Context, db statsdb.DBTX, q *statsdb.Queries, today time.Time, conv convFn) ([]CashInBucket, error) {
	runs, err := upcomingRuns(ctx, db, today)
	if err != nil {
		return nil, err
	}
	out := make([]CashInBucket, 0, len(cashInSpans))
	for _, s := range cashInSpans {
		from, to := today.AddDate(0, 0, s.lo), today.AddDate(0, 0, s.hi)
		rows, err := q.OpenNotDueByCurrencyBetween(ctx, statsdb.OpenNotDueByCurrencyBetweenParams{FromDate: pgDay(from), ToDate: pgDay(to)})
		if err != nil {
			return nil, err
		}
		open, err := conv(currencyAmounts(rows, func(r statsdb.OpenNotDueByCurrencyBetweenRow) (string, platform.Dec4) {
			return r.Currency, platform.NewDec4(r.Amount)
		}))
		if err != nil {
			return nil, err
		}
		perCur := map[string]decimal.Decimal{}
		estimate := false
		for _, run := range runs {
			if run.NextDate.Before(from) || run.NextDate.After(to) {
				continue
			}
			perCur[run.Currency] = perCur[run.Currency].Add(run.Amount.Decimal())
			estimate = estimate || run.AmountIsEstimate
		}
		cur := make([]invoices.CurrencyAmount, 0, len(perCur))
		for c, v := range perCur {
			cur = append(cur, invoices.CurrencyAmount{Currency: c, Amount: platform.NewDec4(v)})
		}
		slices.SortFunc(cur, func(a, b invoices.CurrencyAmount) int { return strings.Compare(a.Currency, b.Currency) })
		rec, err := conv(cur)
		if err != nil {
			return nil, err
		}
		out = append(out, CashInBucket{Key: s.key, OpenInvoices: platform.NewDec4(open), Recurring: platform.NewDec4(rec), Estimate: estimate})
	}
	return out, nil
}

func needsAttention(ctx context.Context, db statsdb.DBTX, q *statsdb.Queries, today time.Time) ([]AttentionRow, error) {
	rows, err := q.OverdueOldest(ctx, statsdb.OverdueOldestParams{Today: pgDay(today), Lim: needsAttentionLimit})
	if err != nil {
		return nil, err
	}
	var customers []uuid.UUID
	for _, r := range rows {
		if c := platform.UUIDPtr(r.CustomerID); c != nil {
			customers = append(customers, *c)
		}
	}
	// A row with no customer has no reminder history here: last_reminder_at stays null.
	last, err := invoices.LastReminderByCustomer(ctx, db, customers)
	if err != nil {
		return nil, err
	}
	out := make([]AttentionRow, 0, len(rows))
	for _, r := range rows {
		due := r.DueDate.Time
		var lastAt *platform.Time
		if c := platform.UUIDPtr(r.CustomerID); c != nil {
			if t, ok := last[*c]; ok {
				lastAt = &platform.Time{Time: t.UTC()}
			}
		}
		out = append(out, AttentionRow{
			InvoiceID: r.InvoiceID, InvoiceNumber: r.InvoiceNumber, CustomerID: platform.UUIDPtr(r.CustomerID), CustomerName: r.CustomerName,
			BalanceDue:   platform.NewDec4(r.BalanceDue),
			ExpectedCash: platform.NewDec4(withholding.ExpectedCash(r.BalanceDue, r.WhtAmount, r.WhtBooked)),
			Currency:     r.Currency, DueDate: platform.Date{Time: due}, DaysLate: int(today.Sub(due).Hours() / 24), LastReminderAt: lastAt,
		})
	}
	return out, nil
}
