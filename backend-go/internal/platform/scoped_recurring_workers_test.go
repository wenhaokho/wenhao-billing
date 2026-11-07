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

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type recurringTestSender func(context.Context, email.Message) error

func (s recurringTestSender) Send(ctx context.Context, m email.Message) error { return s(ctx, m) }

func TestScopedRecurringWorkersPostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	migrationDB, err := sql.Open("pgx", db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer migrationDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, migrationDB, os.DirFS("../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
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
	count := func(query string, want int, args ...any) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: got %d want %d, %v", query, got, want, err)
		}
	}
	const userA = "11000000-0000-0000-0000-000000000001"
	const userB = "11000000-0000-0000-0000-000000000002"
	const newUserA = "11000000-0000-0000-0000-000000000003"
	const customerA = "12000000-0000-0000-0000-000000000001"
	const customerB = "12000000-0000-0000-0000-000000000002"
	const templateA = "13000000-0000-0000-0000-000000000001"
	const templateB = "13000000-0000-0000-0000-000000000002"
	const templateA2 = "13000000-0000-0000-0000-000000000003"
	const tenantA = "10000000-0000-0000-0000-000000000001"
	const tenantB = "10000000-0000-0000-0000-000000000002"
	a, _ := business.Parse(tenantA)
	b, _ := business.Parse(tenantB)
	exec("INSERT INTO businesses(business_id) VALUES ($1),($2)", tenantA, tenantB)
	exec("INSERT INTO users(business_id,user_id,email,password_hash,role) VALUES ($1,$2,'a@example.test','x','admin'),($3,$4,'b@example.test','x','admin')", tenantA, userA, tenantB, userB)
	exec("INSERT INTO customers(business_id,customer_id,name) VALUES ($1,$2,'Customer A'),($3,$4,'Customer B')", tenantA, customerA, tenantB, customerB)
	const insertTemplate = `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref)
	VALUES ($1,$2,$3,'RECURRING','USD',10,10,'DRAFT',true,'{"frequency":"MONTHLY","start_date":"2025-01-01"}')`
	exec(insertTemplate, tenantA, templateA, customerA)
	exec(insertTemplate, tenantA, templateA2, customerA)
	exec(insertTemplate, tenantB, templateB, customerB)

	block, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(context.Background())
	if _, err := block.Exec(ctx, "SELECT invoice_id FROM invoices WHERE business_id=$1 AND invoice_id=$2 FOR UPDATE", tenantA, templateA); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 2, 15, 2, 0, 0, 0, time.UTC)
	type scanResult struct {
		n   int
		err error
	}
	scanDone := make(chan scanResult, 1)
	go func() {
		n, err := ScanScopedRecurring(ctx, pool, a, now)
		scanDone <- scanResult{n, err}
	}()
	for {
		// A is waiting on its first template after reading the recipient snapshot.
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock'
			AND query LIKE '%FROM invoices%' AND query LIKE '%FOR UPDATE%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case result := <-scanDone:
			t.Fatalf("scan did not wait on the owned invoice: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if n, err := ScanScopedRecurring(ctx, pool, a, now); err != nil || n != 0 {
		t.Fatalf("duplicate scan: %d, %v", n, err)
	}
	if _, found, err := claimScopedRecurringDraft(ctx, pool, a); err != nil || found {
		t.Fatalf("claim during own scan: %v, %v", found, err)
	}
	if n, err := ScanScopedRecurring(ctx, pool, b, now); err != nil || n != 1 {
		t.Fatalf("independent scan: %d, %v", n, err)
	}
	bClaim, found, err := claimScopedRecurringDraft(ctx, pool, b)
	if err != nil || !found {
		t.Fatalf("independent claim: %v, %v", found, err)
	}
	exec("INSERT INTO users(business_id,user_id,email,password_hash,role) VALUES ($1,$2,'new@example.test','x','admin')", tenantA, newUserA)
	exec("UPDATE users SET email='updated@example.test' WHERE business_id=$1 AND user_id=$2", tenantA, userA)
	if err := block.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if result := <-scanDone; result.err != nil || result.n != 2 {
		t.Fatalf("first scan: %+v", result)
	}
	if n, err := ScanScopedRecurring(ctx, pool, a, now); err != nil || n != 2 {
		t.Fatalf("idempotent scan: %d, %v", n, err)
	}
	count("SELECT count(*) FROM recurring_draft_events WHERE business_id=$1", 2, tenantA)
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND user_id=$2 AND email='a@example.test'", 2, tenantA, userA)
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1", 2, tenantA)
	count("SELECT count(DISTINCT scan_batch_id) FROM recurring_draft_events WHERE business_id=$1", 1, tenantA)

	type claimResult struct {
		claim recurringDraftClaim
		found bool
		err   error
	}
	// Hold A's claim inside its UPDATE so the competing claims overlap in
	// PostgreSQL. B can reclaim its expired lease while A holds the job lock.
	exec(`CREATE FUNCTION pause_scoped_claim() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.business_id='10000000-0000-0000-0000-000000000001' THEN
			PERFORM pg_advisory_xact_lock(120001);
		END IF;
		RETURN NEW;
	END $$;
	CREATE TRIGGER pause_scoped_claim BEFORE UPDATE ON recurring_draft_deliveries FOR EACH ROW EXECUTE FUNCTION pause_scoped_claim()`)
	claimBlock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer claimBlock.Rollback(context.Background())
	if _, err := claimBlock.Exec(ctx, "SELECT pg_advisory_xact_lock(120001)"); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE recurring_draft_deliveries SET lease_until=now()-interval '1 second' WHERE business_id=$1", tenantB)
	claims := make(chan claimResult, 1)
	go func() {
		claim, found, err := claimScopedRecurringDraft(ctx, pool, a)
		claims <- claimResult{claim, found, err}
	}()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event='advisory'
			AND query LIKE '%UPDATE recurring_draft_deliveries%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case result := <-claims:
			t.Fatalf("claim did not wait on the test lock: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if _, found, err := claimScopedRecurringDraft(ctx, pool, a); err != nil || found {
		t.Fatalf("duplicate claim: %v, %v", found, err)
	}
	bClaim, found, err = claimScopedRecurringDraft(ctx, pool, b)
	if err != nil || !found {
		t.Fatalf("claim in B during claim in A: %v, %v", found, err)
	}
	if err := claimBlock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-claims
	if result.err != nil || !result.found {
		t.Fatalf("first claim: %+v", result)
	}
	aClaim := result.claim
	exec("DROP TRIGGER pause_scoped_claim ON recurring_draft_deliveries")
	var foreignInvoice pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT invoice_id FROM recurring_draft_deliveries WHERE business_id=$1", tenantB).Scan(&foreignInvoice); err != nil {
		t.Fatal(err)
	}
	bClaim.items = []recurringDraftItem{{invoice: foreignInvoice}}
	for _, delivered := range []bool{true, false} {
		if err := finishScopedRecurringDraft(ctx, pool, a, bClaim, delivered); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareScopedRecurringDraft(ctx, pool, a, &bClaim); err != nil || len(bClaim.items) != 0 {
		t.Fatalf("foreign prepare: %d, %v", len(bClaim.items), err)
	}
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND lease_token=$2 AND attempts=0", 1, tenantB, bClaim.token)
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND lease_token=$2 AND attempts=0", 2, tenantA, aClaim.token)

	// Expire the first claim before sending. A stale token cannot renew it.
	exec("UPDATE recurring_draft_deliveries SET lease_until=now()-interval '1 second' WHERE business_id=$1", tenantA)
	if err := prepareScopedRecurringDraft(ctx, pool, a, &aClaim); err != nil || len(aClaim.items) != 0 {
		t.Fatalf("stale prepare: %d, %v", len(aClaim.items), err)
	}
	sendCtx, stop := context.WithCancel(ctx)
	sent := 0
	sender := recurringTestSender(func(_ context.Context, m email.Message) error {
		sent++
		if m.To[0] != "updated@example.test" || !strings.Contains(m.Subject, "2 draft invoices") || strings.Contains(m.Text, "Customer B") {
			t.Errorf("unexpected digest: %+v", m)
		}
		if sent == 1 {
			stop()
		}
		if sent == 2 {
			return errors.New("private provider detail")
		}
		return nil
	})
	defer stop()
	if err := DeliverScopedRecurringDrafts(sendCtx, pool, a, sender, ""); !errors.Is(err, context.Canceled) || sent != 1 {
		t.Fatalf("uncertain acceptance: sent=%d err=%v", sent, err)
	}
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND attempts=1 AND lease_token IS NOT NULL AND delivered_at IS NULL", 2, tenantA)
	if err := DeliverScopedRecurringDrafts(ctx, pool, a, sender, ""); err != nil || sent != 1 {
		t.Fatalf("active lease retry: sent=%d err=%v", sent, err)
	}
	exec("UPDATE recurring_draft_deliveries SET lease_until=now()-interval '1 second' WHERE business_id=$1", tenantA)
	if err := DeliverScopedRecurringDrafts(ctx, pool, a, sender, ""); err != nil || sent != 2 {
		t.Fatalf("provider failure: sent=%d err=%v", sent, err)
	}
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND attempts=2 AND next_attempt_at>now() AND lease_token IS NULL AND last_error='delivery failed'", 2, tenantA)
	exec("UPDATE recurring_draft_deliveries SET next_attempt_at=now() WHERE business_id=$1", tenantA)
	if err := DeliverScopedRecurringDrafts(ctx, pool, a, sender, ""); err != nil || sent != 3 {
		t.Fatalf("successful retry: sent=%d err=%v", sent, err)
	}
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND attempts=3 AND delivered_at IS NOT NULL AND last_error IS NULL", 2, tenantA)

	// Ownership must hold even when an outbox snapshot names a foreign user or invoice.
	exec("UPDATE recurring_draft_deliveries SET lease_until=now()-interval '1 second' WHERE business_id=$1", tenantB)
	exec("UPDATE recurring_draft_deliveries SET user_id=$2 WHERE business_id=$1", tenantB, userA)
	exec(`INSERT INTO recurring_draft_events(business_id,invoice_id,cycle_key,invoice_number,customer_name,amount,currency)
		VALUES ($1,$2,'2025-02-01','foreign','foreign',10,'USD')`, tenantB, templateA)
	exec("INSERT INTO recurring_draft_deliveries(business_id,invoice_id,user_id,email) VALUES ($1,$2,$3,'b@example.test')", tenantB, templateA, userB)
	if err := DeliverScopedRecurringDrafts(ctx, pool, b, sender, ""); err != nil || sent != 3 {
		t.Fatalf("foreign references reached sender: sent=%d err=%v", sent, err)
	}
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND attempts=0 AND cancelled_at IS NOT NULL AND last_error='recipient unavailable'", 1, tenantB)
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND attempts=0 AND cancelled_at IS NOT NULL AND last_error='invoice unavailable'", 1, tenantB)

	exec(`CREATE FUNCTION reject_scoped_draft() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'fixture failure'; END $$;
	CREATE TRIGGER reject_scoped_draft BEFORE INSERT ON recurring_draft_events FOR EACH ROW EXECUTE FUNCTION reject_scoped_draft()`)
	march := now.AddDate(0, 1, 0)
	if n, err := ScanScopedRecurring(ctx, pool, a, march); n != 0 || err == nil {
		t.Fatalf("failed outbox insert: %d, %v", n, err)
	}
	count("SELECT count(*) FROM invoices WHERE business_id=$1 AND NOT is_template", 2, tenantA)
	exec("DROP TRIGGER reject_scoped_draft ON recurring_draft_events")
	if n, err := ScanScopedRecurring(ctx, pool, a, march); n != 2 || err != nil {
		t.Fatalf("scan after rollback: %d, %v", n, err)
	}

	// A revoked recipient after claim gets no provider call. Cancellation is durable
	// and doesn't increment attempts.
	revoked, found, err := claimScopedRecurringDraft(ctx, pool, a)
	if err != nil || !found {
		t.Fatalf("claim for revocation: %v, %v", found, err)
	}
	exec("DELETE FROM users WHERE business_id=$1 AND user_id=$2", tenantA, revoked.user)
	if err := prepareScopedRecurringDraft(ctx, pool, a, &revoked); err != nil || len(revoked.items) != 0 {
		t.Fatalf("revoked claim: %d, %v", len(revoked.items), err)
	}
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND user_id=$2 AND attempts=0 AND cancelled_at IS NOT NULL AND lease_token IS NULL", 2, tenantA, revoked.user)

	// Delete a live invoice while preflight is trying to lock it. The other
	// member must still be sent, and completion must leave the deleted member
	// cancelled with no attempt or delivered timestamp.
	partial, found, err := claimScopedRecurringDraft(ctx, pool, a)
	if err != nil || !found {
		t.Fatalf("claim for partial digest: %v, %v", found, err)
	}
	var deleted pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT invoice_id FROM recurring_draft_deliveries WHERE business_id=$1 AND lease_token=$2 ORDER BY invoice_id LIMIT 1", tenantA, partial.token).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	deletion, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer deletion.Rollback(context.Background())
	if _, err := deletion.Exec(ctx, "DELETE FROM invoices WHERE business_id=$1 AND invoice_id=$2", tenantA, deleted); err != nil {
		t.Fatal(err)
	}
	prepared := make(chan error, 1)
	go func() { prepared <- prepareScopedRecurringDraft(ctx, pool, a, &partial) }()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock'
			AND query LIKE '%WITH live_invoices%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-prepared:
			t.Fatalf("preflight did not wait on the deleted invoice: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := deletion.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-prepared; err != nil || len(partial.items) != 1 || partial.items[0].invoice == deleted {
		t.Fatalf("partial digest: %+v, %v", partial.items, err)
	}
	if err := finishScopedRecurringDraft(ctx, pool, a, partial, true); err != nil {
		t.Fatal(err)
	}
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND user_id=$2 AND invoice_id=$3 AND attempts=0 AND cancelled_at IS NOT NULL AND delivered_at IS NULL", 1, tenantA, partial.user, deleted)
	count("SELECT count(*) FROM recurring_draft_deliveries WHERE business_id=$1 AND user_id=$2 AND invoice_id=$3 AND attempts=1 AND delivered_at IS NOT NULL", 1, tenantA, partial.user, partial.items[0].invoice)

	// Historical ambiguity remains a safe stop for the affected template.
	exec(`INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref)
		VALUES ($1,gen_random_uuid(),$2,'RECURRING','USD',10,10,'DRAFT',false,jsonb_build_object('template_invoice_id',$3::text,'cycle_key','malformed'))`, tenantA, customerA, templateA)
	april := march.AddDate(0, 1, 0)
	if n, err := ScanScopedRecurring(ctx, pool, a, april); n != 1 || err == nil {
		t.Fatalf("ambiguous cycle: %d, %v", n, err)
	}
	count("SELECT count(*) FROM recurring_draft_events WHERE business_id=$1 AND cycle_key='2025-04-01'", 1, tenantA)
	count("SELECT count(*) FROM recurring_draft_events WHERE business_id=$1", 2, tenantB)

}
