package fx_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
)

const summaryPath = ratesPath + "/summary"

// currencySummary is CurrencySummary as the client reads it.
type currencySummary struct {
	Currency     string   `json:"currency"`
	LatestRate   string   `json:"latest_rate"`
	LatestAsOf   string   `json:"latest_as_of"`
	PreviousRate *string  `json:"previous_rate"`
	PreviousAsOf *string  `json:"previous_as_of"`
	ChangePct    *float64 `json:"change_pct"`
}

type lastSync struct {
	RanAt   string `json:"ran_at"`
	Trigger string `json:"trigger"`
	Source  string `json:"source"`
}

type summary struct {
	BaseCurrency string            `json:"base_currency"`
	Currencies   []currencySummary `json:"currencies"`
	LastSync     *lastSync         `json:"last_sync"`
}

func getSummary(t *testing.T, a *apitest.App) summary {
	t.Helper()
	return apitest.Decode[summary](t, a.Do(t, http.MethodGet, summaryPath, nil), http.StatusOK)
}

// syncRun is one fx_sync_runs row: trigger, source, counts and actor.
type syncRun struct {
	Trigger, Source  string
	Created, Updated int
	Actor            *uuid.UUID
}

func syncRuns(t *testing.T, a *apitest.App) []syncRun {
	t.Helper()
	rows, err := a.Pool.Query(t.Context(),
		`SELECT trigger, source, created, updated, actor_user_id FROM fx_sync_runs ORDER BY run_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []syncRun
	for rows.Next() {
		var r syncRun
		if err := rows.Scan(&r.Trigger, &r.Source, &r.Created, &r.Updated, &r.Actor); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The seeded rates (SGD and USD to IDR on 2026-01-01) plus a newer SGD rate:
// SGD has a previous rate and a change, USD has neither, and nothing has
// synced yet.
func TestSummaryChangeAndNoPrevious(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	createRate(t, a, map[string]any{"from_currency": "sgd", "to_currency": "IDR", "rate": "12600", "as_of_date": "2026-01-08"})
	// Another pair's rate is not a rate to the base currency.
	createRate(t, a, map[string]any{"from_currency": "EUR", "to_currency": "USD", "rate": "1.1", "as_of_date": "2026-01-08"})

	got := getSummary(t, a)
	if got.BaseCurrency != "IDR" || got.LastSync != nil {
		t.Errorf("base %q last_sync %+v, want IDR and null", got.BaseCurrency, got.LastSync)
	}
	if len(got.Currencies) != 2 {
		t.Fatalf("currencies %+v, want SGD and USD", got.Currencies)
	}
	sgd, usd := got.Currencies[0], got.Currencies[1]
	if sgd.Currency != "SGD" || sgd.LatestRate != "12600.00000000" || sgd.LatestAsOf != "2026-01-08" ||
		sgd.PreviousRate == nil || *sgd.PreviousRate != "12000.00000000" ||
		sgd.PreviousAsOf == nil || *sgd.PreviousAsOf != "2026-01-01" ||
		sgd.ChangePct == nil || *sgd.ChangePct != 5 {
		t.Errorf("SGD %+v, want 12600 on 2026-01-08 from 12000 on 2026-01-01, +5%%", sgd)
	}
	if usd.Currency != "USD" || usd.LatestRate != "16000.00000000" || usd.LatestAsOf != "2026-01-01" ||
		usd.PreviousRate != nil || usd.PreviousAsOf != nil || usd.ChangePct != nil {
		t.Errorf("USD %+v, want 16000 on 2026-01-01 with no previous", usd)
	}
}

// A fall is a negative change, rounded to four places.
func TestSummaryNegativeChange(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	createRate(t, a, map[string]any{"from_currency": "USD", "to_currency": "IDR", "rate": "15000", "as_of_date": "2026-01-05"})
	got := getSummary(t, a)
	usd := got.Currencies[1]
	if usd.Currency != "USD" || usd.ChangePct == nil || *usd.ChangePct != -6.25 {
		t.Errorf("USD %+v, want -6.25%%", usd)
	}
}

// With no rate at all the list is empty, not null.
func TestSummaryNoRates(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	if _, err := a.Pool.Exec(t.Context(), `DELETE FROM fx_rates`); err != nil {
		t.Fatal(err)
	}
	rec := a.Do(t, http.MethodGet, summaryPath, nil)
	want := `{"base_currency":"IDR","currencies":[],"last_sync":null}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Errorf("%d %s, want %s", rec.Code, rec.Body, want)
	}
}

// "Sync now" records a MANUAL run by the caller, and the summary reports it.
func TestManualSyncRecordsRun(t *testing.T) {
	t.Parallel()
	f := newFrankfurter(t, map[string]answer{
		"USD": quote("USD", "16250.5", "2026-01-09"),
		"SGD": quote("SGD", "12100.25", "2026-01-09"),
	})
	a := syncApp(t, f)
	wantSync(t, runSync(t, a), 2, 0, 2, "USD->IDR", "SGD->IDR")

	runs := syncRuns(t, a)
	if len(runs) != 1 || runs[0].Trigger != "MANUAL" || runs[0].Source != "FRANKFURTER" ||
		runs[0].Created != 2 || runs[0].Updated != 0 || runs[0].Actor == nil || *runs[0].Actor != a.AdminID {
		t.Fatalf("runs %+v, want one MANUAL FRANKFURTER run of 2 created by %s", runs, a.AdminID)
	}
	got := getSummary(t, a)
	if got.LastSync == nil || got.LastSync.Trigger != "MANUAL" || got.LastSync.Source != "FRANKFURTER" {
		t.Fatalf("last_sync %+v, want MANUAL FRANKFURTER", got.LastSync)
	}
	ranAt, err := time.Parse(time.RFC3339Nano, got.LastSync.RanAt)
	if err != nil || time.Since(ranAt) > time.Minute || time.Since(ranAt) < -time.Minute {
		t.Errorf("ran_at %q (%v), want about now", got.LastSync.RanAt, err)
	}
	if got.Currencies[0].Currency != "SGD" || got.Currencies[0].LatestRate != "12100.25000000" {
		t.Errorf("SGD %+v, want the synced rate", got.Currencies[0])
	}
}

// The weekly job's Sync records a SCHEDULED run with no actor; a failed sync
// records nothing.
func TestScheduledSyncRecordsRun(t *testing.T) {
	t.Parallel()
	f := newFrankfurter(t, map[string]answer{"USD": quote("USD", "16250.5", "2026-01-09")})
	cfg := fxConfig(f.URL+"/v1", "USD")
	a := newApp(t, cfg)
	client := &http.Client{Timeout: time.Second}
	if _, err := fx.Sync(context.Background(), a.Pool, cfg, client, fx.TriggerScheduled, nil); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := fx.Sync(context.Background(), a.Pool, cfg, client, fx.TriggerScheduled, nil); err == nil {
		t.Fatal("a sync against a closed provider succeeded")
	}
	runs := syncRuns(t, a)
	if len(runs) != 1 || runs[0].Trigger != "SCHEDULED" || runs[0].Created != 1 || runs[0].Actor != nil {
		t.Errorf("runs %+v, want one SCHEDULED run of 1 created with no actor", runs)
	}
	if got := getSummary(t, a); got.LastSync == nil || got.LastSync.Trigger != "SCHEDULED" {
		t.Errorf("last_sync %+v, want SCHEDULED", got.LastSync)
	}
}
