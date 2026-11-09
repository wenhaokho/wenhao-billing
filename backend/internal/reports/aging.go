package reports

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// AgingBuckets is an amount split by days past due.
type AgingBuckets struct {
	Current platform.Dec4 `json:"current"`
	D1To30  platform.Dec4 `json:"d_1_30"`
	D31To60 platform.Dec4 `json:"d_31_60"`
	D61To90 platform.Dec4 `json:"d_61_90"`
	D90Plus platform.Dec4 `json:"d_90_plus"`
	Total   platform.Dec4 `json:"total"`
}

// AgingRow is one party in one currency.
type AgingRow struct {
	PartyID   string       `json:"party_id"`
	PartyName string       `json:"party_name"`
	Currency  string       `json:"currency"`
	Buckets   AgingBuckets `json:"buckets"`
}

// AgingReport is open AR (kind "AR") or AP (kind "AP") as of a date. Its
// totals_by_currency is a JSON object whose keys encoding/json writes sorted,
// which is currency order.
type AgingReport struct {
	AsOf             platform.Date           `json:"as_of"`
	Kind             string                  `json:"kind"`
	Rows             []AgingRow              `json:"rows"`
	TotalsByCurrency map[string]AgingBuckets `json:"totals_by_currency"`
}

// buckets accumulates exact decimals; it is converted to AgingBuckets once.
type buckets struct{ current, d30, d60, d90, d90plus, total decimal.Decimal }

func (b *buckets) add(days int, amount decimal.Decimal) {
	switch {
	case days <= 0:
		b.current = b.current.Add(amount)
	case days <= 30:
		b.d30 = b.d30.Add(amount)
	case days <= 60:
		b.d60 = b.d60.Add(amount)
	case days <= 90:
		b.d90 = b.d90.Add(amount)
	default:
		b.d90plus = b.d90plus.Add(amount)
	}
	b.total = b.total.Add(amount)
}

func (b buckets) out() AgingBuckets {
	return AgingBuckets{
		Current: platform.NewDec4(b.current), D1To30: platform.NewDec4(b.d30), D31To60: platform.NewDec4(b.d60),
		D61To90: platform.NewDec4(b.d90), D90Plus: platform.NewDec4(b.d90plus), Total: platform.NewDec4(b.total),
	}
}

// agingItem is one open document, whichever table it came from.
type agingItem struct {
	partyID  string // "-" when the document has no party
	name     *string
	currency string
	due      pgtype.Date
	balance  decimal.Decimal
}

// daysPastDue is as_of minus the due date in calendar days; a null due date
// is 0, which is current.
func daysPastDue(asOf time.Time, due pgtype.Date) int {
	if !due.Valid {
		return 0
	}
	a := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)
	d := time.Date(due.Time.Year(), due.Time.Month(), due.Time.Day(), 0, 0, 0, 0, time.UTC)
	return int(a.Sub(d) / (24 * time.Hour))
}

// aging groups items by (party, currency) and totals them by currency. Rows
// sort by lower-cased party name, currency, then party id, so equal names
// never depend on query order. noParty is the name of a party-less row.
func aging(kind string, asOf time.Time, noParty string, items []agingItem) AgingReport {
	type key struct{ party, currency string }
	rows := map[key]*AgingRow{}
	acc := map[key]*buckets{}
	totals := map[string]*buckets{}
	for _, it := range items {
		k := key{it.partyID, it.currency}
		if rows[k] == nil {
			name := noParty
			if it.name != nil && *it.name != "" {
				name = *it.name
			}
			rows[k] = &AgingRow{PartyID: it.partyID, PartyName: name, Currency: it.currency}
			acc[k] = &buckets{}
		}
		if totals[it.currency] == nil {
			totals[it.currency] = &buckets{}
		}
		days := daysPastDue(asOf, it.due)
		acc[k].add(days, it.balance)
		totals[it.currency].add(days, it.balance)
	}
	out := AgingReport{
		AsOf: platform.Date{Time: asOf}, Kind: kind,
		Rows: make([]AgingRow, 0, len(rows)), TotalsByCurrency: map[string]AgingBuckets{},
	}
	lower := cases.Lower(language.Und) // built per call: a Caser is not safe to share
	type sortable struct {
		row   AgingRow
		lower string
	}
	list := make([]sortable, 0, len(rows))
	for k, r := range rows {
		r.Buckets = acc[k].out()
		list = append(list, sortable{row: *r, lower: lower.String(r.PartyName)})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.lower != b.lower {
			return a.lower < b.lower
		}
		if a.row.Currency != b.row.Currency {
			return a.row.Currency < b.row.Currency
		}
		return a.row.PartyID < b.row.PartyID
	})
	for _, s := range list {
		out.Rows = append(out.Rows, s.row)
	}
	for c, b := range totals {
		out.TotalsByCurrency[c] = b.out()
	}
	return out
}

type agingOut struct{ Body AgingReport }

func (r routes) arAging(ctx context.Context, in *asOfIn) (*agingOut, error) {
	asOf := r.asOf(in)
	rows, err := r.q.ArAgingRows(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]agingItem, len(rows))
	for i, x := range rows {
		id := "-"
		if x.CustomerID.Valid {
			id = uuid.UUID(x.CustomerID.Bytes).String()
		}
		items[i] = agingItem{partyID: id, name: x.PartyName, currency: x.Currency, due: x.DueDate, balance: x.BalanceDue}
	}
	return &agingOut{Body: aging("AR", asOf, "(no customer)", items)}, nil
}

func (r routes) apAging(ctx context.Context, in *asOfIn) (*agingOut, error) {
	asOf := r.asOf(in)
	rows, err := r.q.ApAgingRows(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]agingItem, len(rows))
	for i, x := range rows {
		items[i] = agingItem{partyID: x.VendorID.String(), name: x.PartyName, currency: x.Currency, due: x.DueDate, balance: x.BalanceDue}
	}
	return &agingOut{Body: aging("AP", asOf, "(no vendor)", items)}, nil
}
