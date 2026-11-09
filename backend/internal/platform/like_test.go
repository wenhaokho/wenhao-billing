package platform

import "testing"

func TestLikeEscape(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"Acme", "Acme"},
		{"100%", `100\%`},
		{"a_b", `a\_b`},
		{`a\b`, `a\\b`},
		{`%_\`, `\%\_\\`},
	} {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := LikeEscape(c.in); got != c.want {
				t.Fatalf("LikeEscape(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
