package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HostingRestorer re-activates the hosting an invoice pays for, as Python's
// ensure_hosting_restored does on every transition to PAID, split in two so
// Cloudflare never fails a payment (Go change 1):
//   - StampPaid runs in the payment's transaction (hosting_last_paid_at); its
//     error fails the payment, as any database error does;
//   - RestoreAfterCommit runs after the transaction committed, best effort:
//     it never fails the payment and reports its own failures. It is told
//     of the invoice of every payment that committed CLEARED, and acts only
//     when that invoice is paid (balance_due <= 0) as committed.
type HostingRestorer interface {
	StampPaid(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) error
	RestoreAfterCommit(ctx context.Context, invoiceID uuid.UUID)
}

// NoHosting stamps and restores nothing (tests that do not exercise hosting).
type NoHosting struct{}

// StampPaid does nothing.
func (NoHosting) StampPaid(context.Context, pgx.Tx, uuid.UUID) error { return nil }

// RestoreAfterCommit does nothing.
func (NoHosting) RestoreAfterCommit(context.Context, uuid.UUID) {}
