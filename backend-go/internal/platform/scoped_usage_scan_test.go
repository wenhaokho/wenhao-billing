package platform

import (
	"context"
	"database/sql"
	"errors"
	"math"
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
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestScopedUsageScanPostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
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
	t.Run("fence rejects absent business", func(t *testing.T) {
		testCandidateTransactionsRejectAbsentBusiness(t, ctx, pool)
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := business.Parse("10000000-0000-0000-0000-000000000001")
	b, _ := business.Parse("10000000-0000-0000-0000-000000000002")
	const customerB = "11000000-0000-0000-0000-000000000002"
	exec(`INSERT INTO businesses(business_id) VALUES ($1),($2)`, a.UUID(), b.UUID())
	exec(`INSERT INTO customers(business_id,customer_id,name) VALUES ($1,$2,'Customer B')`, b.UUID(), customerB)
	const due = `{"cut_off_day":31,"nested":{"n":12345678901234567890}}`
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.FixedZone("UTC+7", 7*3600))
	insert := func(scope business.ID, currency, ref string) pgtype.UUID {
		t.Helper()
		var id pgtype.UUID
		err := pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,billing_cycle_ref)
VALUES ($1,gen_random_uuid(),'USAGE',$2,1,1,'DRAFT','PPH23',2,true,$3) RETURNING invoice_id`, scope.UUID(), currency, ref).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	snapshot := func(scope business.ID, id pgtype.UUID) string {
		t.Helper()
		var state string
		if err := pool.QueryRow(ctx, `SELECT to_jsonb(i)::text FROM invoices i WHERE business_id=$1 AND invoice_id=$2`, scope.UUID(), id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	expect := func(scope business.ID, id pgtype.UUID, values ...string) {
		t.Helper()
		state := snapshot(scope, id)
		for _, value := range values {
			if !strings.Contains(state, value) {
				t.Fatalf("expected %s in %s", value, state)
			}
		}
	}
	amount := decimal.NewFromInt(100)
	own, foreign := insert(a, "USD", due), insert(b, "IDR", due)
	beforeA, beforeB := snapshot(a, own), snapshot(b, foreign)
	for _, meter := range []ScopedUsageMeter{nil, ScopedUsageMeterFunc(nil)} {
		if n, err := ScanScopedUsageCutoff(ctx, nil, a, now, meter); n != 0 || err == nil || snapshot(a, own) != beforeA || snapshot(b, foreign) != beforeB {
			t.Fatalf("disabled meter (%T): %d %v", meter, n, err)
		}
	}
	type result struct {
		count int
		err   error
	}
	run := func(ctx context.Context, scope business.ID, meter ScopedUsageMeter) <-chan result {
		done := make(chan result, 1)
		go func() {
			n, err := ScanScopedUsageCutoff(ctx, pool, scope, now, meter)
			done <- result{n, err}
		}()
		return done
	}
	await := func(done <-chan result) result {
		t.Helper()
		select {
		case r := <-done:
			return r
		case <-ctx.Done():
			t.Fatal(ctx.Err())
			return result{}
		}
	}
	entered := make(chan struct{})
	meterCtx, cancelMeter := context.WithCancel(ctx)
	defer cancelMeter()
	firstA := run(meterCtx, a, ScopedUsageMeterFunc(func(ctx context.Context, scope business.ID, id pgtype.UUID) (decimal.Decimal, error) {
		if scope != a || id != own {
			return decimal.Zero, errors.New("unexpected metering scope")
		}
		close(entered)
		<-ctx.Done()
		return amount, nil
	}))
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	forbid := ScopedUsageMeterFunc(func(context.Context, business.ID, pgtype.UUID) (decimal.Decimal, error) {
		t.Error("unexpected meter call")
		return decimal.Zero, errors.New("unexpected meter call")
	})
	if n, err := ScanScopedUsageCutoff(ctx, nil, business.ID{}, now, forbid); n != 0 || err == nil {
		t.Fatalf("invalid scope: %d %v", n, err)
	}
	if r := await(run(ctx, a, forbid)); r.count != 0 || r.err != nil {
		t.Fatalf("duplicate A scan: %+v", r)
	}
	if r := await(run(ctx, b, ScopedUsageMeterFunc(func(_ context.Context, scope business.ID, id pgtype.UUID) (decimal.Decimal, error) {
		if scope != b || id != foreign {
			t.Error("B meter received foreign invoice")
		}
		return amount, nil
	}))); r.count != 1 || r.err != nil {
		t.Fatalf("independent B scan: %+v", r)
	}
	expect(b, foreign, `"amount": 102.0000`, `"wht_amount": 2.0000`)
	cancelMeter()
	if r := await(firstA); r.count != 0 || !errors.Is(r.err, context.Canceled) || snapshot(a, own) != beforeA {
		t.Fatalf("cancelled A scan: %+v", r)
	}
	beforeB = snapshot(b, foreign)
	badRef := insert(a, "USD", `{"cut_off_day":"bad"}`)
	damaged := insert(a, "USD", due)
	broken := insert(a, "USD", due)
	failedMeter := insert(a, "USD", due)
	overflow := insert(a, "USD", due)
	grossOverflow := insert(a, "USD", due)
	corruptRate := insert(a, "USD", due)
	nearHalf := insert(a, "USD", due)
	grossOverprecision := insert(a, "USD", due)
	exec(`UPDATE invoices SET wht_rate=101 WHERE business_id=$1 AND invoice_id=$2`, a.UUID(), corruptRate)
	exec(`UPDATE invoices SET wht_gross_up=false WHERE business_id=$1 AND invoice_id=$2`, a.UUID(), nearHalf)
	notDue := insert(a, "USD", `{"cut_off_day":30}`)
	exec(`ALTER TABLE invoices DROP CONSTRAINT invoices_customer_id_fkey`)
	exec(`UPDATE invoices SET customer_id=$3 WHERE business_id=$1 AND invoice_id=$2`, a.UUID(), damaged, customerB)
	exec(`UPDATE invoices SET customer_id=$3 WHERE business_id=$1 AND invoice_id=$2`, a.UUID(), broken, "11000000-0000-0000-0000-000000000003")
	wanted := map[string]UsageFailureCategory{
		badRef.String():             UsageInvalidBillingRef,
		damaged.String():            UsageInvalidReference,
		broken.String():             UsageInvalidReference,
		failedMeter.String():        UsageMeterFailed,
		overflow.String():           UsageMeterFailed,
		grossOverflow.String():      UsageInvoiceUpdateFailed,
		nearHalf.String():           UsageMeterFailed,
		grossOverprecision.String(): UsageMeterFailed,
		corruptRate.String():        UsageInvoiceUpdateFailed,
	}
	before := map[pgtype.UUID]string{}
	for _, id := range []pgtype.UUID{badRef, damaged, broken, failedMeter, overflow, grossOverflow, corruptRate, nearHalf, grossOverprecision, notDue} {
		before[id] = snapshot(a, id)
	}
	n, err := ScanScopedUsageCutoff(ctx, pool, a, now, ScopedUsageMeterFunc(func(_ context.Context, scope business.ID, id pgtype.UUID) (decimal.Decimal, error) {
		if scope != a {
			t.Error("A meter received foreign scope")
		}
		switch id {
		case own, corruptRate:
			return amount, nil
		case failedMeter:
			return decimal.Zero, errors.New("provider credential")
		case nearHalf:
			return decimal.RequireFromString("0.24999999999999999999"), nil
		case grossOverprecision:
			return decimal.RequireFromString("100.00005"), nil
		case grossOverflow:
			return decimal.RequireFromString("999999999999999.9999"), nil
		case overflow:
			return decimal.RequireFromString("10000000000000000"), nil
		default:
			t.Errorf("unexpected meter call for %s", id)
			return decimal.Zero, errors.New("unexpected invoice")
		}
	}))
	var report *UsageScanError
	if n != 1 || !errors.As(err, &report) || len(report.Failures) != len(wanted) || strings.Contains(err.Error(), "provider credential") {
		t.Fatalf("partial scan: %d %v", n, err)
	}
	for _, f := range report.Failures {
		if wanted[f.InvoiceID] != f.Category {
			t.Fatalf("unexpected failure: %+v", f)
		}
		delete(wanted, f.InvoiceID)
	}
	for id, state := range before {
		if snapshot(a, id) != state {
			t.Errorf("invoice %s changed on failure or skip", id)
		}
	}
	expect(a, own, `"amount": 102.0400`, `"wht_amount": 2.0400`, `"wht_base": 100.0000`, `"balance_due": 102.0400`, `"locked": true`, `"cutoff_date": "2026-10-31"`, "12345678901234567890", `"status": "DRAFT"`)
	if snapshot(b, foreign) != beforeB {
		t.Fatal("A scan changed B's invoice")
	}
	for _, id := range []pgtype.UUID{badRef, damaged, broken, failedMeter, overflow, grossOverflow, corruptRate, nearHalf, grossOverprecision} {
		exec(`DELETE FROM invoices WHERE business_id=$1 AND invoice_id=$2`, a.UUID(), id)
	}
	if n, err := ScanScopedUsageCutoff(ctx, pool, a, now, forbid); n != 0 || err != nil {
		t.Fatalf("idempotent rescan: %d %v", n, err)
	}

	zeroExponents := map[pgtype.UUID]int32{
		insert(a, "USD", due): math.MinInt32,
		insert(a, "IDR", due): math.MaxInt32,
	}
	if n, err := ScanScopedUsageCutoff(ctx, pool, a, now, ScopedUsageMeterFunc(func(_ context.Context, scope business.ID, id pgtype.UUID) (decimal.Decimal, error) {
		exponent, ok := zeroExponents[id]
		if !ok || scope != a {
			return decimal.Zero, errors.New("unexpected metering scope")
		}
		return decimal.New(0, exponent), nil
	})); n != len(zeroExponents) || err != nil {
		t.Fatalf("extreme exponent zeros: %d %v", n, err)
	}
	for id := range zeroExponents {
		expect(a, id, `"amount": 0.0000`, `"wht_amount": 0.0000`, `"wht_base": 0.0000`, `"balance_due": 0.0000`, `"locked": true`)
	}

	for _, update := range []string{
		`UPDATE invoices SET status='OPEN' WHERE business_id=$1 AND invoice_id=$2`,
		`UPDATE invoices SET billing_cycle_ref='{"cut_off_day":31,"locked":true}' WHERE business_id=$1 AND invoice_id=$2`,
	} {
		id := insert(a, "USD", due)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(ctx, update, a.UUID(), id); err != nil {
			t.Fatal(err)
		}
		done := run(ctx, a, forbid)
		for {
			var waiting bool
			err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_stat_activity
WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%name: LockInvoice :one%')`).Scan(&waiting)
			if err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if r := await(done); r.count != 0 || r.err != nil {
			t.Fatalf("stale candidate: %+v", r)
		}
		expect(a, id, `"amount": 1.0000`)
	}
}
