package platform

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errStaleHostingCandidate = errors.New("hosting restore candidate claimed by another worker")

const maxStaleHostingCandidates = 32

type hostingClaim struct {
	invoice, item, token       pgtype.UUID
	target                     HostingRestoreTarget
	targetID                   pgtype.UUID
	itemStatus, providerStatus string
}

// ConsumeHostingRestoreIntents processes currently due work. Delivery is
// at-least-once; a crash after the PATCH may cause the same PATCH to be repeated.
func ConsumeHostingRestoreIntents(ctx context.Context, pool *pgxpool.Pool, provider HostingRestoreProvider) error {
	if pool == nil || provider == nil {
		return errors.New("hosting restore dependencies required")
	}
	staleCandidates := 0
	for {
		c, found, err := claimHostingRestore(ctx, pool)
		if errors.Is(err, errStaleHostingCandidate) {
			staleCandidates++
			if staleCandidates >= maxStaleHostingCandidates {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		staleCandidates = 0
		if !c.token.Valid {
			continue
		}
		err = provider.Restore(ctx, c.target)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e := finishHostingRestore(ctx, pool, c, err); e != nil {
			return e
		}
	}
}

func claimHostingRestore(ctx context.Context, pool *pgxpool.Pool) (hostingClaim, bool, error) {
	var c hostingClaim
	tx, err := pool.Begin(ctx)
	if err != nil {
		return c, false, err
	}
	defer tx.Rollback(context.Background())
	// Choose without locking, then acquire the item before the intent, matching
	// payment's item-before-intent order. The item serializes claims for that item.
	err = tx.QueryRow(ctx, `SELECT d.invoice_id,d.item_id FROM hosting_restore_intents d WHERE d.next_attempt_at<=now() AND (d.lease_until IS NULL OR d.lease_until<=now()) AND NOT EXISTS (SELECT 1 FROM hosting_restore_intents active WHERE active.item_id=d.item_id AND active.lease_token IS NOT NULL AND active.lease_until>now()) ORDER BY d.created_at,d.invoice_id,d.item_id LIMIT 1`).Scan(&c.invoice, &c.item)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	var hosting bool
	err = tx.QueryRow(ctx, `SELECT is_hosting,coalesce(hosting_status,'') FROM items WHERE item_id=$1 FOR UPDATE`, c.item).Scan(&hosting, &c.itemStatus)
	if err != nil {
		return c, false, err
	}
	drift, err := hostingLeaseRecoveryDrift(ctx, tx, c.item)
	if err != nil {
		return c, false, err
	}
	if drift {
		if _, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET manual_review_required=true WHERE item_id=$1`, c.item); err != nil {
			return c, false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE hosting_restore_intents SET next_attempt_at=now()+interval '1 minute',lease_token=NULL,lease_until=NULL WHERE invoice_id=$1 AND item_id=$2`, c.invoice, c.item); err != nil {
			return c, false, err
		}
		return c, true, tx.Commit(ctx)
	}
	// An expired restore claim also expires its shared operation lease.
	if _, err = tx.Exec(ctx, `UPDATE hosting_operation_leases o SET operation='SUSPEND',repair_required=true,lease_until=now() WHERE o.item_id=$1 AND o.operation='RESTORE' AND EXISTS(SELECT 1 FROM hosting_restore_intents i WHERE i.item_id=o.item_id AND i.lease_token=o.lease_token AND i.lease_until<=now())`, c.item); err != nil {
		return c, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE hosting_restore_intents SET lease_token=NULL,lease_until=NULL WHERE item_id=$1 AND lease_until<=now()`, c.item); err != nil {
		return c, false, err
	}
	var operationBusy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosting_operation_leases WHERE item_id=$1 AND (lease_until>now() OR manual_review_required))`, c.item).Scan(&operationBusy); err != nil {
		return c, false, err
	}
	if operationBusy {
		_, err = tx.Exec(ctx, `UPDATE hosting_restore_intents SET next_attempt_at=now()+interval '1 minute' WHERE invoice_id=$1 AND item_id=$2 AND lease_token IS NULL`, c.invoice, c.item)
		if err != nil {
			return c, false, err
		}
		return c, true, tx.Commit(ctx)
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hosting_restore_intents x JOIN invoices paid ON paid.invoice_id=x.invoice_id WHERE x.invoice_id=$1 AND x.item_id=$2 AND x.lease_token IS NULL AND x.next_attempt_at<=now() AND paid.status='PAID' AND paid.balance_due<=0 AND EXISTS(SELECT 1 FROM invoice_line_items l WHERE l.invoice_id=paid.invoice_id AND l.item_id=x.item_id)) AND NOT EXISTS(SELECT 1 FROM invoice_line_items l JOIN invoices i ON i.invoice_id=l.invoice_id WHERE l.item_id=$2 AND NOT i.is_template AND i.status IN ('OPEN','SENT','PARTIAL') AND i.balance_due>0) AND NOT EXISTS(SELECT 1 FROM hosting_restore_intents x WHERE x.item_id=$2 AND x.lease_token IS NOT NULL)`, c.invoice, c.item).Scan(&eligible)
	if err != nil {
		return c, false, err
	}
	if !hosting || (c.itemStatus != "ACTIVE" && c.itemStatus != "SUSPENDED" && c.itemStatus != "SUSPEND_PENDING") || !eligible {
		// A leased sibling must finish first. Keep blocked intents for retry,
		// including when another invoice is later voided rather than paid.
		var leased bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosting_restore_intents WHERE item_id=$1 AND lease_token IS NOT NULL)`, c.item).Scan(&leased); err != nil {
			return c, false, err
		}
		if leased {
			// The selection preceded another worker's committed lease. Reselect
			// after rolling back, so unrelated due items are not starved.
			return c, false, errStaleHostingCandidate
		}
		if hosting && (c.itemStatus == "ACTIVE" || c.itemStatus == "SUSPENDED" || c.itemStatus == "SUSPEND_PENDING") {
			_, err = tx.Exec(ctx, `UPDATE hosting_restore_intents SET next_attempt_at=now()+interval '1 minute' WHERE invoice_id=$1 AND item_id=$2 AND lease_token IS NULL`, c.invoice, c.item)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM hosting_restore_intents WHERE invoice_id=$1 AND item_id=$2 AND lease_token IS NULL`, c.invoice, c.item)
		}
		if err != nil {
			return c, false, err
		}
		return c, true, tx.Commit(ctx)
	}
	err = tx.QueryRow(ctx, `SELECT target_id,zone_id,record_id,record_name,record_type,live_content,Proxied,provider_status FROM cloudflare_targets WHERE item_id=$1 FOR UPDATE`, c.item).Scan(&c.targetID, &c.target.ZoneID, &c.target.RecordID, &c.target.RecordName, &c.target.RecordType, &c.target.LiveContent, &c.target.Proxied, &c.providerStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `UPDATE hosting_restore_intents SET next_attempt_at=now()+interval '1 minute' WHERE invoice_id=$1 AND item_id=$2`, c.invoice, c.item)
		if err != nil {
			return c, false, err
		}
		return c, true, tx.Commit(ctx)
	}
	var repairPending bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosting_operation_leases WHERE item_id=$1 AND repair_required)`, c.item).Scan(&repairPending); e != nil {
		return c, false, e
	}
	if err == nil && !repairPending && c.itemStatus == "ACTIVE" && c.providerStatus == "ACTIVE" {
		_, err = tx.Exec(ctx, `DELETE FROM hosting_restore_intents WHERE invoice_id=$1 AND item_id=$2`, c.invoice, c.item)
		if err != nil {
			return c, false, err
		}
		return c, true, tx.Commit(ctx)
	}
	if err != nil {
		return c, false, err
	}
	c.token, err = itemUUID()
	if err != nil {
		return c, false, err
	}
	var sharedToken pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO hosting_operation_leases(item_id,operation,lease_token,lease_until,target_fingerprint,item_state_fingerprint) SELECT $1,'RESTORE',$2,now()+interval '2 minutes',md5(jsonb_build_array(t.target_id,t.zone_id,t.record_id,t.record_name,t.record_type,t.live_content,t.maintenance_content,t.proxied)::text),md5(jsonb_build_array(i.is_hosting,i.hosting_status,i.hosting_suspension_enabled)::text) FROM cloudflare_targets t JOIN items i USING(item_id) WHERE i.item_id=$1 ON CONFLICT(item_id) DO UPDATE SET operation=excluded.operation,lease_token=excluded.lease_token,lease_until=excluded.lease_until,target_fingerprint=excluded.target_fingerprint,item_state_fingerprint=excluded.item_state_fingerprint WHERE hosting_operation_leases.lease_until<=now() RETURNING lease_token`, c.item, c.token).Scan(&sharedToken)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `UPDATE hosting_restore_intents SET next_attempt_at=now()+interval '1 minute' WHERE invoice_id=$1 AND item_id=$2`, c.invoice, c.item)
		if err != nil {
			return c, false, err
		}
		c.token = pgtype.UUID{}
		return c, true, tx.Commit(ctx)
	}
	if err != nil {
		return c, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE hosting_restore_intents SET lease_token=$3,lease_until=now()+interval '2 minutes',attempts=attempts+1 WHERE invoice_id=$1 AND item_id=$2`, c.invoice, c.item, c.token)
	if err != nil {
		return c, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return c, false, err
	}
	return c, true, nil
}

func finishHostingRestore(ctx context.Context, pool *pgxpool.Pool, c hostingClaim, providerErr error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var hosting bool
	var status string
	if err = tx.QueryRow(ctx, `SELECT is_hosting,coalesce(hosting_status,'') FROM items WHERE item_id=$1 FOR UPDATE`, c.item).Scan(&hosting, &status); err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosting_restore_intents WHERE invoice_id=$1 AND item_id=$2 AND lease_token=$3 AND lease_until>now()) AND EXISTS(SELECT 1 FROM hosting_operation_leases WHERE item_id=$2 AND operation='RESTORE' AND lease_token=$3 AND lease_until>now())`, c.invoice, c.item, c.token).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return tx.Commit(ctx)
	}
	// Lock the target before comparing and updating it, so a concurrent edit
	// cannot slip between the recheck and the final state write.
	var lockedTarget pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT target_id FROM cloudflare_targets WHERE item_id=$1 FOR UPDATE`, c.item).Scan(&lockedTarget)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var same bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cloudflare_targets t JOIN items i USING(item_id) JOIN hosting_operation_leases l USING(item_id) WHERE t.target_id=$1 AND t.item_id=$2 AND t.zone_id=$3 AND t.record_id=$4 AND t.record_name=$5 AND t.record_type=$6 AND t.live_content=$7 AND t.proxied=$8 AND t.provider_status=$9 AND l.lease_token=$10 AND l.target_fingerprint=md5(jsonb_build_array(t.target_id,t.zone_id,t.record_id,t.record_name,t.record_type,t.live_content,t.maintenance_content,t.proxied)::text) AND l.item_state_fingerprint=md5(jsonb_build_array(i.is_hosting,i.hosting_status,i.hosting_suspension_enabled)::text))`, c.targetID, c.item, c.target.ZoneID, c.target.RecordID, c.target.RecordName, c.target.RecordType, c.target.LiveContent, c.target.Proxied, c.providerStatus, c.token).Scan(&same)
	if err != nil {
		return err
	}
	var clear bool
	err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM invoice_line_items l JOIN invoices i ON i.invoice_id=l.invoice_id WHERE l.item_id=$1 AND NOT i.is_template AND i.status IN ('OPEN','SENT','PARTIAL') AND i.balance_due>0) AND EXISTS (SELECT 1 FROM invoices i JOIN invoice_line_items l ON l.invoice_id=i.invoice_id WHERE i.invoice_id=$2 AND l.item_id=$1 AND i.status='PAID' AND i.balance_due<=0)`, c.item, c.invoice).Scan(&clear)
	if err != nil {
		return err
	}
	if hosting && status == c.itemStatus && status != "CANCELLED" && same && clear {
		if providerErr == nil {
			if _, err = tx.Exec(ctx, `UPDATE items SET hosting_status='ACTIVE' WHERE item_id=$1`, c.item); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE cloudflare_targets SET provider_status='ACTIVE',last_error=NULL,last_action_at=date_trunc('second',timezone('UTC',now())) WHERE target_id=$1`, c.targetID); err != nil {
				return err
			}
		} else {
			if _, err = tx.Exec(ctx, `UPDATE cloudflare_targets SET provider_status='ERROR',last_error='Cloudflare request failed',last_action_at=date_trunc('second',timezone('UTC',now())) WHERE target_id=$1`, c.targetID); err != nil {
				return err
			}
		}
	}
	if providerErr == nil && hosting && status == c.itemStatus && same && clear {
		_, err = tx.Exec(ctx, `DELETE FROM hosting_restore_intents WHERE invoice_id=$1 AND item_id=$2 AND lease_token=$3`, c.invoice, c.item, c.token)
	} else {
		_, err = tx.Exec(ctx, `UPDATE hosting_restore_intents SET lease_token=NULL,lease_until=NULL,next_attempt_at=now()+interval '1 minute' WHERE invoice_id=$1 AND item_id=$2 AND lease_token=$3`, c.invoice, c.item, c.token)
	}
	if err != nil {
		return err
	}
	if !hosting || status != c.itemStatus || !same {
		_, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET manual_review_required=true,lease_until=now() WHERE item_id=$1 AND lease_token=$2`, c.item, c.token)
	} else if !clear {
		// The restore may have reached Cloudflare even on timeout. Persist a
		// compensating suspension when an invoice opened during the request.
		_, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET operation='SUSPEND',repair_required=true,lease_until=now() WHERE item_id=$1 AND lease_token=$2`, c.item, c.token)
	} else if providerErr != nil {
		// The external PATCH may have succeeded even when its response failed.
		_, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET repair_required=true,lease_until=now() WHERE item_id=$1 AND lease_token=$2`, c.item, c.token)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM hosting_operation_leases WHERE item_id=$1 AND lease_token=$2`, c.item, c.token)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
