package platform

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestUsageScanLocksDueInvoiceAndRequiresMeter(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,billing_cycle_ref) VALUES ('30000000-0000-0000-0000-000000000001','USAGE','USD',10,10,'DRAFT','PPH23',2,true,'{"cut_off_day":31}')`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	if _, err = ScanUsageCutoff(context.Background(), p, now, nil); err == nil {
		t.Fatal("missing meter accepted")
	}
	var ref, amount string
	if err = p.QueryRow(context.Background(), `SELECT billing_cycle_ref::text, amount::text FROM invoices`).Scan(&ref, &amount); err != nil || strings.Contains(ref, `"locked"`) || amount != "10.0000" {
		t.Fatalf("missing meter mutated invoice: %s %s %v", ref, amount, err)
	}
	meter := UsageMeterFunc(func(context.Context, pgtype.UUID) (decimal.Decimal, error) { return decimal.NewFromInt(100), nil })
	conn, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	key := int64(crc32.ChecksumIEEE([]byte("usage-lock-cutoff-scan")))
	if _, err = conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	if n, err := ScanUsageCutoff(context.Background(), p, now, meter); err != nil || n != 0 {
		t.Fatalf("advisory contention: %d %v", n, err)
	}
	if _, err = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestUsageScanLockProcess$")
	cmd.Env = append(os.Environ(), "USAGE_LOCK_DB="+d.URL())
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdin, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		stdin.Close()
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	line, e := bufio.NewReader(stdout).ReadString('\n')
	if e != nil || strings.TrimSpace(line) != "READY" {
		t.Fatalf("lock child %q %v", line, e)
	}
	if n, e := ScanUsageCutoff(context.Background(), p, now, meter); e != nil || n != 0 {
		t.Fatalf("process contention: %d %v", n, e)
	}
	stdin.Close()
	if e = cmd.Wait(); e != nil {
		t.Fatal(e)
	}
	n, err := ScanUsageCutoff(context.Background(), p, now, meter)
	if err != nil || n != 1 {
		t.Fatalf("scan %d %v", n, err)
	}
	if err = p.QueryRow(context.Background(), `SELECT billing_cycle_ref::text, amount::text FROM invoices`).Scan(&ref, &amount); err != nil || !strings.Contains(ref, `"locked": true`) || !strings.Contains(ref, `2026-10-31`) || amount != "102.0400" {
		t.Fatalf("invoice: %s %s %v", ref, amount, err)
	}
	n, err = ScanUsageCutoff(context.Background(), p, now, meter)
	if err != nil || n != 0 {
		t.Fatalf("rerun %d %v", n, err)
	}
	_, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,billing_cycle_ref) VALUES ('30000000-0000-0000-0000-000000000002','USAGE','USD',10,10,'DRAFT','{"cut_off_day":31}'),('30000000-0000-0000-0000-000000000003','USAGE','USD',10,10,'DRAFT','{"cut_off_day":31}'),('30000000-0000-0000-0000-000000000004','USAGE','USD',10,10,'DRAFT','{"cut_off_day":"bad"}')`)
	if err != nil {
		t.Fatal(err)
	}
	failing := UsageMeterFunc(func(_ context.Context, id pgtype.UUID) (decimal.Decimal, error) {
		if id.Bytes[15] == 2 {
			return decimal.Zero, errors.New("provider-secret-123")
		}
		return decimal.NewFromInt(25), nil
	})
	n, err = ScanUsageCutoff(context.Background(), p, now, failing)
	if err == nil || n != 1 {
		t.Fatalf("isolation: %d %v", n, err)
	}
	var report *UsageScanError
	if !errors.As(err, &report) || report.Locked != 1 || len(report.Failures) != 2 {
		t.Fatalf("missing structured failures: %#v", err)
	}
	if report.Failures[0].Category != UsageMeterFailed || report.Failures[0].InvoiceID != "30000000-0000-0000-0000-000000000002" || report.Failures[1].Category != UsageInvalidBillingRef || strings.Contains(err.Error(), "provider-secret-123") {
		t.Fatalf("unsafe or wrong failures: %#v %v", report, err)
	}
	var failed, successful bool
	err = p.QueryRow(context.Background(), `SELECT (SELECT billing_cycle_ref ? 'locked' FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000002'),(SELECT billing_cycle_ref ? 'locked' FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000003')`).Scan(&failed, &successful)
	if err != nil || failed || !successful {
		t.Fatalf("isolation snapshot %v %v %v", failed, successful, err)
	}
	_, err = p.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000004'; CREATE FUNCTION usage_scan_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'database-secret-456'; END $$; CREATE TRIGGER usage_scan_fail BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION usage_scan_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err = ScanUsageCutoff(context.Background(), p, now, meter); err == nil || n != 0 {
		t.Fatalf("write failure: %d %v", n, err)
	}
	if !errors.As(err, &report) || len(report.Failures) != 1 || report.Failures[0].Category != UsageInvoiceUpdateFailed || report.Failures[0].InvoiceID != "30000000-0000-0000-0000-000000000002" {
		t.Fatalf("write category: %#v", err)
	}
	if err = p.QueryRow(context.Background(), `SELECT billing_cycle_ref::text,amount::text FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000002'`).Scan(&ref, &amount); err != nil || strings.Contains(ref, `"locked"`) || amount != "10.0000" {
		t.Fatalf("write rollback: %s %s %v", ref, amount, err)
	}
	_, err = p.Exec(context.Background(), `DROP TRIGGER usage_scan_fail ON invoices; DROP FUNCTION usage_scan_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	active, cancelActive := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := ScanUsageCutoff(active, p, now, UsageMeterFunc(func(ctx context.Context, _ pgtype.UUID) (decimal.Decimal, error) {
			close(entered)
			<-ctx.Done()
			return decimal.Zero, ctx.Err()
		}))
		done <- e
	}()
	<-entered
	cancelActive()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatalf("active cancellation: %v", e)
	}
	if err = p.QueryRow(context.Background(), `SELECT billing_cycle_ref::text,amount::text FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000002'`).Scan(&ref, &amount); err != nil || strings.Contains(ref, `"locked"`) || amount != "10.0000" {
		t.Fatalf("cancellation rollback: %s %s %v", ref, amount, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = ScanUsageCutoff(ctx, p, now, meter); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}

}

func TestUsageDueCutoffAndMalformedValues(t *testing.T) {
	for _, tc := range []struct {
		raw       string
		day       int
		want, bad bool
	}{
		{`{"cut_off_day":31}`, 31, true, false}, {`{"cut_off_day":" 31 "}`, 31, true, false},
		{`{"cut_off_day":30}`, 31, false, false}, {`{"cut_off_day":31,"locked":true}`, 31, false, false},
		{`{}`, 31, false, false}, {`null`, 31, false, false}, {`[]`, 31, false, false}, {`0`, 31, false, false}, {`{"cut_off_day":"bad"}`, 31, false, true}, {`[1]`, 31, false, true},
	} {
		got, err := usageDue([]byte(tc.raw), tc.day)
		if got != tc.want || (err != nil) != tc.bad {
			t.Errorf("%s: %v %v", tc.raw, got, err)
		}
	}
}

func TestUsageScanLockProcess(t *testing.T) {
	db := os.Getenv("USAGE_LOCK_DB")
	if db == "" {
		return
	}
	p, err := pgxpool.New(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	conn, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	key := int64(crc32.ChecksumIEEE([]byte("usage-lock-cutoff-scan")))
	if _, err = conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
	if _, err = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatal(err)
	}
}
