package platform

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type scopedHostingFake func(context.Context, business.ID, HostingOperation) error

func (f scopedHostingFake) ApplyHosting(ctx context.Context, scope business.ID, op HostingOperation) error {
	return f(ctx, scope, op)
}

func TestScopedHostingPostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migrationDB, err := sql.Open("pgx", db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer migrationDB.Close()
	gooseProvider, err := goose.NewProvider(goose.DialectPostgres, migrationDB, os.DirFS("../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gooseProvider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	type fixture struct {
		scope                 business.ID
		item, invoice, target pgtype.UUID
	}
	seed := func(scopeStr, record string) fixture {
		t.Helper()
		var f fixture
		f.scope, err = business.Parse(scopeStr)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO businesses(business_id) VALUES ($1) ON CONFLICT DO NOTHING`, f.scope.UUID())
		if err := pool.QueryRow(ctx, `INSERT INTO items(business_id,item_id,name,item_type,is_hosting,hosting_status,hosting_suspension_enabled,hosting_grace_days) VALUES ($1,gen_random_uuid(),'Host','SERVICE',true,'ACTIVE',true,2) RETURNING item_id`, f.scope.UUID()).Scan(&f.item); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO cloudflare_targets(business_id,target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ($1,gen_random_uuid(),$2,'zone',$3,'example.com','A','192.0.2.1','192.0.2.2','ACTIVE') RETURNING target_id`, f.scope.UUID(), f.item, record).Scan(&f.target); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,due_date) VALUES ($1,gen_random_uuid(),'MILESTONE','USD',10,10,'OPEN','2026-01-01') RETURNING invoice_id`, f.scope.UUID()).Scan(&f.invoice); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO invoice_line_items(business_id,line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ($1,gen_random_uuid(),$2,$3,0,'Hosting',1,10,10)`, f.scope.UUID(), f.invoice, f.item)
		return f
	}
	a := seed("10000000-0000-0000-0000-000000000001", "a")
	b := seed("10000000-0000-0000-0000-000000000002", "b")
	noCall := scopedHostingFake(func(context.Context, business.ID, HostingOperation) error {
		t.Error("unexpected provider call")
		return errors.New("unexpected provider call")
	})
	day := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	if _, err := RunScopedHostingEnforcement(ctx, pool, business.ID{}, noCall, day); err == nil {
		t.Fatal("zero business accepted")
	}
	if _, err := RunScopedHostingEnforcement(ctx, pool, a.scope, noCall, day.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := claimScopedHosting(ctx, pool, a.scope, b.item, pgtype.UUID{}, "2026-01-04"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign item claim: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		counts, err := RunScopedHostingEnforcement(ctx, pool, a.scope, scopedHostingFake(func(ctx context.Context, scope business.ID, op HostingOperation) error {
			if scope != a.scope || op.RecordID != "a" || !op.Suspend {
				return errors.New("wrong A operation")
			}
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}), day)
		if err == nil && counts.Suspended != 1 {
			err = errors.New("A did not suspend")
		}
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("A exited: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if _, err := RunScopedHostingEnforcement(ctx, pool, a.scope, noCall, day); !errors.Is(err, ErrHostingEnforcementAlreadyRunning) {
		t.Fatalf("duplicate A worker: %v", err)
	}
	counts, err := RunScopedHostingEnforcement(ctx, pool, b.scope, scopedHostingFake(func(_ context.Context, scope business.ID, op HostingOperation) error {
		if scope != b.scope || op.RecordID != "b" || !op.Suspend {
			t.Error("wrong B operation")
		}
		return errors.New("uncertain response")
	}), day)
	if err != nil || counts.Errors != 1 {
		t.Fatalf("B did not run independently: %+v %v", counts, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var repair bool
	if err := pool.QueryRow(ctx, `SELECT repair_required FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2`, b.scope.UUID(), b.item).Scan(&repair); err != nil || !repair {
		t.Fatalf("B lost uncertainty: %v %v", repair, err)
	}

	exec(`UPDATE invoices SET status='PAID',balance_due=0 WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), a.invoice)
	exec(`INSERT INTO hosting_restore_intents(business_id,invoice_id,item_id) VALUES ($1,$2,$3)`, a.scope.UUID(), a.invoice, a.item)
	if err := ConsumeScopedHostingRestoreIntents(ctx, pool, b.scope, noCall); err != nil {
		t.Fatal(err)
	}
	// An earlier malformed intent has a token without an expiry. Normalize
	// it under its item lock and keep its unpaid invoice from starving A.
	malformed := seed(a.scope.String(), "malformed")
	exec(`INSERT INTO hosting_restore_intents(business_id,invoice_id,item_id,lease_token,created_at)
VALUES ($1,$2,$3,gen_random_uuid(),now()-interval '1 minute')`, a.scope.UUID(), malformed.invoice, malformed.item)
	restoreEntered, restoreRelease := make(chan struct{}), make(chan struct{})
	restoreDone := make(chan error, 1)
	go func() {
		restoreDone <- ConsumeScopedHostingRestoreIntents(ctx, pool, a.scope, scopedHostingFake(func(ctx context.Context, scope business.ID, op HostingOperation) error {
			if scope != a.scope || op.RecordID != "a" || op.Suspend {
				t.Error("wrong restore operation")
			}
			close(restoreEntered)
			select {
			case <-restoreRelease:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}))
	}()
	select {
	case <-restoreEntered:
	case err := <-restoreDone:
		t.Fatalf("A restore exited before calling provider: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer func() {
		select {
		case <-restoreRelease:
		default:
			close(restoreRelease)
		}
	}()
	if err := ConsumeScopedHostingRestoreIntents(ctx, pool, a.scope, noCall); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE items SET hosting_suspension_enabled=false WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), malformed.item)
	if _, err := RunScopedHostingEnforcement(ctx, pool, a.scope, noCall, day); err != nil {
		t.Fatal(err)
	}
	close(restoreRelease)
	if err := <-restoreDone; err != nil {
		t.Fatal(err)
	}

	var malformedNormalized bool
	if err := pool.QueryRow(ctx, `SELECT lease_token IS NULL AND lease_until IS NULL AND next_attempt_at>now()
FROM hosting_restore_intents WHERE business_id=$1 AND invoice_id=$2 AND item_id=$3`, a.scope.UUID(), malformed.invoice, malformed.item).Scan(&malformedNormalized); err != nil || !malformedNormalized {
		t.Fatalf("malformed earlier intent was not normalized: %v %v", malformedNormalized, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT hosting_status FROM items WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), a.item).Scan(&status); err != nil || status != "ACTIVE" {
		t.Fatalf("A was not restored: %s %v", status, err)
	}

	// A claim and all of its identifiers belong to the scoped business.
	exec(`UPDATE invoices SET status='OPEN',balance_due=10 WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), a.invoice)
	claim, action, err := claimScopedHosting(ctx, pool, a.scope, a.item, pgtype.UUID{}, "2026-01-04")
	if err != nil || !action {
		t.Fatalf("A claim: %v %v", action, err)
	}
	bLease := func() pgtype.UUID {
		t.Helper()
		var token pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT lease_token FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2`, b.scope.UUID(), b.item).Scan(&token); err != nil {
			t.Fatal(err)
		}
		return token
	}
	foreignToken := bLease()
	stale := claim
	stale.token = foreignToken
	if ready, err := deliverScopedHosting(ctx, pool, a.scope, noCall, stale, "2026-01-04"); ready || err != nil {
		t.Fatalf("foreign token authorized: %v %v", ready, err)
	}
	if done, err := settleScopedHosting(ctx, pool, a.scope, stale, "2026-01-04", false, false); done || err != nil {
		t.Fatalf("foreign token completed: %v %v", done, err)
	}
	if ready, err := deliverScopedHosting(ctx, pool, b.scope, noCall, claim, "2026-01-04"); ready || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign item authorized: %v %v", ready, err)
	}

	// Reopening a balance within grace cannot authorize a pending PATCH.
	exec(`UPDATE invoices SET due_date='2026-01-03' WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), a.invoice)
	if ready, err := deliverScopedHosting(ctx, pool, a.scope, noCall, claim, "2026-01-04"); ready || err != nil {
		t.Fatalf("in-grace preflight: %v %v", ready, err)
	}
	assertReview := func(f fixture) {
		t.Helper()
		var review bool
		if err := pool.QueryRow(ctx, `SELECT manual_review_required FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2`, f.scope.UUID(), f.item).Scan(&review); err != nil || !review {
			t.Fatalf("expected manual review: %v %v", review, err)
		}
	}
	assertReview(a)
	clearA := func() {
		t.Helper()
		exec(`DELETE FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), a.item)
		exec(`UPDATE items SET hosting_suspension_enabled=true,hosting_status='ACTIVE' WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), a.item)
		exec(`UPDATE invoices SET due_date='2026-01-01' WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), a.invoice)
	}
	claimA := func() scopedHostingClaim {
		t.Helper()
		c, action, err := claimScopedHosting(ctx, pool, a.scope, a.item, pgtype.UUID{}, "2026-01-04")
		if err != nil || !action {
			t.Fatalf("A claim: %v %v", action, err)
		}
		return c
	}
	clearA()
	claim = claimA()
	foreignTarget := claim
	foreignTarget.op.TargetID = b.target
	if done, err := deliverScopedHosting(ctx, pool, a.scope, noCall, foreignTarget, "2026-01-04"); done || !errors.Is(err, errScopedHostingReview) {
		t.Fatalf("foreign target: %v %v", done, err)
	}
	assertReview(a)

	clearA()
	claim = claimA()
	exec(`UPDATE items SET hosting_suspension_enabled=false WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), a.item)
	if done, err := deliverScopedHosting(ctx, pool, a.scope, noCall, claim, "2026-01-04"); done || !errors.Is(err, errScopedHostingReview) {
		t.Fatalf("revoked eligibility: %v %v", done, err)
	}
	assertReview(a)

	clearA()
	claim = claimA()
	if done, err := deliverScopedHosting(ctx, pool, a.scope, scopedHostingFake(func(_ context.Context, _ business.ID, _ HostingOperation) error {
		exec(`UPDATE cloudflare_targets SET record_id='changed' WHERE business_id=$1 AND target_id=$2`, a.scope.UUID(), a.target)
		return nil
	}), claim, "2026-01-04"); done || !errors.Is(err, errScopedHostingReview) {
		t.Fatalf("in-flight target drift: %v %v", done, err)
	}
	assertReview(a)
	exec(`UPDATE cloudflare_targets SET record_id='a' WHERE business_id=$1 AND target_id=$2`, a.scope.UUID(), a.target)

	clearA()
	claim = claimA()
	attemptCtx, stopAttempt := context.WithCancel(ctx)
	defer stopAttempt()
	if done, err := deliverScopedHosting(attemptCtx, pool, a.scope, scopedHostingFake(func(_ context.Context, _ business.ID, _ HostingOperation) error {
		stopAttempt()
		return nil
	}), claim, "2026-01-04"); done || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled response: %v %v", done, err)
	}
	assertClaim := func(itemStatus, providerStatus string) {
		t.Helper()
		var token pgtype.UUID
		var item, provider string
		if err := pool.QueryRow(ctx, `
SELECT l.lease_token,i.hosting_status,t.provider_status
FROM hosting_operation_leases l
JOIN items i ON i.business_id=l.business_id AND i.item_id=l.item_id
JOIN cloudflare_targets t ON t.business_id=l.business_id AND t.item_id=l.item_id
WHERE l.business_id=$1 AND l.item_id=$2`, a.scope.UUID(), a.item).Scan(&token, &item, &provider); err != nil || token != claim.token || item != itemStatus || provider != providerStatus {
			t.Fatalf("claim state: %v %v %v %v", token, item, provider, err)
		}
	}
	assertClaim("SUSPEND_PENDING", "ACTIVE")
	exec(`ALTER TABLE cloudflare_targets ADD CONSTRAINT test_no_suspend CHECK (provider_status<>'SUSPENDED')`)
	if done, err := settleScopedHosting(ctx, pool, a.scope, claim, "2026-01-04", false, false); done || err == nil {
		t.Fatalf("partial completion: %v %v", done, err)
	}
	assertClaim("SUSPEND_PENDING", "ACTIVE")
	exec(`ALTER TABLE cloudflare_targets DROP CONSTRAINT test_no_suspend`)
	exec(`UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second' WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), a.item)
	old := claim
	claim = claimA()
	if ready, err := deliverScopedHosting(ctx, pool, a.scope, noCall, old, "2026-01-04"); ready || err != nil {
		t.Fatalf("expired claim authorized: %v %v", ready, err)
	}
	assertClaim("SUSPEND_PENDING", "ACTIVE")
	if bLease() != foreignToken {
		t.Fatal("A changed B's lease")
	}

	// If settlement is revoked during restore, keep the retry and compensate
	// only after the scoped enforcement scan confirms the debt is overdue.
	clearA()
	exec(`UPDATE items SET hosting_status='SUSPENDED' WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), a.item)
	exec(`UPDATE cloudflare_targets SET provider_status='SUSPENDED' WHERE business_id=$1 AND target_id=$2`, a.scope.UUID(), a.target)
	exec(`UPDATE invoices SET status='PAID',balance_due=0 WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), a.invoice)
	exec(`INSERT INTO hosting_restore_intents(business_id,invoice_id,item_id) VALUES ($1,$2,$3)`, a.scope.UUID(), a.invoice, a.item)
	if err := ConsumeScopedHostingRestoreIntents(ctx, pool, a.scope, scopedHostingFake(func(_ context.Context, scope business.ID, op HostingOperation) error {
		if scope != a.scope || op.Suspend {
			t.Error("expected owned restore")
		}
		exec(`UPDATE invoices SET status='OPEN',balance_due=10 WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), a.invoice)
		return errors.New("restore result uncertain")
	})); err != nil {
		t.Fatal(err)
	}
	var repairOperation string
	if err := pool.QueryRow(ctx, `SELECT operation,repair_required FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), a.item).Scan(&repairOperation, &repair); err != nil || repairOperation != "SUSPEND" || !repair {
		t.Fatalf("restore compensation missing: %v %v %v", repairOperation, repair, err)
	}
	if counts, err := RunScopedHostingEnforcement(ctx, pool, a.scope, scopedHostingFake(func(_ context.Context, scope business.ID, op HostingOperation) error {
		if scope != a.scope || !op.Suspend {
			t.Error("expected owned compensating suspension")
		}
		return nil
	}), day); err != nil || counts.Suspended != 1 {
		t.Fatalf("compensating suspension: %+v %v", counts, err)
	}

	// A failed restore can retry when automatic suspension was disabled
	// at the original claim, even if the enforcement scan runs in between.
	disabled := seed(a.scope.String(), "disabled")
	exec(`UPDATE items SET hosting_status='SUSPENDED',hosting_suspension_enabled=false WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), disabled.item)
	exec(`UPDATE cloudflare_targets SET provider_status='SUSPENDED' WHERE business_id=$1 AND target_id=$2`, a.scope.UUID(), disabled.target)
	exec(`UPDATE invoices SET status='PAID',balance_due=0 WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), disabled.invoice)
	exec(`INSERT INTO hosting_restore_intents(business_id,invoice_id,item_id) VALUES ($1,$2,$3)`, a.scope.UUID(), disabled.invoice, disabled.item)
	attempts := 0
	fake := scopedHostingFake(func(_ context.Context, scope business.ID, op HostingOperation) error {
		if scope != a.scope || op.RecordID != "disabled" || op.Suspend {
			t.Error("expected disabled item's restore")
		}
		attempts++
		if attempts == 1 {
			return errors.New("uncertain response")
		}
		return nil
	})
	if err := ConsumeScopedHostingRestoreIntents(ctx, pool, a.scope, fake); err != nil {
		t.Fatal(err)
	}
	if counts, err := RunScopedHostingEnforcement(ctx, pool, a.scope, noCall, day); err != nil || counts.Errors != 0 {
		t.Fatalf("scan preempted restore: %+v %v", counts, err)
	}
	exec(`UPDATE hosting_restore_intents SET next_attempt_at=now() WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), disabled.invoice)
	if err := ConsumeScopedHostingRestoreIntents(ctx, pool, a.scope, fake); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("restore retry attempts: %d", attempts)
	}

	// Invalid-only intents cannot keep a disabled restore lease in limbo.
	for _, revoke := range []string{
		`UPDATE invoices SET status='VOID' WHERE business_id=$1 AND invoice_id=$2`,
		`DELETE FROM invoice_line_items WHERE business_id=$1 AND invoice_id=$2`,
	} {
		exec(`DELETE FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), disabled.item)
		exec(`UPDATE items SET hosting_status='SUSPENDED' WHERE business_id=$1 AND item_id=$2`, a.scope.UUID(), disabled.item)
		exec(`UPDATE cloudflare_targets SET provider_status='SUSPENDED' WHERE business_id=$1 AND target_id=$2`, a.scope.UUID(), disabled.target)
		exec(`UPDATE invoices SET status='PAID' WHERE business_id=$1 AND invoice_id=$2`, a.scope.UUID(), disabled.invoice)
		exec(`INSERT INTO hosting_restore_intents(business_id,invoice_id,item_id) VALUES ($1,$2,$3)
ON CONFLICT (business_id,invoice_id,item_id) DO UPDATE SET next_attempt_at=now()`, a.scope.UUID(), disabled.invoice, disabled.item)
		attempts = 0
		if err := ConsumeScopedHostingRestoreIntents(ctx, pool, a.scope, fake); err != nil || attempts != 1 {
			t.Fatalf("restore uncertainty: %d %v", attempts, err)
		}
		exec(revoke, a.scope.UUID(), disabled.invoice)
		if counts, err := RunScopedHostingEnforcement(ctx, pool, a.scope, noCall, day); err != nil || counts.Errors != 1 {
			t.Fatalf("invalid restore intent: %+v %v", counts, err)
		}
		assertReview(disabled)
	}
}
