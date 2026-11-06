package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type fxSyncResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Fetched int      `json:"fetched"`
	Pairs   []string `json:"pairs"`
	Source  string   `json:"source"`
}
type providerRate struct {
	from, to string
	rate     pgtype.Numeric
	date     pgtype.Date
}

func registerFXSync(api huma.API, s sessionHandler) {
	response := func(desc string) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/accounting/fx-rates/sync", OperationID: "sync-fx-rates", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.fxSync)}, Responses: map[string]*huma.Response{"401": response("Not authenticated"), "403": response("Admin required"), "500": response("Internal error"), "502": response("Provider error")}}, func(context.Context, *struct{}) (*struct{ Body fxSyncResult }, error) { panic("middleware") })
}

func fetchFX(ctx context.Context, config SessionConfig) ([]providerRate, error) {
	base := config.FXProviderURL
	if base == "" {
		base = "https://api.frankfurter.dev/v1"
	}
	base = strings.TrimRight(base, "/")
	currencies := config.FXSyncCurrencies
	if currencies == nil {
		currencies = []string{"USD", "SGD"}
	}
	target := config.BaseCurrency
	if target == "" {
		target = "IDR"
	}
	target = strings.ToUpper(target)
	timeout := config.FXProviderTimeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	client := http.Client{Timeout: timeout}
	result := make([]providerRate, 0, len(currencies))
	for _, raw := range currencies {
		src := strings.ToUpper(raw)
		if src == target {
			continue
		}
		u, err := url.Parse(base + "/latest")
		if err != nil {
			return nil, errors.New("invalid provider URL")
		}
		q := u.Query()
		q.Set("base", src)
		q.Set("symbols", target)
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return nil, errors.New("invalid provider URL")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fx provider request failed for %s->%s", src, target)
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || readErr != nil {
			return nil, fmt.Errorf("fx provider request failed for %s->%s", src, target)
		}
		if len(b) > 1<<20 {
			return nil, errors.New("fx provider response too large")
		}
		var body struct {
			Date  string                     `json:"date"`
			Rates map[string]json.RawMessage `json:"rates"`
		}
		if json.Unmarshal(b, &body) != nil {
			return nil, fmt.Errorf("fx provider returned invalid JSON for %s->%s", src, target)
		}
		rawRate, ok := body.Rates[target]
		if !ok || string(rawRate) == "null" {
			continue
		}
		date, err := time.Parse("2006-01-02", body.Date)
		if err != nil || date.Year() < 1 {
			return nil, fmt.Errorf("fx provider returned invalid date for %s->%s", src, target)
		}
		payload, _ := json.Marshal(map[string]any{"from_currency": src, "to_currency": target, "rate": json.RawMessage(rawRate), "as_of_date": body.Date})
		p, errs := parseFXCreate(payload)
		if len(errs) > 0 {
			return nil, fmt.Errorf("fx provider returned invalid rate for %s->%s", src, target)
		}
		result = append(result, providerRate{src, target, p.Rate, p.AsOfDate})
	}
	return result, nil
}

func (s sessionHandler) fxSync(w http.ResponseWriter, r *http.Request) {
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	rates, err := fetchFX(r.Context(), s.config)
	if err != nil {
		authError(w, 502, err.Error())
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbquery.New(tx)
	user, err := q.LockSessionUserByID(ctx, current.UserID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := applyFXSync(ctx, q, rates)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// applyFXSync serializes every FX writer and applies the complete batch in the caller's transaction.
func applyFXSync(ctx context.Context, q *dbquery.Queries, rates []providerRate) (fxSyncResult, error) {
	if err := q.LockFXSync(ctx); err != nil {
		return fxSyncResult{}, err
	}
	out := fxSyncResult{Fetched: len(rates), Pairs: make([]string, 0, len(rates)), Source: "FRANKFURTER"}
	for _, item := range rates {
		out.Pairs = append(out.Pairs, item.from+"->"+item.to)
		existing, e := q.FindFXSyncRate(ctx, dbquery.FindFXSyncRateParams{FromCurrency: item.from, ToCurrency: item.to, AsOfDate: item.date})
		if errors.Is(e, pgx.ErrNoRows) {
			_, e = q.CreateFXRate(ctx, dbquery.CreateFXRateParams{FromCurrency: item.from, ToCurrency: item.to, Rate: item.rate, AsOfDate: item.date, Source: "FRANKFURTER"})
			out.Created++
		} else if e == nil {
			if !existing.Rate.Valid || existing.Rate.NaN || existing.Rate.InfinityModifier != pgtype.Finite || existing.Rate.Int == nil {
				return fxSyncResult{}, errors.New("invalid stored rate")
			}
			old := decimal.NewFromBigInt(existing.Rate.Int, existing.Rate.Exp)
			next := decimal.NewFromBigInt(item.rate.Int, item.rate.Exp)
			if !old.Equal(next) || existing.Source != "FRANKFURTER" {
				e = q.UpdateFXSyncRate(ctx, dbquery.UpdateFXSyncRateParams{RateID: existing.RateID, Rate: item.rate})
				out.Updated++
			}
		}
		if e != nil {
			return fxSyncResult{}, e
		}
	}
	return out, nil
}

var ErrFXJobAlreadyRunning = errors.New("weekly FX sync already running")

// RunWeeklyFXSync uses the Python scheduler's job lock and the same connection for
// the transaction, so a pool with one connection cannot deadlock itself.
func RunWeeklyFXSync(ctx context.Context, pool *pgxpool.Pool, cfg SessionConfig) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	key := int64(crc32.ChecksumIEEE([]byte("fx-rates-weekly-sync")))
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return ErrFXJobAlreadyRunning
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock($1)`, key).Scan(&unlocked); err != nil || !unlocked {
			raw := conn.Hijack()
			_ = raw.Close(cleanup)
		}
	}()
	rates, err := fetchFX(ctx, cfg)
	if err != nil {
		return err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = applyFXSync(ctx, dbquery.New(tx), rates); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
