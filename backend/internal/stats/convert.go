package stats

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// toBase sums native per-currency rows into base at today's rate, each
// currency's total rounded half-even to four places. A currency with no rate
// is left out and returned in the sorted skipped list, never counted as zero.
func toBase(ctx context.Context, db statsdb.DBTX, base string, today time.Time, rows []invoices.CurrencyAmount) (decimal.Decimal, []string, error) {
	fq := fxdb.New(db)
	total := decimal.Zero
	skipped := []string{}
	for _, r := range rows {
		c, err := fx.Convert(ctx, fq, r.Amount.Decimal(), r.Currency, today, base)
		if errors.Is(err, fx.ErrRateMissing) {
			skipped = append(skipped, r.Currency)
			continue
		}
		if err != nil {
			return decimal.Zero, nil, err
		}
		total = total.Add(c.BaseAmount)
	}
	slices.Sort(skipped)
	return total, skipped, nil
}
