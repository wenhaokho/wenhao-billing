package platform

import (
	"encoding/json"
	"math/big"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

type invoiceSummaryCurrencyAmount struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type invoiceSummaryOutput struct {
	Overdue      []invoiceSummaryCurrencyAmount `json:"overdue"`
	Due30Days    []invoiceSummaryCurrencyAmount `json:"due_30d"`
	AvgDaysToPay *float64                       `json:"avg_days_to_pay"`
}

func (h scopedProfileRouter) invoiceSummary(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	ctx := r.Context()
	today := pgtype.Date{Time: time.Now(), Valid: true}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbqueryv8.New(tx)
	buckets, err := q.GetInvoiceSummaryBuckets(ctx, dbqueryv8.GetInvoiceSummaryBucketsParams{BusinessID: h.scope.UUID(), Today: today})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	payments, err := q.GetInvoiceSummaryPaymentDays(ctx, dbqueryv8.GetInvoiceSummaryPaymentDaysParams{BusinessID: h.scope.UUID(), Today: today})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := invoiceSummaryOutput{
		Overdue:   []invoiceSummaryCurrencyAmount{},
		Due30Days: []invoiceSummaryCurrencyAmount{},
	}
	for _, bucket := range buckets {
		amount, _, err := projectNumeric(bucket.Amount)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		row := invoiceSummaryCurrencyAmount{Currency: bucket.Currency, Amount: amount.StringFixed(4)}
		if bucket.Overdue {
			out.Overdue = append(out.Overdue, row)
		} else {
			out.Due30Days = append(out.Due30Days, row)
		}
	}
	if payments.PaymentCount > 0 {
		total, _, err := projectNumeric(payments.TotalDays)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		// Match Python's integer division by rounding the exact ratio only once.
		average, _ := new(big.Rat).SetFrac(total.BigInt(), big.NewInt(payments.PaymentCount)).Float64()
		out.AvgDaysToPay = &average
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
