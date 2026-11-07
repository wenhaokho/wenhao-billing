package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedfx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
)

const scopedFXWriteTimeout = 30 * time.Second

func (h scopedProfileRouter) fxSync(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	current, ok := h.admin(w, r.WithContext(ctx))
	if !ok {
		return
	}
	rates, err := fetchFX(ctx, h.cfg)
	if err != nil {
		authError(w, 502, err.Error())
		return
	}
	ctx, cancelWrite := context.WithTimeout(ctx, scopedFXWriteTimeout)
	defer cancelWrite()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer rollbackScopedFX(tx)
	user, err := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := applyScopedFXSync(ctx, tx, h.scope, rates)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func applyScopedFXSync(ctx context.Context, tx pgx.Tx, scope business.ID, rates []providerRate) (fxSyncResult, error) {
	if err := scopedfx.LockSync(ctx, tx, scope); err != nil {
		return fxSyncResult{}, err
	}
	const source = "FRANKFURTER"
	out := fxSyncResult{Fetched: len(rates), Pairs: make([]string, 0, len(rates)), Source: source}
	for _, item := range rates {
		out.Pairs = append(out.Pairs, item.from+"->"+item.to)
		existing, err := scopedfx.FindSync(ctx, tx, scope, item.from, item.to, item.date)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			_, err = scopedfx.Create(ctx, tx, scope, scopedfx.Rate{From: item.from, To: item.to, Value: item.rate, Date: item.date, Source: source})
			out.Created++
		case err == nil:
			if !existing.Rate.Valid || existing.Rate.NaN || existing.Rate.InfinityModifier != pgtype.Finite || existing.Rate.Int == nil {
				return fxSyncResult{}, errors.New("invalid stored rate")
			}
			old := decimal.NewFromBigInt(existing.Rate.Int, existing.Rate.Exp)
			next := decimal.NewFromBigInt(item.rate.Int, item.rate.Exp)
			if !old.Equal(next) || existing.Source != source {
				err = scopedfx.UpdateSync(ctx, tx, scope, existing.RateID, item.rate)
				out.Updated++
			}
		}
		if err != nil {
			return fxSyncResult{}, err
		}
	}
	return out, nil
}

func rollbackScopedFX(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// RunScopedWeeklyFXSync runs the staged v8 job for one business. The job and
// writer locks use the same transaction, even when the pool has one connection.
func RunScopedWeeklyFXSync(ctx context.Context, pool *pgxpool.Pool, scope business.ID, cfg SessionConfig) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackScopedFX(tx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('fx-weekly-sync:' || $1::uuid::text, 0))`, scope.UUID()).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return ErrFXJobAlreadyRunning
	}
	rates, err := fetchFX(ctx, cfg)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	ctx, cancelWrite := context.WithTimeout(ctx, scopedFXWriteTimeout)
	defer cancelWrite()
	if _, err := applyScopedFXSync(ctx, tx, scope, rates); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
