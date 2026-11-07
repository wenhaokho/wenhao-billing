package platform

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/trialbalance"
)

type trialBalanceRowOutput struct {
	AccountID  int64  `json:"account_id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Currency   string `json:"currency"`
	Debit      string `json:"debit"`
	Credit     string `json:"credit"`
	Net        string `json:"net"`
	BaseDebit  string `json:"base_debit"`
	BaseCredit string `json:"base_credit"`
	BaseNet    string `json:"base_net"`
}

type trialBalanceOutput struct {
	AsOf            string                  `json:"as_of"`
	BaseCurrency    string                  `json:"base_currency"`
	Rows            []trialBalanceRowOutput `json:"rows"`
	TotalDebitBase  string                  `json:"total_debit_base"`
	TotalCreditBase string                  `json:"total_credit_base"`
}

func (h scopedProfileRouter) trialBalance(w http.ResponseWriter, r *http.Request) {
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
	lines, err := scopedledger.TrialBalanceLines(ctx, tx, h.scope, asOf)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	// Use noon because the core treats Go's zero time as an absent date.
	y, m, d := asOf.Date()
	report, err := trialbalance.Aggregate(time.Date(y, m, d, 12, 0, 0, 0, time.UTC), strings.ToUpper(base), lines)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := trialBalanceOutput{
		AsOf: report.AsOf.Format(time.DateOnly), BaseCurrency: base,
		Rows:           make([]trialBalanceRowOutput, 0, len(report.Rows)),
		TotalDebitBase: "0", TotalCreditBase: "0",
	}
	for _, row := range report.Rows {
		out.Rows = append(out.Rows, trialBalanceRowOutput{
			AccountID: row.AccountID, Code: row.Code, Name: row.Name, Type: row.Type, Currency: row.Currency,
			Debit: row.Debit.StringFixed(4), Credit: row.Credit.StringFixed(4), Net: row.Net.StringFixed(4),
			BaseDebit: row.BaseDebit.StringFixed(4), BaseCredit: row.BaseCredit.StringFixed(4), BaseNet: row.BaseNet.StringFixed(4),
		})
	}
	if len(out.Rows) != 0 {
		out.TotalDebitBase = report.TotalDebitBase.StringFixed(4)
		out.TotalCreditBase = report.TotalCreditBase.StringFixed(4)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h scopedProfileRouter) accountingReportParams(w http.ResponseWriter, r *http.Request) (time.Time, string, bool) {
	asOf := time.Now()
	if values, ok := r.URL.Query()["as_of"]; ok {
		date, problem := fxDate(values[len(values)-1])
		if problem != nil {
			problem.Loc = []any{"query", "as_of"}
			validationResponse(w, []validationError{*problem})
			return time.Time{}, "", false
		}
		asOf = date
	}
	base := h.cfg.BaseCurrency
	if base == "" {
		base = "IDR"
	}
	return asOf, base, true
}
