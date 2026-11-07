package platform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
)

// DeliverScopedRecurringDrafts drains one business's queue. A cancelled context
// after provider acceptance leaves the lease for recovery and possible redelivery.
func DeliverScopedRecurringDrafts(ctx context.Context, pool *pgxpool.Pool, scope business.ID, sender email.Sender, baseURL string) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if sender == nil {
		return errors.New("notification sender is required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		claim, found, err := claimScopedRecurringDraft(ctx, pool, scope)
		if err != nil || !found {
			return err
		}
		if err := prepareScopedRecurringDraft(ctx, pool, scope, &claim); err != nil {
			return err
		}
		if len(claim.items) == 0 {
			continue
		}
		message, err := recurringDraftMessage(claim, baseURL)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		sendErr := sender.Send(sendCtx, message)
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := finishScopedRecurringDraft(ctx, pool, scope, claim, sendErr == nil); err != nil {
			return err
		}
	}
}

func claimScopedRecurringDraft(ctx context.Context, pool *pgxpool.Pool, scope business.ID) (recurringDraftClaim, bool, error) {
	var claim recurringDraftClaim
	if err := scope.Validate(); err != nil {
		return claim, false, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return claim, false, err
	}
	defer tx.Rollback(context.Background())
	var locked bool
	err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`, scopedRecurringJob(scope)).Scan(&locked)
	if err != nil || !locked {
		return claim, false, err
	}

	var invoice, batch pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT d.invoice_id, d.user_id, e.scan_batch_id
		FROM recurring_draft_deliveries d
		JOIN recurring_draft_events e ON e.business_id=d.business_id AND e.invoice_id=d.invoice_id
		WHERE d.business_id=$1 AND d.delivered_at IS NULL AND d.cancelled_at IS NULL
		  AND d.next_attempt_at<=now() AND (d.lease_until IS NULL OR d.lease_until<=now())
		ORDER BY d.next_attempt_at, d.invoice_id, d.user_id
		FOR UPDATE OF d SKIP LOCKED LIMIT 1`, scope.UUID()).Scan(&invoice, &claim.user, &batch)
	if errors.Is(err, pgx.ErrNoRows) {
		return claim, false, nil
	}
	if err != nil {
		return claim, false, err
	}
	claim.token, err = itemUUID()
	if err != nil {
		return claim, false, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE recurring_draft_deliveries d SET lease_token=$5, lease_until=now()+interval '2 minutes'
		FROM recurring_draft_events e
		WHERE d.business_id=$1 AND e.business_id=d.business_id AND e.invoice_id=d.invoice_id
		  AND d.user_id=$2 AND (d.invoice_id=$3 OR e.scan_batch_id=$4)
		  AND d.delivered_at IS NULL AND d.cancelled_at IS NULL
		  AND d.next_attempt_at<=now() AND (d.lease_until IS NULL OR d.lease_until<=now())`,
		scope.UUID(), claim.user, invoice, batch, claim.token)
	if err != nil {
		return claim, false, err
	}
	return claim, true, tx.Commit(ctx)
}

// prepareScopedRecurringDraft rechecks live ownership and refreshes the email
// before the provider call. Only rows still held by this claim can change.
func prepareScopedRecurringDraft(ctx context.Context, pool *pgxpool.Pool, scope business.ID, claim *recurringDraftClaim) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	claim.items = nil
	claim.address = ""
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	err = tx.QueryRow(ctx, `SELECT email FROM users WHERE business_id=$1 AND user_id=$2 FOR SHARE`, scope.UUID(), claim.user).Scan(&claim.address)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	// Cancellation and renewal use the same locked invoice set, including any
	// concurrent deletion resolved by the row lock.
	rows, err := tx.Query(ctx, `
		WITH live_invoices AS MATERIALIZED (
			SELECT i.business_id, i.invoice_id FROM invoices i
			JOIN recurring_draft_deliveries d ON d.business_id=i.business_id AND d.invoice_id=i.invoice_id
			WHERE i.business_id=$1 AND d.user_id=$2 AND d.lease_token=$3 AND $4 <> ''
			  AND d.lease_until>clock_timestamp() AND d.delivered_at IS NULL AND d.cancelled_at IS NULL
			FOR KEY SHARE OF i
		), cancelled AS (
			UPDATE recurring_draft_deliveries d
			SET cancelled_at=now(), email='', lease_token=NULL, lease_until=NULL,
			    last_error=CASE WHEN $4 = '' THEN 'recipient unavailable' ELSE 'invoice unavailable' END
			WHERE d.business_id=$1 AND d.user_id=$2 AND d.lease_token=$3 AND d.lease_until>clock_timestamp()
			  AND d.delivered_at IS NULL AND d.cancelled_at IS NULL
			  AND NOT EXISTS (
			      SELECT 1 FROM live_invoices i WHERE i.business_id=d.business_id AND i.invoice_id=d.invoice_id
			  )
		)
		UPDATE recurring_draft_deliveries d
		SET email=$4, lease_until=clock_timestamp()+interval '2 minutes', attempts=attempts+1
		FROM recurring_draft_events e
		JOIN live_invoices i ON i.business_id=e.business_id AND i.invoice_id=e.invoice_id
		WHERE d.business_id=$1 AND e.business_id=d.business_id AND e.invoice_id=d.invoice_id
		  AND d.user_id=$2 AND d.lease_token=$3 AND d.lease_until>clock_timestamp()
		  AND d.delivered_at IS NULL AND d.cancelled_at IS NULL
		RETURNING d.invoice_id, e.invoice_number, e.customer_name, e.amount::text, e.currency, e.cycle_key`,
		scope.UUID(), claim.user, claim.token, claim.address)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (recurringDraftItem, error) {
		var item recurringDraftItem
		err := row.Scan(&item.invoice, &item.number, &item.customer, &item.amount, &item.currency, &item.cycle)
		return item, err
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	claim.items = items
	return nil
}

func finishScopedRecurringDraft(ctx context.Context, pool *pgxpool.Pool, scope business.ID, claim recurringDraftClaim, delivered bool) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	invoices := make([]pgtype.UUID, 0, len(claim.items))
	for _, item := range claim.items {
		invoices = append(invoices, item.invoice)
	}
	_, err := pool.Exec(ctx, `
		UPDATE recurring_draft_deliveries
		SET delivered_at=CASE WHEN $4 THEN now() ELSE NULL END,
		    lease_token=NULL, lease_until=NULL,
		    last_error=CASE WHEN $4 THEN NULL ELSE 'delivery failed' END,
		    next_attempt_at=CASE WHEN $4 THEN next_attempt_at
		        ELSE now()+make_interval(secs => LEAST(3600, 60 * power(2,LEAST(attempts-1,6))::int))
		    END
		WHERE business_id=$1 AND user_id=$2 AND lease_token=$3 AND lease_until>clock_timestamp()
		  AND invoice_id=ANY($5::uuid[])
		  AND delivered_at IS NULL AND cancelled_at IS NULL`, scope.UUID(), claim.user, claim.token, delivered, invoices)
	return err
}
