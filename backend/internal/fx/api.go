package fx

import (
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// RateOut is a stored FX rate, as every rate route answers (Python's FxRateOut).
type RateOut struct {
	RateID       int64         `json:"rate_id"`
	FromCurrency string        `json:"from_currency"`
	ToCurrency   string        `json:"to_currency"`
	Rate         platform.Dec8 `json:"rate"`
	AsOfDate     platform.Date `json:"as_of_date"`
	Source       string        `json:"source"`
	CreatedAt    platform.Time `json:"created_at"`
}

func rateOut(r fxdb.FxRate) RateOut {
	return RateOut{
		RateID: r.RateID, FromCurrency: r.FromCurrency, ToCurrency: r.ToCurrency,
		Rate: platform.NewDec8(r.Rate), AsOfDate: platform.Date{Time: r.AsOfDate.Time},
		Source: r.Source, CreatedAt: platform.Time{Time: r.CreatedAt},
	}
}

// RateCreate adds a rate by hand (Python's FxRateCreate). The currencies are
// stored upper-cased; rate must be above zero; source defaults to MANUAL and
// may be left out but not sent as null.
type RateCreate struct {
	FromCurrency string                   `json:"from_currency" minLength:"3" maxLength:"3"`
	ToCurrency   string                   `json:"to_currency" minLength:"3" maxLength:"3"`
	Rate         platform.Dec8            `json:"rate" doc:"Greater than 0."`
	AsOfDate     platform.Date            `json:"as_of_date"`
	Source       platform.NotNull[string] `json:"source" minLength:"1" maxLength:"50" doc:"Defaults to MANUAL."`
}

// SyncResult is what a provider sync did (Python's FxSyncResult): rows
// created and updated, pairs fetched, and the source written.
type SyncResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Fetched int      `json:"fetched"`
	Pairs   []string `json:"pairs"`
	Source  string   `json:"source"`
}

// BaseCurrencyOut is the ledger's functional currency.
type BaseCurrencyOut struct {
	BaseCurrency string `json:"base_currency"`
}

// SummaryOut is the FX Rates page's cards: each currency's newest rate to the
// base currency, and the last successful provider sync (null before any).
type SummaryOut struct {
	BaseCurrency string            `json:"base_currency"`
	Currencies   []CurrencySummary `json:"currencies" nullable:"false" doc:"One entry per currency with any rate to the base currency, sorted by currency."`
	LastSync     *LastSyncOut      `json:"last_sync" doc:"The newest successful provider sync; null when none has run."`
}

// CurrencySummary is one currency's newest rate to the base currency and the
// rate of the newest earlier date.
type CurrencySummary struct {
	Currency     string         `json:"currency"`
	LatestRate   platform.Dec8  `json:"latest_rate"`
	LatestAsOf   platform.Date  `json:"latest_as_of"`
	PreviousRate *platform.Dec8 `json:"previous_rate" doc:"The rate of the newest earlier date; null when there is none."`
	PreviousAsOf *platform.Date `json:"previous_as_of"`
	//nolint:forbidigo // a percentage change for display, not money
	ChangePct *float64 `json:"change_pct" doc:"(latest - previous) / previous x 100, rounded to 4 places; null when there is no previous rate."`
}

// LastSyncOut is a recorded provider sync: when, SCHEDULED or MANUAL, and the
// provider.
type LastSyncOut struct {
	RanAt   platform.Time `json:"ran_at"`
	Trigger string        `json:"trigger" enum:"SCHEDULED,MANUAL"`
	Source  string        `json:"source"`
}

var hundred = decimal.NewFromInt(100)

func currencySummary(r fxdb.RateSummaryRow) CurrencySummary {
	out := CurrencySummary{
		Currency: r.Currency, LatestRate: platform.NewDec8(r.LatestRate),
		LatestAsOf: platform.Date{Time: r.LatestAsOf.Time},
	}
	if r.PreviousRate.Valid && r.PreviousAsOf.Valid {
		prev := platform.NewDec8(r.PreviousRate.Decimal)
		out.PreviousRate = &prev
		out.PreviousAsOf = &platform.Date{Time: r.PreviousAsOf.Time}
		if !r.PreviousRate.Decimal.IsZero() {
			pct := r.LatestRate.Sub(r.PreviousRate.Decimal).Mul(hundred).Div(r.PreviousRate.Decimal).Round(4).InexactFloat64()
			out.ChangePct = &pct
		}
	}
	return out
}
