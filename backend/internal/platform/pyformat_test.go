package platform

import (
	"math"
	"strings"
	"testing"
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
