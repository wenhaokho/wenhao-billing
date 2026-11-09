package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Source is the source a provider rate is stored with.
const Source = "FRANKFURTER"

// ErrProvider is matched (errors.Is) by the error Sync returns when the
// provider fails; its text is Python's FxProviderError sentence.
var ErrProvider = errors.New("fx provider error")

type providerError struct{ msg string }

func (e providerError) Error() string        { return e.msg }
func (e providerError) Is(target error) bool { return target == ErrProvider }

func providerErr(format string, args ...any) error {
	return providerError{fmt.Sprintf(format, args...)}
}

// NewClient is the provider's HTTP client: cfg's timeout, and redirects not
// followed (httpx's default), so a 3xx is a "Redirect response" error.
func NewClient(cfg platform.Config) *http.Client {
	return &http.Client{
		Timeout: cfg.FXProviderTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// fetched is one provider rate (Python's FetchedRate).
type fetched struct {
	from, to string
	rate     decimal.Decimal
	asOf     time.Time
}

// maxBody bounds what is read of one provider answer (a real one is ~100 bytes).
const maxBody = 1 << 20

// maxRate is the first value numeric(19,8) cannot hold (10^11).
var maxRate = decimal.New(1, 11)

// fetchLatest is Python's fetch_latest_rates: the latest published
// source->target rate for each source currency, upper-cased, skipping the
// target itself and any source the provider has no rate for. Every pair is
// fetched before anything is returned, so a failure writes nothing.
func fetchLatest(ctx context.Context, client *http.Client, cfg platform.Config, sources []string, target string) ([]fetched, error) {
	target = strings.ToUpper(target)
	base := strings.TrimRight(cfg.FXProviderBaseURL, "/")
	var out []fetched
	for _, raw := range sources {
		src := strings.ToUpper(raw)
		if src == target {
			continue
		}
		f, ok, err := fetchPair(ctx, client, cfg.FXProviderTimeout, base, src, target)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, f)
		}
	}
	return out, nil
}

// answer is the part of Frankfurter's /latest body that is read. The rate is
// kept as its JSON literal so it never passes through float64.
type answer struct {
	Date  json.RawMessage            `json:"date"`
	Rates map[string]json.RawMessage `json:"rates"`
}

// fetchPair asks for one src->target rate; ok is false when the provider
// has none.
func fetchPair(ctx context.Context, client *http.Client, timeout time.Duration, base, src, target string) (fetched, bool, error) {
	pair := src + "->" + target
	body, err := get(ctx, client, timeout, base+"/latest?"+url.Values{"base": {src}, "symbols": {target}}.Encode())
	if err != nil {
		return fetched{}, false, providerErr("fx provider request failed for %s: %v", pair, err)
	}
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return fetched{}, false, providerErr("fx provider returned invalid JSON for %s: %v", pair, err)
	}
	value, ok := a.Rates[target]
	if !ok || isNull(value) {
		return fetched{}, false, nil // the provider does not cover this pair
	}
	asOf, err := parseDate(a.Date)
	if err != nil {
		return fetched{}, false, providerErr("fx provider returned invalid date for %s: %s", pair, platform.PyJSONRepr(a.Date))
	}
	rate, err := decimal.NewFromString(string(value))
	// fx_rates.rate is numeric(19,8): Postgres rounds to 8 places and then
	// holds at most 11 integer digits. A rate that is not positive, or does
	// not fit, is the provider's fault: refused before anything is written.
	if err != nil || rate.Round(8).Sign() <= 0 || rate.Round(8).Cmp(maxRate) >= 0 {
		return fetched{}, false, providerErr("fx provider returned invalid rate for %s: %s", pair, value)
	}
	return fetched{from: src, to: target, rate: rate, asOf: asOf}, true, nil
}

// get is the body of a GET answered 2xx; anything else is an error whose
// text is httpx's first line for a status error.
func get(ctx context.Context, client *http.Client, timeout time.Duration, u string) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		kind := "Server error"
		switch {
		case resp.StatusCode < 200:
			kind = "Informational response"
		case resp.StatusCode < 400:
			kind = "Redirect response"
		case resp.StatusCode < 500:
			kind = "Client error"
		}
		return nil, fmt.Errorf("%s '%s' for url '%s'", kind, resp.Status, u)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

func isNull(raw json.RawMessage) bool { return len(raw) == 0 || string(raw) == "null" }

// parseDate reads a "YYYY-MM-DD" JSON string.
func parseDate(raw json.RawMessage) (time.Time, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, errors.New("not a string")
	}
	return time.Parse(time.DateOnly, s)
}
