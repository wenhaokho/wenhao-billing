package platform

import (
	"math/big"
	"unicode"
)

// documentNumberSuffix mirrors Python's str.isdigit followed by int: decimal
// digits from any script are accepted, while non-decimal digits make int fail.
func documentNumberSuffix(s string) (*big.Int, bool, bool) {
	if s == "" {
		return nil, false, false
	}
	out := new(big.Int)
	invalid := false
	for _, r := range s {
		digit := -1
		for _, x := range unicode.Digit.R16 {
			if r >= rune(x.Lo) && r <= rune(x.Hi) && (r-rune(x.Lo))%rune(x.Stride) == 0 {
				digit = int((r-rune(x.Lo))/rune(x.Stride)) % 10
				break
			}
		}
		if digit < 0 {
			for _, x := range unicode.Digit.R32 {
				if r >= rune(x.Lo) && r <= rune(x.Hi) && (r-rune(x.Lo))%rune(x.Stride) == 0 {
					digit = int((r-rune(x.Lo))/rune(x.Stride)) % 10
					break
				}
			}
		}
		if digit >= 0 {
			out.Mul(out, big.NewInt(10))
			out.Add(out, big.NewInt(int64(digit)))
			continue
		}
		if isNonDecimalPythonDigit(r) {
			invalid = true
			continue
		}
		return nil, false, false
	}
	return out, true, invalid
}
func isNonDecimalPythonDigit(r rune) bool {
	for _, p := range [][2]rune{{178, 179}, {185, 185}, {4969, 4977}, {6618, 6618}, {8304, 8304}, {8308, 8313}, {8320, 8329}, {9312, 9320}, {9332, 9340}, {9352, 9360}, {9450, 9450}, {9461, 9469}, {9471, 9471}, {10102, 10110}, {10112, 10120}, {10122, 10130}, {68160, 68163}, {69216, 69224}, {69714, 69722}, {127232, 127242}} {
		if r >= p[0] && r <= p[1] {
			return true
		}
	}
	return false
}
