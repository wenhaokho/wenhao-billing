package invoices

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// NextInvoiceNumber is the next free number of today's year,
// WH{YYYY}{NNNN} (invoicing.py:157-171): platform.NextSequence over the
// invoice numbers starting "WH{YYYY}" (at least four digits, growing past
// four as needed: WH202610000). The year is today's UTC calendar year,
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
//
// The number lock comes last in the package's lock order (see Trigger):
// a caller may hold a template's advisory lock or an invoice row FOR UPDATE
// when it calls this, and takes neither after it.
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
	return platform.NextSequence(prefix, numbers), nil
}
