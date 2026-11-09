package matching

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func TestRatioMatchesPython(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/ratio_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Cases [][3]string `json:"cases"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Cases) < 30 {
		t.Fatalf("expected the full golden set, got %d cases", len(golden.Cases))
	}
	for _, c := range golden.Cases {
		a, b, want := c[0], c[1], c[2]
		name := []rune(a + "|" + b)
		if len(name) > 40 {
			name = name[:40]
		}
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			if got := platform.PyFloatRepr(Ratio(a, b)); got != want {
				t.Fatalf("Ratio(%q, %q) = %s, want %s", a, b, got, want)
			}
		})
	}
}
