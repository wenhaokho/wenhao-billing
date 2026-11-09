// Package fx looks up FX rates and converts amounts into a target (base)
// currency, as Python's services/fx.py does. Queries run on a pool or a
// transaction, so the ledger can convert inside its own.
package fx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ErrRateMissing is matched (errors.Is) by the error GetRate and Convert return
// when no rate applies, whose text is Python's sentence;
// callers decide whether that blocks posting or degrades to manual review.
var ErrRateMissing = errors.New("fx rate missing")

// missingError carries Python's sentence as its text and matches ErrRateMissing.
type missingError struct{ msg string }

func (e missingError) Error() string        { return e.msg }
func (e missingError) Is(target error) bool { return target == ErrRateMissing }

// Conversion is an amount in the target currency and the rate that made it.
type Conversion struct{ BaseAmount, Rate decimal.Decimal }

// GetRate is the newest from->to rate whose as_of_date is on or before the
// calendar date of on. on's calendar date in on's own location is the as-of
// date (it is not converted to UTC or any zone): pass a date column's value
// unchanged. The same currency is rate 1 without a query.
func GetRate(ctx context.Context, q *fxdb.Queries, from string, on time.Time, to string) (decimal.Decimal, error) {
	src, dst := strings.ToUpper(from), strings.ToUpper(to)
	if src == dst {
		return decimal.NewFromInt(1), nil
	}
	asOf := pgtype.Date{Time: time.Date(on.Year(), on.Month(), on.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
	rate, err := q.GetRate(ctx, fxdb.GetRateParams{FromCurrency: src, ToCurrency: dst, AsOfDate: asOf})
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Decimal{}, missingError{fmt.Sprintf("no fx rate for %s->%s on or before %s", src, dst, on.Format(time.DateOnly))}
	}
	if err != nil {
		return decimal.Decimal{}, err
	}
	return rate, nil
}

// Convert is amount × the rate on on, rounded half-even to four places.
func Convert(ctx context.Context, q *fxdb.Queries, amount decimal.Decimal, from string, on time.Time, to string) (Conversion, error) {
	rate, err := GetRate(ctx, q, from, on, to)
	if err != nil {
		return Conversion{}, err
	}
	return Conversion{BaseAmount: platform.RoundHalfEven4(amount.Mul(rate)), Rate: rate}, nil
}

// RateOn is GetRate that also says which day's rate it found, and reports a
// missing rate as ok=false rather than an error. It serves revaluation's own
// query: the currency codes are used as given (no upper-casing), and the newest
// as_of_date on or before on's calendar date wins. The same currency is 1, with
// asOf equal to on's date.
func RateOn(ctx context.Context, q *fxdb.Queries, from string, on time.Time, to string) (rate decimal.Decimal, asOf time.Time, ok bool, err error) {
	day := time.Date(on.Year(), on.Month(), on.Day(), 0, 0, 0, 0, time.UTC)
	if from == to {
		return decimal.NewFromInt(1), day, true, nil
	}
	row, err := q.LatestRate(ctx, fxdb.LatestRateParams{FromCurrency: from, ToCurrency: to, OnDate: pgtype.Date{Time: day, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Decimal{}, time.Time{}, false, nil
	}
	if err != nil {
		return decimal.Decimal{}, time.Time{}, false, err
	}
	return row.Rate, row.AsOfDate.Time, true, nil
}
