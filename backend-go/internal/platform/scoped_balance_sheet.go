package platform

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/balancesheet"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedledger"
)

type balanceSheetLineOutput struct {
	AccountID   int64  `json:"account_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	BaseBalance string `json:"base_balance"`
}

type balanceSheetGroupOutput struct {
	Type      string                   `json:"type"`
	TotalBase string                   `json:"total_base"`
	Lines     []balanceSheetLineOutput `json:"lines"`
}

type balanceSheetOutput struct {
	AsOf                 string                    `json:"as_of"`
	BaseCurrency         string                    `json:"base_currency"`
	Groups               []balanceSheetGroupOutput `json:"groups"`
	AssetsTotalBase      string                    `json:"assets_total_base"`
	LiabilitiesTotalBase string                    `json:"liabilities_total_base"`
	EquityTotalBase      string                    `json:"equity_total_base"`
	IncomeTotalBase      string                    `json:"income_total_base"`
	ExpenseTotalBase     string                    `json:"expense_total_base"`
}

func (h scopedProfileRouter) balanceSheet(w http.ResponseWriter, r *http.Request) {
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
	lines, err := scopedledger.BalanceSheetLines(ctx, tx, h.scope, asOf)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	// Noon keeps the first day of year 1 distinct from the zero-time sentinel.
	y, m, d := asOf.Date()
	report, err := balancesheet.Aggregate(time.Date(y, m, d, 12, 0, 0, 0, time.UTC), strings.ToUpper(base), lines)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := balanceSheetOutput{
		AsOf: report.AsOf.Format(time.DateOnly), BaseCurrency: base,
		Groups:          make([]balanceSheetGroupOutput, 0, len(report.Groups)),
		AssetsTotalBase: "0", LiabilitiesTotalBase: "0", EquityTotalBase: "0", IncomeTotalBase: "0", ExpenseTotalBase: "0",
	}
	for _, group := range report.Groups {
		item := balanceSheetGroupOutput{
			Type: group.Type, TotalBase: group.TotalBase.StringFixed(4),
			Lines: make([]balanceSheetLineOutput, 0, len(group.Lines)),
		}
		for _, line := range group.Lines {
			item.Lines = append(item.Lines, balanceSheetLineOutput{
				AccountID: line.AccountID, Code: line.Code, Name: line.Name, BaseBalance: line.BaseBalance.StringFixed(4),
			})
		}
		out.Groups = append(out.Groups, item)
		switch group.Type {
		case "ASSET":
			out.AssetsTotalBase = item.TotalBase
		case "LIABILITY":
			out.LiabilitiesTotalBase = item.TotalBase
		case "EQUITY":
			out.EquityTotalBase = item.TotalBase
		case "INCOME":
			out.IncomeTotalBase = item.TotalBase
		case "EXPENSE", "COGS":
			out.ExpenseTotalBase = report.ExpenseTotalBase.StringFixed(4)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
