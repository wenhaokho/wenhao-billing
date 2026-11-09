package bills

import (
	"testing"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func strp(s string) *string { return &s }

func decp(s string) *decimal.Decimal { d := dec(s); return &d }

// A line amount is qty × price rounded to 4 places with ties to the even
// digit (Python's default quantize), where invoices and quotations round
// half-up.
func TestBillLinesRoundHalfEven(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ qty, price, want string }{
		{"0.5", "0.0001", "0.0000"}, // 0.00005: a tie, the even digit is 0
		{"1.5", "0.0001", "0.0002"}, // 0.00015: a tie, the even digit is 2
		{"3", "2.5", "7.5000"},
	} {
		if got := lineAmount(dec(c.qty), dec(c.price)); got.StringFixed(4) != c.want {
			t.Errorf("%s × %s = %s, want %s", c.qty, c.price, got.StringFixed(4), c.want)
		}
	}
}

// The subtotal is the sum of the rounded lines, not the rounded sum of the
// raw products: two lines of 0.00015 are 0.0002 each, 0.0004 together.
func TestBillSubtotalSumsRoundedLines(t *testing.T) {
	t.Parallel()
	line := lineAmount(dec("1.5"), dec("0.0001"))
	subtotal, amount := totals([]decimal.Decimal{line, line}, nil, nil)
	if subtotal.StringFixed(4) != "0.0004" || amount.StringFixed(4) != "0.0004" {
		t.Errorf("subtotal %s amount %s, want 0.0004 both", subtotal.StringFixed(4), amount.StringFixed(4))
	}
}

func TestBillPercentDiscount(t *testing.T) {
	t.Parallel()
	lines := []decimal.Decimal{dec("100.0000"), dec("33.3333")}
	for _, c := range []struct {
		name             string
		kind             *string
		value            *decimal.Decimal
		subtotal, amount string
	}{
		{"none", nil, nil, "133.3333", "133.3333"},
		{"percent", strp("PERCENT"), decp("12.5"), "133.3333", "116.6666"},                     // 133.3333 × 0.875 = 116.666637...
		{"amount", strp("AMOUNT"), decp("33.3333"), "133.3333", "100.0000"},                    //
		{"amount over the subtotal clamps", strp("AMOUNT"), decp("500"), "133.3333", "0.0000"}, //
		{"percent over 100 clamps", strp("PERCENT"), decp("150"), "133.3333", "0.0000"},        //
		{"a type without a value", strp("PERCENT"), nil, "133.3333", "133.3333"},
		{"a value without a type", nil, decp("10"), "133.3333", "133.3333"},
	} {
		subtotal, amount := totals(lines, c.kind, c.value)
		if subtotal.StringFixed(4) != c.subtotal || amount.StringFixed(4) != c.amount {
			t.Errorf("%s: subtotal %s amount %s, want %s %s", c.name, subtotal.StringFixed(4), amount.StringFixed(4), c.subtotal, c.amount)
		}
	}
}

func TestBillDiscountedAmountRoundsHalfEven(t *testing.T) {
	t.Parallel()
	// 0.0005 × 0.9 = 0.00045: a tie at 4 places for the even digit 4.
	_, amount := totals([]decimal.Decimal{dec("0.0005")}, strp("PERCENT"), decp("10"))
	if amount.StringFixed(4) != "0.0004" {
		t.Errorf("amount %s, want 0.0004", amount.StringFixed(4))
	}
}
