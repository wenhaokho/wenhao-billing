package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/schedule"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// divPlaces is the decimal places of a monthly-equivalent division, far past
// the four the per-currency sum is rounded to (Python divides in a 28-digit
// context).
const divPlaces = 28

// storedConfig is a stored billing_cycle_ref with its whole numbers kept as
// written; nil when it is NULL or not an object.
func storedConfig(raw json.RawMessage) map[string]any {
	var cfg map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&cfg); err != nil {
		return nil
	}
	return cfg
}

// monthlyEquivalent is a per-cycle amount as a monthly figure, not yet rounded
// (Python's _monthly_equiv): MONTHLY amount / interval, YEARLY amount /
// (12 interval), WEEKLY amount 52 / (12 interval), DAILY amount 365 /
// (12 interval).
func monthlyEquivalent(amount decimal.Decimal, frequency string, interval int) decimal.Decimal {
	n := int64(interval)
	switch frequency {
	case "YEARLY":
		return amount.DivRound(decimal.NewFromInt(12*n), divPlaces)
	case "WEEKLY":
		return amount.Mul(decimal.NewFromInt(52)).DivRound(decimal.NewFromInt(12*n), divPlaces)
	case "DAILY":
		return amount.Mul(decimal.NewFromInt(365)).DivRound(decimal.NewFromInt(12*n), divPlaces)
	default: // MONTHLY; schedule.Parse admits no other
		return amount.DivRound(decimal.NewFromInt(n), divPlaces)
	}
}

// MRR is the monthly recurring revenue per currency of the active RECURRING
// templates, never converted across currencies: a template is skipped when
// its schedule is paused, unparseable or ended (no current and no later cycle
// on today). A currency's monthly figures are summed, then rounded half-even
// to four places. Largest first, equal totals in currency order; never nil.
func MRR(ctx context.Context, db statsdb.DBTX, today time.Time) ([]invoices.CurrencyAmount, error) {
	rows, err := statsdb.New(db).ListRecurringTemplates(ctx)
	if err != nil {
		return nil, err
	}
	totals := map[string]decimal.Decimal{}
	for _, t := range rows {
		cfg := storedConfig(t.BillingCycleRef)
		if schedule.IsPaused(cfg) {
			continue
		}
		s, err := schedule.Parse(cfg)
		if err != nil {
			continue
		}
		if _, ok := s.Current(today); !ok {
			if _, ok := s.NextAfter(today); !ok {
				continue
			}
		}
		totals[t.Currency] = totals[t.Currency].Add(monthlyEquivalent(t.Amount, s.Frequency, s.Interval))
	}
	currencies := make([]string, 0, len(totals))
	for c := range totals {
		currencies = append(currencies, c)
	}
	slices.Sort(currencies)
	out := make([]invoices.CurrencyAmount, 0, len(currencies))
	for _, c := range currencies {
		out = append(out, invoices.CurrencyAmount{Currency: c, Amount: platform.NewDec4(platform.RoundHalfEven4(totals[c]))})
	}
	sortLargestFirst(out)
	return out, nil
}

// sortLargestFirst orders by amount, descending; the sort is stable, so equal
// amounts keep their (currency) order.
func sortLargestFirst(rows []invoices.CurrencyAmount) {
	slices.SortStableFunc(rows, func(a, b invoices.CurrencyAmount) int {
		return b.Amount.Decimal().Cmp(a.Amount.Decimal())
	})
}
