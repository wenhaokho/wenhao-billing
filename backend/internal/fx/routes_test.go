package fx_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const ratesPath = "/api/v1/accounting/fx-rates"

type problem struct {
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
	Errors []struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	} `json:"errors"`
}

// rate is RateOut as the client reads it.
type rate struct {
	RateID       int64  `json:"rate_id"`
	FromCurrency string `json:"from_currency"`
	ToCurrency   string `json:"to_currency"`
	Rate         string `json:"rate"`
	AsOfDate     string `json:"as_of_date"`
	Source       string `json:"source"`
	CreatedAt    string `json:"created_at"`
}

// fxConfig is the configuration the FX routes see: base IDR, syncing
// currencies from the provider at baseURL.
func fxConfig(baseURL string, currencies ...string) platform.Config {
	if len(currencies) == 0 {
		currencies = []string{"USD", "SGD"}
	}
	return platform.Config{
		BaseCurrency: "IDR", FXProviderBaseURL: baseURL,
		FXProviderTimeout: 5 * time.Second, FXSyncCurrencies: currencies,
	}
}

// newApp serves the FX routes with cfg, through a client with cfg's timeout.
func newApp(t *testing.T, cfg platform.Config) *apitest.App {
	t.Helper()
	client := fx.NewClient(cfg)
	return apitest.New(t, func(api huma.API, d apitest.Deps) { fx.Register(api, d.Pool, cfg, client) })
}

// newRoutesApp serves the FX routes with no provider: only sync needs one.
func newRoutesApp(t *testing.T) *apitest.App {
	t.Helper()
	return newApp(t, fxConfig("http://provider.invalid"))
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code, detail string) problem {
	t.Helper()
	p := apitest.Decode[problem](t, rec, status)
	if p.Detail != detail || (code != "" && p.Code != code) {
		t.Errorf("got %q %q, want %q %q: %s", p.Code, p.Detail, code, detail, rec.Body)
	}
	return p
}

// want422At checks rec is a 422 with an error at location.
func want422At(t *testing.T, rec *httptest.ResponseRecorder, location string) problem {
	t.Helper()
	p := apitest.Decode[problem](t, rec, http.StatusUnprocessableEntity)
	for _, e := range p.Errors {
		if e.Location == location {
			return p
		}
	}
	t.Errorf("no error at %s: %s", location, rec.Body)
	return p
}

func listRates(t *testing.T, a *apitest.App, query string) []rate {
	t.Helper()
	return apitest.Decode[[]rate](t, a.Do(t, http.MethodGet, ratesPath+query, nil), http.StatusOK)
}

func createRate(t *testing.T, a *apitest.App, body map[string]any) rate {
	t.Helper()
	return apitest.Decode[rate](t, a.Do(t, http.MethodPost, ratesPath, body), http.StatusCreated)
}

// key is a rate's pair, date, value and source, what the tests compare.
func key(r rate) string {
	return fmt.Sprintf("%s->%s %s %s %s", r.FromCurrency, r.ToCurrency, r.AsOfDate, r.Rate, r.Source)
}

func keys(rs []rate) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, key(r))
	}
	return out
}

func wantKeys(t *testing.T, got []rate, want ...string) {
	t.Helper()
	g := keys(got)
	if fmt.Sprint(g) != fmt.Sprint(want) {
		t.Errorf("rates\n got %q\nwant %q", g, want)
	}
}

func TestBaseCurrency(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	rec := a.Do(t, http.MethodGet, "/api/v1/accounting/base-currency", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"base_currency":"IDR"}`+"\n" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestListRatesOrderAndFilters(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	createRate(t, a, map[string]any{"from_currency": "EUR", "to_currency": "IDR", "rate": "17000", "as_of_date": "2026-01-05"})
	createRate(t, a, map[string]any{"from_currency": "USD", "to_currency": "IDR", "rate": "16100", "as_of_date": "2026-01-05"})
	seedIDR := "IDR->IDR 2026-11-08 1.00000000 SEED"
	usd5, eur5 := "USD->IDR 2026-01-05 16100.00000000 MANUAL", "EUR->IDR 2026-01-05 17000.00000000 MANUAL"
	usd1, sgd1 := "USD->IDR 2026-01-01 16000.00000000 TEST", "SGD->IDR 2026-01-01 12000.00000000 TEST"
	// as_of_date newest first, then the newest rate_id: the seeds went in
	// USD before SGD, so SGD (the later id) comes first on 01-01.
	wantKeys(t, listRates(t, a, ""), seedIDR, usd5, eur5, sgd1, usd1)
	wantKeys(t, listRates(t, a, "?from_currency=usd"), usd5, usd1)
	wantKeys(t, listRates(t, a, "?to_currency=idr&from_currency=EUR"), eur5)
	wantKeys(t, listRates(t, a, "?to_currency=IDR"), seedIDR, usd5, eur5, sgd1, usd1)
	if rec := a.Do(t, http.MethodGet, ratesPath+"?from_currency=GBP", nil); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Errorf("no match: %d %q, want 200 []", rec.Code, rec.Body)
	}
}

func TestListRatesFilterLengthIs422(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	for _, q := range []struct{ query, location string }{
		{"?from_currency=US", "from_currency"},
		{"?from_currency=USDX", "from_currency"},
		{"?to_currency=ID", "to_currency"},
		{"?to_currency=IDRX", "to_currency"},
	} {
		want422At(t, a.Do(t, http.MethodGet, ratesPath+q.query, nil), q.location)
	}
}

func TestCreateRate201(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	rec := a.Do(t, http.MethodPost, ratesPath, map[string]any{
		"from_currency": "eur", "to_currency": "idr", "rate": "16250.5", "as_of_date": "2026-02-01",
	})
	got := apitest.Decode[rate](t, rec, http.StatusCreated)
	if key(got) != "EUR->IDR 2026-02-01 16250.50000000 MANUAL" || got.RateID <= 1 || got.CreatedAt == "" {
		t.Errorf("%+v", got)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["rate_id"]) != fmt.Sprint(got.RateID) || string(raw["rate"]) != `"16250.50000000"` {
		t.Errorf("rate_id %s rate %s", raw["rate_id"], raw["rate"])
	}
	if _, err := time.Parse(time.RFC3339Nano, got.CreatedAt); err != nil {
		t.Errorf("created_at %q: %v", got.CreatedAt, err)
	}
	// source is the client's when sent; a JSON number rate is accepted too.
	other := createRate(t, a, map[string]any{
		"from_currency": "GBP", "to_currency": "IDR", "rate": 20000.125, "as_of_date": "2026-02-01", "source": "BANK",
	})
	if key(other) != "GBP->IDR 2026-02-01 20000.12500000 BANK" {
		t.Errorf("%+v", other)
	}
	wantKeys(t, listRates(t, a, "?from_currency=EUR"), key(got))
}

func TestCreateRateDuplicateIs409(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	rec := a.Do(t, http.MethodPost, ratesPath, map[string]any{
		"from_currency": "usd", "to_currency": "IDR", "rate": "1", "as_of_date": "2026-01-01",
	})
	wantProblem(t, rec, http.StatusConflict, "", "fx rate for this currency pair and date already exists")
	wantKeys(t, listRates(t, a, "?from_currency=USD"), "USD->IDR 2026-01-01 16000.00000000 TEST")
}

func TestCreateRateNinePlacesIs422(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	want422At(t, a.Do(t, http.MethodPost, ratesPath, map[string]any{
		"from_currency": "EUR", "to_currency": "IDR", "rate": "1.123456789", "as_of_date": "2026-02-01",
	}), "rate")
	wantKeys(t, listRates(t, a, "?from_currency=EUR"))
}

func TestCreateRateZeroIs422(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	for _, r := range []any{"0", "-1", 0, "0.00000000"} {
		p := want422At(t, a.Do(t, http.MethodPost, ratesPath, map[string]any{
			"from_currency": "EUR", "to_currency": "IDR", "rate": r, "as_of_date": "2026-02-01",
		}), "rate")
		if len(p.Errors) != 1 || p.Errors[0].Message != "Input should be greater than 0" {
			t.Errorf("%v: %+v", r, p)
		}
	}
	wantKeys(t, listRates(t, a, "?from_currency=EUR"))
}

func TestCreateRateFieldRules(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	good := func() map[string]any {
		return map[string]any{"from_currency": "EUR", "to_currency": "IDR", "rate": "1", "as_of_date": "2026-02-01"}
	}
	for field, bad := range map[string]any{
		"from_currency": "EU", "to_currency": "IDRX", "as_of_date": "2026-02-30", "source": "",
	} {
		body := good()
		body[field] = bad
		want422At(t, a.Do(t, http.MethodPost, ratesPath, body), field)
	}
	body := good()
	body["source"] = nil
	want422At(t, a.Do(t, http.MethodPost, ratesPath, body), "source")
	body = good()
	body["source"] = string(make([]byte, 51))
	want422At(t, a.Do(t, http.MethodPost, ratesPath, body), "source")
}

func latest(t *testing.T, a *apitest.App, query string) rate {
	t.Helper()
	return apitest.Decode[rate](t, a.Do(t, http.MethodGet, ratesPath+"/latest"+query, nil), http.StatusOK)
}

func TestLatestRate(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	createRate(t, a, map[string]any{"from_currency": "USD", "to_currency": "IDR", "rate": "16500", "as_of_date": "2026-01-10"})
	createRate(t, a, map[string]any{"from_currency": "USD", "to_currency": "SGD", "rate": "1.3", "as_of_date": "2026-01-02"})
	for _, c := range []struct{ query, want string }{
		{"?from_currency=usd", "USD->IDR 2026-01-10 16500.00000000 MANUAL"}, // to_currency defaults to the base
		{"?from_currency=USD&to_currency=idr", "USD->IDR 2026-01-10 16500.00000000 MANUAL"},
		{"?from_currency=USD&on_date=2026-01-10", "USD->IDR 2026-01-10 16500.00000000 MANUAL"},
		{"?from_currency=USD&on_date=2026-01-09", "USD->IDR 2026-01-01 16000.00000000 TEST"},
		{"?from_currency=USD&to_currency=SGD", "USD->SGD 2026-01-02 1.30000000 MANUAL"},
		{"?from_currency=SGD&on_date=2026-03-01", "SGD->IDR 2026-01-01 12000.00000000 TEST"},
	} {
		if got := latest(t, a, c.query); key(got) != c.want {
			t.Errorf("%s: %s, want %s", c.query, key(got), c.want)
		}
	}
	want422At(t, a.Do(t, http.MethodGet, ratesPath+"/latest", nil), "from_currency")
	want422At(t, a.Do(t, http.MethodGet, ratesPath+"/latest?from_currency=US", nil), "from_currency")
	want422At(t, a.Do(t, http.MethodGet, ratesPath+"/latest?from_currency=USD&to_currency=I", nil), "to_currency")
	want422At(t, a.Do(t, http.MethodGet, ratesPath+"/latest?from_currency=USD&on_date=2026-1-9", nil), "on_date")
}

func TestLatestRateNotFound404(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	for _, q := range []string{"?from_currency=EUR", "?from_currency=USD&on_date=2025-12-31", "?from_currency=USD&to_currency=SGD"} {
		wantProblem(t, a.Do(t, http.MethodGet, ratesPath+"/latest"+q, nil), http.StatusNotFound, "fx_rate_none_on_or_before", "no rate found")
	}
}

func TestDeleteRate204ThenGone(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	r := createRate(t, a, map[string]any{"from_currency": "EUR", "to_currency": "IDR", "rate": "17000", "as_of_date": "2026-01-05"})
	rec := a.Do(t, http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, r.RateID), nil)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	wantKeys(t, listRates(t, a, "?from_currency=EUR"))
	wantProblem(t, a.Do(t, http.MethodDelete, fmt.Sprintf("%s/%d", ratesPath, r.RateID), nil), http.StatusNotFound, "fx_rate_not_found", "rate not found")
}

func TestDeleteUnknown404(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	wantProblem(t, a.Do(t, http.MethodDelete, ratesPath+"/999999", nil), http.StatusNotFound, "fx_rate_not_found", "rate not found")
	if rec := a.Do(t, http.MethodDelete, ratesPath+"/abc", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-integer id: %d %s", rec.Code, rec.Body)
	}
}

func TestLiteralPathsNotSwallowed(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	if got := latest(t, a, "?from_currency=USD"); key(got) != "USD->IDR 2026-01-01 16000.00000000 TEST" {
		t.Errorf("GET /latest: %+v", got)
	}
	before := keys(listRates(t, a, ""))
	for _, p := range []string{"/latest", "/sync"} {
		rec := a.Do(t, http.MethodDelete, ratesPath+p, nil)
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("DELETE %s: %d %s, want 405 or 422", p, rec.Code, rec.Body)
		}
	}
	if after := keys(listRates(t, a, "")); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("rates changed:\n%q\n%q", before, after)
	}
}

func TestFXRoutesRequireAdmin(t *testing.T) {
	t.Parallel()
	a := newRoutesApp(t)
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/accounting/base-currency"},
		{http.MethodGet, ratesPath},
		{http.MethodPost, ratesPath},
		{http.MethodPost, ratesPath + "/sync"},
		{http.MethodGet, ratesPath + "/latest?from_currency=USD"},
		{http.MethodDelete, ratesPath + "/1"},
	} {
		wantProblem(t, a.Anon(t, req.method, req.path, nil), http.StatusUnauthorized, "", "not authenticated")
	}
	wantKeys(t, listRates(t, a, "?from_currency=IDR"), "IDR->IDR 2026-11-08 1.00000000 SEED")
}
