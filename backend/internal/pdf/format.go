// Package pdf holds what a billing document (invoice, quotation or payment
// receipt) shows: the display formatters, the one render-data struct and the
// loaders that fill it. The renderer is separate. It must not import
// payments: payments imports this package.
package pdf

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
)

// Dash is what a missing value prints as.
const Dash = "—"

// Minus is the sign the PDF puts before a deduction (U+2212, as Python's
// HTML context did); email bodies keep an ASCII '-'.
const Minus = "−"

// zeroDecimal are the currencies with no minor unit (ISO 4217), as Python's
// _ZERO_DECIMAL_CURRENCIES.
var zeroDecimal = map[string]bool{
	"IDR": true, "JPY": true, "KRW": true, "VND": true, "CLP": true, "ISK": true, "HUF": true,
	"PYG": true, "RWF": true, "UGX": true, "XAF": true, "XOF": true, "XPF": true,
}

// CurrencyDecimals is how many places an amount of the currency shows: 0 for
// a zero-decimal currency (compared upper-cased), else 2.
func CurrencyDecimals(currency string) int32 {
	if zeroDecimal[strings.ToUpper(currency)] {
		return 0
	}
	return 2
}

// group is the amount rounded half-even (Python's quantize in its default
// context) to places, thousands grouped by commas. A negative amount keeps
// its minus even when it rounds to zero ("-0"), as Python's Decimal does.
func group(amount decimal.Decimal, places int32) string {
	s := amount.RoundBank(places).Abs().StringFixed(places)
	whole, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	if amount.IsNegative() {
		b.WriteByte('-')
	}
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if hasFrac {
		b.WriteString("." + frac)
	}
	return b.String()
}

// FmtAmount is Python's _fmt_amount (invoice_pdf.py): the amount grouped and
// rounded half-even to the currency's places, then a space and the currency
// as given.
func FmtAmount(amount decimal.Decimal, currency string) string {
	return fmt.Sprintf("%s %s", group(amount, CurrencyDecimals(currency)), currency)
}

// FmtNum is Python's _fmt_num: FmtAmount without the currency suffix (a
// document states its currency once). An empty currency shows 2 places.
func FmtNum(v decimal.Decimal, currency string) string {
	return group(v, CurrencyDecimals(currency))
}

// FmtNumN is FmtNum of a nullable amount: Dash for NULL.
func FmtNumN(v decimal.NullDecimal, currency string) string {
	if !v.Valid {
		return Dash
	}
	return FmtNum(v.Decimal, currency)
}

// FmtQty is Python's _fmt_qty: a quantity without trailing zeros (6.0000 is
// "6", 6.5000 is "6.5", 600.0000 is "600").
func FmtQty(v decimal.Decimal) string { return v.String() }

// FmtPct is Python's _fmt_pct: a percentage without trailing zeros (5.0000
// is "5", 7.5000 is "7.5"). A NULL rate counts as 0.
func FmtPct(v decimal.NullDecimal) string { return v.Decimal.String() }

// DiscountAmount is Python's _discount_amount: the discount as money, half-even
// to 0.01. ok is false when there is no discount: no type, or a missing or
// zero value. PERCENT is a share of the subtotal (a missing subtotal counts
// as 0); any other type is the value itself.
func DiscountAmount(typ *string, value, subtotal decimal.NullDecimal) (amount decimal.Decimal, ok bool) {
	if typ == nil || *typ == "" || !value.Valid || value.Decimal.IsZero() {
		return decimal.Zero, false
	}
	if *typ == "PERCENT" {
		return subtotal.Decimal.Mul(value.Decimal).Shift(-2).RoundBank(2), true
	}
	return value.Decimal.RoundBank(2), true
}

// NPWPLine is Python's _npwp_line: "NPWP <number> a.n. <taxpayer>" under the
// payment instructions. Only an invoice with withholding shows it, and only
// when the business has an NPWP. "" when there is none.
func NPWPLine(whtType string, business *businessdb.BusinessProfile) string {
	if whtType == "" || whtType == "NONE" || business == nil {
		return ""
	}
	npwp := strings.TrimSpace(deref(business.Npwp))
	if npwp == "" {
		return ""
	}
	if name := strings.TrimSpace(deref(business.NpwpName)); name != "" {
		return "NPWP " + npwp + " a.n. " + name
	}
	return "NPWP " + npwp
}

// FmtDate is Python's _fmt_date: "23 Jul 2026", or Dash.
func FmtDate(d pgtype.Date) string {
	if !d.Valid {
		return Dash
	}
	return d.Time.Format("02 Jan 2006")
}

var statusBadge = map[string]string{
	// invoices
	"DRAFT": "draft", "OPEN": "sent", "SENT": "sent", "PAID": "paid",
	"PARTIALLY_PAID": "partial", "PARTIAL": "partial",
	"AWAITING_FINALIZATION": "partial", "OVERDUE": "overdue", "VOID": "draft",
	// quotations
	"ACCEPTED": "paid", "DECLINED": "overdue", "EXPIRED": "overdue",
	"INVOICED": "sent",
}

// StatusBadgeClass is the colour class of a status pill; an unknown status is
// "sent".
func StatusBadgeClass(status string) string {
	if c, ok := statusBadge[strings.ToUpper(status)]; ok {
		return c
	}
	return "sent"
}

// StatusLabel is Python's (status or "—").replace("_", " ").title():
// "Partially Paid".
func StatusLabel(status string) string {
	if status == "" {
		return Dash
	}
	return pyTitle(strings.ReplaceAll(status, "_", " "))
}

// pyTitle is Python's str.title(): a letter after a non-letter is upper-case,
// every other letter lower-case.
func pyTitle(s string) string {
	var b strings.Builder
	prevLetter := false
	for _, r := range s {
		if prevLetter {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(unicode.ToUpper(r))
		}
		prevLetter = unicode.IsLetter(r)
	}
	return b.String()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// rowNumber is an item row's printed number, 1-based and zero-padded to two
// digits: "01", "02", … "10", "100".
func rowNumber(i int) string { return fmt.Sprintf("%02d", i) }

var (
	ones = []string{
		"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
		"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen",
	}
	tens   = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}
	scales = []string{"", "thousand", "million", "billion", "trillion", "quadrillion"}
)

// currencyWords is a currency's name in words: singular, plural.
var currencyWords = map[string][2]string{
	"IDR": {"rupiah", "rupiah"},
	"SGD": {"Singapore dollar", "Singapore dollars"},
	"USD": {"US dollar", "US dollars"},
	"MYR": {"Malaysian ringgit", "Malaysian ringgit"},
}

// AmountInWords is the amount in English words for the receipt: the first
// word capitalised, the currency's name after the whole part, and any cents
// as "and NN/100" ("One thousand two hundred thirty-four US dollars and
// 56/100"). The amount is first rounded half-even to the currency's places,
// exactly as FmtNum prints it, so the words spell the printed figure: a
// zero-decimal currency (IDR) never has cents. A currency without a name
// prints its code.
func AmountInWords(amount decimal.Decimal, currency string) string {
	a := amount.RoundBank(CurrencyDecimals(currency)).Abs() // as group() rounds the printed figure
	whole := a.Truncate(0)
	cents := a.Sub(whole).Shift(2).IntPart()
	name := currency
	if n, ok := currencyWords[strings.ToUpper(currency)]; ok {
		name = n[1]
		if whole.Equal(decimal.NewFromInt(1)) {
			name = n[0]
		}
	}
	s := wholeWords(whole) + " " + name
	if cents != 0 {
		s += fmt.Sprintf(" and %02d/100", cents)
	}
	if amount.IsNegative() && !a.IsZero() {
		s = "minus " + s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// wholeWords is a whole number in words: "two million five hundred
// thousand".
func wholeWords(n decimal.Decimal) string {
	if n.IsZero() {
		return ones[0]
	}
	thousand := decimal.NewFromInt(1000)
	var groups []int64 // least significant first
	for n.IsPositive() {
		q := n.Div(thousand).Truncate(0)
		groups = append(groups, n.Sub(q.Mul(thousand)).IntPart())
		n = q
	}
	var words []string
	for i := len(groups) - 1; i >= 0; i-- {
		if groups[i] == 0 {
			continue
		}
		w := hundredsWords(groups[i])
		if i < len(scales) && scales[i] != "" {
			w += " " + scales[i]
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// hundredsWords is 1 to 999 in words: "two hundred thirty-four".
func hundredsWords(n int64) string {
	var w []string
	if n >= 100 {
		w = append(w, ones[n/100]+" hundred")
		n %= 100
	}
	switch {
	case n == 0:
	case n < 20:
		w = append(w, ones[n])
	case n%10 == 0:
		w = append(w, tens[n/10])
	default:
		w = append(w, tens[n/10]+"-"+ones[n%10])
	}
	return strings.Join(w, " ")
}
