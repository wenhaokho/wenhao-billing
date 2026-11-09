package platform

import (
	"fmt"
	"math/big"
	"strings"
)

// NextSequence is the next document number after numbers in prefix's
// sequence, as Python's generate_invoice_number and
// generate_quotation_number compute it: one past the highest number whose
// tail after prefix is all digits, written with at least four digits and
// growing past four as needed (prefix + "10000"). Numbers outside the
// sequence (another prefix, or a tail that is not all digits) are ignored.
//
// Only ASCII digits count as a tail, where Python's isdigit also took other
// scripts' digits. A tail of any length counts, as Python's int does.
func NextSequence(prefix string, numbers []string) string {
	highest := new(big.Int)
	for _, n := range numbers {
		tail, ok := strings.CutPrefix(n, prefix)
		if !ok || !asciiDigits(tail) {
			continue
		}
		v, _ := new(big.Int).SetString(tail, 10)
		if v.Cmp(highest) > 0 {
			highest = v
		}
	}
	return fmt.Sprintf("%s%04d", prefix, highest.Add(highest, big.NewInt(1)))
}

// asciiDigits is Python's str.isdigit for ASCII: non-empty, every byte 0-9.
func asciiDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
