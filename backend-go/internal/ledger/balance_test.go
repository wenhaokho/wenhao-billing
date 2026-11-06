package ledger

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func line(currency, debit, credit, baseDebit, baseCredit string) Line {
	return Line{Currency: currency, Debit: decimal.RequireFromString(debit), Credit: decimal.RequireFromString(credit), BaseDebit: decimal.RequireFromString(baseDebit), BaseCredit: decimal.RequireFromString(baseCredit)}
}

func TestValidateBalance(t *testing.T) {
	tests := []struct {
		name  string
		lines []Line
		want  string
	}{
		{"empty", nil, ""},
		{"balanced single currency", []Line{line("USD", "10", "0", "100", "0"), line("USD", "0", "10", "0", "100")}, ""},
		{"positive base net", []Line{line("USD", "1", "1", "1.0001", "1")}, "base currency: net=0.0001"},
		{"negative base net", []Line{line("USD", "1", "1", "1", "1.0001")}, "base currency: net=-0.0001"},
		{"positive raw net", []Line{line("USD", "1.0001", "1", "1", "1")}, "USD: net=0.0001"},
		{"negative raw net", []Line{line("IDR", "1", "1.0001", "1", "1")}, "IDR: net=-0.0001"},
		{"multi currency raw imbalance allowed", []Line{line("USD", "1", "0", "10", "0"), line("IDR", "0", "10", "0", "10")}, ""},
		{"multi currency base imbalance rejected", []Line{line("USD", "1", "0", "10", "0"), line("IDR", "0", "10", "0", "9")}, "base currency: net=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBalance(tt.lines)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want error containing %q", err, tt.want)
			}
		})
	}
}
