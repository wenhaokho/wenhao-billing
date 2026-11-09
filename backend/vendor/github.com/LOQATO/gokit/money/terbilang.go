package money

import (
	"errors"
	"strings"

	"github.com/shopspring/decimal"
)

var (
	digits = [...]string{"", "satu", "dua", "tiga", "empat", "lima", "enam", "tujuh", "delapan", "sembilan"}
	scales = [...]string{"", "ribu", "juta", "miliar", "triliun"}
)

// Terbilang writes an IDR amount in Indonesian words, as a receipt does:
// "Satu juta empat ratus ribu rupiah", cents following as "dan lima puluh
// sen". It refuses a negative amount and one of a thousand trillion or more.
func Terbilang(amount decimal.Decimal) (string, error) {
	if amount.IsNegative() {
		return "", errors.New("terbilang takes a non-negative amount")
	}
	totalCents := amount.Mul(decimal.NewFromInt(100)).Round(0)
	rupiah := totalCents.Div(decimal.NewFromInt(100)).Truncate(0)
	cents := totalCents.Sub(rupiah.Mul(decimal.NewFromInt(100))).IntPart()
	if rupiah.GreaterThanOrEqual(decimal.New(1, 15)) {
		return "", errors.New("amount too large to write in words")
	}
	text := angka(rupiah.IntPart()) + " rupiah"
	if cents > 0 {
		text += " dan " + angka(cents) + " sen"
	}
	return strings.ToUpper(text[:1]) + text[1:], nil
}

func angka(n int64) string {
	if n == 0 {
		return "nol"
	}
	var groups []int64
	for n > 0 {
		groups = append(groups, n%1000)
		n /= 1000
	}
	var words []string
	for i := len(groups) - 1; i >= 0; i-- {
		g := groups[i]
		if g == 0 {
			continue
		}
		// "seribu", never "satu ribu"; every other scale keeps "satu".
		if scales[i] == "ribu" && g == 1 {
			words = append(words, "seribu")
			continue
		}
		words = append(words, strings.TrimSpace(ratusan(g)+" "+scales[i]))
	}
	return strings.Join(words, " ")
}

// ratusan writes 0 < n < 1000.
func ratusan(n int64) string {
	var words []string
	hundreds, rest := n/100, n%100
	switch {
	case hundreds == 1:
		words = append(words, "seratus")
	case hundreds > 1:
		words = append(words, digits[hundreds]+" ratus")
	}
	switch {
	case rest >= 20:
		words = append(words, digits[rest/10]+" puluh")
		if rest%10 != 0 {
			words = append(words, digits[rest%10])
		}
	case rest >= 12:
		words = append(words, digits[rest-10]+" belas")
	case rest == 11:
		words = append(words, "sebelas")
	case rest == 10:
		words = append(words, "sepuluh")
	case rest > 0:
		words = append(words, digits[rest])
	}
	return strings.Join(words, " ")
}
