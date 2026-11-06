// Package ledger contains ledger domain rules.
package ledger

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Line is the monetary data needed to validate a journal line.
type Line struct {
	Currency              string
	Debit, Credit         decimal.Decimal
	BaseDebit, BaseCredit decimal.Decimal
}

// ValidateBalance checks the base balance and, for a single currency, the raw balance.
func ValidateBalance(lines []Line) error {
	baseNet := decimal.Zero
	rawNets := make(map[string]decimal.Decimal)
	for _, line := range lines {
		baseNet = baseNet.Add(line.BaseDebit).Sub(line.BaseCredit)
		rawNets[line.Currency] = rawNets[line.Currency].Add(line.Debit).Sub(line.Credit)
	}
	if !baseNet.IsZero() {
		return fmt.Errorf("journal entry unbalanced in base currency: net=%s", baseNet)
	}
	if len(rawNets) == 1 {
		for currency, net := range rawNets {
			if !net.IsZero() {
				return fmt.Errorf("journal entry unbalanced in %s: net=%s", currency, net)
			}
		}
	}
	return nil
}
