package scopedledger

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/balancesheet"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
)

func BalanceSheetLines(ctx context.Context, tx pgx.Tx, scope business.ID, asOf time.Time) ([]balancesheet.Line, error) {
	rows, err := reportJournalLines(ctx, tx, scope, asOf)
	if err != nil {
		return nil, err
	}
	lines := make([]balancesheet.Line, 0, len(rows))
	for _, row := range rows {
		debit, err := decimal.NewFromString(row.BaseDebit)
		if err != nil {
			return nil, err
		}
		credit, err := decimal.NewFromString(row.BaseCredit)
		if err != nil {
			return nil, err
		}
		lines = append(lines, balancesheet.Line{
			AccountID: int64(row.AccountID), Code: row.AccountCode, Name: row.AccountName,
			Type: row.AccountType, PostedAt: reportPostingDay(row.PostedAt.Time),
			BaseDebit: debit, BaseCredit: credit,
		})
	}
	return lines, nil
}
