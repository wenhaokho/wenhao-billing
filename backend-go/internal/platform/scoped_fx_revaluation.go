package platform

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedledger"
)

type fxRevaluationLineOutput struct {
	AccountID          int64   `json:"account_id"`
	Code               string  `json:"code"`
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	Currency           string  `json:"currency"`
	FaceBalance        string  `json:"face_balance"`
	OriginalBase       string  `json:"original_base"`
	RevaluedBase       *string `json:"revalued_base"`
	Rate               *string `json:"rate"`
	RateAsOf           *string `json:"rate_as_of"`
	UnrealisedGainLoss *string `json:"unrealised_gain_loss"`
	RateMissing        bool    `json:"rate_missing"`
}

func (h scopedProfileRouter) fxRevaluation(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	asOf, base, ok := h.accountingReportParams(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	report, err := scopedledger.FXRevaluation(ctx, tx, h.scope, asOf, base)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := struct {
		AsOf                string                    `json:"as_of"`
		BaseCurrency        string                    `json:"base_currency"`
		Lines               []fxRevaluationLineOutput `json:"lines"`
		TotalUnrealisedGain string                    `json:"total_unrealised_gain"`
		TotalUnrealisedLoss string                    `json:"total_unrealised_loss"`
		MissingRates        []string                  `json:"missing_rates"`
	}{
		AsOf: report.AsOf.Format(time.DateOnly), BaseCurrency: report.BaseCurrency,
		Lines:               make([]fxRevaluationLineOutput, 0, len(report.Lines)),
		TotalUnrealisedGain: fxReportTotal(report.TotalUnrealisedGain),
		TotalUnrealisedLoss: fxReportTotal(report.TotalUnrealisedLoss),
		MissingRates:        report.MissingRates,
	}
	for _, line := range report.Lines {
		item := fxRevaluationLineOutput{
			AccountID: line.AccountID, Code: line.Code, Name: line.Name, Type: line.Type,
			Currency: line.Currency, FaceBalance: line.FaceBalance.StringFixed(4),
			OriginalBase: line.OriginalBase.StringFixed(4), RateMissing: line.RateMissing,
		}
		if !line.RateMissing {
			revalued := line.RevaluedBase.StringFixed(4)
			rate := line.Rate.StringFixed(8)
			// Python Decimal uses scientific notation below 0.000001.
			if digits, small := strings.CutPrefix(rate, "0.000000"); small {
				if digits[0] == '0' {
					rate = digits[1:] + "E-8"
				} else {
					rate = digits[:1] + "." + digits[1:] + "E-7"
				}
			}
			rateAsOf := line.RateAsOf.Format(time.DateOnly)
			gainLoss := line.UnrealisedGainLoss.StringFixed(4)
			// Decimal values in Go lose the sign of zero. Python retains it
			// when a negative product rounds to zero and when a liability's
			// zero difference is multiplied by -1.
			negativeZero := line.RevaluedBase.IsZero() && line.FaceBalance.IsNegative()
			if negativeZero {
				revalued = "-" + revalued
			}
			if line.UnrealisedGainLoss.IsZero() && (negativeZero != (line.Type == "LIABILITY")) {
				gainLoss = "-" + gainLoss
			}
			item.RevaluedBase, item.Rate, item.RateAsOf, item.UnrealisedGainLoss = &revalued, &rate, &rateAsOf, &gainLoss
		}
		out.Lines = append(out.Lines, item)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func fxReportTotal(amount decimal.Decimal) string {
	if amount.IsZero() {
		return "0"
	}
	return amount.StringFixed(4)
}
