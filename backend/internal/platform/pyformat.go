package platform

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shopspring/decimal"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// The Python printers: how the Python backend wrote a value into a refusal
// sentence, so the Go port's sentences stay byte-identical.

// PyRepr is Python's repr of s: single quotes unless s holds a single quote
// and no double quote; backslash, the quote, \t, \n and \r escaped; other
// non-printable characters (Go's unicode.IsPrint standing in for
// str.isprintable) as \xNN, \uNNNN or \UNNNNNNNN.
func PyRepr(s string) string {
	quote := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == quote || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < ' ' || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f || unicode.IsPrint(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}

// PyFloatRepr is Python's repr of f: nan, inf and -inf; otherwise the
// shortest round-trip digits, fixed notation (with ".0" when whole) for
// 1e-4 <= |f| < 1e16 and zero, else exponent notation ("1e+16", "1.5e-05":
// Go's 'e' format also writes at least two exponent digits).
func PyFloatRepr(f float64) string { //nolint:forbidigo // not money: Python's repr of a JSON number
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	if a := math.Abs(f); a != 0 && (a < 1e-4 || a >= 1e16) {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// PyDecimalStr is str(d) of Python's Decimal (to_sci_string) for the
// coefficient and exponent d was parsed with; shopspring keeps both as the
// literal wrote them, as Python's Decimal does: "600.00" stays 600.00, and
// 6e2 is 6E+2.
func PyDecimalStr(d decimal.Decimal) string {
	sign, coef := "", d.Coefficient()
	if coef.Sign() < 0 {
		sign = "-"
		coef.Neg(coef)
	}
	digits, exp := coef.String(), int(d.Exponent())
	left := exp + len(digits)
	dot := 1
	if exp <= 0 && left > -6 {
		dot = left
	}
	var s string
	switch {
	case dot <= 0:
		s = "0." + strings.Repeat("0", -dot) + digits
	case dot >= len(digits):
		s = digits + strings.Repeat("0", dot-len(digits))
	default:
		s = digits[:dot] + "." + digits[dot:]
	}
	if left != dot {
		s += fmt.Sprintf("E%+d", left-dot)
	}
	return sign + s
}

// PyUpper is Python's str.upper (full case mapping), not strings.ToUpper's
// one-rune-to-one-rune mapping: it can lengthen a value ("ß" is "SS"), so a
// caller with a length limit checks the result.
func PyUpper(s string) string { return cases.Upper(language.Und).String(s) }

// PyUpperAtMost is PyUpper(s), refused with pydantic's max_length sentence as
// a 422 at field when the upper-cased value is longer than n characters
// (Python checked the length before upper-casing, then failed on the column).
func PyUpperAtMost(field, s string, n int) (string, error) {
	u := PyUpper(s)
	if utf8.RuneCountInString(u) > n {
		return "", FieldError(field, fmt.Sprintf("String should have at most %d characters", n))
	}
	return u, nil
}
