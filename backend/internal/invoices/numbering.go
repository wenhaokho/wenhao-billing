package invoices

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
)

// NextInvoiceNumber is the next free number of today's year,
// WH{YYYY}{NNNN} (invoicing.py:157-171): one past the highest number whose
// tail after "WH{YYYY}" is all digits, at least four of them, growing past
// four as needed (WH202610000). The year is today's UTC calendar year,
// never an issue date's.
//
// Go changes this (port note): Python scanned without a lock, so two
// concurrent creates could take the same number. This first takes
// pg_advisory_xact_lock(hashtextextended('invoice-number:<year>', 0)) in
// tx, held until tx ends, so the caller must insert the invoice in the same
// tx. That tx must be READ COMMITTED (the default), so the scan after the
// lock sees the numbers a transaction that held it before has committed.
// And only ASCII digits count as a tail, where Python's isdigit also
// took other scripts' digits.
func NextInvoiceNumber(ctx context.Context, tx pgx.Tx, today time.Time) (string, error) {
	year := strconv.Itoa(today.UTC().Year())
	q := invoicesdb.New(tx)
	if err := q.LockInvoiceNumbers(ctx, year); err != nil {
		return "", err
	}
	prefix := "WH" + year
	numbers, err := q.ListInvoiceNumbersWithPrefix(ctx, prefix)
	if err != nil {
		return "", err
	}
	highest := new(big.Int)
	for _, n := range numbers {
		tail := strings.TrimPrefix(n, prefix)
		if !allDigits(tail) {
			continue
		}
		// Arbitrary length, as Python's int: the column holds 44 digits.
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
