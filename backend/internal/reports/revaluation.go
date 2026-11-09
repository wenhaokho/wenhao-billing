package reports

import (
	"context"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/reports/reportsdb"
)

type cachedRate struct {
	rate decimal.Decimal
	date time.Time
	ok   bool
}

// revaluation builds the report from the query's rows (ordered by code and
// currency). Each currency's rate is looked up once. A bucket whose face and
// original base are both zero is skipped; one with only a base residue is
// kept and revalued to zero.
func revaluation(ctx context.Context, fq *fxdb.Queries, asOf time.Time, base string, rows []reportsdb.RevaluationRowsRow) (FxRevaluationReport, error) {
	out := FxRevaluationReport{
		AsOf: platform.Date{Time: asOf}, BaseCurrency: base,
		Lines: []FxRevaluationLine{}, MissingRates: []string{},
	}
	rates := map[string]cachedRate{}
	missing := map[string]bool{}
	var gain, loss decimal.Decimal
	for _, r := range rows {
		var face, original, sign decimal.Decimal
		switch r.Type {
		case "ASSET":
			face, original, sign = r.Debit.Sub(r.Credit), r.BaseDebit.Sub(r.BaseCredit), decimal.NewFromInt(1)
		case "LIABILITY":
			face, original, sign = r.Credit.Sub(r.Debit), r.BaseCredit.Sub(r.BaseDebit), decimal.NewFromInt(-1)
		default: // the query returns only ASSET and LIABILITY
			continue
		}
		if face.IsZero() && original.IsZero() {
			continue
		}
		line := FxRevaluationLine{
			AccountID: r.AccountID, Code: r.Code, Name: r.Name, Type: r.Type, Currency: r.Currency,
			FaceBalance: platform.NewDec4(face), OriginalBase: platform.NewDec4(original),
		}
		cr, cached := rates[r.Currency]
		if !cached {
			rate, date, ok, err := fx.RateOn(ctx, fq, r.Currency, asOf, base)
			if err != nil {
				return FxRevaluationReport{}, err
			}
			cr = cachedRate{rate: rate, date: date, ok: ok}
			rates[r.Currency] = cr
		}
		if !cr.ok {
			missing[r.Currency] = true
			line.RateMissing = true
			out.Lines = append(out.Lines, line)
			continue
		}
		revalued := platform.RoundHalfEven4(face.Mul(cr.rate))
		unrealised := revalued.Sub(original).Mul(sign)
		if unrealised.IsPositive() {
			gain = gain.Add(unrealised)
		} else if unrealised.IsNegative() {
			loss = loss.Add(unrealised.Neg())
		}
		rv, rate, u, date := platform.NewDec4(revalued), platform.NewDec8(cr.rate), platform.NewDec4(unrealised), platform.Date{Time: cr.date}
		line.RevaluedBase, line.Rate, line.UnrealisedGainLoss, line.RateAsOf = &rv, &rate, &u, &date
		out.Lines = append(out.Lines, line)
	}
	for c := range missing {
		out.MissingRates = append(out.MissingRates, c)
	}
	sort.Strings(out.MissingRates)
	out.TotalUnrealisedGain, out.TotalUnrealisedLoss = platform.NewDec4(gain), platform.NewDec4(loss)
	return out, nil
}
