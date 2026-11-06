package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestUsageScheduledWorkerSanitizesRealFailures(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	d := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), d.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,billing_cycle_ref) VALUES ('30000000-0000-0000-0000-000000000001','USAGE','USD',10,10,'DRAFT','{"cut_off_day":31}'),('30000000-0000-0000-0000-000000000002','USAGE','USD',10,10,'DRAFT','{"cut_off_day":"bad"}'),('30000000-0000-0000-0000-000000000003','USAGE','USD',10,10,'DRAFT','{"cut_off_day":31}'); CREATE FUNCTION usage_log_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'database-secret-456'; END $$; CREATE TRIGGER usage_log_fail BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION usage_log_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	meter := platform.UsageMeterFunc(func(_ context.Context, id pgtype.UUID) (decimal.Decimal, error) {
		if id.Bytes[15] == 1 {
			return decimal.Zero, errors.New("provider-secret-123")
		}
		return decimal.NewFromInt(20), nil
	})
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	ticks := make(chan time.Time, 1)
	ran := make(chan struct{}, 1)
	waits := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	job := usageScanJob(logger, func(ctx context.Context) (int, error) {
		n, e := platform.ScanUsageCutoff(ctx, p, time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC), meter)
		return n, e
	})
	w := startScheduledDailyWorker(ctx, true, func() time.Time { return time.Date(2026, 11, 1, 2, 59, 0, 0, time.UTC) }, func(time.Duration) (<-chan time.Time, func()) { waits <- struct{}{}; return ticks, func() {} }, logger, nextUsageScan, "usage cutoff scan", job)
	<-waits
	ticks <- time.Now()
	go func() { <-waits; ran <- struct{}{} }()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		cancel()
		w.Wait()
		t.Fatal("worker did not run")
	}
	cancel()
	w.Wait()
	for _, part := range []string{"locked=0", "failed=3", "30000000-0000-0000-0000-000000000001", "meter_failed", "30000000-0000-0000-0000-000000000002", "invalid_billing_ref", "30000000-0000-0000-0000-000000000003", "invoice_update_failed"} {
		if !strings.Contains(logs.String(), part) {
			t.Fatalf("missing %q in %s", part, logs.String())
		}
	}
	if strings.Contains(logs.String(), "provider-secret-123") || strings.Contains(logs.String(), "database-secret-456") {
		t.Fatalf("secret in logs: %s", logs.String())
	}
	var changed int
	err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE amount<>10 OR billing_cycle_ref ? 'locked'`).Scan(&changed)
	if err != nil || changed != 0 {
		t.Fatalf("unexpected writes %d %v", changed, err)
	}
}
