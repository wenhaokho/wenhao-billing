package stats_test

import (
	"testing"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

// arDay is the date test_open_ar_base.py runs on; the seeded rates are
// as of 2026-01-01.
var arDay = date(2026, time.July, 24)

type arCase struct{ cur, balance, status string }

func baseOf(t *testing.T, cases ...arCase) ([]string, []string) {
	t.Helper()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	for _, k := range cases {
		invoice(t, a, c, k.cur, k.balance, k.status)
	}
	rows, unconverted, err := stats.OpenARBase(t.Context(), a.Pool, "IDR", arDay)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Currency+" "+r.BaseAmount.Decimal().StringFixed(4))
	}
	return out, unconverted
}

func TestOpenAREmptyDBReturnsEmpty(t *testing.T) {
	t.Parallel()
	rows, un := baseOf(t)
	eq(t, "rows", rows, []string{})
	eq(t, "unconverted", un, []string{})
}

func TestOpenARBaseCurrencyUsesRateOne(t *testing.T) {
	t.Parallel()
	rows, un := baseOf(t, arCase{"IDR", "5000000", "SENT"})
	eq(t, "rows", rows, []string{"IDR 5000000.0000"})
	eq(t, "unconverted", un, []string{})
}

func TestOpenARForeignCurrencyConvertedViaSeededRate(t *testing.T) {
	t.Parallel()
	rows, un := baseOf(t, arCase{"USD", "100", "SENT"}, arCase{"SGD", "100", "SENT"})
	eq(t, "rows", rows, []string{"USD 1600000.0000", "SGD 1200000.0000"})
	eq(t, "unconverted", un, []string{})
}

func TestOpenARMissingRateCurrencyLandsInUnconverted(t *testing.T) {
	t.Parallel()
	rows, un := baseOf(t, arCase{"GBP", "100", "SENT"})
	eq(t, "rows", rows, []string{})
	eq(t, "unconverted", un, []string{"GBP"})
}

func TestOpenARDraftExcluded(t *testing.T) {
	t.Parallel()
	rows, un := baseOf(t, arCase{"USD", "100", "DRAFT"})
	eq(t, "rows", rows, []string{})
	eq(t, "unconverted", un, []string{})
}

// Conversion is of the per-currency sum, not per invoice: at 0.33335 each
// 0.0001 balance converts to 0.0000, while their sum 0.0003 converts to 0.0001.
func TestOpenARConvertsThePerCurrencySum(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	exec(t, a, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('GBP', 'IDR', 0.33335, '2026-01-01', 'TEST')`)
	for range 3 {
		invoice(t, a, c, "GBP", "0.0001", "SENT")
	}
	rows, _, err := stats.OpenARBase(t.Context(), a.Pool, "IDR", arDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BaseAmount.Decimal().StringFixed(4) != "0.0001" {
		t.Fatalf("rows = %+v, want one GBP row of 0.0001", rows)
	}
}

// TestOpenARExcludesTemplatesAndZeroBalances is Go change 2: a template, a
// zero balance and a negative balance move neither the dashboard's open AR nor
// its base conversion.
func TestOpenARExcludesTemplatesAndZeroBalances(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	invoice(t, a, c, "USD", "100", "SENT")
	invoiceFull(t, a, c, "USD", "900", "900", "SENT", true)  // a template
	invoiceFull(t, a, c, "USD", "50", "0", "PARTIAL", false) // a zero balance
	invoiceFull(t, a, c, "USD", "50", "-7", "OPEN", false)   // a negative balance
	invoiceFull(t, a, c, "SGD", "50", "0", "SENT", false)    // only a zero balance: no SGD row
	rows, un, err := stats.OpenARBase(t.Context(), a.Pool, "IDR", arDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Currency != "USD" || rows[0].BaseAmount.Decimal().StringFixed(4) != "1600000.0000" || len(un) != 0 {
		t.Fatalf("rows = %+v unconverted = %v, want only USD 1600000.0000", rows, un)
	}
	dash, err := stats.Dashboard(t.Context(), a.Pool, "IDR", arDay, a.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "open_ar_by_currency", amounts(dash.OpenARByCurrency), []string{"USD 100.0000"})
	if len(dash.OpenARBaseByCurrency) != 1 || dash.OpenARBaseByCurrency[0].BaseAmount.Decimal().StringFixed(4) != "1600000.0000" {
		t.Fatalf("open_ar_base_by_currency = %+v", dash.OpenARBaseByCurrency)
	}
}

// Equal base totals are ordered by currency.
func TestOpenARBaseTieOrderedByCurrency(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := newCustomer(t, a, "Acme Corp")
	exec(t, a, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('AUD', 'IDR', 16000, '2026-01-01', 'TEST')`)
	invoice(t, a, c, "USD", "100", "SENT")
	invoice(t, a, c, "AUD", "100", "SENT")
	rows, _, err := stats.OpenARBase(t.Context(), a.Pool, "IDR", arDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Currency != "AUD" || rows[1].Currency != "USD" {
		t.Fatalf("rows = %+v, want AUD then USD", rows)
	}
}
