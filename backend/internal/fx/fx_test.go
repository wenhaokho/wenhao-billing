package fx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func day(m time.Month, d int) time.Time { return time.Date(2026, m, d, 15, 30, 0, 0, time.UTC) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func newQueries(t *testing.T) (*fxdb.Queries, *apitest.App) {
	t.Helper()
	a := apitest.New(t, func(huma.API, apitest.Deps) {})
	return fxdb.New(a.Pool), a
}

func insertRate(t *testing.T, a *apitest.App, from, rate string, m time.Month, d int) {
	t.Helper()
	if _, err := a.Pool.Exec(context.Background(),
		`INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ($1, 'IDR', $2, $3, 'TEST')`,
		from, rate, day(m, d)); err != nil {
		t.Fatal(err)
	}
}

func TestGetRateBaseIsOne(t *testing.T) {
	t.Parallel()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	pool.Close() // any query on it now fails
	got, err := fx.GetRate(context.Background(), fxdb.New(pool), "idr", day(1, 1), "IDR")
	if err != nil || !got.Equal(decimal.NewFromInt(1)) {
		t.Errorf("rate = %s, err = %v; want 1 and no query", got, err)
	}
}

func TestGetRatePicksNewestOnOrBefore(t *testing.T) {
	t.Parallel()
	q, a := newQueries(t)
	insertRate(t, a, "EUR", "17000", 1, 1)
	insertRate(t, a, "EUR", "18000", 1, 10)
	for _, c := range []struct {
		on   time.Time
		want string
	}{{day(1, 1), "17000"}, {day(1, 9), "17000"}, {day(1, 10), "18000"}, {day(2, 1), "18000"}} {
		got, err := fx.GetRate(context.Background(), q, "EUR", c.on, "IDR")
		if err != nil || !got.Equal(dec(c.want)) {
			t.Errorf("on %s: rate = %s, err = %v; want %s", c.on.Format(time.DateOnly), got, err, c.want)
		}
	}
}

func TestGetRateMissing(t *testing.T) {
	t.Parallel()
	q, _ := newQueries(t)
	_, err := fx.GetRate(context.Background(), q, "EUR", day(1, 1), "IDR")
	if !errors.Is(err, fx.ErrRateMissing) {
		t.Fatalf("err = %v, want ErrRateMissing", err)
	}
	if want := "no fx rate for EUR->IDR on or before 2026-01-01"; err.Error() != want {
		t.Errorf("text = %q, want %q", err.Error(), want)
	}
}

func TestGetRateUpperCases(t *testing.T) {
	t.Parallel()
	q, _ := newQueries(t)
	got, err := fx.GetRate(context.Background(), q, "usd", day(1, 1), "idr")
	if err != nil || !got.Equal(dec("16000")) {
		t.Errorf("rate = %s, err = %v; want 16000", got, err)
	}
}

func TestConvertRoundsHalfEven(t *testing.T) {
	t.Parallel()
	q, a := newQueries(t)
	insertRate(t, a, "EUR", "1.00005", 1, 1) // 1 × rate = 1.00005: even 4th digit, tie stays 1.0000
	insertRate(t, a, "GBP", "1.00015", 1, 1) // odd 4th digit, tie goes up to 1.0002
	for _, c := range []struct{ from, want string }{{"EUR", "1.0000"}, {"GBP", "1.0002"}} {
		got, err := fx.Convert(context.Background(), q, dec("1"), c.from, day(1, 1), "IDR")
		if err != nil {
			t.Fatal(err)
		}
		if got.BaseAmount.String() != dec(c.want).String() || got.BaseAmount.Exponent() != -4 {
			t.Errorf("%s: base = %s (exp %d), want %s", c.from, got.BaseAmount, got.BaseAmount.Exponent(), c.want)
		}
	}
	got, err := fx.Convert(context.Background(), q, dec("2.5"), "USD", day(1, 1), "IDR")
	if err != nil || !got.BaseAmount.Equal(dec("40000")) || !got.Rate.Equal(dec("16000")) {
		t.Errorf("USD: %+v, %v", got, err)
	}
}

func TestGetRateUsesOnsOwnCalendarDate(t *testing.T) {
	t.Parallel()
	q, a := newQueries(t)
	insertRate(t, a, "EUR", "17000", 1, 1)
	insertRate(t, a, "EUR", "18000", 1, 10)
	// 03:00 on 01-10 at +07:00 is 20:00 on 01-09 in UTC: the date in on's
	// own zone (01-10) rules, so the 01-10 rate applies; a UTC reading would
	// pick the 01-01 rate.
	on := time.Date(2026, 1, 10, 3, 0, 0, 0, time.FixedZone("WIB", 7*3600))
	got, err := fx.GetRate(context.Background(), q, "EUR", on, "IDR")
	if err != nil || !got.Equal(dec("18000")) {
		t.Errorf("rate = %s, err = %v; want the 01-10 rate", got, err)
	}
	_, err = fx.GetRate(context.Background(), q, "GBP", on, "IDR")
	if want := "no fx rate for GBP->IDR on or before 2026-01-10"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestGetRateAndConvertInsideATransaction(t *testing.T) {
	t.Parallel()
	_, a := newQueries(t)
	tx, err := a.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(),
		`INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('EUR', 'IDR', 17000, '2026-01-01', 'TEST')`); err != nil {
		t.Fatal(err)
	}
	q := fxdb.New(tx) // sees the uncommitted rate: the ledger's path
	got, err := fx.Convert(context.Background(), q, dec("2"), "EUR", day(1, 1), "IDR")
	if err != nil || !got.BaseAmount.Equal(dec("34000")) || !got.Rate.Equal(dec("17000")) {
		t.Errorf("Convert in tx: %+v, %v", got, err)
	}
}

func TestRateOnReturnsDate(t *testing.T) {
	t.Parallel()
	q, a := newQueries(t)
	insertRate(t, a, "EUR", "17000", 1, 1)
	insertRate(t, a, "EUR", "18000", 1, 10)
	insertRate(t, a, "EUR", "19000", 1, 20)
	rate, asOf, ok, err := fx.RateOn(context.Background(), q, "EUR", day(1, 15), "IDR")
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if !rate.Equal(dec("18000")) {
		t.Errorf("rate = %s, want 18000", rate)
	}
	if want := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC); !asOf.Equal(want) {
		t.Errorf("asOf = %v, want %v", asOf, want)
	}
}

func TestRateOnMissing(t *testing.T) {
	t.Parallel()
	q, a := newQueries(t)
	insertRate(t, a, "EUR", "17000", 2, 1)
	_, _, ok, err := fx.RateOn(context.Background(), q, "EUR", day(1, 15), "IDR")
	if err != nil || ok {
		t.Errorf("ok = %v, err = %v; want not ok, no error", ok, err)
	}
}

func TestRateOnCaseSensitive(t *testing.T) {
	t.Parallel()
	q, a := newQueries(t)
	insertRate(t, a, "EUR", "17000", 1, 1)
	_, _, ok, err := fx.RateOn(context.Background(), q, "eur", day(1, 15), "IDR")
	if err != nil || ok {
		t.Errorf("lower-case from: ok = %v, err = %v; want not ok", ok, err)
	}
}

func TestRateOnSameCurrency(t *testing.T) {
	t.Parallel()
	q, _ := newQueries(t)
	on := day(1, 15)
	rate, asOf, ok, err := fx.RateOn(context.Background(), q, "IDR", on, "IDR")
	if err != nil || !ok || !rate.Equal(decimal.NewFromInt(1)) || !asOf.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("rate = %s, asOf = %v, ok = %v, err = %v", rate, asOf, ok, err)
	}
}

func TestRateOnExactDateIsIncluded(t *testing.T) {
	t.Parallel()
	q, a := newQueries(t)
	insertRate(t, a, "EUR", "17000", 1, 1)
	insertRate(t, a, "EUR", "18000", 1, 10)
	rate, asOf, ok, err := fx.RateOn(context.Background(), q, "EUR", day(1, 10), "IDR")
	if err != nil || !ok || !rate.Equal(dec("18000")) || !asOf.Equal(time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("rate = %s, asOf = %v, ok = %v, err = %v; want the 10th's 18000", rate, asOf, ok, err)
	}
}
