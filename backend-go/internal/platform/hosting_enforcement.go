package platform

import (
	"context"
	"errors"
	"hash/crc32"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HostingEnforcementProvider interface {
	HostingRestoreProvider
	HostingSuspendProvider
}
type HostingEnforcementCounts struct{ Suspended, Restored, Errors int }

var ErrHostingEnforcementAlreadyRunning = errors.New("hosting enforcement already running")

type enforcementClaim struct {
	item, targetID, token                 pgtype.UUID
	itemStatus, providerStatus, operation string
	target                                HostingSuspendTarget
	live                                  string
	repair                                bool
}

// RunHostingEnforcement uses the same session advisory key as the Python job.
// Provider PATCHes are at least once: a crash after a PATCH can cause a repeat.
func RunHostingEnforcement(ctx context.Context, pool *pgxpool.Pool, provider HostingEnforcementProvider, day time.Time) (HostingEnforcementCounts, error) {
	var counts HostingEnforcementCounts
	if pool == nil || provider == nil {
		return counts, errors.New("hosting enforcement dependencies required")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return counts, err
	}
	defer conn.Release()
	key := int64(crc32.ChecksumIEEE([]byte("hosting-overdue-enforcement")))
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		return counts, err
	}
	if !locked {
		return counts, ErrHostingEnforcementAlreadyRunning
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var ok bool
		if e := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock($1)`, key).Scan(&ok); e != nil || !ok {
			conn.Hijack().Close(context.Background())
		}
	}()
	rows, err := conn.Query(ctx, `SELECT item_id FROM items i WHERE (is_hosting AND hosting_suspension_enabled IS TRUE AND hosting_status IN ('ACTIVE','SUSPEND_PENDING','SUSPENDED')) OR EXISTS(SELECT 1 FROM hosting_operation_leases l WHERE l.item_id=i.item_id AND (l.repair_required OR l.manual_review_required OR l.lease_until<=now())) ORDER BY created_at,item_id`)
	if err != nil {
		return counts, err
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return counts, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return counts, err
	}
	// Python uses date.today(); retain the date component supplied by the caller.
	date := day.Format("2006-01-02")
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return counts, err
		}
		c, action, e := claimEnforcement(ctx, conn, id, date)
		if e != nil {
			if ctx.Err() != nil {
				return counts, ctx.Err()
			}
			counts.Errors++
			continue
		}
		if !action {
			continue
		}
		if c.operation == "SUSPEND" {
			e = provider.Suspend(ctx, c.target)
		} else {
			e = provider.Restore(ctx, HostingRestoreTarget{ZoneID: c.target.ZoneID, RecordID: c.target.RecordID, RecordName: c.target.RecordName, RecordType: c.target.RecordType, LiveContent: c.live, Proxied: c.target.Proxied})
		}
		if ctx.Err() != nil {
			return counts, ctx.Err()
		}
		done, finishErr := finishEnforcement(ctx, conn, c, date, e)
		if finishErr != nil {
			counts.Errors++
			continue
		}
		if e != nil {
			counts.Errors++
		} else if done {
			if c.operation == "SUSPEND" {
				counts.Suspended++
			} else {
				counts.Restored++
			}
		}
	}
	return counts, nil
}

func claimEnforcement(ctx context.Context, pool *pgxpool.Conn, id pgtype.UUID, day string) (c enforcementClaim, action bool, err error) {
	c.item = id
	tx, err := pool.Begin(ctx)
	if err != nil {
		return c, false, err
	}
	defer tx.Rollback(context.Background())
	var hosting, enabled bool
	err = tx.QueryRow(ctx, `SELECT is_hosting,coalesce(hosting_suspension_enabled,false),coalesce(hosting_status,'') FROM items WHERE item_id=$1 FOR UPDATE`, id).Scan(&hosting, &enabled, &c.itemStatus)
	if err != nil {
		return c, false, err
	}
	var manualReview bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosting_operation_leases WHERE item_id=$1 AND manual_review_required)`, id).Scan(&manualReview); err != nil {
		return c, false, err
	}
	if manualReview {
		return c, false, errors.New("hosting operation requires manual review")
	}
	drift, err := hostingLeaseRecoveryDrift(ctx, tx, id)
	if err != nil {
		return c, false, err
	}
	if drift {
		if _, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET manual_review_required=true WHERE item_id=$1`, id); err != nil {
			return c, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return c, false, err
		}
		return c, false, errors.New("hosting operation requires manual review")
	}
	if !hosting || !enabled || (c.itemStatus != "ACTIVE" && c.itemStatus != "SUSPEND_PENDING" && c.itemStatus != "SUSPENDED") {
		return c, false, errors.New("hosting operation requires manual review")
	}
	var overdue, open bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT i.invoice_id FROM invoices i JOIN invoice_line_items l ON l.invoice_id=i.invoice_id WHERE l.item_id=$1 AND NOT i.is_template AND i.status IN ('OPEN','SENT','PARTIAL') AND i.balance_due>0 AND i.due_date + coalesce((SELECT hosting_grace_days FROM items WHERE item_id=$1),0) < $2::date ORDER BY i.due_date ASC NULLS LAST,i.created_at ASC LIMIT 1), EXISTS(SELECT 1 FROM invoices i JOIN invoice_line_items l ON l.invoice_id=i.invoice_id WHERE l.item_id=$1 AND NOT i.is_template AND i.status IN ('OPEN','SENT','PARTIAL') AND i.balance_due>0)`, id, day).Scan(&overdue, &open)
	if err != nil {
		return c, false, err
	}
	var repairPending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosting_operation_leases WHERE item_id=$1 AND (repair_required OR lease_until<=now()))`, id).Scan(&repairPending); err != nil {
		return c, false, err
	}
	if repairPending && open {
		if !overdue {
			if _, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET manual_review_required=true WHERE item_id=$1`, id); err != nil {
				return c, false, err
			}
			if err = tx.Commit(ctx); err != nil {
				return c, false, err
			}
			return c, false, errors.New("hosting operation requires manual review")
		}
		c.operation, c.repair = "SUSPEND", true
	} else if repairPending {
		c.operation, c.repair = "RESTORE", true
	} else if overdue {
		c.operation = "SUSPEND"
	} else if !open {
		c.operation = "RESTORE"
	} else {
		return c, false, nil
	}
	err = tx.QueryRow(ctx, `SELECT target_id,zone_id,record_id,record_name,record_type,maintenance_content,live_content,proxied,provider_status FROM cloudflare_targets WHERE item_id=$1 FOR UPDATE`, id).Scan(&c.targetID, &c.target.ZoneID, &c.target.RecordID, &c.target.RecordName, &c.target.RecordType, &c.target.MaintenanceContent, &c.live, &c.target.Proxied, &c.providerStatus)
	if errors.Is(err, pgx.ErrNoRows) && !overdue && !c.repair {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	if !c.repair && ((c.operation == "SUSPEND" && c.itemStatus == "SUSPENDED" && c.providerStatus == "SUSPENDED") || (c.operation == "RESTORE" && c.itemStatus == "ACTIVE" && c.providerStatus == "ACTIVE")) {
		return c, false, nil
	}
	c.token, err = itemUUID()
	if err != nil {
		return c, false, err
	}
	var token pgtype.UUID
	if c.operation == "SUSPEND" {
		_, err = tx.Exec(ctx, `UPDATE items SET hosting_status='SUSPEND_PENDING' WHERE item_id=$1`, id)
		if err != nil {
			return c, false, err
		}
		c.itemStatus = "SUSPEND_PENDING"
	}
	err = tx.QueryRow(ctx, `INSERT INTO hosting_operation_leases(item_id,operation,lease_token,lease_until,repair_required,target_fingerprint,item_state_fingerprint) SELECT $1,$2,$3,now()+interval '2 minutes',$4,md5(jsonb_build_array(t.target_id,t.zone_id,t.record_id,t.record_name,t.record_type,t.live_content,t.maintenance_content,t.proxied)::text),md5(jsonb_build_array(i.is_hosting,i.hosting_status,i.hosting_suspension_enabled)::text) FROM cloudflare_targets t JOIN items i USING(item_id) WHERE i.item_id=$1 ON CONFLICT(item_id) DO UPDATE SET operation=excluded.operation,lease_token=excluded.lease_token,lease_until=excluded.lease_until,repair_required=excluded.repair_required,target_fingerprint=excluded.target_fingerprint,item_state_fingerprint=excluded.item_state_fingerprint WHERE hosting_operation_leases.lease_until<=now() RETURNING lease_token`, id, c.operation, c.token, c.repair).Scan(&token)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	return c, true, tx.Commit(ctx)
}

// Compare only a hash of the original target configuration; credentials never enter it.
func hostingLeaseRecoveryDrift(ctx context.Context, tx pgx.Tx, id pgtype.UUID) (bool, error) {
	var drift bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosting_operation_leases l WHERE l.item_id=$1 AND (l.lease_until<=now() OR l.repair_required) AND (l.target_fingerprint IS NULL OR l.target_fingerprint IS DISTINCT FROM (SELECT md5(jsonb_build_array(target_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,proxied)::text) FROM cloudflare_targets WHERE item_id=$1) OR l.item_state_fingerprint IS NULL OR l.item_state_fingerprint IS DISTINCT FROM (SELECT md5(jsonb_build_array(is_hosting,hosting_status,hosting_suspension_enabled)::text) FROM items WHERE item_id=$1) OR EXISTS(SELECT 1 FROM items WHERE item_id=$1 AND (NOT is_hosting OR hosting_suspension_enabled IS NOT TRUE OR coalesce(hosting_status,'') NOT IN ('ACTIVE','SUSPEND_PENDING','SUSPENDED')))))`, id).Scan(&drift)
	return drift, err
}

func finishEnforcement(ctx context.Context, pool *pgxpool.Conn, c enforcementClaim, day string, providerErr error) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var hosting, enabled bool
	var status string
	err = tx.QueryRow(ctx, `SELECT is_hosting,coalesce(hosting_suspension_enabled,false),coalesce(hosting_status,'') FROM items WHERE item_id=$1 FOR UPDATE`, c.item).Scan(&hosting, &enabled, &status)
	if err != nil {
		return false, err
	}
	var target pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT target_id FROM cloudflare_targets WHERE item_id=$1 FOR UPDATE`, c.item).Scan(&target)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosting_operation_leases WHERE item_id=$1 AND operation=$2 AND lease_token=$3 AND lease_until>now()) AND EXISTS(SELECT 1 FROM cloudflare_targets WHERE target_id=$4 AND item_id=$1 AND zone_id=$5 AND record_id=$6 AND record_name=$7 AND record_type=$8 AND maintenance_content=$9 AND live_content=$10 AND proxied=$11 AND provider_status=$12)`, c.item, c.operation, c.token, c.targetID, c.target.ZoneID, c.target.RecordID, c.target.RecordName, c.target.RecordType, c.target.MaintenanceContent, c.live, c.target.Proxied, c.providerStatus).Scan(&valid)
	if err != nil {
		return false, err
	}
	var overdue, open bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT i.invoice_id FROM invoices i JOIN invoice_line_items l ON l.invoice_id=i.invoice_id WHERE l.item_id=$1 AND NOT i.is_template AND i.status IN ('OPEN','SENT','PARTIAL') AND i.balance_due>0 AND i.due_date + coalesce((SELECT hosting_grace_days FROM items WHERE item_id=$1),0) < $2::date ORDER BY i.due_date ASC NULLS LAST,i.created_at ASC LIMIT 1), EXISTS(SELECT 1 FROM invoices i JOIN invoice_line_items l ON l.invoice_id=i.invoice_id WHERE l.item_id=$1 AND NOT i.is_template AND i.status IN ('OPEN','SENT','PARTIAL') AND i.balance_due>0)`, c.item, day).Scan(&overdue, &open)
	if err != nil {
		return false, err
	}
	sameTargetAndLease := valid
	valid = valid && hosting && enabled && status == c.itemStatus && ((c.operation == "SUSPEND" && overdue) || (c.operation == "RESTORE" && !open))
	if valid {
		if providerErr != nil {
			_, err = tx.Exec(ctx, `UPDATE cloudflare_targets SET provider_status='ERROR',last_error='Cloudflare request failed',last_action_at=date_trunc('second',timezone('UTC',now())) WHERE target_id=$1`, c.targetID)
		} else {
			newStatus := "ACTIVE"
			if c.operation == "SUSPEND" {
				newStatus = "SUSPENDED"
			}
			_, err = tx.Exec(ctx, `UPDATE items SET hosting_status=$2 WHERE item_id=$1`, c.item, newStatus)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE cloudflare_targets SET provider_status=$2,last_error=NULL,last_action_at=date_trunc('second',timezone('UTC',now())) WHERE target_id=$1`, c.targetID, newStatus)
			}
		}
		if err != nil {
			return false, err
		}
	}
	ambiguous := !valid && sameTargetAndLease && hosting && enabled && status == c.itemStatus && c.operation == "SUSPEND" && open && !overdue
	if ambiguous {
		_, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET manual_review_required=true,lease_until=now() WHERE item_id=$1 AND lease_token=$2`, c.item, c.token)
	} else if !valid && sameTargetAndLease && hosting && enabled && status == c.itemStatus && ((c.operation == "RESTORE" && open) || (c.operation == "SUSPEND" && !open)) {
		repairOp := "SUSPEND"
		if c.operation == "SUSPEND" {
			repairOp = "RESTORE"
		}
		_, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET operation=$3,repair_required=true,lease_until=now() WHERE item_id=$1 AND lease_token=$2`, c.item, c.token, repairOp)
	} else if !valid && (!sameTargetAndLease || !hosting || !enabled || status != c.itemStatus) {
		_, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET manual_review_required=true,lease_until=now() WHERE item_id=$1 AND lease_token=$2`, c.item, c.token)
	} else if providerErr != nil {
		// A failed response cannot tell us whether the PATCH reached the provider.
		_, err = tx.Exec(ctx, `UPDATE hosting_operation_leases SET repair_required=true,lease_until=now() WHERE item_id=$1 AND lease_token=$2`, c.item, c.token)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM hosting_operation_leases WHERE item_id=$1 AND lease_token=$2`, c.item, c.token)
	}
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	if ambiguous || (!valid && (!sameTargetAndLease || !hosting || !enabled || status != c.itemStatus)) {
		return false, errors.New("hosting operation requires manual review")
	}
	return valid && providerErr == nil, nil
}
