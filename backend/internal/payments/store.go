package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wenhaokho/wenhao-billing/backend/internal/matching"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

// Reconciliation log actions.
const (
	actionCleared    = "CLEARED"
	actionHeld       = "HELD"
	actionOverridden = "OVERRIDDEN"
	actionReversed   = "REVERSED"
)

// uniqueSourceRef is the payments unique key on (intake_source, external_ref),
// the only idempotency guard intake has.
const uniqueSourceRef = "uq_payments_source_ref"

// insertPayment stores a new payment; a repeated (intake_source,
// external_ref) is ErrDuplicate. The violation aborts the transaction.
func insertPayment(ctx context.Context, q *paymentsdb.Queries, arg paymentsdb.InsertPaymentParams) (paymentsdb.Payment, error) {
	p, err := q.InsertPayment(ctx, arg)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.ConstraintName == uniqueSourceRef {
		return paymentsdb.Payment{}, ErrDuplicate
	}
	if err != nil {
		return paymentsdb.Payment{}, fmt.Errorf("payments: insert payment: %w", err)
	}
	return p, nil
}

// lockPayment reads payment id FOR UPDATE; an unknown one is Python's
// refusal.
func lockPayment(ctx context.Context, q *paymentsdb.Queries, id uuid.UUID) (paymentsdb.Payment, error) {
	p, err := q.LockPayment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return paymentsdb.Payment{}, refuse("payment %s not found", id)
	}
	if err != nil {
		return paymentsdb.Payment{}, fmt.Errorf("payments: lock payment: %w", err)
	}
	return p, nil
}

// lockInvoice reads invoice id FOR UPDATE; found is false when there is none.
func lockInvoice(ctx context.Context, q *paymentsdb.Queries, id uuid.UUID) (inv paymentsdb.LockInvoiceRow, found bool, err error) {
	inv, err = q.LockInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return paymentsdb.LockInvoiceRow{}, false, nil
	}
	if err != nil {
		return paymentsdb.LockInvoiceRow{}, false, fmt.Errorf("payments: lock invoice: %w", err)
	}
	return inv, true, nil
}

// saveInvoice writes the settlement fields of inv.
func saveInvoice(ctx context.Context, q *paymentsdb.Queries, inv paymentsdb.LockInvoiceRow) error {
	if err := q.SetInvoiceSettlement(ctx, paymentsdb.SetInvoiceSettlementParams{
		InvoiceID: inv.InvoiceID, BalanceDue: inv.BalanceDue, WhtBooked: inv.WhtBooked, Status: inv.Status,
	}); err != nil {
		return fmt.Errorf("payments: update invoice: %w", err)
	}
	return nil
}

// logAction writes a reconciliation log row; reasons nil is stored as {}.
func logAction(ctx context.Context, q *paymentsdb.Queries, paymentID uuid.UUID, action string, reasons []string, actor *uuid.UUID) error {
	if reasons == nil {
		reasons = []string{}
	}
	if err := q.InsertReconLog(ctx, paymentsdb.InsertReconLogParams{
		LogID: uuid.New(), PaymentID: paymentID, Action: action, Reasons: reasons, ActorUserID: pgUUID(actor),
	}); err != nil {
		return fmt.Errorf("payments: insert log: %w", err)
	}
	return nil
}

// getPayment re-reads payment id, so callers answer with the stored values.
func getPayment(ctx context.Context, q *paymentsdb.Queries, id uuid.UUID) (paymentsdb.Payment, error) {
	p, err := q.GetPayment(ctx, id)
	if err != nil {
		return paymentsdb.Payment{}, fmt.Errorf("payments: get payment: %w", err)
	}
	return p, nil
}

// openCandidates are the open invoices in currency as the engine's
// candidates, each presenting its unbooked withholding.
func openCandidates(ctx context.Context, q *paymentsdb.Queries, currency string) ([]matching.Candidate, error) {
	rows, err := q.OpenCandidates(ctx, currency)
	if err != nil {
		return nil, fmt.Errorf("payments: candidates: %w", err)
	}
	cands := make([]matching.Candidate, 0, len(rows))
	for _, r := range rows {
		var aliases []string
		if err := json.Unmarshal(r.MatchingAliases, &aliases); err != nil {
			return nil, fmt.Errorf("payments: customer %s matching_aliases: %w", r.CustomerID, err)
		}
		cands = append(cands, matching.Candidate{
			InvoiceID: r.InvoiceID, CustomerID: r.CustomerID, Currency: r.Currency, Status: r.Status,
			BalanceDue: r.BalanceDue, WhtOutstanding: r.WhtAmount.Sub(r.WhtBooked),
			IssueDate: timePtr(r.IssueDate), DueDate: timePtr(r.DueDate),
			CustomerName: r.CustomerName, Aliases: aliases,
		})
	}
	return cands, nil
}

func pgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func timePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	return &d.Time
}

// pgDate is t's calendar date.
func pgDate(t time.Time) pgtype.Date {
	y, m, d := t.Date()
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}
