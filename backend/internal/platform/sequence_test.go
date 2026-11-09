package platform

import "testing"

func TestNextSequence(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		numbers []string
		want    string
	}{
		{"empty", nil, "Q20260001"},
		{"pads to four digits", []string{"Q20260005"}, "Q20260006"},
		{"highest wins, in any order", []string{"Q20260007", "Q20260012", "Q20260003"}, "Q20260013"},
		{"leading zeros count", []string{"Q2026000000000000041"}, "Q20260042"},
		{"grows past four digits", []string{"Q20269999"}, "Q202610000"},
		{"tail beyond int64", []string{"Q202699999999999999999999"}, "Q2026100000000000000000000"},
		{
			"non-digit tails are ignored",
			[]string{"Q2026", "Q2026ABC", "Q20260900-A", "Q2026 77", "Q2026+99", "Q2026-5", "Q20260002"},
			"Q20260003",
		},
		// Arabic-Indic 99 and full-width 7: Python's isdigit took them.
		{"non-ASCII digits are ignored", []string{"Q2026٩٩", "Q2026７", "Q20260001"}, "Q20260002"},
		{"other prefixes are ignored", []string{"Q20250042", "QUO-0001", "WH20260009", "xQ20260050"}, "Q20260001"},
	} {
		if got := NextSequence("Q2026", c.numbers); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	if got := NextSequence("WH2026", []string{"WH20260041"}); got != "WH20260042" {
		t.Errorf("WH: %s", got)
	}
}
