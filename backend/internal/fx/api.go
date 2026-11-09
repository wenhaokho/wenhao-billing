package fx

import (
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
