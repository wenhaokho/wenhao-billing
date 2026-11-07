package scopedledger

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/aging"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func CurrentAging(ctx context.Context, tx pgx.Tx, scope business.ID, today time.Time, kind string) (aging.Report, error) {
	if err := scope.Validate(); err != nil {
		return aging.Report{}, err
	}
	rows, err := dbqueryv8.New(tx).CurrentAgingDocuments(ctx, dbqueryv8.CurrentAgingDocumentsParams{
		BusinessID: scope.UUID(), Kind: kind,
		AsOf: pgtype.Date{Time: reportPostingDay(today), Valid: true},
	})
	if err != nil {
		return aging.Report{}, err
	}
	records := make([]aging.Record, 0, len(rows))
	for _, row := range rows {
		if !row.DamagedReference.Valid || row.DamagedReference.Bool {
			return aging.Report{}, errors.New("unresolved aging party reference")
		}
		issue, err := agingDate(row.IssueDate)
		if err != nil {
			return aging.Report{}, err
		}
		due, err := agingDate(row.DueDate)
		if err != nil {
			return aging.Report{}, err
		}
		balance, err := decimal.NewFromString(row.Balance)
		if err != nil {
			return aging.Report{}, err
		}
		record := aging.Record{
			PartyID: row.PartyID, PartyName: row.PartyName, Currency: row.Currency,
			DocumentDate: issue, Balance: balance,
		}
		if row.DueDate.Valid {
			record.DueDate = &due
		}
		records = append(records, record)
	}
	return aging.CalculateCurrent(today, kind, records)
}

func agingDate(date pgtype.Date) (time.Time, error) {
	if !date.Valid {
		return time.Time{}, nil
	}
	y, m, d := date.Time.Date()
	if date.InfinityModifier != pgtype.Finite || y < 1 || y > 9999 {
		return time.Time{}, errors.New("invalid aging document date")
	}
	// Keep 0001-01-01 distinct from a missing date.
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC), nil
}
