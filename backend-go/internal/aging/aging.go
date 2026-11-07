// Package aging calculates aging from authorized open balances.
package aging

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

type Record struct {
	PartyID, PartyName, Currency string
	// DocumentDate is the issue date (AR) or bill date (AP). DueDate may be nil.
	DocumentDate time.Time
	DueDate      *time.Time
	Balance      decimal.Decimal
}
type Buckets struct{ Current, D1To30, D31To60, D61To90, D90Plus, Total decimal.Decimal }
type Row struct {
	PartyID, PartyName, Currency string
	Buckets                      Buckets
}
type Report struct {
	AsOf             time.Time
	Kind             string
	Rows             []Row
	TotalsByCurrency map[string]Buckets
}

func day(t time.Time) time.Time { y, m, d := t.Date(); return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
func currency(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) == 3 && !strings.ContainsRune(s, '\x00')
}

func add(b *Buckets, days int, v decimal.Decimal) {
	switch {
	case days <= 0:
		b.Current = b.Current.Add(v)
	case days <= 30:
		b.D1To30 = b.D1To30.Add(v)
	case days <= 60:
		b.D31To60 = b.D31To60.Add(v)
	case days <= 90:
		b.D61To90 = b.D61To90.Add(v)
	default:
		b.D90Plus = b.D90Plus.Add(v)
	}
	b.Total = b.Total.Add(v)
}

// Calculate does not fetch data. The caller must authorize and scope every record,
// provide exactly one row per document, choose AR OPEN/SENT/PARTIAL excluding
// templates or AP OPEN/PARTIAL, and reconstruct both lifecycle status and balance
// as of the cutoff (current values cannot establish historical aging). Document dates after
// the cutoff are excluded. Missing due dates are current, as in the Python report.
// Party IDs and names must be nonempty and consistent, and document dates must
// be present. Python permits nullable AR customers and invoice or bill dates and uses a
// fallback party label. This calculation fails closed on those inputs, so it is
// not endpoint-compatible until the nullable and snapshot policies are resolved.
// Currency keys preserve their exact case, as in Python. A separate DTO is needed
// to reproduce the Python JSON wire format.
// The caller must explicitly attest that the supplied balances represent the
// requested cutoff. Python exposes no FX conversion; totals remain per currency.
func Calculate(asOf time.Time, kind string, snapshotVerified bool, records []Record) (Report, error) {
	if !snapshotVerified {
		return Report{}, fmt.Errorf("unverified aging snapshot")
	}
	return calculate(asOf, kind, records, false)
}

// CalculateCurrent buckets today's open balances. An undated document can
// contribute because its current status and balance are known. The caller
// supplies today's date and must use one authorized row per document. A
// missing party must use a stable fallback ID and label.
func CalculateCurrent(today time.Time, kind string, records []Record) (Report, error) {
	return calculate(today, kind, records, true)
}

func calculate(asOf time.Time, kind string, records []Record, allowUndated bool) (Report, error) {
	if asOf.IsZero() || (kind != "AR" && kind != "AP") {
		return Report{}, fmt.Errorf("invalid report parameters")
	}
	asOf = day(asOf)
	out := Report{AsOf: asOf, Kind: kind, Rows: []Row{}, TotalsByCurrency: map[string]Buckets{}}
	type key struct{ id, currency string }
	groups := map[key]*Row{}
	names := map[string]string{}
	for i, r := range records {
		if r.DocumentDate.IsZero() && !allowUndated {
			return Report{}, fmt.Errorf("record %d: missing document date", i)
		}
		if day(r.DocumentDate).After(asOf) {
			continue
		}
		if strings.TrimSpace(r.PartyID) == "" || r.PartyName == "" || !currency(r.Currency) || !r.Balance.IsPositive() || r.Balance.Exponent() < -4 || r.Balance.Abs().GreaterThan(decimal.RequireFromString("999999999999999.9999")) || (r.DueDate != nil && r.DueDate.IsZero()) {
			return Report{}, fmt.Errorf("record %d: invalid fields", i)
		}
		if name, ok := names[r.PartyID]; ok && name != r.PartyName {
			return Report{}, fmt.Errorf("conflicting party name for %s", r.PartyID)
		}
		names[r.PartyID] = r.PartyName
		k := key{r.PartyID, r.Currency}
		row := groups[k]
		if row == nil {
			row = &Row{PartyID: r.PartyID, PartyName: r.PartyName, Currency: r.Currency}
			groups[k] = row
		}
		days := 0
		if r.DueDate != nil {
			days = int((asOf.Unix() - day(*r.DueDate).Unix()) / 86400)
		}
		add(&row.Buckets, days, r.Balance)
		b := out.TotalsByCurrency[r.Currency]
		add(&b, days, r.Balance)
		out.TotalsByCurrency[r.Currency] = b
	}
	for _, row := range groups {
		out.Rows = append(out.Rows, *row)
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if strings.ToLower(a.PartyName) != strings.ToLower(b.PartyName) {
			return strings.ToLower(a.PartyName) < strings.ToLower(b.PartyName)
		}
		if a.Currency != b.Currency {
			return a.Currency < b.Currency
		}
		return a.PartyID < b.PartyID
	})

	return out, nil
}
