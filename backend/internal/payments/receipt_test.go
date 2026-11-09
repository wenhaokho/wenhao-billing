package payments

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Ports of the [IN] tests of tests/unit/test_receipt_pdf.py, then Go's own.

func TestReceiptReferenceShape(t *testing.T) {
	t.Parallel()
	ref := ReceiptReference(uuid.MustParse("0a1b2c3d-4e5f-6789-abcd-ef0123456789"))
	if ref != "RCT-0A1B2C3D" {
		t.Errorf("ref %q", ref)
	}
	if !strings.HasPrefix(ref, "RCT-") || len(ref) != 12 || ref != strings.ToUpper(ref) {
		t.Errorf("shape of %q", ref)
	}
}

func TestBalanceNotePartialPaymentShowsRemaining(t *testing.T) {
	t.Parallel()
	note := BalanceNote(dec("600.0000"), "USD")
	if note != "Remaining balance: 600.00 USD." {
		t.Errorf("note %q", note)
	}
}

func TestBalanceNoteFullPaymentSaysPaidInFull(t *testing.T) {
	t.Parallel()
	for _, b := range []string{"0", "0.0000", "-5"} {
		if got := BalanceNote(dec(b), "USD"); got != "This invoice is now paid in full." {
			t.Errorf("balance %s: %q", b, got)
		}
	}
}

// Goldens are the output of the frozen Python's _fmt_amount
// (docker compose run backend python, 2026-11-10).
func TestFormatAmountMatchesPython(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ amount, currency, want string }{
		{"2.5", "USD", "2.50 USD"},
		{"2.505", "USD", "2.50 USD"}, // tie at 2 places: half-even
		{"2.515", "USD", "2.52 USD"},
		{"0.005", "USD", "0.00 USD"},
		{"0.015", "USD", "0.02 USD"},
		{"0.025", "USD", "0.02 USD"},
		{"2.5", "IDR", "2 IDR"}, // tie at 0 places
		{"3.5", "IDR", "4 IDR"},
		{"0.5", "IDR", "0 IDR"},
		{"1.5", "IDR", "2 IDR"},
		{"1234567.8951", "IDR", "1,234,568 IDR"},
		{"1234567.8951", "USD", "1,234,567.90 USD"},
		{"1234.5", "JPY", "1,234 JPY"},
		{"1234.5", "jpy", "1,234 jpy"}, // zero-decimal check is case-blind, the suffix is as given
		{"1234.5", "usd", "1,234.50 usd"},
		{"999999999999.995", "USD", "1,000,000,000,000.00 USD"},
		{"-1234.565", "USD", "-1,234.56 USD"},
		{"-0.5", "IDR", "-0 IDR"},
		{"-0.004", "USD", "-0.00 USD"},
		{"0", "USD", "0.00 USD"},
		{"0", "IDR", "0 IDR"},
		{"1000", "SGD", "1,000.00 SGD"},
		{"100", "KRW", "100 KRW"},
		{"12345678901234.5", "VND", "12,345,678,901,234 VND"},
		{"600.0000", "USD", "600.00 USD"},
		{"400", "USD", "400.00 USD"},
	} {
		t.Run(c.amount+" "+c.currency, func(t *testing.T) {
			t.Parallel()
			if got := FormatAmount(dec(c.amount), c.currency); got != c.want {
				t.Errorf("FormatAmount(%s, %s) = %q, want %q", c.amount, c.currency, got, c.want)
			}
		})
	}
}

func TestStubRendererReturnsAPDF(t *testing.T) {
	t.Parallel()
	b, err := StubRenderer{}.RenderReceipt(t.Context(), ReceiptData{})
	if err != nil || !strings.HasPrefix(string(b), "%PDF-") {
		t.Errorf("%v %.8q", err, b)
	}
}
