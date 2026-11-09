package quotations

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

// NextQuotationNumber is the next free number of today's year,
// Q{YYYY}{NNNN} (quoting.py:73-89): one past the highest quotation number
// whose tail after "Q{YYYY}" is all digits, at least four of them, growing
// past four as needed (Q202610000). The year is today's UTC calendar year,
// never an issue date's. Invoice numbers are a separate sequence and never
// counted.
//
// Go changes this (port note): Python scanned without a lock, so two
// concurrent creates could take the same number (a 500 on the unique key).
// This first takes
// pg_advisory_xact_lock(hashtextextended('quotation-number:<year>', 0)) in
// tx, held until tx ends, so the caller must insert the quotation in the
// same tx, which must be READ COMMITTED (the default) so the scan after the
// lock sees the numbers a transaction that held it before has committed.
// Only ASCII digits count as a tail, where Python's isdigit also took other
// scripts' digits (and int() then failed on some).
func NextQuotationNumber(ctx context.Context, tx pgx.Tx, today time.Time) (string, error) {
	year := strconv.Itoa(today.UTC().Year())
	q := quotationsdb.New(tx)
	if err := q.LockQuotationNumbers(ctx, year); err != nil {
		return "", err
	}
	prefix := "Q" + year
	numbers, err := q.ListQuotationNumbersWithPrefix(ctx, prefix)
	if err != nil {
		return "", err
	}
	highest := new(big.Int)
	for _, n := range numbers {
		tail := strings.TrimPrefix(n, prefix)
		if !allDigits(tail) {
			continue
		}
		// Arbitrary length, as Python's int: the column holds 45 digits.
		v, _ := new(big.Int).SetString(tail, 10)
		if v.Cmp(highest) > 0 {
			highest = v
		}
	}
	return fmt.Sprintf("%s%04d", prefix, highest.Add(highest, big.NewInt(1))), nil
}

// allDigits is Python's str.isdigit for ASCII: non-empty, every byte 0-9.
func allDigits(s string) bool {
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
