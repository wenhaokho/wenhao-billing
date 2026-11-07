package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type UsageMeter interface {
	Accrued(context.Context, pgtype.UUID) (decimal.Decimal, error)
}
type UsageMeterFunc func(context.Context, pgtype.UUID) (decimal.Decimal, error)

func (f UsageMeterFunc) Accrued(ctx context.Context, id pgtype.UUID) (decimal.Decimal, error) {
	return f(ctx, id)
}

type UsageFailureCategory string

const (
	UsageMeterFailed         UsageFailureCategory = "meter_failed"
	UsageInvalidBillingRef   UsageFailureCategory = "invalid_billing_ref"
	UsageInvoiceReadFailed   UsageFailureCategory = "invoice_read_failed"
	UsageInvoiceUpdateFailed UsageFailureCategory = "invoice_update_failed"
)

type UsageInvoiceFailure struct {
	InvoiceID string
	Category  UsageFailureCategory
}
type UsageScanError struct {
	Locked   int
	Failures []UsageInvoiceFailure
}

func (e *UsageScanError) Error() string {
	return fmt.Sprintf("usage cutoff scan: locked=%d failed=%d", e.Locked, len(e.Failures))
}

// ScanUsageCutoff isolates failures per invoice; successful invoices commit independently.
// now uses the server's local date, matching Python date.today().
func ScanUsageCutoff(ctx context.Context, pool *pgxpool.Pool, now time.Time, meter UsageMeter) (int, error) {
	if meter == nil {
		return 0, errors.New("usage meter is not configured")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	key := int64(crc32.ChecksumIEEE([]byte("usage-lock-cutoff-scan")))
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, nil
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key) }()
	rows, err := conn.Query(ctx, `SELECT invoice_id FROM invoices WHERE invoice_type='USAGE' AND status='DRAFT' ORDER BY invoice_id`)
	if err != nil {
		return 0, err
	}
	return scanUsageCandidates(ctx, rows, now, func(ctx context.Context, id pgtype.UUID, cutoff time.Time) (bool, UsageFailureCategory, error) {
		return scanUsageInvoice(ctx, conn, id, cutoff, meter)
	})
}

func scanUsageCandidates(ctx context.Context, rows pgx.Rows, now time.Time, scan func(context.Context, pgtype.UUID, time.Time) (bool, UsageFailureCategory, error)) (int, error) {
	var err error
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	cutoff := now.AddDate(0, 0, -1)
	count := 0
	var failures []UsageInvoiceFailure
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return count, err
		}
		ok, category, e := scan(ctx, id, cutoff)
		if e != nil {
			if ctx.Err() != nil {
				return count, ctx.Err()
			}
			value, _ := id.Value()
			failures = append(failures, UsageInvoiceFailure{InvoiceID: value.(string), Category: category})
			continue
		}
		if ok {
			count++
		}
	}
	if len(failures) > 0 {
		return count, &UsageScanError{Locked: count, Failures: failures}
	}
	return count, nil
}
func scanUsageInvoice(ctx context.Context, conn *pgxpool.Conn, id pgtype.UUID, cutoff time.Time, meter UsageMeter) (bool, UsageFailureCategory, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, UsageInvoiceReadFailed, err
	}
	defer tx.Rollback(context.Background())
	q := dbquery.New(tx)
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
	amount, err := meter.Accrued(ctx, id)
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

func validMeterAmount(amount decimal.Decimal) bool {
	if amount.IsZero() {
		return true
	}
	// Reject magnitudes outside the storage range before rescaling.
	digitsBeforeDecimal := int64(amount.NumDigits()) + int64(amount.Exponent())
	return !amount.IsNegative() && digitsBeforeDecimal > -4 && digitsBeforeDecimal <= 15 &&
		amount.Equal(amount.Truncate(4))
}

func usageDue(raw []byte, day int) (bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return false, err
	}
	if !pythonTruthy(v) {
		return false, nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return false, errors.New("invalid billing reference")
	}
	if x := obj["locked"]; pythonTruthy(x) {
		return false, nil
	}
	x, ok := obj["cut_off_day"]
	if !ok || x == nil {
		return false, nil
	}
	var n int
	switch v := x.(type) {
	case json.Number:
		f, e := strconv.ParseFloat(string(v), 64)
		if e != nil {
			return false, e
		}
		n = int(f)
	case string:
		i, e := strconv.Atoi(strings.TrimSpace(v))
		if e != nil {
			return false, e
		}
		n = i
	case bool:
		if v {
			n = 1
		}
	default:
		return false, errors.New("invalid cutoff day")
	}
	return n == day, nil
}
func pythonTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		d, e := decimal.NewFromString(string(x))
		return e != nil || !d.IsZero()
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}
