package fx

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Sync pulls the latest provider rate of each cfg.FXSyncCurrencies currency
// into the base currency and stores them (Python's run_fx_sync), for the
// "sync now" route and the weekly job. Every pair is fetched first, then all
// are written in one transaction, each an upsert on the pair and date: a new
// row counts created, a changed rate or a non-provider source is overwritten
// and counts updated, an identical row counts neither. A provider failure
// returns an error matching ErrProvider and writes nothing. client must not be
// nil; each request also has cfg.FXProviderTimeout.
func Sync(ctx context.Context, pool *pgxpool.Pool, cfg platform.Config, client *http.Client) (SyncResult, error) {
	rates, err := fetchLatest(ctx, client, cfg, cfg.FXSyncCurrencies, strings.ToUpper(cfg.BaseCurrency))
	if err != nil {
		return SyncResult{}, err
	}
	res := SyncResult{Fetched: len(rates), Pairs: make([]string, 0, len(rates)), Source: Source}
	err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		q := fxdb.New(tx)
		for _, r := range rates {
			res.Pairs = append(res.Pairs, r.from+"->"+r.to)
			written, err := q.UpsertSyncedRate(ctx, fxdb.UpsertSyncedRateParams{
				FromCurrency: r.from, ToCurrency: r.to, Rate: r.rate,
				AsOfDate: pgtype.Date{Time: r.asOf, Valid: true},
			})
			if err != nil {
				return err
			}
			for _, inserted := range written {
				if inserted {
					res.Created++
				} else {
					res.Updated++
				}
			}
		}
		return nil
	})
	if err != nil {
		return SyncResult{}, err
	}
	return res, nil
}
