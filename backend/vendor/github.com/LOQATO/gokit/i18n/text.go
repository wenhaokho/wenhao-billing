package i18n

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Text is a sentence not yet in a language: its English template, with
// {name} placeholders ({{ and }} for literal braces), and its parameters.
type Text struct {
	Template string
	Params   map[string]any
}

// T builds a Text from a template and name/value pairs:
// T("Unit {code} already exists", "code", unit.Code). It panics on an odd
// number of pairs or a name that is not a string: both are programming
// errors at the call site, never data.
func T(template string, kv ...any) Text {
	if len(kv)%2 != 0 {
		panic(fmt.Sprintf("i18n.T(%q): odd number of name/value arguments", template))
	}
	params := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		name, ok := kv[i].(string)
		if !ok {
			panic(fmt.Sprintf("i18n.T(%q): parameter name %v is not a string", template, kv[i]))
		}
		params[name] = kv[i+1]
	}
	return Text{Template: template, Params: params}
}

// String is the English sentence.
func (t Text) String() string { return t.Render(English, nil) }

// Parameter kinds a Text renders per language. A plain string is data and is
// never translated; int-like values are printed as they are.
type (
	// Date is a calendar day: "10 Oct 2026" / "10 Okt 2026".
	Date struct{ time.Time }
	// Month is a month named in full: "October 2026" / "Oktober 2026".
	Month struct{ time.Time }
	// ShortMonth is a month named short: "Oct 2026" / "Okt 2026".
	ShortMonth struct{ time.Time }
	// MonthName is a month's short name with no year: "Oct" / "Okt".
	MonthName struct{ time.Time }
	// Money is an amount, written by money.Format in both languages.
	Money struct {
		Amount   decimal.Decimal
		Currency string
	}
	// Word is a stored value read as words: "IN_PROGRESS" -> "In progress",
	// in Indonesian from Catalog.Words.
	Word string
	// Noun is a type's name inside a sentence ("Contact not found"):
	// English prints it as it is, Indonesian from Catalog.Nouns. Not storable.
	Noun string
	// Joined is several parts in a row with Sep between; a Text part is
	// rendered, a string part is data.
	Joined struct {
		Items []any
		Sep   string
	}
)

// Catalog is the Indonesian for a project: sentences keyed by their English
// template, stored values as words, and type names as nouns.
type Catalog struct {
	Sentences map[string]string
	Words     map[string]string
	Nouns     map[string]string
}
