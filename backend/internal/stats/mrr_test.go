package stats_test

import (
	"testing"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

// mrrDay is the date test_mrr_stats.py runs on.
var mrrDay = date(2026, time.July, 24)

type mrrCase struct {
	cur, amount string
	cfg         map[string]any
}

func mrrOf(t *testing.T, cases ...mrrCase) []string {
	t.Helper()
	a := newApp(t)
	c := newCustomer(t, a, "Retainer Corp")
	for _, m := range cases {
		template(t, a, c, m.cur, m.amount, m.cfg)
	}
	rows, err := stats.MRR(t.Context(), a.Pool, mrrDay)
	if err != nil {
		t.Fatal(err)
	}
	return amounts(rows)
}

func TestMRREmptyDBReturnsNoRows(t *testing.T) {
	t.Parallel()
	eq(t, "mrr", mrrOf(t), []string{})
}

func TestMRRMonthlyInterval1IsFullAmount(t *testing.T) {
	t.Parallel()
	eq(t, "mrr", mrrOf(t, mrrCase{"USD", "1200", monthly()}), []string{"USD 1200.0000"})
}

func TestMRRYearlyDividedByTwelve(t *testing.T) {
	t.Parallel()
	eq(t, "mrr", mrrOf(t, mrrCase{"USD", "1200", with(monthly(), "frequency", "YEARLY")}), []string{"USD 100.0000"})
}

func TestMRRWeeklyTimes52Over12(t *testing.T) {
	t.Parallel()
	eq(t, "mrr", mrrOf(t, mrrCase{"USD", "100", with(monthly(), "frequency", "WEEKLY")}), []string{"USD 433.3333"})
}

func TestMRRMonthlyInterval3IsThird(t *testing.T) {
	t.Parallel()
	eq(t, "mrr", mrrOf(t, mrrCase{"USD", "900", with(monthly(), "interval", 3)}), []string{"USD 300.0000"})
}

func TestMRRPausedTemplateExcluded(t *testing.T) {
	t.Parallel()
	eq(t, "mrr", mrrOf(t, mrrCase{"USD", "1200", with(monthly(), "paused", true)}), []string{})
}

func TestMRREndedTemplateExcluded(t *testing.T) {
	t.Parallel()
	cfg := with(monthly(), "end_mode", "ON_DATE", "end_date", "2026-03-01")
	eq(t, "mrr", mrrOf(t, mrrCase{"USD", "1200", cfg}), []string{})
}

func TestMRRNonTemplateRecurringExcluded(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Retainer Corp")
	template(t, a, c, "USD", "1200", monthly())
	exec(t, a, `UPDATE invoices SET is_template = false`)
	rows, err := stats.MRR(t.Context(), a.Pool, mrrDay)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "mrr", amounts(rows), []string{})
}

func TestMRRGroupedAndSortedDesc(t *testing.T) {
	t.Parallel()
	got := mrrOf(t, mrrCase{"USD", "1000", monthly()}, mrrCase{"SGD", "6000", monthly()})
	eq(t, "mrr", got, []string{"SGD 6000.0000", "USD 1000.0000"})
}

// An unparseable or empty schedule is skipped, never an error.
func TestMRRUnparseableTemplateSkipped(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Retainer Corp")
	template(t, a, c, "USD", "700", map[string]any{"frequency": "HOURLY"})
	exec(t, a, `INSERT INTO invoices (invoice_id, customer_id, invoice_type, currency, amount, balance_due, status, is_template)
VALUES (gen_random_uuid(), $1, 'RECURRING', 'USD', 5, 0, 'DRAFT', true)`, c)
	template(t, a, c, "USD", "1200", monthly())
	rows, err := stats.MRR(t.Context(), a.Pool, mrrDay)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "mrr", amounts(rows), []string{"USD 1200.0000"})
}

// TestMRRWeeklyAndTie pins the weekly 52/12 arithmetic with an interval, the
// per-currency sum rounded half-even, and equal totals ordered by currency.
func TestMRRWeeklyAndTie(t *testing.T) {
	t.Parallel()
	t.Run("weekly every 2 weeks", func(t *testing.T) {
		t.Parallel()
		// 100 * 52 / (12 * 2) = 216.666...
		cfg := with(monthly(), "frequency", "WEEKLY", "interval", 2)
		eq(t, "mrr", mrrOf(t, mrrCase{"USD", "100", cfg}), []string{"USD 216.6667"})
	})
	t.Run("daily", func(t *testing.T) {
		t.Parallel()
		// 365 / 12 = 30.41666...
		cfg := with(monthly(), "frequency", "DAILY")
		eq(t, "mrr", mrrOf(t, mrrCase{"USD", "1", cfg}), []string{"USD 30.4167"})
	})
	t.Run("sum rounds half-even", func(t *testing.T) {
		t.Parallel()
		// 0.0005 / 2 = 0.00025 -> 0.0002 (half-up would give 0.0003) and
		// 0.0015 / 2 = 0.00075 -> 0.0008 (the even neighbour).
		cfg := with(monthly(), "interval", 2)
		got := mrrOf(t, mrrCase{"USD", "0.0005", cfg}, mrrCase{"SGD", "0.0015", cfg})
		eq(t, "mrr", got, []string{"SGD 0.0008", "USD 0.0002"})
	})
	t.Run("equal totals ordered by currency", func(t *testing.T) {
		t.Parallel()
		got := mrrOf(t, mrrCase{"USD", "1000", monthly()}, mrrCase{"SGD", "1000", monthly()}, mrrCase{"AUD", "1000", monthly()})
		eq(t, "mrr", got, []string{"AUD 1000.0000", "SGD 1000.0000", "USD 1000.0000"})
	})
}
