package stats

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// upcomingHorizonDays is how far ahead the upcoming list looks, inclusive.
const upcomingHorizonDays = 90

// Upcoming is the next generation date of every active recurring template and
// every unlocked DRAFT usage invoice that falls within today + 90 days,
// inclusive (Python's compute_upcoming_invoices), by (date, invoice id).
// A recurring template's date is its first cycle strictly after today; a usage
// invoice's is the first cut-off day on or after today. Never nil.
func Upcoming(ctx context.Context, db statsdb.DBTX, today time.Time) ([]UpcomingInvoiceItem, error) {
	q := statsdb.New(db)
	horizon := today.AddDate(0, 0, upcomingHorizonDays)
	out := []UpcomingInvoiceItem{}

	templates, err := q.ListUpcomingRecurring(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range templates {
		s, ok := activeSchedule(t.BillingCycleRef, today)
		if !ok {
			continue
		}
		next, ok := s.NextAfter(today)
		if !ok || next.Start.After(horizon) {
			continue
		}
		out = append(out, upcomingItem(t.InvoiceID, t.CustomerID, t.CustomerName, "RECURRING", next.Start, t.Amount, t.Currency, false))
	}

	drafts, err := q.ListUpcomingUsage(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range drafts {
		cfg := storedConfig(u.BillingCycleRef)
		if truthy(cfg["locked"]) {
			continue
		}
		dom, ok := cutOffInt(cfg["cut_off_day"])
		if !ok || dom < 1 || dom > 31 {
			continue
		}
		next := nextDayOfMonth(today, int(dom))
		if next.After(horizon) {
			continue
		}
		out = append(out, upcomingItem(u.InvoiceID, u.CustomerID, u.CustomerName, "USAGE", next, u.Amount, u.Currency, true))
	}

	slices.SortFunc(out, func(a, b UpcomingInvoiceItem) int {
		if c := a.NextDate.Compare(b.NextDate.Time); c != 0 {
			return c
		}
		return strings.Compare(a.InvoiceID.String(), b.InvoiceID.String())
	})
	return out, nil
}

func upcomingItem(id uuid.UUID, customer pgtype.UUID, name *string, mode string, next time.Time, amount decimal.Decimal, currency string, estimate bool) UpcomingInvoiceItem {
	return UpcomingInvoiceItem{
		InvoiceID: id, CustomerID: platform.UUIDPtr(customer), CustomerName: name, Mode: mode,
		NextDate: platform.Date{Time: next}, Amount: platform.NewDec4(amount), Currency: currency, AmountIsEstimate: estimate,
	}
}

// cutOffInt is Python's isinstance(v, int) for what JSON holds: a whole-number
// literal (20, never 20.0 or 2e1) or a boolean, which is an int there (true is
// 1, false 0).
func cutOffInt(v any) (int64, bool) {
	switch v := v.(type) {
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case json.Number:
		n, err := strconv.ParseInt(v.String(), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// truthy is Python's bool(v) for a decoded JSON value.
func truthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		f, err := strconv.ParseFloat(v.String(), 64)
		return err != nil || f != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

// nextDayOfMonth is the smallest date on or after today whose day of month is
// dom, clamped to the month's length (Python's _next_day_of_month).
func nextDayOfMonth(today time.Time, dom int) time.Time {
	cand := clampDay(today.Year(), today.Month(), dom)
	if !cand.Before(today) {
		return cand
	}
	first := time.Date(today.Year(), today.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return clampDay(first.Year(), first.Month(), dom)
}

func clampDay(year int, month time.Month, day int) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, month, min(day, last), 0, 0, 0, 0, time.UTC)
}
