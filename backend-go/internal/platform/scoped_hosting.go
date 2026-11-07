package platform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
)

// ScopedHostingProvider is an injection point for development fakes. A production
// implementation must independently authorize the physical zone/record for the
// supplied business before changing it.
type ScopedHostingProvider interface {
	ApplyHosting(context.Context, business.ID, HostingOperation) error
}

type HostingOperation struct {
	TargetID                                          pgtype.UUID
	ZoneID, RecordID, RecordName, RecordType, Content string
	Suspend, Proxied                                  bool
}

type scopedHostingDB interface {
	Begin(context.Context) (pgx.Tx, error)
}

type scopedHostingClaim struct {
	item, invoice, token pgtype.UUID
	op                   HostingOperation
}

type scopedHostingState struct {
	hosting, enabled, overdue, open, settled              bool
	itemStatus, providerStatus                            string
	target                                                HostingOperation
	live, maintenance, targetFingerprint, itemFingerprint string
}

type scopedHostingLease struct {
	token                              pgtype.UUID
	operation                          string
	active, review                     bool
	targetFingerprint, itemFingerprint pgtype.Text
}

var errScopedHostingReview = errors.New("hosting operation requires manual review")

func scopedHostingJob(scope business.ID) string {
	return "billing:v8:hosting-overdue-enforcement:" + scope.String()
}

// RunScopedHostingEnforcement scans one business using the supplied local date.
// It has no startup registration.
func RunScopedHostingEnforcement(ctx context.Context, pool *pgxpool.Pool, scope business.ID, provider ScopedHostingProvider, day time.Time) (HostingEnforcementCounts, error) {
	var counts HostingEnforcementCounts
	if err := scope.Validate(); err != nil {
		return counts, err
	}
	if pool == nil || provider == nil {
		return counts, errors.New("hosting enforcement dependencies required")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return counts, err
	}
	defer conn.Release()
	job := scopedHostingJob(scope)
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, job).Scan(&locked); err != nil {
		closeScopedHostingConn(conn)
		return counts, err
	}
	if !locked {
		return counts, ErrHostingEnforcementAlreadyRunning
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, job).Scan(&unlocked); err != nil || !unlocked {
			closeScopedHostingConn(conn)
		}
	}()
	rows, err := conn.Query(ctx, `
SELECT i.item_id FROM items i
WHERE i.business_id=$1
  AND ((i.is_hosting AND i.hosting_suspension_enabled IS TRUE
        AND i.hosting_status IN ('ACTIVE','SUSPEND_PENDING','SUSPENDED'))
       OR EXISTS (SELECT 1 FROM hosting_operation_leases l
                  WHERE l.business_id=$1 AND l.item_id=i.item_id
                    AND (l.repair_required OR l.manual_review_required OR l.lease_until<=now())))
ORDER BY i.created_at,i.item_id`, scope.UUID())
	if err != nil {
		return counts, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[pgtype.UUID])
	if err != nil {
		return counts, err
	}
	date := day.Format("2006-01-02")
	for _, id := range ids {
		claim, action, err := claimScopedHosting(ctx, conn, scope, id, pgtype.UUID{}, date)
		var done bool
		if err == nil && action {
			done, err = deliverScopedHosting(ctx, conn, scope, provider, claim, date)
		}
		if ctx.Err() != nil {
			return counts, ctx.Err()
		}
		switch {
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			counts.Errors++
		case done && claim.op.Suspend:
			counts.Suspended++
		case done:
			counts.Restored++
		}
	}
	return counts, nil
}

func closeScopedHostingConn(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Conn().Close(ctx)
}

// ConsumeScopedHostingRestoreIntents drains due intents for one business.
// Replicas may drain separate items concurrently.
func ConsumeScopedHostingRestoreIntents(ctx context.Context, pool *pgxpool.Pool, scope business.ID, provider ScopedHostingProvider) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if pool == nil || provider == nil {
		return errors.New("hosting restore dependencies required")
	}
	for {
		var invoice, item pgtype.UUID
		err := pool.QueryRow(ctx, `
SELECT d.invoice_id,d.item_id FROM hosting_restore_intents d
JOIN items i ON i.business_id=d.business_id AND i.item_id=d.item_id
WHERE d.business_id=$1 AND d.next_attempt_at<=now()
  AND (d.lease_token IS NULL OR d.lease_until IS NULL OR d.lease_until<=now())
  AND NOT EXISTS (SELECT 1 FROM hosting_operation_leases l
                  WHERE l.business_id=$1 AND l.item_id=d.item_id AND l.lease_until>now())
  AND NOT EXISTS (SELECT 1 FROM hosting_restore_intents x
                  WHERE x.business_id=$1 AND x.item_id=d.item_id AND x.lease_token IS NOT NULL AND x.lease_until>now())
ORDER BY d.created_at,d.invoice_id,d.item_id LIMIT 1`, scope.UUID()).Scan(&invoice, &item)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		claim, action, err := claimScopedHosting(ctx, pool, scope, item, invoice, "")
		if errors.Is(err, errScopedHostingReview) || errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if action {
			if _, err = deliverScopedHosting(ctx, pool, scope, provider, claim, ""); err != nil && !errors.Is(err, errScopedHostingProvider) && !errors.Is(err, errScopedHostingReview) {
				return err
			}
		}
	}
}

func loadScopedHostingState(ctx context.Context, tx pgx.Tx, scope business.ID, c scopedHostingClaim, day string) (scopedHostingState, error) {
	var s scopedHostingState
	biz := scope.UUID()
	err := tx.QueryRow(ctx, `
SELECT is_hosting,coalesce(hosting_suspension_enabled,false),coalesce(hosting_status,''),
       md5(jsonb_build_array(business_id,is_hosting,hosting_status,hosting_suspension_enabled)::text)
FROM items WHERE business_id=$1 AND item_id=$2 FOR UPDATE`, biz, c.item).
		Scan(&s.hosting, &s.enabled, &s.itemStatus, &s.itemFingerprint)
	if err != nil {
		return s, err
	}
	err = tx.QueryRow(ctx, `
SELECT target_id,zone_id,record_id,record_name,record_type,proxied,live_content,maintenance_content,provider_status,
       md5(jsonb_build_array(business_id,target_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,proxied,provider_status)::text)
FROM cloudflare_targets WHERE business_id=$1 AND item_id=$2 FOR UPDATE`, biz, c.item).
		Scan(&s.target.TargetID, &s.target.ZoneID, &s.target.RecordID, &s.target.RecordName,
			&s.target.RecordType, &s.target.Proxied, &s.live, &s.maintenance, &s.providerStatus, &s.targetFingerprint)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, err
	}
	err = tx.QueryRow(ctx, `
SELECT coalesce(bool_or(i.due_date+coalesce(item.hosting_grace_days,0)<nullif($3,'')::date)
                FILTER (WHERE NOT i.is_template AND i.status IN ('OPEN','SENT','PARTIAL') AND i.balance_due>0),false),
       coalesce(bool_or(NOT i.is_template AND i.status IN ('OPEN','SENT','PARTIAL') AND i.balance_due>0),false),
       coalesce(bool_or(i.invoice_id=$4 AND i.status='PAID' AND i.balance_due<=0),false)
FROM invoice_line_items l
JOIN invoices i ON i.business_id=l.business_id AND i.invoice_id=l.invoice_id
JOIN items item ON item.business_id=l.business_id AND item.item_id=l.item_id
WHERE l.business_id=$1 AND l.item_id=$2`, biz, c.item, day, c.invoice).Scan(&s.overdue, &s.open, &s.settled)
	return s, err
}

func loadScopedHostingLease(ctx context.Context, tx pgx.Tx, scope business.ID, item pgtype.UUID) (scopedHostingLease, error) {
	var l scopedHostingLease
	err := tx.QueryRow(ctx, `
SELECT lease_token,operation,lease_until>clock_timestamp(),manual_review_required,
       target_fingerprint,item_state_fingerprint
FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2 FOR UPDATE`, scope.UUID(), item).
		Scan(&l.token, &l.operation, &l.active, &l.review, &l.targetFingerprint, &l.itemFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return l, err
}

func (s scopedHostingState) matches(l scopedHostingLease) bool {
	return s.target.TargetID.Valid && l.targetFingerprint.Valid && l.itemFingerprint.Valid &&
		l.targetFingerprint.String == s.targetFingerprint && l.itemFingerprint.String == s.itemFingerprint
}

func (s scopedHostingState) hostingEligible(intent bool) bool {
	return s.hosting && (intent || s.enabled) &&
		(s.itemStatus == "ACTIVE" || s.itemStatus == "SUSPENDED" || s.itemStatus == "SUSPEND_PENDING")
}

func claimScopedHosting(ctx context.Context, db scopedHostingDB, scope business.ID, item, invoice pgtype.UUID, day string) (scopedHostingClaim, bool, error) {
	c := scopedHostingClaim{item: item, invoice: invoice}
	if err := scope.Validate(); err != nil {
		return c, false, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return c, false, err
	}
	defer tx.Rollback(context.Background())
	s, err := loadScopedHostingState(ctx, tx, scope, c, day)
	if err != nil {
		return c, false, err
	}
	l, err := loadScopedHostingLease(ctx, tx, scope, item)
	if err != nil || l.active {
		return c, false, err
	}
	biz := scope.UUID()
	// Every claim holds the item lock before touching intents, matching settlement.
	if _, err := tx.Exec(ctx, `
UPDATE hosting_restore_intents SET lease_token=NULL,lease_until=NULL
WHERE business_id=$1 AND item_id=$2
  AND ((lease_token IS NOT NULL AND (lease_until IS NULL OR lease_until<=clock_timestamp()))
       OR (lease_token IS NULL AND lease_until IS NOT NULL))`, biz, item); err != nil {
		return c, false, err
	}
	var activeIntent, dueIntent bool
	err = tx.QueryRow(ctx, `
SELECT coalesce(bool_or(lease_token IS NOT NULL),false),
       coalesce(bool_or(invoice_id=$3 AND lease_token IS NULL AND next_attempt_at<=clock_timestamp()),false)
FROM hosting_restore_intents WHERE business_id=$1 AND item_id=$2`, biz, item, invoice).Scan(&activeIntent, &dueIntent)
	if err != nil || activeIntent || (invoice.Valid && !dueIntent) {
		return c, false, err
	}
	// An intent may restore service even when automated suspension was
	// already disabled. Give that intent its retry if nothing has drifted.
	if !invoice.Valid && !s.enabled && !s.open && l.operation == "RESTORE" &&
		!l.review && s.hostingEligible(true) && s.matches(l) {
		var restorable bool
		err := tx.QueryRow(ctx, `
SELECT EXISTS (
 SELECT 1 FROM hosting_restore_intents d
 JOIN invoices i ON i.business_id=d.business_id AND i.invoice_id=d.invoice_id
 JOIN invoice_line_items l ON l.business_id=d.business_id AND l.invoice_id=d.invoice_id AND l.item_id=d.item_id
 WHERE d.business_id=$1 AND d.item_id=$2 AND i.status='PAID' AND i.balance_due<=0
)`, biz, item).Scan(&restorable)
		if err != nil {
			return c, false, err
		}
		if restorable {
			return c, false, tx.Commit(ctx)
		}
	}
	if l.token.Valid && (l.review || !s.matches(l) || !s.hostingEligible(invoice.Valid) || (!invoice.Valid && s.open && !s.overdue)) {
		if err := markScopedHostingReview(ctx, tx, scope, c.item, l.token); err != nil {
			return c, false, err
		}
		if err := retryScopedHostingIntent(ctx, tx, scope, c); err != nil {
			return c, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return c, false, err
		}
		return c, false, errScopedHostingReview
	}
	if invoice.Valid {
		switch {
		case !s.hostingEligible(true):
			err = deleteScopedHostingIntent(ctx, tx, scope, c)
		case !s.settled || s.open || !s.target.TargetID.Valid:
			err = retryScopedHostingIntent(ctx, tx, scope, c)
		case !l.token.Valid && s.itemStatus == "ACTIVE" && s.providerStatus == "ACTIVE":
			err = deleteScopedHostingIntent(ctx, tx, scope, c)
		default:
			return saveScopedHostingClaim(ctx, tx, scope, c, s, l, "RESTORE")
		}
		if err != nil {
			return c, false, err
		}
		return c, false, tx.Commit(ctx)
	}
	if !s.hostingEligible(false) || (s.open && !s.overdue) {
		return c, false, nil
	}
	if !s.target.TargetID.Valid {
		if s.overdue {
			return c, false, errors.New("hosting target unavailable")
		}
		return c, false, nil
	}
	operation, doneStatus := "RESTORE", "ACTIVE"
	if s.overdue {
		operation, doneStatus = "SUSPEND", "SUSPENDED"
	}
	if !l.token.Valid && s.itemStatus == doneStatus && s.providerStatus == doneStatus {
		return c, false, nil
	}
	return saveScopedHostingClaim(ctx, tx, scope, c, s, l, operation)
}

func saveScopedHostingClaim(ctx context.Context, tx pgx.Tx, scope business.ID, c scopedHostingClaim, s scopedHostingState, l scopedHostingLease, operation string) (scopedHostingClaim, bool, error) {
	token, err := itemUUID()
	if err != nil {
		return c, false, err
	}
	biz := scope.UUID()
	c.token, c.op = token, s.target
	c.op.Content = s.live
	if operation == "SUSPEND" {
		c.op.Suspend, c.op.Content = true, s.maintenance
		err = tx.QueryRow(ctx, `
UPDATE items SET hosting_status='SUSPEND_PENDING' WHERE business_id=$1 AND item_id=$2
RETURNING md5(jsonb_build_array(business_id,is_hosting,hosting_status,hosting_suspension_enabled)::text)`,
			biz, c.item).Scan(&s.itemFingerprint)
		if err != nil {
			return c, false, err
		}
	}
	var leased pgtype.UUID
	err = tx.QueryRow(ctx, `
INSERT INTO hosting_operation_leases
 (business_id,item_id,operation,lease_token,lease_until,repair_required,target_fingerprint,item_state_fingerprint)
VALUES ($1,$2,$3,$4,clock_timestamp()+interval '2 minutes',$5,$6,$7)
ON CONFLICT (business_id,item_id) DO UPDATE
SET operation=excluded.operation,lease_token=excluded.lease_token,lease_until=excluded.lease_until,
    repair_required=excluded.repair_required,target_fingerprint=excluded.target_fingerprint,
    item_state_fingerprint=excluded.item_state_fingerprint
WHERE hosting_operation_leases.business_id=$1 AND hosting_operation_leases.lease_until<=clock_timestamp()
  AND NOT hosting_operation_leases.manual_review_required
RETURNING lease_token`, biz, c.item, operation, token, l.token.Valid, s.targetFingerprint, s.itemFingerprint).Scan(&leased)
	if err != nil {
		return c, false, err
	}
	if c.invoice.Valid {
		tag, err := tx.Exec(ctx, `
UPDATE hosting_restore_intents
SET lease_token=$4,lease_until=clock_timestamp()+interval '2 minutes',attempts=attempts+1
WHERE business_id=$1 AND item_id=$2 AND invoice_id=$3 AND lease_token IS NULL`, biz, c.item, c.invoice, token)
		if err != nil {
			return c, false, err
		}
		if tag.RowsAffected() != 1 {
			return c, false, pgx.ErrNoRows
		}
	}
	return c, true, tx.Commit(ctx)
}

func retryScopedHostingIntent(ctx context.Context, tx pgx.Tx, scope business.ID, c scopedHostingClaim) error {
	if !c.invoice.Valid {
		return nil
	}
	_, err := tx.Exec(ctx, `
UPDATE hosting_restore_intents SET lease_token=NULL,lease_until=NULL,next_attempt_at=clock_timestamp()+interval '1 minute'
WHERE business_id=$1 AND item_id=$2 AND invoice_id=$3 AND lease_token IS NOT DISTINCT FROM $4`, scope.UUID(), c.item, c.invoice, c.token)
	return err
}

func deleteScopedHostingIntent(ctx context.Context, tx pgx.Tx, scope business.ID, c scopedHostingClaim) error {
	if !c.invoice.Valid {
		return nil
	}
	_, err := tx.Exec(ctx, `
DELETE FROM hosting_restore_intents WHERE business_id=$1 AND item_id=$2 AND invoice_id=$3
AND lease_token IS NOT DISTINCT FROM $4`, scope.UUID(), c.item, c.invoice, c.token)
	return err
}

func markScopedHostingReview(ctx context.Context, tx pgx.Tx, scope business.ID, item, token pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
UPDATE hosting_operation_leases SET manual_review_required=true,lease_until=clock_timestamp()
WHERE business_id=$1 AND item_id=$2 AND lease_token=$3`, scope.UUID(), item, token)
	return err
}

var errScopedHostingProvider = errors.New("hosting provider request failed")

func deliverScopedHosting(ctx context.Context, db scopedHostingDB, scope business.ID, provider ScopedHostingProvider, c scopedHostingClaim, day string) (bool, error) {
	ready, err := settleScopedHosting(ctx, db, scope, c, day, true, false)
	if err != nil || !ready {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Keep each attempt shorter than the renewed lease. Providers must honor
	// context cancellation. Cancellation leaves the durable claim for recovery.
	requestCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	failed := provider.ApplyHosting(requestCtx, scope, c.op) != nil || requestCtx.Err() != nil
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	done, err := settleScopedHosting(ctx, db, scope, c, day, false, failed)
	if err == nil && failed {
		err = errScopedHostingProvider
	}
	return done, err
}

// The preflight and completion use the same snapshot and ownership checks.
func settleScopedHosting(ctx context.Context, db scopedHostingDB, scope business.ID, c scopedHostingClaim, day string, preflight, failed bool) (bool, error) {
	if err := scope.Validate(); err != nil {
		return false, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	s, err := loadScopedHostingState(ctx, tx, scope, c, day)
	if err != nil {
		return false, err
	}
	l, err := loadScopedHostingLease(ctx, tx, scope, c.item)
	if err != nil || !c.token.Valid || l.token != c.token || !l.active || l.review {
		return false, err
	}
	biz := scope.UUID()
	var intentCurrent bool
	err = tx.QueryRow(ctx, `
SELECT CASE WHEN $3::uuid IS NULL THEN NOT EXISTS (SELECT 1 FROM hosting_restore_intents
 WHERE business_id=$1 AND item_id=$2 AND lease_token=$4)
 ELSE EXISTS (SELECT 1 FROM hosting_restore_intents
 WHERE business_id=$1 AND item_id=$2 AND invoice_id=$3 AND lease_token=$4 AND lease_until>clock_timestamp()
 FOR UPDATE) END`, biz, c.item, c.invoice, c.token).Scan(&intentCurrent)
	if err != nil {
		return false, err
	}
	expected := s.target
	expected.Suspend, expected.Content = l.operation == "SUSPEND", s.live
	if expected.Suspend {
		expected.Content = s.maintenance
	}
	if !intentCurrent {
		// A removed or replaced intent cannot authorize a provider call or
		// complete this lease. Leave uncertainty for the next owned scan.
		return false, nil
	}
	if !s.matches(l) || expected != c.op || !s.hostingEligible(c.invoice.Valid) {
		if err := markScopedHostingReview(ctx, tx, scope, c.item, c.token); err != nil {
			return false, err
		}
		if err := retryScopedHostingIntent(ctx, tx, scope, c); err != nil {
			return false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, errScopedHostingReview
	}
	balanceOK := (c.op.Suspend && s.overdue) || (!c.op.Suspend && !s.open && (!c.invoice.Valid || s.settled))
	switch {
	case !balanceOK:
		if s.open && !s.overdue && !c.invoice.Valid {
			err = markScopedHostingReview(ctx, tx, scope, c.item, c.token)
		} else {
			repairOp := "RESTORE"
			if s.open {
				repairOp = "SUSPEND"
			}
			err = expireScopedHostingLease(ctx, tx, scope, c, repairOp, s.targetFingerprint)
		}
		if err == nil {
			err = retryScopedHostingIntent(ctx, tx, scope, c)
		}
		if err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	case preflight:
		tag, err := tx.Exec(ctx, `
UPDATE hosting_operation_leases SET lease_until=clock_timestamp()+interval '2 minutes'
WHERE business_id=$1 AND item_id=$2 AND lease_token=$3 AND lease_until>clock_timestamp()`, biz, c.item, c.token)
		if err != nil || tag.RowsAffected() != 1 {
			return false, err
		}
		if c.invoice.Valid {
			tag, err := tx.Exec(ctx, `
UPDATE hosting_restore_intents SET lease_until=clock_timestamp()+interval '2 minutes'
WHERE business_id=$1 AND item_id=$2 AND invoice_id=$3 AND lease_token=$4 AND lease_until>clock_timestamp()`, biz, c.item, c.invoice, c.token)
			if err != nil || tag.RowsAffected() != 1 {
				return false, err
			}
		}
		return true, tx.Commit(ctx)
	case failed:
		err = tx.QueryRow(ctx, `
UPDATE cloudflare_targets
SET provider_status='ERROR',last_error='Cloudflare request failed',last_action_at=date_trunc('second',timezone('UTC',now()))
WHERE business_id=$1 AND item_id=$2 AND target_id=$3
RETURNING md5(jsonb_build_array(business_id,target_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,proxied,provider_status)::text)`,
			biz, c.item, c.op.TargetID).Scan(&s.targetFingerprint)
		if err == nil {
			err = expireScopedHostingLease(ctx, tx, scope, c, l.operation, s.targetFingerprint)
		}
		if err == nil {
			err = retryScopedHostingIntent(ctx, tx, scope, c)
		}
	default:
		err = completeScopedHosting(ctx, tx, scope, c)
	}
	if err != nil {
		return false, err
	}
	return !failed, tx.Commit(ctx)
}

func expireScopedHostingLease(ctx context.Context, tx pgx.Tx, scope business.ID, c scopedHostingClaim, operation, targetFingerprint string) error {
	_, err := tx.Exec(ctx, `
UPDATE hosting_operation_leases
SET operation=$4,repair_required=true,lease_until=clock_timestamp(),target_fingerprint=$5
WHERE business_id=$1 AND item_id=$2 AND lease_token=$3`, scope.UUID(), c.item, c.token, operation, targetFingerprint)
	return err
}

func completeScopedHosting(ctx context.Context, tx pgx.Tx, scope business.ID, c scopedHostingClaim) error {
	status := "ACTIVE"
	if c.op.Suspend {
		status = "SUSPENDED"
	}
	biz := scope.UUID()
	if _, err := tx.Exec(ctx, `
UPDATE items SET hosting_status=$3 WHERE business_id=$1 AND item_id=$2`, biz, c.item, status); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
UPDATE cloudflare_targets
SET provider_status=$4,last_error=NULL,last_action_at=date_trunc('second',timezone('UTC',now()))
WHERE business_id=$1 AND item_id=$2 AND target_id=$3`, biz, c.item, c.op.TargetID, status); err != nil {
		return err
	}
	if err := deleteScopedHostingIntent(ctx, tx, scope, c); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
DELETE FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2 AND lease_token=$3`, biz, c.item, c.token)
	return err
}
