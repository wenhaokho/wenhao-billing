package invoices

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// nextNumber is NextInvoiceNumber in its own committed transaction.
func nextNumber(t *testing.T, pool *pgxpool.Pool, today time.Time) string {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	n, err := NextInvoiceNumber(t.Context(), tx, today)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNextInvoiceNumberFirstOfYear(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// Numbers of other years and invoices without a number do not count.
	insertInvoice(t, a.Pool, strPtr("WH20250042"))
	insertInvoice(t, a.Pool, nil)
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "WH20260001" {
		t.Errorf("number = %s, want WH20260001", got)
	}
}

func TestNextInvoiceNumberSkipsNonDigitTails(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	for _, n := range []string{
		"WH20260005", "WH2026", "WH2026ABC", "WH20260900-A", "WH2026 77", "WH2026+99",
		"WH2026٩٩", // Arabic-Indic 99: Python's isdigit took it, Go counts ASCII digits only
	} {
		insertInvoice(t, a.Pool, strPtr(n))
	}
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "WH20260006" {
		t.Errorf("number = %s, want WH20260006", got)
	}
}

func TestNextInvoiceNumberGrowsPastFourDigits(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	insertInvoice(t, a.Pool, strPtr("WH20269999"))
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "WH202610000" {
		t.Errorf("number = %s, want WH202610000", got)
	}
	// Leading zeros and a tail too long for int64 still count.
	insertInvoice(t, a.Pool, strPtr("WH2026000000000000000000000000041"))
	insertInvoice(t, a.Pool, strPtr("WH202699999999999999999999"))
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "WH2026100000000000000000000" {
		t.Errorf("number = %s, want WH2026100000000000000000000", got)
	}
}

func TestNextInvoiceNumberUsesTodaysYear(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	// An issue date in another year does not matter: only the number's
	// prefix and today's year do.
	id := insertInvoice(t, a.Pool, strPtr("WH20260003"))
	if _, err := a.Pool.Exec(t.Context(), `UPDATE invoices SET issue_date = '2025-12-31' WHERE invoice_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	insertInvoice(t, a.Pool, strPtr("WH20270008"))
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "WH20260004" {
		t.Errorf("number = %s, want WH20260004", got)
	}
	// The year is today's UTC calendar year: 2026-12-31T23:30 at UTC-5 is
	// 2027 in UTC.
	late := time.Date(2026, 12, 31, 23, 30, 0, 0, time.FixedZone("EST", -5*3600))
	if got := nextNumber(t, a.Pool, late); got != "WH20270009" {
		t.Errorf("number = %s, want WH20270009", got)
	}
	a.Clock.Advance(365 * 24 * time.Hour)
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "WH20270009" {
		t.Errorf("number next year = %s, want WH20270009", got)
	}
}

func TestNextInvoiceNumberConcurrent(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	const n = 20
	numbers := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			numbers[i], errs[i] = numberAndInsert(t.Context(), a.Pool, a.Clock.Now())
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	slices.Sort(numbers)
	for i, got := range numbers {
		if want := fmt.Sprintf("WH2026%04d", i+1); got != want {
			t.Errorf("numbers = %v, want WH20260001..WH20260020", numbers)
			break
		}
	}
}

// numberAndInsert takes the next number and stores an invoice with it, in
// one transaction, as create_invoice does.
func numberAndInsert(ctx context.Context, pool *pgxpool.Pool, today time.Time) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	number, err := NextInvoiceNumber(ctx, tx, today)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invoices (invoice_id, invoice_type, currency, amount, balance_due, status, invoice_number)
VALUES (gen_random_uuid(), 'MILESTONE', 'IDR', 0, 0, 'DRAFT', $1)`, number); err != nil {
		return "", err
	}
	return number, tx.Commit(ctx)
}
