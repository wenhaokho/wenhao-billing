package quotations

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

// NextQuotationNumber is the next free number of today's year,
// Q{YYYY}{NNNN} (quoting.py:73-89): platform.NextSequence over the
// quotation numbers starting "Q{YYYY}" (at least four digits, growing past
// four as needed: Q202610000). The year is today's UTC calendar year, never
// an issue date's. Invoice numbers are a separate sequence and never
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
	return platform.NextSequence(prefix, numbers), nil
}
