package withholding

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func ptr(s string) *decimal.Decimal { v := d(s); return &v }

func eq(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(d(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

func TestPph23NoGrossUp(t *testing.T) {
	r := Compute(d("10000000"), "IDR", "PPH23", ptr("2"), false)
	eq(t, "total", r.Total, "10000000")
	eq(t, "amount", r.Amount, "200000")
	eq(t, "base", r.Base, "10000000")
}

func TestPph23GrossUp(t *testing.T) {
	r := Compute(d("10000000"), "IDR", "PPH23", ptr("2"), true)
	eq(t, "total", r.Total, "10204082")
	eq(t, "amount", r.Amount, "204082")
	eq(t, "total-amount", r.Total.Sub(r.Amount), "10000000")
}

func TestPph21UsdGrossUpTwoDecimals(t *testing.T) {
	r := Compute(d("1000"), "USD", "PPH21", ptr("2.5"), true)
	eq(t, "total", r.Total, "1025.64")
	eq(t, "amount", r.Amount, "25.64")
}

func TestNoneType(t *testing.T) {
	r := Compute(d("500"), "USD", "NONE", nil, true)
	eq(t, "total", r.Total, "500")
	eq(t, "amount", r.Amount, "0")
}

func TestZeroBaseGrossUp(t *testing.T) {
	r := Compute(d("0"), "IDR", "PPH23", ptr("2"), true)
	eq(t, "total", r.Total, "0")
	eq(t, "amount", r.Amount, "0")
}

func TestRoundHalfUp(t *testing.T) {
	eq(t, "idr", platform.RoundWithholding(d("0.5"), "IDR"), "1")
	eq(t, "usd", platform.RoundWithholding(d("1.005"), "USD"), "1.01")
}

func TestNormalizeDefaultsRate(t *testing.T) {
	typ, rate, gu, err := Normalize("PPH21", nil, true)
	if err != nil || typ != "PPH21" || rate == nil || !rate.Equal(d("2.5")) || !gu {
		t.Fatalf("got %v %v %v %v", typ, show(rate), gu, err)
	}
}

func TestNormalizeNoneClears(t *testing.T) {
	typ, rate, gu, err := Normalize("NONE", ptr("2"), true)
	if err != nil || typ != "NONE" || rate != nil || gu {
		t.Fatalf("got %v %v %v %v", typ, show(rate), gu, err)
	}
}

func TestNormalizeRejectsInvalid(t *testing.T) {
	rateMsg := "Withholding tax rate must be greater than 0 and less than 100"
	cases := []struct {
		typ  string
		rate *decimal.Decimal
		want string
	}{
		{"PPH23", ptr("100"), rateMsg},
		{"PPH23", ptr("0"), rateMsg},
		{"PPN", nil, "Unknown withholding tax type: PPN"},
	}
	for _, c := range cases {
		_, _, _, err := Normalize(c.typ, c.rate, false)
		if err == nil || err.Error() != c.want {
			t.Errorf("Normalize(%s) err = %v, want %q", c.typ, err, c.want)
		}
	}
}

func TestExpectedCash(t *testing.T) {
	eq(t, "cash", ExpectedCash(d("1000"), d("20"), d("5")), "985")
}

func TestExpectedCashNeverNegative(t *testing.T) {
	eq(t, "cash", ExpectedCash(d("10"), d("50"), d("0")), "0")
}

// Ties at each of Compute's two rounding call sites (half-up, not half-even).
func TestComputeRoundsTiesHalfUp(t *testing.T) {
	r := Compute(d("100.5"), "USD", "PPH23", ptr("1"), false)
	eq(t, "usd amount (1.005 tie)", r.Amount, "1.01")
	r = Compute(d("25"), "IDR", "PPH23", ptr("2"), false)
	eq(t, "idr amount (0.5 tie)", r.Amount, "1")
	r = Compute(d("0.25"), "IDR", "PPH23", ptr("50"), true)
	eq(t, "idr gross-up total (0.5 tie)", r.Total, "1")
	eq(t, "idr gross-up amount", r.Amount, "0.75")
	r = Compute(d("1.0025"), "USD", "PPH23", ptr("50"), true)
	eq(t, "usd gross-up total (2.005 tie)", r.Total, "2.01")
}

func show(r *decimal.Decimal) string {
	if r == nil {
		return "<nil>"
	}
	return r.String()
}
