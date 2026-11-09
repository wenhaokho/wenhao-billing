package fx_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
)

const syncPath = ratesPath + "/sync"

// answer is what the fake Frankfurter sends for one base currency.
type answer struct {
	status int
	body   string
	delay  time.Duration
}

// frankfurter is a fake Frankfurter at /v1: GET /v1/latest?base=X&symbols=IDR
// answers with answers[X], or a rate-less body for a currency it was not given.
type frankfurter struct {
	*httptest.Server
	mu      sync.Mutex
	answers map[string]answer
	calls   []string // "base=X&symbols=Y", in order
	// gate, when set, holds every USD request until two have arrived, so two
	// syncs reach the database together.
	gate chan struct{}
	usd  int
}

func newFrankfurter(t *testing.T, answers map[string]answer) *frankfurter {
	t.Helper()
	f := &frankfurter{answers: answers}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *frankfurter) serve(w http.ResponseWriter, r *http.Request) {
	base, symbols := r.URL.Query().Get("base"), r.URL.Query().Get("symbols")
	f.mu.Lock()
	f.calls = append(f.calls, "base="+base+"&symbols="+symbols)
	a, ok := f.answers[base]
	gate := f.gate
	if gate != nil && base == "USD" {
		f.usd++
		if f.usd == 2 {
			close(gate)
		}
	}
	f.mu.Unlock()
	if r.URL.Path != "/v1/latest" {
		http.NotFound(w, r)
		return
	}
	if gate != nil && base == "USD" {
		select {
		case <-gate:
		case <-time.After(5 * time.Second):
		}
	}
	if !ok {
		a = answer{body: `{"amount":1.0,"base":"` + base + `","date":"2026-01-09","rates":{}}`}
	}
	if a.delay > 0 {
		select {
		case <-time.After(a.delay):
		case <-r.Context().Done():
			return
		}
	}
	if a.status != 0 {
		w.WriteHeader(a.status)
	}
	_, _ = w.Write([]byte(a.body))
}

func (f *frankfurter) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// quote is Frankfurter's answer for base->IDR at rate (a JSON literal) on date.
func quote(base, rateLiteral, date string) answer {
	return answer{body: fmt.Sprintf(`{"amount":1.0,"base":%q,"date":%q,"rates":{"IDR":%s}}`, base, date, rateLiteral)}
}

// syncResult is SyncResult as the client reads it.
type syncResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Fetched int      `json:"fetched"`
	Pairs   []string `json:"pairs"`
	Source  string   `json:"source"`
}

func runSync(t *testing.T, a *apitest.App) syncResult {
	t.Helper()
	return apitest.Decode[syncResult](t, a.Do(t, http.MethodPost, syncPath, nil), http.StatusOK)
}

func wantSync(t *testing.T, got syncResult, created, updated, fetched int, pairs ...string) {
	t.Helper()
	if got.Created != created || got.Updated != updated || got.Fetched != fetched ||
		!slices.Equal(got.Pairs, pairs) || got.Source != "FRANKFURTER" {
		t.Errorf("sync %+v, want created %d updated %d fetched %d pairs %q", got, created, updated, fetched, pairs)
	}
}

// syncApp serves the FX routes against f at its /v1/ (trailing slash
// included: the base URL's is trimmed).
func syncApp(t *testing.T, f *frankfurter, currencies ...string) *apitest.App {
	t.Helper()
	return newApp(t, fxConfig(f.URL+"/v1/", currencies...))
}

func TestSyncCreatesThenIdempotent(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{
		"USD": quote("USD", "16250.5", "2026-01-09"),
		"SGD": quote("SGD", "12100.25", "2026-01-09"),
	})
	a := syncApp(t, f)
	wantSync(t, runSync(t, a), 2, 0, 2, "USD->IDR", "SGD->IDR")
	if calls := f.Calls(); !slices.Equal(calls, []string{"base=USD&symbols=IDR", "base=SGD&symbols=IDR"}) {
		t.Errorf("calls %q", calls)
	}
	usd9, sgd9 := "USD->IDR 2026-01-09 16250.50000000 FRANKFURTER", "SGD->IDR 2026-01-09 12100.25000000 FRANKFURTER"
	wantKeys(t, listRates(t, a, "?from_currency=USD"), usd9, "USD->IDR 2026-01-01 16000.00000000 TEST")
	wantKeys(t, listRates(t, a, "?from_currency=SGD"), sgd9, "SGD->IDR 2026-01-01 12000.00000000 TEST")

	// The same answers again change nothing.
	wantSync(t, runSync(t, a), 0, 0, 2, "USD->IDR", "SGD->IDR")
	// "16250.50" is the stored value: not a change either.
	f.mu.Lock()
	f.answers["USD"] = quote("USD", "16250.50", "2026-01-09")
	f.mu.Unlock()
	wantSync(t, runSync(t, a), 0, 0, 2, "USD->IDR", "SGD->IDR")

	// A changed rate updates its row in place.
	f.mu.Lock()
	f.answers["USD"] = quote("USD", "16300", "2026-01-09")
	f.mu.Unlock()
	wantSync(t, runSync(t, a), 0, 1, 2, "USD->IDR", "SGD->IDR")
	wantKeys(t, listRates(t, a, "?from_currency=USD"), "USD->IDR 2026-01-09 16300.00000000 FRANKFURTER", "USD->IDR 2026-01-01 16000.00000000 TEST")
}

func TestSyncOverwritesManualSameDate(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{
		"USD": quote("USD", "16250.5", "2026-01-09"),
		"SGD": quote("SGD", "12100.25", "2026-01-09"),
	})
	a := syncApp(t, f)
	// Same value, MANUAL: the source alone makes it an update.
	createRate(t, a, map[string]any{"from_currency": "USD", "to_currency": "IDR", "rate": "16250.5", "as_of_date": "2026-01-09"})
	// Another value, MANUAL.
	createRate(t, a, map[string]any{"from_currency": "SGD", "to_currency": "IDR", "rate": "1", "as_of_date": "2026-01-09"})
	wantSync(t, runSync(t, a), 0, 2, 2, "USD->IDR", "SGD->IDR")
	wantKeys(t, listRates(t, a, "?from_currency=USD"), "USD->IDR 2026-01-09 16250.50000000 FRANKFURTER", "USD->IDR 2026-01-01 16000.00000000 TEST")
	wantKeys(t, listRates(t, a, "?from_currency=SGD"), "SGD->IDR 2026-01-09 12100.25000000 FRANKFURTER", "SGD->IDR 2026-01-01 12000.00000000 TEST")
}

func TestSyncLeavesOtherDates(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{"USD": quote("USD", "16250.5", "2026-01-09")})
	a := syncApp(t, f, "USD")
	createRate(t, a, map[string]any{"from_currency": "USD", "to_currency": "IDR", "rate": "1", "as_of_date": "2026-01-08"})
	wantSync(t, runSync(t, a), 1, 0, 1, "USD->IDR")
	wantKeys(t, listRates(t, a, "?from_currency=USD"),
		"USD->IDR 2026-01-09 16250.50000000 FRANKFURTER",
		"USD->IDR 2026-01-08 1.00000000 MANUAL",
		"USD->IDR 2026-01-01 16000.00000000 TEST")
}

func TestSyncSkipsMissingPair(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{
		"USD": quote("USD", "16250.5", "2026-01-09"),
		"SGD": {body: `{"amount":1.0,"base":"SGD","date":"2026-01-09","rates":{}}`},
		"EUR": {body: `{"amount":1.0,"base":"EUR","date":"2026-01-09","rates":{"IDR":null}}`},
		"GBP": {body: `{"amount":1.0,"base":"GBP","date":"2026-01-09"}`},
	})
	a := syncApp(t, f, "USD", "SGD", "EUR", "GBP")
	wantSync(t, runSync(t, a), 1, 0, 1, "USD->IDR")
	wantKeys(t, listRates(t, a, "?from_currency=SGD"), "SGD->IDR 2026-01-01 12000.00000000 TEST")
	wantKeys(t, listRates(t, a, "?from_currency=EUR"))
}

func TestSyncSkipsBaseCurrency(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{"USD": quote("USD", "16250.5", "2026-01-09")})
	a := syncApp(t, f, "idr", "usd")
	wantSync(t, runSync(t, a), 1, 0, 1, "USD->IDR")
	if calls := f.Calls(); !slices.Equal(calls, []string{"base=USD&symbols=IDR"}) {
		t.Errorf("calls %q, want USD only", calls)
	}
}

// wantNoSyncedRows checks the sync wrote nothing: no FRANKFURTER row exists.
func wantNoSyncedRows(t *testing.T, a *apitest.App) {
	t.Helper()
	for _, r := range listRates(t, a, "") {
		if r.Source == "FRANKFURTER" {
			t.Errorf("written: %s", key(r))
		}
	}
}

func wantProviderError(t *testing.T, a *apitest.App, prefix string) problem {
	t.Helper()
	p := apitest.Decode[problem](t, a.Do(t, http.MethodPost, syncPath, nil), http.StatusBadGateway)
	if !strings.HasPrefix(p.Detail, prefix) || p.Code != "fx_provider_error" {
		t.Errorf("got %q %q, want fx_provider_error and a detail starting %q", p.Code, p.Detail, prefix)
	}
	wantNoSyncedRows(t, a)
	return p
}

func TestSyncProviderErrorIs502WritesNothing(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{
		"USD": quote("USD", "16250.5", "2026-01-09"),
		"SGD": {status: http.StatusInternalServerError, body: `{"message":"boom"}`},
	})
	a := syncApp(t, f)
	p := wantProviderError(t, a, "fx provider request failed for SGD->IDR: ")
	if !strings.Contains(p.Detail, "500") {
		t.Errorf("detail %q does not name the status", p.Detail)
	}
	// A transport error (nothing listening) is the same failure.
	f.Close()
	wantProviderError(t, a, "fx provider request failed for USD->IDR: ")
}

func TestSyncInvalidJSONIs502(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{
		"USD": quote("USD", "16250.5", "2026-01-09"),
		"SGD": {body: `<html>maintenance</html>`},
	})
	wantProviderError(t, syncApp(t, f), "fx provider returned invalid JSON for SGD->IDR: ")
}

func TestSyncInvalidDateIs502(t *testing.T) {
	for body, detail := range map[string]string{
		`{"date":"2026-13-01","rates":{"IDR":16000}}`: "fx provider returned invalid date for USD->IDR: '2026-13-01'",
		`{"rates":{"IDR":16000}}`:                     "fx provider returned invalid date for USD->IDR: None",
		`{"date":null,"rates":{"IDR":16000}}`:         "fx provider returned invalid date for USD->IDR: None",
	} {
		f := newFrankfurter(t, map[string]answer{"USD": {body: body}})
		a := syncApp(t, f, "USD")
		if p := wantProviderError(t, a, detail); p.Detail != detail {
			t.Errorf("%s: detail %q, want %q", body, p.Detail, detail)
		}
	}
}

func TestSyncKeepsProviderLiteral(t *testing.T) {
	// 19 significant digits: a float64 keeps about 16, so a decode through
	// float64 would store 12345678901.12345700 (or similar).
	f := newFrankfurter(t, map[string]answer{"USD": quote("USD", "12345678901.12345678", "2026-01-09")})
	a := syncApp(t, f, "USD")
	wantSync(t, runSync(t, a), 1, 0, 1, "USD->IDR")
	wantKeys(t, listRates(t, a, "?from_currency=USD"),
		"USD->IDR 2026-01-09 12345678901.12345678 FRANKFURTER", "USD->IDR 2026-01-01 16000.00000000 TEST")
	// The same literal again is no change.
	wantSync(t, runSync(t, a), 0, 0, 1, "USD->IDR")
}

func TestSyncConcurrentIsIdempotent(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{
		"USD": quote("USD", "16250.5", "2026-01-09"),
		"SGD": quote("SGD", "12100.25", "2026-01-09"),
	})
	f.gate = make(chan struct{})
	a := syncApp(t, f)
	var wg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, 2)
	for i := range recs {
		wg.Go(func() {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, syncPath, nil)
			req.Header.Set("Cookie", a.Cookie)
			recs[i] = httptest.NewRecorder()
			a.Handler.ServeHTTP(recs[i], req)
		})
	}
	wg.Wait()
	created, updated := 0, 0
	for _, rec := range recs {
		r := apitest.Decode[syncResult](t, rec, http.StatusOK)
		created += r.Created
		updated += r.Updated
	}
	if created != 2 || updated != 0 {
		t.Errorf("created %d updated %d across both, want 2 and 0", created, updated)
	}
	wantKeys(t, listRates(t, a, "?from_currency=USD"), "USD->IDR 2026-01-09 16250.50000000 FRANKFURTER", "USD->IDR 2026-01-01 16000.00000000 TEST")
	wantKeys(t, listRates(t, a, "?from_currency=SGD"), "SGD->IDR 2026-01-09 12100.25000000 FRANKFURTER", "SGD->IDR 2026-01-01 12000.00000000 TEST")
}

func TestSyncTimeout(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{"USD": {body: quote("USD", "1", "2026-01-09").body, delay: time.Second}})
	cfg := fxConfig(f.URL+"/v1", "USD")
	cfg.FXProviderTimeout = 100 * time.Millisecond
	a := newApp(t, cfg)
	start := time.Now()
	wantProviderError(t, a, "fx provider request failed for USD->IDR: ")
	if took := time.Since(start); took > 900*time.Millisecond {
		t.Errorf("took %s: the 100 ms timeout did not apply", took)
	}
}

// Sync is what the weekly job calls: the same result as the route.
func TestSyncDirect(t *testing.T) {
	f := newFrankfurter(t, map[string]answer{"USD": quote("USD", "16250.5", "2026-01-09")})
	cfg := fxConfig(f.URL+"/v1", "USD")
	a := newApp(t, cfg)
	got, err := fx.Sync(context.Background(), a.Pool, cfg, &http.Client{Timeout: time.Second})
	if err != nil || got.Created != 1 || got.Fetched != 1 || !slices.Equal(got.Pairs, []string{"USD->IDR"}) || got.Source != "FRANKFURTER" {
		t.Errorf("%+v, %v", got, err)
	}
	f.Close()
	_, err = fx.Sync(context.Background(), a.Pool, cfg, &http.Client{Timeout: time.Second})
	if err == nil || !strings.HasPrefix(err.Error(), "fx provider request failed for USD->IDR: ") {
		t.Errorf("err %v", err)
	}
	if !errors.Is(err, fx.ErrProvider) {
		t.Errorf("err %v does not match fx.ErrProvider", err)
	}
}

func TestSyncNothingFetchedPairsIsArray(t *testing.T) {
	f := newFrankfurter(t, nil)
	a := syncApp(t, f, "IDR", "EUR") // the base is skipped; EUR has no IDR rate
	rec := a.Do(t, http.MethodPost, syncPath, nil)
	want := `{"created":0,"updated":0,"fetched":0,"pairs":[],"source":"FRANKFURTER"}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Errorf("%d %s, want %s", rec.Code, rec.Body, want)
	}
}
