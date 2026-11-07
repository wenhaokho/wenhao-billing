package platform

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/aging"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedledger"
)

type agingBucketsOutput struct {
	Current string `json:"current"`
	D1To30  string `json:"d_1_30"`
	D31To60 string `json:"d_31_60"`
	D61To90 string `json:"d_61_90"`
	D90Plus string `json:"d_90_plus"`
	Total   string `json:"total"`
}

type agingRowOutput struct {
	PartyID   string             `json:"party_id"`
	PartyName string             `json:"party_name"`
	Currency  string             `json:"currency"`
	Buckets   agingBucketsOutput `json:"buckets"`
}

type agingOutput struct {
	AsOf             string                        `json:"as_of"`
	Kind             string                        `json:"kind"`
	Rows             []agingRowOutput              `json:"rows"`
	TotalsByCurrency map[string]agingBucketsOutput `json:"totals_by_currency"`
}

func agingOutputBuckets(b aging.Buckets) agingBucketsOutput {
	amount := func(d decimal.Decimal) string {
		if d.IsZero() {
			return "0"
		}
		return d.StringFixed(4)
	}
	return agingBucketsOutput{
		Current: amount(b.Current), D1To30: amount(b.D1To30), D31To60: amount(b.D31To60),
		D61To90: amount(b.D61To90), D90Plus: amount(b.D90Plus), Total: amount(b.Total),
	}
}

func (h scopedProfileRouter) currentAging(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.admin(w, r); !ok {
			return
		}
		today := time.Now()
		if values, ok := r.URL.Query()["as_of"]; ok {
			asOf, problem := fxDate(values[len(values)-1])
			if problem != nil {
				problem.Loc = []any{"query", "as_of"}
				validationResponse(w, []validationError{*problem})
				return
			}
			if asOf.Format(time.DateOnly) != today.Format(time.DateOnly) {
				authError(w, http.StatusUnprocessableEntity, "aging reports only support the current server date")
				return
			}
		}
		ctx := r.Context()
		tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			authError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		defer tx.Rollback(ctx)
		report, err := scopedledger.CurrentAging(ctx, tx, h.scope, today, kind)
		if err != nil {
			authError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			authError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		out := agingOutput{
			AsOf: report.AsOf.Format(time.DateOnly), Kind: report.Kind,
			Rows:             make([]agingRowOutput, 0, len(report.Rows)),
			TotalsByCurrency: make(map[string]agingBucketsOutput, len(report.TotalsByCurrency)),
		}
		for _, row := range report.Rows {
			out.Rows = append(out.Rows, agingRowOutput{
				PartyID: row.PartyID, PartyName: row.PartyName, Currency: row.Currency,
				Buckets: agingOutputBuckets(row.Buckets),
			})
		}
		for currency, total := range report.TotalsByCurrency {
			out.TotalsByCurrency[currency] = agingOutputBuckets(total)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}
}
