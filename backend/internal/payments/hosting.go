package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HostingRestorer re-activates the hosting subscription an invoice pays for,
// as Python's ensure_hosting_restored does on every transition to PAID. It
// runs in the caller's transaction; an error fails the whole payment.
type HostingRestorer interface {
	RestoreIfPaid(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) error
}

// NoHosting restores nothing: hosting is not ported yet (the integrations
// plan replaces it). The switch-over must not happen before that plan lands.
type NoHosting struct{}

// RestoreIfPaid does nothing.
func (NoHosting) RestoreIfPaid(context.Context, pgx.Tx, uuid.UUID) error { return nil }
