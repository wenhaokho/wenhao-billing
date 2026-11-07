package platform

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
)

type ScopedUsageMeter interface {
	Accrued(context.Context, business.ID, pgtype.UUID) (decimal.Decimal, error)
}

type ScopedUsageMeterFunc func(context.Context, business.ID, pgtype.UUID) (decimal.Decimal, error)

func (f ScopedUsageMeterFunc) Accrued(ctx context.Context, scope business.ID, invoice pgtype.UUID) (decimal.Decimal, error) {
	return f(ctx, scope, invoice)
}

func scopedUsageMeterConfigured(meter ScopedUsageMeter) bool {
	switch value := reflect.ValueOf(meter); value.Kind() {
	case reflect.Invalid:
		return false
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		return !value.IsNil()
	default:
		return true
	}
}

func scopedUsageJob(scope business.ID) string {
	return "billing:v8:usage-lock-cutoff-scan:" + scope.String()
}

// ScanScopedUsageCutoff is an isolated v8 entry point. It uses the caller's local
// calendar and requires an injected meter. The meter should read a stable
// accrued amount and honor cancellation because a failed transaction can retry.
func ScanScopedUsageCutoff(ctx context.Context, pool *pgxpool.Pool, scope business.ID, now time.Time, meter ScopedUsageMeter) (int, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	if !scopedUsageMeterConfigured(meter) {
		return 0, errors.New("usage meter is not configured")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	job := scopedUsageJob(scope)
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, job).Scan(&locked); err != nil {
		// Lock acquisition may have reached Postgres before cancellation.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Conn().Close(cleanup)
		return 0, err
	}
	if !locked {
		return 0, nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, job); err != nil {
			_ = conn.Conn().Close(cleanup)
		}
	}()

	rows, err := conn.Query(ctx, `SELECT invoice_id FROM invoices
WHERE business_id=$1 AND invoice_type='USAGE' AND status='DRAFT' ORDER BY invoice_id`, scope.UUID())
	if err != nil {
		return 0, err
	}
	return scanUsageCandidates(ctx, rows, now, func(ctx context.Context, id pgtype.UUID, cutoff time.Time) (bool, UsageFailureCategory, error) {
		return scanScopedUsageInvoice(ctx, conn, scope, id, cutoff, meter)
	})
}

const UsageInvalidReference UsageFailureCategory = "invalid_reference"

func scanScopedUsageInvoice(ctx context.Context, conn *pgxpool.Conn, scope business.ID, id pgtype.UUID, cutoff time.Time, meter ScopedUsageMeter) (bool, UsageFailureCategory, error) {
	tx, err := (candidateTransactions{db: conn, scope: scope}).Begin(ctx)
	if err != nil {
		return false, UsageInvoiceReadFailed, err
	}
	defer tx.Rollback(context.Background())
	q := scopedInvoiceWriteQueries{tx: tx, scope: scope}
	row, err := q.LockInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, UsageInvoiceReadFailed, err
	}
	if row.InvoiceType != "USAGE" || row.Status != "DRAFT" {
		return false, "", nil
	}
	due, err := usageDue(row.BillingCycleRef, cutoff.Day())
	if err != nil {
		return false, UsageInvalidBillingRef, err
	}
	if !due {
		return false, "", nil
	}
	if err = lockUsageReferences(ctx, q, row); err != nil {
		return false, UsageInvalidReference, err
	}
	amount, err := meter.Accrued(ctx, scope, id)
	if err != nil {
		return false, UsageMeterFailed, err
	}
	if !validMeterAmount(amount) {
		return false, UsageMeterFailed, errors.New("accrued usage must fit DECIMAL(19,4)")
	}
	if err = applyUsageLock(ctx, q, row, amount, cutoff.Format("2006-01-02")); err != nil {
		return false, UsageInvoiceUpdateFailed, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, UsageInvoiceUpdateFailed, err
	}
	return true, "", nil
}
