package withholding

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal    { return decimal.RequireFromString(s) }
func ptr(s string) *decimal.Decimal { v := d(s); return &v }
func equal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(d(want)) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name, base, currency string
		kind                 Type
		rate                 *decimal.Decimal
		gross                bool
		total, amount        string
	}{
		{"PPH23", "10000000", "IDR", PPH23, ptr("2"), false, "10000000", "200000"},
		{"PPH23 gross", "10000000", "IDR", PPH23, ptr("2"), true, "10204082", "204082"},
		{"PPH21 USD gross", "1000", "USD", PPH21, ptr("2.5"), true, "1025.64", "25.64"},
		{"none", "500", "USD", None, nil, true, "500", "0"},
		{"zero base", "0", "IDR", PPH23, ptr("2"), true, "0", "0"},
		{"nil rate", "500", "USD", PPH23, nil, true, "500", "0"},
		{"explicit zero rate", "500", "USD", PPH23, ptr("0"), false, "500", "0"},
		{"fractional rate", "1000", "USD", PPH21, ptr("2.55"), false, "1000", "25.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Compute(d(tt.base), tt.currency, tt.kind, tt.rate, tt.gross)
			equal(t, r.Base, tt.base)
			equal(t, r.Total, tt.total)
			equal(t, r.Withholding, tt.amount)
			if tt.gross && tt.rate != nil && tt.kind != None {
				equal(t, r.Total.Sub(r.Withholding), tt.base)
			}
		})
	}
}

func TestRoundCurrency(t *testing.T) {
	for _, tt := range []struct{ value, currency, want string }{{"0.5", "IDR", "1"}, {"1.005", "USD", "1.01"}, {"-0.5", "IDR", "-1"}, {"-1.005", "USD", "-1.01"}, {"1.005", "EUR", "1.01"}} {
		t.Run(tt.value+tt.currency, func(t *testing.T) { equal(t, RoundCurrency(d(tt.value), tt.currency), tt.want) })
	}
}

func TestNormalize(t *testing.T) {
	for _, tt := range []struct {
		name      string
		kind      Type
		rate      *decimal.Decimal
		gross     bool
		wantKind  Type
		wantRate  *decimal.Decimal
		wantGross bool
		wantErr   string
	}{
		{"PPH21 default", PPH21, nil, true, PPH21, ptr("2.5"), true, ""},
		{"PPH23 default", PPH23, nil, false, PPH23, ptr("2"), false, ""},
		{"none clears", None, ptr("2"), true, None, nil, false, ""},
		{"fractional", PPH23, ptr("0.001"), true, PPH23, ptr("0.001"), true, ""},
		{"unknown", Type("PPN"), nil, false, "", nil, false, "Unknown withholding tax type: PPN"},
		{"zero", PPH23, ptr("0"), false, "", nil, false, "Withholding tax rate must be greater than 0 and less than 100"},
		{"negative", PPH23, ptr("-0.01"), false, "", nil, false, "Withholding tax rate must be greater than 0 and less than 100"},
		{"hundred", PPH23, ptr("100"), false, "", nil, false, "Withholding tax rate must be greater than 0 and less than 100"},
		{"over hundred", PPH23, ptr("100.01"), false, "", nil, false, "Withholding tax rate must be greater than 0 and less than 100"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kind, rate, gross, err := Normalize(tt.kind, tt.rate, tt.gross)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("got error %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind != tt.wantKind || gross != tt.wantGross {
				t.Errorf("got %s %t", kind, gross)
			}
			if (rate == nil) != (tt.wantRate == nil) {
				t.Fatalf("rate nil mismatch: %v", rate)
			}
			if rate != nil {
				equal(t, *rate, tt.wantRate.String())
			}
		})
	}
}

func TestExpectedCash(t *testing.T) {
	equal(t, ExpectedCash(d("1000"), d("20"), d("5")), "985")
	equal(t, ExpectedCash(d("10"), d("50"), d("0")), "0")
	equal(t, ExpectedCash(d("10"), d("20"), d("25")), "15")
}

func TestComputePythonDecimalContext(t *testing.T) {
	rate := ptr("19.99999999999999999999999999")
	_, _, _, err := Normalize(PPH23, rate, true)
	if err != nil {
		t.Fatal(err)
	}
	got := Compute(d("0.804"), "USD", PPH23, rate, true)
	equal(t, got.Total, "1.01")
	equal(t, got.Withholding, "0.206")
}

func TestDefaultContextRounding(t *testing.T) {
	for _, tt := range []struct{ value, want string }{
		{"1.2345678901234567890123456785", "1.234567890123456789012345678"},
		{"1.2345678901234567890123456795", "1.234567890123456789012345680"},
		{"-1.2345678901234567890123456795", "-1.234567890123456789012345680"},
	} {
		t.Run(tt.value, func(t *testing.T) { equal(t, contextRound(d(tt.value)), tt.want) })
	}
	equal(t, contextDivide(d("1"), d("8")), "0.125")
	equal(t, contextDivide(d("1"), d("3")), "0.3333333333333333333333333333")
	equal(t, contextDivide(d("-2"), d("3")), "-0.6666666666666666666666666667")
}
