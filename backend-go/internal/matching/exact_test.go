package matching

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/shopspring/decimal"
)

func TestExactMatchPython(t *testing.T) {
	cases := [][]string{
		{"95", "usd", "100", "5", "0", "USD"},
		{"95.0001", "USD", "100", "5", "0", "usd"},
		{"94.9999", "USD", "100", "5", "0", "USD"},
		{"95.0002", "USD", "100", "5", "0", "USD"},
		{"95", "SGD", "100", "5", "0", "USD"},
		{"97", "USD", "100", "5", "2", "USD"},
		{"0", "USD", "1", "5", "0", "USD"},
		{"0.0002", "USD", "1", "5", "0", "USD"},
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	script := `import json,sys
from decimal import Decimal
from types import SimpleNamespace
from app.services.matching_engine import is_exact_match
rows=json.load(sys.stdin)
print(json.dumps([is_exact_match(SimpleNamespace(amount=Decimal(a), currency=pc), SimpleNamespace(balance_due=Decimal(b), wht_amount=Decimal(w), wht_booked=Decimal(k), currency=ic)) for a,pc,b,w,k,ic in rows]))`
	cmd := exec.Command("python", "-c", script)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v: %s", err, output)
	}
	var expected []bool
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		got := ExactMatch(decimal.RequireFromString(c[0]), c[1], decimal.RequireFromString(c[2]), decimal.RequireFromString(c[3]), decimal.RequireFromString(c[4]), c[5])
		if got != expected[i] {
			t.Errorf("case %v: got %v, Python %v", c, got, expected[i])
		}
	}
}

// Unsupported currency strings fail closed even if both sides are identical.
func TestUnsupportedCurrencyRequiresReview(t *testing.T) {
	for _, code := range []string{"straße", "ß", "Aß", "U$D", "US", "USDD", ""} {
		if ExactMatch(decimal.Zero, code, decimal.Zero, decimal.Zero, decimal.Zero, code) {
			t.Errorf("accepted unsupported currency %q", code)
		}
	}
}
