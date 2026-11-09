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
// calendar date of on. The same currency is rate 1 without a query.
func GetRate(ctx context.Context, q *fxdb.Queries, from string, on time.Time, to string) (decimal.Decimal, error) {
	src, dst := strings.ToUpper(from), strings.ToUpper(to)
	if src == dst {
		return decimal.NewFromInt(1), nil
	}
	rate, err := q.GetRate(ctx, fxdb.GetRateParams{FromCurrency: src, ToCurrency: dst, AsOfDate: pgtype.Date{Time: time.Date(on.Year(), on.Month(), on.Day(), 0, 0, 0, 0, time.UTC), Valid: true}})
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
