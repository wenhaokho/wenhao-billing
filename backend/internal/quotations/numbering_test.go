package quotations_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
)

// nextNumber is NextQuotationNumber in its own committed transaction.
func nextNumber(t *testing.T, pool *pgxpool.Pool, today time.Time) string {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	n, err := quotations.NextQuotationNumber(t.Context(), tx, today)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return n
}

// insertNumbered stores a quotation numbered number for customer.
func insertNumbered(t *testing.T, a *apitest.App, customer uuid.UUID, number string) {
	t.Helper()
	exec(t, a, `INSERT INTO quotations (quotation_id, customer_id, currency, amount, quotation_number)
VALUES (gen_random_uuid(), $1, 'IDR', 0, $2)`, customer, number)
}

func TestNextQuotationNumberIgnoresInvoiceNumbers(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	// Invoices are a separate sequence, even one numbered like a quotation.
	exec(t, a, `INSERT INTO invoices (invoice_id, invoice_type, currency, amount, balance_due, status, invoice_number)
VALUES (gen_random_uuid(), 'MILESTONE', 'IDR', 0, 0, 'DRAFT', 'Q20260042'), (gen_random_uuid(), 'MILESTONE', 'IDR', 0, 0, 'DRAFT', 'WH20260007')`)
	// Other years, quotations without a number and non-digit tails do not count.
	exec(t, a, `INSERT INTO quotations (quotation_id, customer_id, currency, amount) VALUES (gen_random_uuid(), $1, 'IDR', 0)`, c)
	for _, n := range []string{
		"Q20250042", "QUO-0001", "Q2026", "Q2026ABC", "Q20260900-A", "Q2026 77", "Q2026+99",
		"Q2026٩٩", // Arabic-Indic 99: Python's isdigit took it (and int() then failed), Go counts ASCII digits only
	} {
		insertNumbered(t, a, c, n)
	}
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "Q20260001" {
		t.Errorf("number = %s, want Q20260001", got)
	}
	insertNumbered(t, a, c, "Q20260005")
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "Q20260006" {
		t.Errorf("number = %s, want Q20260006", got)
	}
	// Past four digits, and leading zeros count.
	insertNumbered(t, a, c, "Q20269999")
	if got := nextNumber(t, a.Pool, a.Clock.Now()); got != "Q202610000" {
		t.Errorf("number = %s, want Q202610000", got)
	}
	// The year is today's UTC calendar year: 2026-12-31T23:30 at UTC-5 is
	// 2027 in UTC.
	late := time.Date(2026, 12, 31, 23, 30, 0, 0, time.FixedZone("EST", -5*3600))
	if got := nextNumber(t, a.Pool, late); got != "Q20270001" {
		t.Errorf("number = %s, want Q20270001", got)
	}
}

func TestNextQuotationNumberConcurrent(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	c := insertCustomer(t, a)
	const n = 20
	numbers := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			numbers[i], errs[i] = numberAndInsert(t.Context(), a.Pool, c, a.Clock.Now())
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
		if want := fmt.Sprintf("Q2026%04d", i+1); got != want {
			t.Errorf("numbers = %v, want Q20260001..Q20260020", numbers)
			break
		}
	}
}

// numberAndInsert takes the next number and stores a quotation with it, in
// one transaction, as the create route does.
func numberAndInsert(ctx context.Context, pool *pgxpool.Pool, customer uuid.UUID, today time.Time) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	number, err := quotations.NextQuotationNumber(ctx, tx, today)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO quotations (quotation_id, customer_id, currency, amount, quotation_number)
VALUES (gen_random_uuid(), $1, 'IDR', 0, $2)`, customer, number); err != nil {
		return "", err
	}
	return number, tx.Commit(ctx)
}
