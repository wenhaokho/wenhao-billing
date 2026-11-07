package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

type monthlyRevenueBucket struct {
	Month              string            `json:"month"`
	Totals             map[string]string `json:"totals"`
	NativeTotals       map[string]string `json:"native_totals"`
	UnpaidTotals       map[string]string `json:"unpaid_totals"`
	NativeUnpaidTotals map[string]string `json:"native_unpaid_totals"`
}

type monthlyRevenueOutput struct {
	BaseCurrency string                 `json:"base_currency"`
	Currencies   []string               `json:"currencies"`
	Unconverted  []string               `json:"unconverted"`
	Months       []monthlyRevenueBucket `json:"months"`
}

func (h scopedProfileRouter) monthlyRevenue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	out, err := h.readMonthlyRevenue(r.Context(), time.Now())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h scopedProfileRouter) readMonthlyRevenue(ctx context.Context, today time.Time) (monthlyRevenueOutput, error) {
	base := h.cfg.BaseCurrency
	if base == "" {
		base = "IDR"
	}
	out := monthlyRevenueOutput{
		BaseCurrency: base, Currencies: []string{}, Unconverted: []string{},
		Months: make([]monthlyRevenueBucket, 12),
	}
	y, m, d := today.Date()
	current := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	start := current.AddDate(0, -11, 0)
	date := pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
	buckets := make(map[string]*monthlyRevenueBucket, len(out.Months))
	for i := range out.Months {
		month := start.AddDate(0, i, 0).Format("2006-01")
		out.Months[i] = monthlyRevenueBucket{
			Month: month, Totals: map[string]string{}, NativeTotals: map[string]string{},
			UnpaidTotals: map[string]string{}, NativeUnpaidTotals: map[string]string{},
		}
		buckets[month] = &out.Months[i]
	}

	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	q := dbqueryv8.New(tx)
	rows, err := q.GetMonthlyRevenue(ctx, dbqueryv8.GetMonthlyRevenueParams{
		BusinessID: h.scope.UUID(), WindowStart: pgtype.Date{Time: start, Valid: true},
		WindowEnd: pgtype.Date{Time: current.AddDate(0, 1, 0), Valid: true},
	})
	if err != nil {
		return out, err
	}

	// A cached zero denotes a missing direct rate. A stored rate must be positive.
	rates := make(map[string]decimal.Decimal)
	target := strings.ToUpper(base)
	for _, row := range rows {
		bucket := buckets[row.Month]
		amount, _, err := projectNumeric(row.Amount)
		if err != nil {
			return out, err
		}
		unpaid, _, err := projectNumeric(row.Unpaid)
		if err != nil {
			return out, err
		}
		currency := row.Currency
		// Python substitutes Decimal("0") for a zero invoice sum.
		native := "0"
		if !amount.IsZero() {
			native = amount.StringFixed(4)
		}
		bucket.NativeTotals[currency] = native
		if !unpaid.IsZero() {
			bucket.NativeUnpaidTotals[currency] = unpaid.StringFixed(4)
		}

		rate, cached := rates[currency]
		if !cached {
			source := strings.ToUpper(currency)
			if source == target {
				rate = decimal.NewFromInt(1)
			} else {
				stored, err := q.GetFXRate(ctx, dbqueryv8.GetFXRateParams{
					BusinessID: h.scope.UUID(), FromCurrency: source, ToCurrency: target, AsOfDate: date,
				})
				switch {
				case errors.Is(err, pgx.ErrNoRows):
					rate = decimal.Zero
				case err != nil:
					return out, err
				default:
					rate, _, err = projectNumeric(stored)
					if err != nil || !rate.IsPositive() {
						return out, errors.New("invalid monthly revenue FX rate")
					}
				}
			}
			rates[currency] = rate
			if rate.IsZero() {
				out.Unconverted = append(out.Unconverted, currency)
			} else {
				out.Currencies = append(out.Currencies, currency)
			}
		}
		if !rate.IsZero() {
			bucket.Totals[currency] = monthlyRevenueConverted(amount, rate)
			if !unpaid.IsZero() {
				bucket.UnpaidTotals[currency] = monthlyRevenueConverted(unpaid, rate)
			}
		}
	}
	sort.Strings(out.Currencies)
	sort.Strings(out.Unconverted)
	return out, tx.Commit(ctx)
}

func monthlyRevenueConverted(amount, rate decimal.Decimal) string {
	rounded := amount.Mul(rate).RoundBank(4)
	// Preserve Python Decimal's negative zero when a negative amount rounds to zero.
	if rounded.IsZero() && amount.IsNegative() {
		return "-0.0000"
	}
	return rounded.StringFixed(4)
}
