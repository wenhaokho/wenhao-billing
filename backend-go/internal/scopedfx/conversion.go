package scopedfx

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
)

// GetRate resolves the business's direct rate as of the posting date.
func GetRate(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, from, base string, on time.Time) (decimal.Decimal, error) {
	if err := scope.Validate(); err != nil {
		return decimal.Zero, err
	}
	from, base = strings.ToUpper(from), strings.ToUpper(base)
	if from == base {
		return decimal.NewFromInt(1), nil
	}
	n, err := Get(ctx, db, scope, from, base, pgtype.Date{Time: on, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, &fx.MissingRateError{FromCurrency: from, ToCurrency: base, Date: on}
	}
	if err != nil {
		return decimal.Zero, err
	}
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil || n.Int.Sign() <= 0 {
		return decimal.Zero, &fx.InvalidRateError{FromCurrency: from, ToCurrency: base, Date: on}
	}
	return decimal.NewFromBigInt(n.Int, n.Exp), nil
}
