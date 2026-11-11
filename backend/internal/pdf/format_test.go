package pdf

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func nd(s string) decimal.NullDecimal { return decimal.NullDecimal{Decimal: dec(s), Valid: true} }

func sp(s string) *string { return &s }

// Goldens are the output of the frozen Python's _fmt_amount, _fmt_num,
// _fmt_qty, _fmt_pct and _discount_amount (docker compose run backend python,
// 2026-11-10).
func TestFormattersMatchPython(t *testing.T) {
	t.Parallel()

	t.Run("amount and num", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ v, cur, amount, num string }{
			{"2.5", "USD", "2.50 USD", "2.50"},
			{"2.5", "IDR", "2 IDR", "2"},
			{"2.505", "USD", "2.50 USD", "2.50"},
			{"2.505", "IDR", "3 IDR", "3"},
			{"2.515", "USD", "2.52 USD", "2.52"},
			{"2.515", "IDR", "3 IDR", "3"},
			{"0.005", "USD", "0.00 USD", "0.00"},
			{"0.005", "IDR", "0 IDR", "0"},
			{"0.015", "USD", "0.02 USD", "0.02"},
			{"0.015", "IDR", "0 IDR", "0"},
			{"0.025", "USD", "0.02 USD", "0.02"},
			{"0.025", "IDR", "0 IDR", "0"},
			{"0.125", "USD", "0.12 USD", "0.12"},
			{"0.125", "IDR", "0 IDR", "0"},
			{"0.135", "USD", "0.14 USD", "0.14"},
			{"0.135", "IDR", "0 IDR", "0"},
			{"0.5", "USD", "0.50 USD", "0.50"},
			{"0.5", "IDR", "0 IDR", "0"},
			{"1.5", "USD", "1.50 USD", "1.50"},
			{"1.5", "IDR", "2 IDR", "2"},
			{"3.5", "USD", "3.50 USD", "3.50"},
			{"3.5", "IDR", "4 IDR", "4"},
			{"-0.5", "USD", "-0.50 USD", "-0.50"},
			{"-0.5", "IDR", "-0 IDR", "-0"},
			{"-1.5", "USD", "-1.50 USD", "-1.50"},
			{"-1.5", "IDR", "-2 IDR", "-2"},
			{"-2.5", "USD", "-2.50 USD", "-2.50"},
			{"-2.5", "IDR", "-2 IDR", "-2"},
			{"1234567.8951", "USD", "1,234,567.90 USD", "1,234,567.90"},
			{"1234567.8951", "IDR", "1,234,568 IDR", "1,234,568"},
			{"999999999999.995", "USD", "1,000,000,000,000.00 USD", "1,000,000,000,000.00"},
			{"999999999999.995", "IDR", "1,000,000,000,000 IDR", "1,000,000,000,000"},
			{"-1234.565", "USD", "-1,234.56 USD", "-1,234.56"},
			{"-1234.565", "IDR", "-1,235 IDR", "-1,235"},
			{"-0.004", "USD", "-0.00 USD", "-0.00"},
			{"-0.004", "IDR", "-0 IDR", "-0"},
			{"0", "USD", "0.00 USD", "0.00"},
			{"0", "IDR", "0 IDR", "0"},
			{"600.0000", "USD", "600.00 USD", "600.00"},
			{"600.0000", "IDR", "600 IDR", "600"},
			{"12345678901234.5", "USD", "12,345,678,901,234.50 USD", "12,345,678,901,234.50"},
			{"12345678901234.5", "IDR", "12,345,678,901,234 IDR", "12,345,678,901,234"},
			{"9999999999999999.99", "USD", "9,999,999,999,999,999.99 USD", "9,999,999,999,999,999.99"},
			{"9999999999999999.99", "IDR", "10,000,000,000,000,000 IDR", "10,000,000,000,000,000"},
			{"-1234567.5", "USD", "-1,234,567.50 USD", "-1,234,567.50"},
			{"-1234567.5", "IDR", "-1,234,568 IDR", "-1,234,568"},
			{"0.0049", "USD", "0.00 USD", "0.00"},
			{"0.0051", "USD", "0.01 USD", "0.01"},
			{"1000", "USD", "1,000.00 USD", "1,000.00"},
			{"1000", "IDR", "1,000 IDR", "1,000"},
			{"100.995", "USD", "101.00 USD", "101.00"},
			{"100.995", "IDR", "101 IDR", "101"},
			// The 13 zero-decimal currencies, then others; the check is
			// case-blind and the suffix is as given.
			{"1234567.5", "IDR", "1,234,568 IDR", "1,234,568"},
			{"1234567.5", "JPY", "1,234,568 JPY", "1,234,568"},
			{"1234567.5", "KRW", "1,234,568 KRW", "1,234,568"},
			{"1234567.5", "VND", "1,234,568 VND", "1,234,568"},
			{"1234567.5", "CLP", "1,234,568 CLP", "1,234,568"},
			{"1234567.5", "ISK", "1,234,568 ISK", "1,234,568"},
			{"1234567.5", "HUF", "1,234,568 HUF", "1,234,568"},
			{"1234567.5", "PYG", "1,234,568 PYG", "1,234,568"},
			{"1234567.5", "RWF", "1,234,568 RWF", "1,234,568"},
			{"1234567.5", "UGX", "1,234,568 UGX", "1,234,568"},
			{"1234567.5", "XAF", "1,234,568 XAF", "1,234,568"},
			{"1234567.5", "XOF", "1,234,568 XOF", "1,234,568"},
			{"1234567.5", "XPF", "1,234,568 XPF", "1,234,568"},
			{"1234567.5", "SGD", "1,234,567.50 SGD", "1,234,567.50"},
			{"1234567.5", "USD", "1,234,567.50 USD", "1,234,567.50"},
			{"1234567.5", "jpy", "1,234,568 jpy", "1,234,568"},
			{"1234567.5", "idr", "1,234,568 idr", "1,234,568"},
			{"1234567.5", "usd", "1,234,567.50 usd", "1,234,567.50"},
		} {
			t.Run(c.v+" "+c.cur, func(t *testing.T) {
				t.Parallel()
				if got := FmtAmount(dec(c.v), c.cur); got != c.amount {
					t.Errorf("FmtAmount(%s, %s) = %q, want %q", c.v, c.cur, got, c.amount)
				}
				if got := FmtNum(dec(c.v), c.cur); got != c.num {
					t.Errorf("FmtNum(%s, %s) = %q, want %q", c.v, c.cur, got, c.num)
				}
			})
		}
	})

	t.Run("qty", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ v, want string }{
			{"6.0000", "6"},
			{"6.5000", "6.5"},
			{"600.0000", "600"},
			{"1", "1"},
			{"0", "0"},
			{"0.0000", "0"},
			{"0.5000", "0.5"},
			{"1E+2", "100"},
			{"6E+2", "600"},
			{"1234567.1234", "1234567.1234"},
			{"-2.5000", "-2.5"},
			{"0.0001", "0.0001"},
			{"100", "100"},
			{"10.10", "10.1"},
			{"1000000", "1000000"},
		} {
			t.Run(c.v, func(t *testing.T) {
				t.Parallel()
				if got := FmtQty(dec(c.v)); got != c.want {
					t.Errorf("FmtQty(%s) = %q, want %q", c.v, got, c.want)
				}
			})
		}
	})

	t.Run("pct", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ v, want string }{
			{"5.0000", "5"},
			{"7.5000", "7.5"},
			{"10", "10"},
			{"0", "0"},
			{"0.0000", "0"},
			{"12.3400", "12.34"},
			{"100.0000", "100"},
			{"2.5", "2.5"},
			{"0.1250", "0.125"},
		} {
			t.Run(c.v, func(t *testing.T) {
				t.Parallel()
				if got := FmtPct(nd(c.v)); got != c.want {
					t.Errorf("FmtPct(%s) = %q, want %q", c.v, got, c.want)
				}
			})
		}
	})

	t.Run("discount", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ typ, v, subtotal, want string }{
			{"PERCENT", "5", "6350.00", "317.50"},
			{"PERCENT", "7.5", "1000", "75.00"},
			{"PERCENT", "0.125", "100", "0.12"},
			{"PERCENT", "12.5", "0.10", "0.01"},
			{"PERCENT", "33.3333", "1000", "333.33"},
			{"AMOUNT", "100", "6350.00", "100.00"},
			{"AMOUNT", "0.125", "1", "0.12"},
			{"AMOUNT", "0.135", "1", "0.14"},
			{"PERCENT", "5", "", "0.00"}, // no subtotal counts as 0
			{"AMOUNT", "1234567.895", "0", "1234567.90"},
		} {
			t.Run(c.typ+" "+c.v+" of "+c.subtotal, func(t *testing.T) {
				t.Parallel()
				var sub decimal.NullDecimal
				if c.subtotal != "" {
					sub = nd(c.subtotal)
				}
				got, ok := DiscountAmount(&c.typ, nd(c.v), sub)
				if !ok || got.StringFixed(2) != c.want {
					t.Errorf("DiscountAmount(%s, %s, %s) = %s, %v, want %s", c.typ, c.v, c.subtotal, got.StringFixed(2), ok, c.want)
				}
			})
		}
	})
}

// test_fmt_num_groups_thousands: no currency shows 2 places; NULL is a dash.
func TestFmtNumGroupsThousands(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"6350": "6,350.00", "1234.5": "1,234.50", "2.505": "2.50", "0.125": "0.12"} {
		if got := FmtNum(dec(in), ""); got != want {
			t.Errorf("FmtNum(%s) = %q, want %q", in, got, want)
		}
	}
	if got := FmtNumN(decimal.NullDecimal{}, ""); got != "—" {
		t.Errorf("FmtNumN(NULL) = %q", got)
	}
}

// test_discount_percent_computes_monetary_amount and the three after it.
func TestDiscountAmount(t *testing.T) {
	t.Parallel()
	percent, amount := sp("PERCENT"), sp("AMOUNT")
	if got, ok := DiscountAmount(percent, nd("5"), nd("6350.00")); !ok || !got.Equal(dec("317.50")) {
		t.Errorf("5%% of 6350 = %s, %v", got, ok)
	}
	if got, ok := DiscountAmount(percent, nd("7.5"), nd("1000")); !ok || !got.Equal(dec("75.00")) {
		t.Errorf("7.5%% of 1000 = %s, %v", got, ok)
	}
	if got, ok := DiscountAmount(amount, nd("100"), nd("6350.00")); !ok || !got.Equal(dec("100.00")) {
		t.Errorf("AMOUNT 100 = %s, %v", got, ok)
	}
	for name, c := range map[string]struct {
		typ   *string
		value decimal.NullDecimal
	}{
		"no type": {nil, decimal.NullDecimal{}}, "empty type": {sp(""), nd("5")},
		"no value": {percent, decimal.NullDecimal{}}, "zero value": {percent, nd("0")},
	} {
		if _, ok := DiscountAmount(c.typ, c.value, nd("6350.00")); ok {
			t.Errorf("%s: a discount was returned", name)
		}
	}
}

// test_fmt_pct_trims_trailing_zeros
func TestFmtPctTrimsTrailingZeros(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"5.0000": "5", "7.5000": "7.5", "10": "10"} {
		if got := FmtPct(nd(in)); got != want {
			t.Errorf("FmtPct(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusBadgeAndLabel(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]string{
		"DRAFT": "draft", "OPEN": "sent", "SENT": "sent", "PAID": "paid", "PARTIALLY_PAID": "partial",
		"PARTIAL": "partial", "AWAITING_FINALIZATION": "partial", "OVERDUE": "overdue", "VOID": "draft",
		"ACCEPTED": "paid", "DECLINED": "overdue", "EXPIRED": "overdue", "INVOICED": "sent",
		"paid": "paid", "weird": "sent", "": "sent",
	} {
		if got := StatusBadgeClass(s); got != want {
			t.Errorf("StatusBadgeClass(%q) = %q, want %q", s, got, want)
		}
	}
	for s, want := range map[string]string{
		"PARTIALLY_PAID": "Partially Paid", "AWAITING_FINALIZATION": "Awaiting Finalization",
		"DRAFT": "Draft", "": "—", "a1b it's": "A1B It'S",
	} {
		if got := StatusLabel(s); got != want {
			t.Errorf("StatusLabel(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestFmtDate(t *testing.T) {
	t.Parallel()
	d := pgtype.Date{Time: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), Valid: true}
	if got := FmtDate(d); got != "03 Jul 2026" {
		t.Errorf("FmtDate = %q", got)
	}
	if got := FmtDate(pgtype.Date{}); got != "—" {
		t.Errorf("FmtDate(NULL) = %q", got)
	}
}

func TestNPWPLine(t *testing.T) {
	t.Parallel()
	biz := &businessdb.BusinessProfile{Npwp: sp(" 2102060605910001 "), NpwpName: sp(" Kardono Wijaya ")}
	for name, c := range map[string]struct {
		wht  string
		biz  *businessdb.BusinessProfile
		want string
	}{
		"with name":    {"PPH23", biz, "NPWP 2102060605910001 a.n. Kardono Wijaya"},
		"no name":      {"PPH21", &businessdb.BusinessProfile{Npwp: sp("123")}, "NPWP 123"},
		"blank name":   {"PPH21", &businessdb.BusinessProfile{Npwp: sp("123"), NpwpName: sp("  ")}, "NPWP 123"},
		"none":         {"NONE", biz, ""},
		"empty type":   {"", biz, ""},
		"blank npwp":   {"PPH23", &businessdb.BusinessProfile{Npwp: sp("  ")}, ""},
		"no npwp":      {"PPH23", &businessdb.BusinessProfile{}, ""},
		"no business":  {"PPH23", nil, ""},
		"name no npwp": {"PPH23", &businessdb.BusinessProfile{NpwpName: sp("X")}, ""},
	} {
		if got := NPWPLine(c.wht, c.biz); got != c.want {
			t.Errorf("%s: NPWPLine = %q, want %q", name, got, c.want)
		}
	}
}

func TestRowNumber(t *testing.T) {
	t.Parallel()
	for i, want := range map[int]string{1: "01", 2: "02", 9: "09", 10: "10", 99: "99", 100: "100"} {
		eq(t, "rowNumber", rowNumber(i), want)
	}
}

func TestAmountInWords(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ amount, currency, want string }{
		{"3600000", "IDR", "Three million six hundred thousand rupiah"},
		{"2000", "SGD", "Two thousand Singapore dollars"},
		{"1234.56", "USD", "One thousand two hundred thirty-four US dollars and 56/100"},
		{"0.5", "USD", "Zero US dollars and 50/100"},
		{"1", "IDR", "One rupiah"},
		{"1", "USD", "One US dollar"},
		{"14400000.0000", "IDR", "Fourteen million four hundred thousand rupiah"},
		{"1000000000", "IDR", "One billion rupiah"},
		{"2500000000000", "IDR", "Two trillion five hundred billion rupiah"},
		{"115", "MYR", "One hundred fifteen Malaysian ringgit"},
		{"40.07", "EUR", "Forty EUR and 07/100"},
		{"19.995", "SGD", "Twenty Singapore dollars"}, // rounds half-even to cents first
		// IDR prints no decimals: the words spell the printed figure, rounded
		// half-even as FmtNum rounds it ("4,523,078"), never cents.
		{"4523077.5", "IDR", "Four million five hundred twenty-three thousand seventy-eight rupiah"},
		{"4523078.5", "IDR", "Four million five hundred twenty-three thousand seventy-eight rupiah"},
		{"0.4", "IDR", "Zero rupiah"},
		{"1001", "USD", "One thousand one US dollars"},
		{"90", "USD", "Ninety US dollars"},
	} {
		t.Run(c.amount+" "+c.currency, func(t *testing.T) {
			t.Parallel()
			eq(t, "AmountInWords", AmountInWords(dec(c.amount), c.currency), c.want)
		})
	}
}
