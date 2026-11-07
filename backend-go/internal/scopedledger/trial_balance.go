package scopedledger

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/trialbalance"
)

func TrialBalanceLines(ctx context.Context, tx pgx.Tx, scope business.ID, asOf time.Time) ([]trialbalance.Line, error) {
	rows, err := reportJournalLines(ctx, tx, scope, asOf)
	if err != nil {
		return nil, err
	}
	lines := make([]trialbalance.Line, 0, len(rows))
	for _, row := range rows {
		line := trialbalance.Line{
			AccountID: int64(row.AccountID), Code: row.AccountCode, Name: row.AccountName,
			Type: row.AccountType, Currency: row.Currency,
			// Only the calendar day is used. Noon keeps year 1's first day
			// distinct from the aggregation core's zero-time sentinel.
			PostedAt: reportPostingDay(row.PostedAt.Time),
		}
		for _, amount := range []struct {
			text string
			out  *decimal.Decimal
		}{
			{row.Debit, &line.Debit}, {row.Credit, &line.Credit},
			{row.BaseDebit, &line.BaseDebit}, {row.BaseCredit, &line.BaseCredit},
		} {
			value, err := decimal.NewFromString(amount.text)
			if err != nil {
				return nil, err
			}
			*amount.out = value
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// reportJournalLines backs reports that use the same owned ledger and cutoff.
func reportJournalLines(ctx context.Context, tx pgx.Tx, scope business.ID, asOf time.Time) ([]dbqueryv8.TrialBalanceLinesRow, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	y, m, d := asOf.Date()
	cutoff := time.Date(y, m, d, 23, 59, 59, 999999000, time.UTC)
	rows, err := dbqueryv8.New(tx).TrialBalanceLines(ctx, dbqueryv8.TrialBalanceLinesParams{
		BusinessID: scope.UUID(),
		Cutoff:     pgtype.Timestamp{Time: cutoff, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if !row.DamagedReference.Valid || row.DamagedReference.Bool ||
			!row.PostedAt.Valid || row.PostedAt.InfinityModifier != pgtype.Finite {
			return nil, errors.New("invalid report journal reference")
		}
	}
	return rows, nil
}

func reportPostingDay(postedAt time.Time) time.Time {
	y, m, d := postedAt.Date()
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}
