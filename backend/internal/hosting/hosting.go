package hosting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/observe"

	"github.com/wenhaokho/wenhao-billing/backend/internal/hosting/hostingdb"
)

// Hosting statuses (items.hosting_status) and provider statuses
// (cloudflare_targets.provider_status) this package reads or writes.
const (
	statusActive         = "ACTIVE"
	statusSuspendPending = "SUSPEND_PENDING"
	statusSuspended      = "SUSPENDED"
	statusCancelled      = "CANCELLED"
	providerError        = "ERROR"
)

// errNoTarget is Python's HostingError for an overdue item with no target.
var errNoTarget = errors.New("hosting item is missing a Cloudflare target")

// now is a write's stamp: UTC at seconds precision (Python's _utcnow).
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// item is a hosting item and its target (nil: none), locked in q's
// transaction.
type item struct {
	hostingdb.LockItemRow
	target *hostingdb.CloudflareTarget
}

func (it item) status() string {
	if it.HostingStatus == nil {
		return ""
	}
	return *it.HostingStatus
}

// suspensionOn is Python's truthy hosting_suspension_enabled (NULL is off).
func (it item) suspensionOn() bool {
	return it.HostingSuspensionEnabled != nil && *it.HostingSuspensionEnabled
}

func (it item) cfTarget() Target {
	t := it.target
	return Target{
		ZoneID: t.ZoneID, RecordID: t.RecordID, RecordName: t.RecordName, RecordType: t.RecordType,
		LiveContent: t.LiveContent, MaintenanceContent: t.MaintenanceContent, Proxied: t.Proxied,
	}
}

// withItem runs fn on the locked item in one transaction. The Cloudflare
// call happens inside it, so enforcement and a payment's restore never act
// on one item at once. A Cloudflare failure fn returns is committed with the
// ERROR stamps fn wrote before it (Python flushes them before re-raising,
// and the caller commits), then returned; any other error rolls back.
// An unknown item does nothing.
func withItem(ctx context.Context, pool *pgxpool.Pool, itemID uuid.UUID, fn func(q *hostingdb.Queries, it item) (bool, error)) (bool, error) {
	var done bool
	var cfErr error
	err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		q := hostingdb.New(tx)
		row, err := q.LockItem(ctx, itemID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("hosting: lock item: %w", err)
		}
		it := item{LockItemRow: row}
		t, err := q.LockTarget(ctx, itemID)
		switch {
		case err == nil:
			it.target = &t
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("hosting: lock target: %w", err)
		}
		done, err = fn(q, it)
		if errors.Is(err, ErrCloudflare) {
			cfErr = err
			return nil
		}
		return err
	})
	if err != nil {
		return false, err
	}
	return done, cfErr
}

// suspend is Python's suspend_hosting on the locked item: nothing when
// suspension is off or the item CANCELLED, errNoTarget without a target,
// nothing when item and target are both SUSPENDED. A Cloudflare failure
// leaves the item SUSPEND_PENDING and the target ERROR with the sentence
// (the next run retries); success makes both SUSPENDED.
func suspend(ctx context.Context, q *hostingdb.Queries, cl *Client, it item) (bool, error) {
	if !it.suspensionOn() || it.status() == statusCancelled {
		return false, nil
	}
	if it.target == nil {
		return false, errNoTarget
	}
	if it.status() == statusSuspended && it.target.ProviderStatus == statusSuspended {
		return false, nil
	}
	if cfErr := cl.Suspend(ctx, it.cfTarget()); cfErr != nil {
		if err := setItem(ctx, q, it, statusSuspendPending); err != nil {
			return false, err
		}
		return false, failTarget(ctx, q, it, cfErr)
	}
	if err := setItem(ctx, q, it, statusSuspended); err != nil {
		return false, err
	}
	return true, setTarget(ctx, q, it, statusSuspended, nil)
}

// restore is Python's restore_hosting_if_eligible on the locked item, with
// Go change 2: nothing when the item is not a hosting item, suspension is off
// or the item CANCELLED (a hand-set suspension stays), nothing without a
// target, while any open invoice for the item has a balance, or when item
// and target are both ACTIVE. A Cloudflare failure marks the target ERROR
// with the sentence; success makes both ACTIVE.
func restore(ctx context.Context, q *hostingdb.Queries, cl *Client, it item) (bool, error) {
	if !it.IsHosting || !it.suspensionOn() || it.status() == statusCancelled || it.target == nil {
		return false, nil
	}
	open, err := q.OpenInvoicesForItem(ctx, it.ItemID)
	if err != nil {
		return false, fmt.Errorf("hosting: open invoices: %w", err)
	}
	if len(open) > 0 {
		return false, nil
	}
	if it.status() == statusActive && it.target.ProviderStatus == statusActive {
		return false, nil
	}
	if cfErr := cl.Restore(ctx, it.cfTarget()); cfErr != nil {
		return false, failTarget(ctx, q, it, cfErr)
	}
	if err := setItem(ctx, q, it, statusActive); err != nil {
		return false, err
	}
	return true, setTarget(ctx, q, it, statusActive, nil)
}

func setItem(ctx context.Context, q *hostingdb.Queries, it item, status string) error {
	if err := q.SetItemStatus(ctx, hostingdb.SetItemStatusParams{ItemID: it.ItemID, HostingStatus: status}); err != nil {
		return fmt.Errorf("hosting: set item status: %w", err)
	}
	return nil
}

func setTarget(ctx context.Context, q *hostingdb.Queries, it item, status string, lastError *string) error {
	if err := q.SetTargetResult(ctx, hostingdb.SetTargetResultParams{
		TargetID: it.target.TargetID, ProviderStatus: status, LastError: lastError, Now: now(),
	}); err != nil {
		return fmt.Errorf("hosting: set target: %w", err)
	}
	return nil
}

// failTarget marks the target ERROR with cfErr's sentence and returns
// cfErr (or the write's error).
func failTarget(ctx context.Context, q *hostingdb.Queries, it item, cfErr error) error {
	msg := cfErr.Error()
	if err := setTarget(ctx, q, it, providerError, &msg); err != nil {
		return err
	}
	return cfErr
}

// Restorer is payments.HostingRestorer (Go change 1): the payment's
// transaction only stamps hosting_last_paid_at; the Cloudflare restore runs
// after the commit, best effort.
type Restorer struct {
	Pool   *pgxpool.Pool
	Client *Client
	// Logger logs a failed restore (nil: slog's default).
	Logger *slog.Logger
}

// StampPaid stamps hosting_last_paid_at (UTC, seconds) on every hosting item
// on the paid invoice's lines, in tx (kept from Python: whether or not it is
// restored). An invoice with a balance stamps nothing.
func (r Restorer) StampPaid(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) error {
	if err := hostingdb.New(tx).StampPaid(ctx, hostingdb.StampPaidParams{PaidAt: now(), InvoiceID: invoiceID}); err != nil {
		return fmt.Errorf("hosting: stamp paid: %w", err)
	}
	return nil
}

// RestoreAfterCommit restores each hosting item on the paid invoice's lines
// that is eligible (Python's ensure_hosting_restored without the stamp). It
// runs after the payment committed and never fails it: a Cloudflare failure
// marks the target ERROR (the daily job retries), and every failure is
// logged and reported. The caller's cancellation does not cut it short; the
// Cloudflare timeout bounds it.
func (r Restorer) RestoreAfterCommit(ctx context.Context, invoiceID uuid.UUID) {
	ctx = context.WithoutCancel(ctx)
	ids, err := hostingdb.New(r.Pool).PaidInvoiceHostingItems(ctx, invoiceID)
	if err != nil {
		r.failed(ctx, invoiceID, uuid.Nil, fmt.Errorf("hosting: paid invoice items: %w", err))
		return
	}
	for _, id := range ids {
		_, err := withItem(ctx, r.Pool, id, func(q *hostingdb.Queries, it item) (bool, error) {
			return restore(ctx, q, r.Client, it)
		})
		if err != nil {
			r.failed(ctx, invoiceID, id, err)
		}
	}
}

func (r Restorer) failed(ctx context.Context, invoiceID, itemID uuid.UUID, err error) {
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.ErrorContext(ctx, "hosting restore after payment failed; the daily enforcement job retries",
		"invoice_id", invoiceID, "item_id", itemID, "err", err)
	observe.Report(err, map[string]string{"component": "hosting", "stage": "restore_after_payment"})
}
