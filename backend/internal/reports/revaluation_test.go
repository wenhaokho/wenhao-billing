package reports_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
)

const fxPath = "/api/v1/accounting/reports/fx-revaluation"

type fxLine struct {
	AccountID          int32   `json:"account_id"`
	Code               string  `json:"code"`
	Type               string  `json:"type"`
	Currency           string  `json:"currency"`
	FaceBalance        string  `json:"face_balance"`
	OriginalBase       string  `json:"original_base"`
	RevaluedBase       *string `json:"revalued_base"`
	Rate               *string `json:"rate"`
	RateAsOf           *string `json:"rate_as_of"`
	UnrealisedGainLoss *string `json:"unrealised_gain_loss"`
	RateMissing        bool    `json:"rate_missing"`
}

type fxReport struct {
	AsOf                string   `json:"as_of"`
	BaseCurrency        string   `json:"base_currency"`
	Lines               []fxLine `json:"lines"`
	TotalUnrealisedGain string   `json:"total_unrealised_gain"`
	TotalUnrealisedLoss string   `json:"total_unrealised_loss"`
	MissingRates        []string `json:"missing_rates"`
}

func fxGet(t *testing.T, a *apitest.App, query string) (fxReport, string) {
	t.Helper()
	rec := a.Do(t, http.MethodGet, fxPath+query, nil)
	return apitest.Decode[fxReport](t, rec, http.StatusOK), rec.Body.String()
}

func str(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

func TestRevaluationAssetGain(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// USD 100 booked at 15000 (1,500,000); the 16000 rate revalues it to 1,600,000.
	post(t, a, day(2026, 1, 5), dr(bank, "100", "USD", "1500000"), cr(income, "100", "USD", "1500000"))
	got, _ := fxGet(t, a, "?as_of=2026-01-10")
	if got.AsOf != "2026-01-10" || got.BaseCurrency != "IDR" || len(got.Lines) != 1 {
		t.Fatalf("got %+v", got)
	}
	l := got.Lines[0]
	if l.Code != "1000" || l.Type != "ASSET" || l.Currency != "USD" || l.FaceBalance != "100.0000" || l.OriginalBase != "1500000.0000" ||
		str(l.RevaluedBase) != "1600000.0000" || str(l.Rate) != "16000.00000000" || str(l.RateAsOf) != "2026-01-01" ||
		str(l.UnrealisedGainLoss) != "100000.0000" || l.RateMissing {
		t.Errorf("line %+v", l)
	}
	if got.TotalUnrealisedGain != "100000.0000" || got.TotalUnrealisedLoss != "0.0000" || len(got.MissingRates) != 0 {
		t.Errorf("totals %s / %s / %v", got.TotalUnrealisedGain, got.TotalUnrealisedLoss, got.MissingRates)
	}
}

func TestRevaluationLiabilityLoss(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// A USD 100 payable booked at 15000 now costs 1,600,000: a loss.
	post(t, a, day(2026, 1, 5), dr(expense, "100", "USD", "1500000"), cr(payable, "100", "USD", "1500000"))
	got, _ := fxGet(t, a, "?as_of=2026-01-10")
	if len(got.Lines) != 1 {
		t.Fatalf("got %+v", got)
	}
	l := got.Lines[0]
	if l.Type != "LIABILITY" || l.FaceBalance != "100.0000" || l.OriginalBase != "1500000.0000" ||
		str(l.RevaluedBase) != "1600000.0000" || str(l.UnrealisedGainLoss) != "-100000.0000" {
		t.Errorf("line %+v", l)
	}
	if got.TotalUnrealisedGain != "0.0000" || got.TotalUnrealisedLoss != "100000.0000" {
		t.Errorf("totals %s / %s", got.TotalUnrealisedGain, got.TotalUnrealisedLoss)
	}
}

func TestRevaluationUnchangedLiabilityIsZero(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5), dr(expense, "100", "USD", "1600000"), cr(payable, "100", "USD", "1600000"))
	got, raw := fxGet(t, a, "?as_of=2026-01-10")
	if len(got.Lines) != 1 || str(got.Lines[0].UnrealisedGainLoss) != "0.0000" {
		t.Fatalf("got %+v", got.Lines)
	}
	if strings.Contains(raw, "-0.0000") || got.TotalUnrealisedGain != "0.0000" || got.TotalUnrealisedLoss != "0.0000" {
		t.Errorf("negative or bare zero in %s", raw)
	}
}

func TestRevaluationMissingRate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5), dr(bank, "10", "EUR", "100"), cr(income, "10", "EUR", "100"))
	post(t, a, day(2026, 1, 5), dr(receivable, "20", "EUR", "200"), cr(income, "20", "EUR", "200"))
	// AUD sits on a later account code, so the rows do not arrive in currency order.
	post(t, a, day(2026, 1, 5), dr(expense, "5", "AUD", "50"), cr(payable, "5", "AUD", "50"))
	for _, c := range []string{"CHF", "GBP", "JPY"} {
		post(t, a, day(2026, 1, 5), dr(bank, "5", c, "50"), cr(income, "5", c, "50"))
	}
	post(t, a, day(2026, 1, 5), dr(bank, "100", "USD", "1500000"), cr(income, "100", "USD", "1500000"))
	got, raw := fxGet(t, a, "?as_of=2026-01-10")
	if len(got.Lines) != 7 {
		t.Fatalf("%d lines: %+v", len(got.Lines), got.Lines)
	}
	if strings.Join(got.MissingRates, ",") != "AUD,CHF,EUR,GBP,JPY" {
		t.Errorf("missing_rates %v, want [AUD CHF EUR GBP JPY]", got.MissingRates)
	}
	for _, l := range got.Lines {
		if l.Currency == "USD" {
			if l.RateMissing || l.RevaluedBase == nil {
				t.Errorf("USD line %+v", l)
			}
			continue
		}
		if !l.RateMissing || l.RevaluedBase != nil || l.Rate != nil || l.RateAsOf != nil || l.UnrealisedGainLoss != nil {
			t.Errorf("missing-rate line %+v", l)
		}
	}
	for _, f := range []string{`"revalued_base":null`, `"rate":null`, `"rate_as_of":null`, `"unrealised_gain_loss":null`} {
		if !strings.Contains(raw, f) {
			t.Errorf("no %s in %s", f, raw)
		}
	}
	// The missing buckets add nothing to the totals.
	if got.TotalUnrealisedGain != "100000.0000" || got.TotalUnrealisedLoss != "0.0000" {
		t.Errorf("totals %s / %s", got.TotalUnrealisedGain, got.TotalUnrealisedLoss)
	}
}

func TestRevaluationSkipsBaseCurrencyAndIncomeAccounts(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// A base-currency asset; USD on INCOME, EXPENSE and EQUITY accounts: none is revalued.
	post(t, a, day(2026, 1, 5), dr(bank, "500", "IDR", "500"), cr(income, "100", "USD", "1500000"))
	post(t, a, day(2026, 1, 5), dr(expense, "100", "USD", "1500000"), cr(equity, "100", "USD", "1500000"))
	got, raw := fxGet(t, a, "?as_of=2026-01-10")
	if len(got.Lines) != 0 || !strings.Contains(raw, `"lines":[]`) || !strings.Contains(raw, `"missing_rates":[]`) {
		t.Errorf("got %s", raw)
	}
	if got.TotalUnrealisedGain != "0.0000" || got.TotalUnrealisedLoss != "0.0000" {
		t.Errorf("totals %s / %s", got.TotalUnrealisedGain, got.TotalUnrealisedLoss)
	}
}

func TestRevaluationSkipsSettledBucket(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, day(2026, 1, 5), dr(bank, "100", "USD", "1500000"), cr(income, "100", "USD", "1500000"))
	post(t, a, day(2026, 1, 6), cr(bank, "100", "USD", "1500000"), dr(expense, "100", "USD", "1500000"))
	got, _ := fxGet(t, a, "?as_of=2026-01-10")
	if len(got.Lines) != 0 {
		t.Errorf("settled bucket listed: %+v", got.Lines)
	}
}

func TestRevaluationResidueBucketKept(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// Face nets to 0 but the base carries a 100,000 residue: kept, revalued to 0.
	post(t, a, day(2026, 1, 5), dr(bank, "100", "USD", "1600000"), cr(income, "100", "USD", "1600000"))
	post(t, a, day(2026, 1, 6), cr(bank, "100", "USD", "1500000"), dr(expense, "100", "USD", "1500000"))
	got, _ := fxGet(t, a, "?as_of=2026-01-10")
	if len(got.Lines) != 1 {
		t.Fatalf("got %+v", got.Lines)
	}
	l := got.Lines[0]
	if l.FaceBalance != "0.0000" || l.OriginalBase != "100000.0000" || str(l.RevaluedBase) != "0.0000" || str(l.UnrealisedGainLoss) != "-100000.0000" {
		t.Errorf("line %+v", l)
	}
	if got.TotalUnrealisedLoss != "100000.0000" || got.TotalUnrealisedGain != "0.0000" {
		t.Errorf("totals %s / %s", got.TotalUnrealisedGain, got.TotalUnrealisedLoss)
	}
}

func TestRevaluationRoundsHalfEven(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	exec(t, a, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('EUR', 'IDR', 1.00005, '2026-01-01', 'TEST')`)
	// 1 x 1.00005 = 1.00005: the tie goes to the even digit, 1.0000 (half-up would give 1.0001).
	post(t, a, day(2026, 1, 5), dr(bank, "1", "EUR", "1"), cr(income, "1", "EUR", "1"))
	got, _ := fxGet(t, a, "?as_of=2026-01-10")
	if len(got.Lines) != 1 || str(got.Lines[0].RevaluedBase) != "1.0000" || str(got.Lines[0].Rate) != "1.00005000" {
		t.Fatalf("got %+v", got.Lines)
	}
	if str(got.Lines[0].UnrealisedGainLoss) != "0.0000" {
		t.Errorf("unrealised %s", str(got.Lines[0].UnrealisedGainLoss))
	}
}

func TestRevaluationUsesNewestRateOnOrBefore(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	exec(t, a, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source)
VALUES ('USD', 'IDR', 17000, '2026-02-01', 'TEST'), ('USD', 'IDR', 18000, '2026-03-01', 'TEST')`)
	post(t, a, day(2026, 1, 5), dr(bank, "100", "USD", "1500000"), cr(income, "100", "USD", "1500000"))
	for _, c := range []struct{ asOf, rate, date, revalued string }{
		{"2026-01-31", "16000.00000000", "2026-01-01", "1600000.0000"},
		{"2026-02-01", "17000.00000000", "2026-02-01", "1700000.0000"},
		{"2026-02-15", "17000.00000000", "2026-02-01", "1700000.0000"},
		{"2026-03-05", "18000.00000000", "2026-03-01", "1800000.0000"},
	} {
		got, _ := fxGet(t, a, "?as_of="+c.asOf)
		if len(got.Lines) != 1 || str(got.Lines[0].Rate) != c.rate || str(got.Lines[0].RateAsOf) != c.date || str(got.Lines[0].RevaluedBase) != c.revalued {
			t.Errorf("as_of %s: %+v", c.asOf, got.Lines)
		}
	}
}

func TestRevaluationCutoffInclusiveOfDay(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	post(t, a, time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC), dr(bank, "10", "USD", "150000"), cr(income, "10", "USD", "150000"))
	post(t, a, time.Date(2026, 1, 10, 23, 59, 59, 999999000, time.UTC), dr(bank, "20", "USD", "300000"), cr(income, "20", "USD", "300000"))
	post(t, a, time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC), dr(bank, "1000", "USD", "15000000"), cr(income, "1000", "USD", "15000000"))
	got, _ := fxGet(t, a, "?as_of=2026-01-10")
	if len(got.Lines) != 1 || got.Lines[0].FaceBalance != "30.0000" || got.Lines[0].OriginalBase != "450000.0000" {
		t.Errorf("as_of day: %+v", got.Lines)
	}
}
