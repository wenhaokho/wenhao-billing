package invoices

import (
	"testing"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func decPtr(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

func strPtr(s string) *string { return &s }

func line(qty, price string) LineIn {
	return LineIn{Description: "x", Quantity: dec(qty), UnitPrice: dec(price)}
}

func wantDec(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

func TestLineAmountHalfUp(t *testing.T) {
	t.Parallel()
	// A half rounds away from zero (ROUND_HALF_UP), never to even.
	wantDec(t, "0.0001 × 0.5", LineAmount(dec("0.0001"), dec("0.5")), "0.0001")
	wantDec(t, "-0.0001 × 0.5", LineAmount(dec("-0.0001"), dec("0.5")), "-0.0001")
	wantDec(t, "3 × 3.33335", LineAmount(dec("3"), dec("3.33335")), "10.0001")
	wantDec(t, "2 × 1500", LineAmount(dec("2"), dec("1500")), "3000")
}

func TestSubtotalRoundsRawSum(t *testing.T) {
	t.Parallel()
	// Each line is 0.00005: rounded alone each is 0.0001 (0.0002 together),
	// but the subtotal rounds the raw sum 0.0001 once.
	got := ComputeTotals([]LineIn{line("0.0001", "0.5"), line("0.0001", "0.5")}, nil, nil, "USD", "NONE", nil, false)
	wantDec(t, "subtotal", got.Subtotal, "0.0001")
	wantDec(t, "amount", got.Amount, "0.0001")
}

func TestPercentDiscount(t *testing.T) {
	t.Parallel()
	got := ComputeTotals([]LineIn{line("1", "1000000")}, strPtr("PERCENT"), decPtr("10"), "IDR", "NONE", nil, false)
	wantDec(t, "subtotal", got.Subtotal, "1000000")
	wantDec(t, "amount", got.Amount, "900000")
	wantDec(t, "wht base", got.WHT.Base, "900000")
	wantDec(t, "wht amount", got.WHT.Amount, "0")
}

func TestAmountDiscountClampsAtZero(t *testing.T) {
	t.Parallel()
	got := ComputeTotals([]LineIn{line("1", "100")}, strPtr("AMOUNT"), decPtr("150"), "IDR", "PPH23", decPtr("2"), false)
	wantDec(t, "subtotal", got.Subtotal, "100")
	wantDec(t, "amount", got.Amount, "0")
	wantDec(t, "wht amount", got.WHT.Amount, "0")
}

func TestDiscountValueWithoutTypeIgnored(t *testing.T) {
	t.Parallel()
	lines := []LineIn{line("2", "50")}
	got := ComputeTotals(lines, nil, decPtr("30"), "IDR", "NONE", nil, false)
	wantDec(t, "no type", got.Amount, "100")
	got = ComputeTotals(lines, strPtr("PERCENT"), nil, "IDR", "NONE", nil, false)
	wantDec(t, "no value", got.Amount, "100")
	got = ComputeTotals(lines, strPtr("BOGUS"), decPtr("30"), "IDR", "NONE", nil, false)
	wantDec(t, "unknown type", got.Amount, "100")
}

func TestNoWHTKeepsFourPlaces(t *testing.T) {
	t.Parallel()
	// Only the withholding path rounds to the currency; a plain total keeps
	// four places.
	got := ComputeTotals([]LineIn{line("1", "10.3333")}, nil, nil, "USD", "NONE", nil, false)
	if got.Amount.String() != "10.3333" {
		t.Errorf("amount = %s, want 10.3333", got.Amount)
	}
	wantDec(t, "subtotal", got.Subtotal, "10.3333")
}

func TestWHTGrossUpIDR(t *testing.T) {
	t.Parallel()
	got := ComputeTotals([]LineIn{line("1", "10000000")}, nil, nil, "IDR", "PPH23", decPtr("2"), true)
	wantDec(t, "subtotal", got.Subtotal, "10000000")
	wantDec(t, "amount", got.Amount, "10204082")
	wantDec(t, "wht amount", got.WHT.Amount, "204082")
	wantDec(t, "wht base", got.WHT.Base, "10000000")
	wantDec(t, "wht total", got.WHT.Total, "10204082")
}

func TestPaymentTermsDays(t *testing.T) {
	t.Parallel()
	cases := []struct {
		terms *string
		want  int
	}{
		{nil, 14},
		{strPtr(""), 14},
		{strPtr("On Receipt"), 0},
		{strPtr("on-receipt"), 0},
		{strPtr("due on receipt"), 0},
		{strPtr("  DUE ON RECEIPT "), 0},
		{strPtr("Net 30"), 30},
		{strPtr(" net  45 "), 45},
		{strPtr("NET 0"), 0},
		{strPtr("Net30"), 14},
		{strPtr("Net 30 days"), 14},
		{strPtr("Monthly"), 14},
		{strPtr("Net 99999999"), 99999999},
	}
	for _, c := range cases {
		name := "nil"
		if c.terms != nil {
			name = *c.terms
		}
		if got, ok := PaymentTermsDays(c.terms); got != c.want || !ok {
			t.Errorf("PaymentTermsDays(%q) = %d, %v, want %d, true", name, got, ok, c.want)
		}
	}
	// A number beyond int is reported, not replaced by the default.
	if _, ok := PaymentTermsDays(strPtr("Net 99999999999999999999")); ok {
		t.Error("PaymentTermsDays(Net 99999999999999999999) ok, want overflow")
	}
}
