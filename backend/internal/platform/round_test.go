package platform

import (
	"testing"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestRoundHalfEven4(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"1.00005": "1.0000", "1.00015": "1.0002"} {
		if got := RoundHalfEven4(dec(in)); got.StringFixed(4) != want {
			t.Errorf("%s: %s, want %s", in, got.StringFixed(4), want)
		}
	}
}

func TestRoundHalfUp4(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"1.00005": "1.0001", "-1.00005": "-1.0001"} {
		if got := RoundHalfUp4(dec(in)); got.StringFixed(4) != want {
			t.Errorf("%s: %s, want %s", in, got.StringFixed(4), want)
		}
	}
}

func TestRoundWithholding(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, cur, want string }{
		{"1500.5", "IDR", "1501"}, {"10.005", "USD", "10.01"}, {"10.004", "SGD", "10.00"},
	} {
		got := RoundWithholding(dec(c.in), c.cur)
		if got.String() != dec(c.want).String() {
			t.Errorf("%s %s: %s, want %s", c.cur, c.in, got, c.want)
		}
	}
}
