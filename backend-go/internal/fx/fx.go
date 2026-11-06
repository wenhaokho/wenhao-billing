// Package fx looks up dated exchange rates and converts amounts to a base currency.
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
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

// MissingRateError means no direct rate existed on or before the requested date.
type MissingRateError struct {
	FromCurrency, ToCurrency string
	Date                     time.Time
}

func (e *MissingRateError) Error() string {
	return fmt.Sprintf("no fx rate for %s->%s on or before %s", e.FromCurrency, e.ToCurrency, e.Date.Format("2006-01-02"))
}

// InvalidRateError indicates that a stored rate is not a finite numeric value.
type InvalidRateError struct {
	FromCurrency, ToCurrency string
	Date                     time.Time
}

func (e *InvalidRateError) Error() string {
	return fmt.Sprintf("invalid fx rate for %s->%s on or before %s", e.FromCurrency, e.ToCurrency, e.Date.Format("2006-01-02"))
}

// GetRate selects a direct pair only. The fx_rates table is global and has no tenant key.
func GetRate(ctx context.Context, db dbquery.DBTX, from, base string, on time.Time) (decimal.Decimal, error) {
	from, base = strings.ToUpper(from), strings.ToUpper(base)
	if from == base {
		return decimal.NewFromInt(1), nil
	}
	n, err := dbquery.New(db).GetFXRate(ctx, dbquery.GetFXRateParams{FromCurrency: from, ToCurrency: base, AsOfDate: pgtype.Date{Time: on, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, &MissingRateError{from, base, on}
	}
	if err != nil {
		return decimal.Zero, err
	}
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return decimal.Zero, &InvalidRateError{from, base, on}
	}
	return decimal.NewFromBigInt(n.Int, n.Exp), nil
}

type Conversion struct{ BaseAmount, Rate decimal.Decimal }

// ErrPrecision indicates Python Decimal's 28-digit quantize would raise InvalidOperation.
var ErrPrecision = errors.New("fx conversion exceeds decimal context precision")

// Convert applies Python Decimal's default precision and half-even rounding.
func Convert(ctx context.Context, db dbquery.DBTX, amount decimal.Decimal, from, base string, on time.Time) (Conversion, error) {
	rate, err := GetRate(ctx, db, from, base, on)
	if err != nil {
		return Conversion{}, err
	}
	product := amount.Mul(rate)
	if !product.IsZero() {
		places := int32(28-product.NumDigits()) - product.Exponent()
		product = product.RoundBank(places)
	}
	value := product.RoundBank(4)
	value = decimal.NewFromBigInt(value.Shift(4).BigInt(), -4)
	if value.NumDigits() > 28 {
		return Conversion{}, ErrPrecision
	}
	return Conversion{BaseAmount: value, Rate: rate}, nil
}
