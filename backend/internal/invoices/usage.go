package invoices

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// UsageLockRequest is the body of a usage lock (schemas/invoice.py).
type UsageLockRequest struct {
	AccruedAmount platform.Dec4 `json:"accrued_amount" doc:"The usage accrued for the period; not negative."`
	CutoffDate    platform.Date `json:"cutoff_date"`
}

// lockUsage is Python's lock_usage_invoice (invoicing.py:604-640): a DRAFT
// USAGE invoice is frozen at the accrued amount. Withholding is recomputed
// from the stored snapshot (type, rate, gross-up; not re-normalized), the
// amount and balance become the total, and the cycle config gains the
// cut-off date and locked: true. The status stays DRAFT, the subtotal and
// lines are untouched, and locking a DRAFT again is allowed.
func lockUsage(ctx context.Context, tx pgx.Tx, id uuid.UUID, accrued decimal.Decimal, cutoffDate time.Time) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	inv, err := lock(ctx, q, id)
	if err != nil {
		return InvoiceOut{}, err
	}
	if inv.InvoiceType != "USAGE" {
		return InvoiceOut{}, refuse("invoice_not_usage", "invoice %s is not USAGE", id)
	}
	if inv.Status != "DRAFT" {
		return InvoiceOut{}, refuse("usage_invoice_locked", "usage invoice already locked (status=%s)", inv.Status)
	}
	var rate *decimal.Decimal
	if inv.WhtRate.Valid {
		rate = &inv.WhtRate.Decimal
	}
	wht := withholding.Compute(accrued, inv.Currency, inv.WhtType, rate, inv.WhtGrossUp)

	cfg := storedConfig(inv.BillingCycleRef)
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["cutoff_date"] = cutoffDate.Format(time.DateOnly)
	cfg["locked"] = true
	ref, err := json.Marshal(cfg)
	if err != nil {
		return InvoiceOut{}, err
	}
	err = q.LockUsageInvoice(ctx, invoicesdb.LockUsageInvoiceParams{
		InvoiceID: id, WhtBase: decimal.NewNullDecimal(wht.Base), WhtAmount: wht.Amount, Total: wht.Total, BillingCycleRef: ref,
	})
	if err != nil {
		return InvoiceOut{}, err
	}
	return Get(ctx, q, id)
}

type usageLockIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
	Body      UsageLockRequest
}

func (r routes) lockUsage(ctx context.Context, in *usageLockIn) (*invoiceBody, error) {
	if in.Body.AccruedAmount.Decimal().IsNegative() {
		return nil, platform.FieldError("accrued_amount", negativeSentence)
	}
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) {
		return lockUsage(ctx, tx, in.InvoiceID, in.Body.AccruedAmount.Decimal(), in.Body.CutoffDate.Time)
	})
}

// AccrualSource is the usage accrued on an invoice for its period (the
// usage-metering hook of workers/tasks/usage_lock.py).
type AccrualSource func(ctx context.Context, invoiceID uuid.UUID) (decimal.Decimal, error)

// CutoffScan is cutoff_scan (workers/tasks/usage_lock.py:27-52): every DRAFT
// USAGE invoice that is not locked and whose cut_off_day is yesterday's day
// of the month is locked at the amount accrued reports, with yesterday as
// its cut-off date; the invoices are visited oldest first. It returns how
// many it locked. As in Python the whole run is one transaction: an accrual
// (or lock) error ends it and nothing is changed. today is taken as its
// UTC calendar date. A cut_off_day that is not an integer is logged and its
// invoice skipped.
func CutoffScan(ctx context.Context, pool *pgxpool.Pool, today time.Time, accrued AccrualSource) (int, error) {
	yesterday := utcDate(today).AddDate(0, 0, -1)
	locked := 0
	err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		locked = 0 // a retried transaction starts over
		rows, err := invoicesdb.New(tx).ListUsageDrafts(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			cfg := storedConfig(row.BillingCycleRef)
			if truthy(cfg["locked"]) {
				continue
			}
			dayOfMonth, ok := cutOffDay(cfg["cut_off_day"])
			if !ok {
				slog.WarnContext(ctx, "usage cut-off: cut_off_day is not an integer; invoice skipped",
					"invoice_id", row.InvoiceID, "cut_off_day", cfg["cut_off_day"])
				continue
			}
			if dayOfMonth != int64(yesterday.Day()) {
				continue
			}
			amount, err := accrued(ctx, row.InvoiceID)
			if err != nil {
				return fmt.Errorf("accrued usage of invoice %s: %w", row.InvoiceID, err)
			}
			if _, err := lockUsage(ctx, tx, row.InvoiceID, amount, yesterday); err != nil {
				return err
			}
			locked++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return locked, nil
}

// cutOffDay is Python's int(cut_off_day) for what JSON holds: a whole
// number, or a string of digits (surrounding whitespace and a sign allowed).
// A missing or null day reports ok = true with a day no month has, so the
// invoice is skipped silently; anything else, a fraction or boolean
// included, is not ok.
func cutOffDay(v any) (day int64, ok bool) {
	switch v := v.(type) {
	case nil:
		return -1, true
	case json.Number:
		n, err := strconv.ParseInt(v.String(), 10, 64)
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// truthy is Python's truth value of a decoded JSON value.
func truthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case json.Number:
		f, err := v.Float64()
		return err != nil || f != 0
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}
