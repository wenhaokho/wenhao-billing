package platform

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestPyRepr(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"", `''`},
		{"abc", `'abc'`},
		{"it's", `"it's"`},
		{`a'b"c`, `'a\'b"c'`},
		{`q"`, `'q"'`},
		{"tab\there\n\r", `'tab\there\n\r'`},
		{`back\slash`, `'back\\slash'`},
		{"A\x01B", `'A\x01B'`},
		{"\x7f", `'\x7f'`},
		{"\u00a0\u0085", `'\xa0\x85'`},
		{"caf\u00e9", "'caf\u00e9'"},
		{"\u2028", `'\u2028'`},
		{"\U000e0001", `'\U000e0001'`},
		{"\u65e5\u672c", "'\u65e5\u672c'"},
	} {
		if got := PyRepr(c.in); got != c.want {
			t.Errorf("PyRepr(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestPyFloatRepr(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in   float64
		want string
	}{
		{0, "0.0"},
		{1.5, "1.5"},
		{100, "100.0"},
		{0.0025, "0.0025"},
		{0.0001, "0.0001"},
		{0.00001, "1e-05"},
		{1e16, "1e+16"},
		{1234567890123456, "1234567890123456.0"},
		{0.1, "0.1"},
		{-600, "-600.0"},
		{math.NaN(), "nan"},
		{math.Inf(1), "inf"},
		{math.Inf(-1), "-inf"},
	} {
		if got := PyFloatRepr(c.in); got != c.want {
			t.Errorf("PyFloatRepr(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestPyDecimalStr(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"600":       "600",
		"600.00":    "600.00",
		"0600.5":    "600.5",
		"6e2":       "6E+2",
		"-6e2":      "-6E+2",
		"0.000001":  "0.000001",
		"0.0000001": "1E-7",
		"1.5e-7":    "1.5E-7",
		"-0.5":      "-0.5",
		"500.0001":  "500.0001",
		"1.23e2":    "123",
		"12.3e-1":   "1.23",
		"0.00":      "0.00",
	} {
		if got := PyDecimalStr(dec(in)); got != want {
			t.Errorf("PyDecimalStr(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestPyUpper(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"usd": "USD", "ßab": "SSAB", "ﬁ": "FI", "Ab1": "AB1"} {
		if got := PyUpper(in); got != want {
			t.Errorf("PyUpper(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPyUpperAtMost(t *testing.T) {
	t.Parallel()
	if got, err := PyUpperAtMost("currency", "usd", 3); err != nil || got != "USD" {
		t.Errorf("usd: %q, %v; want USD", got, err)
	}
	if got, err := PyUpperAtMost("currency", "ßab", 3); err == nil {
		t.Errorf("ßab: %q, want a refusal", got)
	} else if !strings.Contains(err.Error(), "String should have at most 3 characters") {
		t.Errorf("ßab: %v", err)
	}
}

// The private copies this package replaced, kept here as the reference the
// callers' inputs are checked against. Each prints what its caller printed.

func legacyLedgerPyStr(d decimal.Decimal) string {
	digits := new(big.Int).Abs(d.Coefficient()).String()
	sign := ""
	if d.IsNegative() {
		sign = "-"
	}
	exp := int(d.Exponent())
	left := exp + len(digits)
	dot := 1
	if exp <= 0 && left > -6 {
		dot = left
	}
	var intPart, frac string
	switch {
	case dot <= 0:
		intPart, frac = "0", "."+strings.Repeat("0", -dot)+digits
	case dot >= len(digits):
		intPart = digits + strings.Repeat("0", dot-len(digits))
	default:
		intPart, frac = digits[:dot], "."+digits[dot:]
	}
	e := ""
	if left != dot {
		e = fmt.Sprintf("E%+d", left-dot)
	}
	return sign + intPart + frac + e
}

// legacyLedgerPyRepr and legacyFxPyRepr differ from PyRepr only outside the
// callers' inputs (ledger: non-printable non-ASCII; fx: a string holding a
// quote or backslash), where PyRepr is the faithful one.
func legacyLedgerPyRepr(s string) string {
	q := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		q = '"'
	}
	var b strings.Builder
	b.WriteRune(q)
	for _, r := range s {
		switch {
		case r == '\\' || r == q:
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteRune(q)
	return b.String()
}

func legacyFxPyRepr(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "None"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return "'" + s + "'"
	}
	return string(raw)
}

func TestPrintersMatchTheCopiesTheyReplaced(t *testing.T) {
	t.Parallel()
	t.Run("ledger str(Decimal)", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{"0", "0.0000", "-1.50", "1600000.0001", "-0.0001", "1000", "1E+3", "0.0000001", "0.000001", "1.5E+4", "600.00", "-0", "123456789012345.6789"} {
			d, err := decimal.NewFromString(in)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := PyDecimalStr(d), legacyLedgerPyStr(d); got != want {
				t.Errorf("PyDecimalStr(%s) = %q, ledger printed %q", in, got, want)
			}
		}
	})
	t.Run("ledger repr of a status", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{"PENDING_MANUAL_REVIEW", "CLEARED", "FLAGGED", "", "it's", `a'b"c`, "a\nb", "tab\t", "A\x01B", "\x7f", `back\slash`} {
			if got, want := PyRepr(in), legacyLedgerPyRepr(in); got != want {
				t.Errorf("PyRepr(%q) = %s, ledger printed %s", in, got, want)
			}
		}
	})
	t.Run("fx repr of a provider date", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{``, `null`, `"2026-13-45"`, `"not-a-date"`, `""`, `20260101`, `1.5`, `true`, `[1,2]`, `{"a":1}`} {
			if got, want := PyJSONRepr(json.RawMessage(in)), legacyFxPyRepr(json.RawMessage(in)); got != want {
				t.Errorf("PyJSONRepr(%s) = %s, fx printed %s", in, got, want)
			}
		}
		// Past the old copy, which wrapped any string in single quotes.
		if got := PyJSONRepr(json.RawMessage(`"it's"`)); got != `"it's"` {
			t.Errorf("PyJSONRepr of a quoted string = %s", got)
		}
	})
	t.Run("schedule repr of a config value", func(t *testing.T) {
		t.Parallel()
		for in, want := range map[string]string{
			"nil": "None", "true": "True", "false": "False", "'WEEKLY'": "'WEEKLY'", "apostrophe": `"x'y"`,
			"7": "7", "int32": "7", "int64": "7", "float 2.5": "2.5", "float 2": "2.0", "float 1e16": "1e+16",
			"number 2": "2", "number 2.0": "2.0", "number 1e2": "100.0", "number 1.5": "1.5",
			"struct": "<unsupported>",
		} {
			var v any
			switch in {
			case "nil":
			case "true":
				v = true
			case "false":
				v = false
			case "'WEEKLY'":
				v = "WEEKLY"
			case "apostrophe":
				v = "x'y"
			case "7":
				v = 7
			case "int32":
				v = int32(7)
			case "int64":
				v = int64(7)
			case "float 2.5":
				v = 2.5
			case "float 2":
				v = 2.0
			case "float 1e16":
				v = 1e16
			case "number 2":
				v = json.Number("2")
			case "number 2.0":
				v = json.Number("2.0")
			case "number 1e2":
				v = json.Number("1e2")
			case "number 1.5":
				v = json.Number("1.5")
			case "struct":
				v = struct{}{}
			}
			if got := PyValueRepr(v); got != want {
				t.Errorf("PyValueRepr(%s) = %s, want %s", in, got, want)
			}
		}
	})
}
