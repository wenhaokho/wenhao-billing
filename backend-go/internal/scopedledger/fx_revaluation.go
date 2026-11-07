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
	"github.com/wenhaokho/wenhao-billing/backend/internal/fxrevaluation"
)

func FXRevaluation(ctx context.Context, tx pgx.Tx, scope business.ID, asOf time.Time, base string) (fxrevaluation.Report, error) {
	lines, err := TrialBalanceLines(ctx, tx, scope, asOf)
	if err != nil {
		return fxrevaluation.Report{}, err
	}
	type bucketKey struct {
		accountID int64
		currency  string
	}
	buckets := make(map[bucketKey]fxrevaluation.Balance)
	for _, line := range lines {
		if line.Currency == base || (line.Type != "ASSET" && line.Type != "LIABILITY") {
			continue
		}
		key := bucketKey{line.AccountID, line.Currency}
		balance := buckets[key]
		balance.AccountID, balance.Code, balance.Name = line.AccountID, line.Code, line.Name
		balance.Type, balance.Currency = line.Type, line.Currency
		balance.Debit = balance.Debit.Add(line.Debit)
		balance.Credit = balance.Credit.Add(line.Credit)
		balance.BaseDebit = balance.BaseDebit.Add(line.BaseDebit)
		balance.BaseCredit = balance.BaseCredit.Add(line.BaseCredit)
		buckets[key] = balance
	}

	asOf = reportPostingDay(asOf)
	rates := make(map[string]fxrevaluation.Rate)
	lookedUp := make(map[string]bool)
	balances := make([]fxrevaluation.Balance, 0, len(buckets))
	q := dbqueryv8.New(tx)
	for _, balance := range buckets {
		balances = append(balances, balance)
		if balance.Debit.Equal(balance.Credit) && balance.BaseDebit.Equal(balance.BaseCredit) {
			continue
		}
		currency := balance.Currency
		if lookedUp[currency] {
			continue
		}
		lookedUp[currency] = true
		row, err := q.LatestFXRate(ctx, dbqueryv8.LatestFXRateParams{
			BusinessID: scope.UUID(), FromCurrency: currency, ToCurrency: base,
			OnDate: pgtype.Date{Time: asOf, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fxrevaluation.Report{}, err
		}
		if !row.Rate.Valid || row.Rate.Int == nil || row.Rate.NaN || row.Rate.InfinityModifier != pgtype.Finite ||
			!row.AsOfDate.Valid || row.AsOfDate.InfinityModifier != pgtype.Finite {
			return fxrevaluation.Report{}, errors.New("invalid report exchange rate")
		}
		rates[currency] = fxrevaluation.Rate{
			Value: decimal.NewFromBigInt(row.Rate.Int, row.Rate.Exp),
			AsOf:  reportPostingDay(row.AsOfDate.Time),
		}
	}
	return fxrevaluation.Calculate(asOf, base, true, balances, rates)
}
