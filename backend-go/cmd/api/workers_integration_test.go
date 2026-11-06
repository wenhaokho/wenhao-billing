package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type lifecycleSender struct {
	calls atomic.Int32
	fail  atomic.Bool
}

func (s *lifecycleSender) Send(context.Context, email.Message) error {
	s.calls.Add(1)
	if s.fail.Load() {
		return errors.New("secret")
	}
	return nil
}

type lifecycleProvider struct{ calls atomic.Int32 }

func (p *lifecycleProvider) Restore(context.Context, platform.HostingRestoreTarget) error {
	p.calls.Add(1)
	return nil
}
func TestWorkersDeliverDurableQueuesAcrossReplicas(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,invoice_number) VALUES ('30000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','MILESTONE','USD',100,0,'PAID',false,'INV-1'); INSERT INTO recurring_draft_events(invoice_id,cycle_key,invoice_number,customer_name,amount,currency) VALUES ('30000000-0000-0000-0000-000000000001','2025-02-01','INV-1','Customer',100,'USD'); INSERT INTO recurring_draft_deliveries(invoice_id,user_id,email) VALUES ('30000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','a@example.com'); INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status) VALUES ('50000000-0000-0000-0000-000000000001','Hosting','SERVICE',true,'SUSPENDED'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'Hosting',1,100,100); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','record','example.com','A','192.0.2.1','192.0.2.2','SUSPENDED'); INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	s := &lifecycleSender{}
	s.fail.Store(true)
	provider := &lifecycleProvider{}
	jobs := []workerJob{func(ctx context.Context) error { return platform.DeliverRecurringDrafts(ctx, p, s, "") }, func(ctx context.Context) error { return platform.ConsumeHostingRestoreIntents(ctx, p, provider) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.New(io.Discard, "", 0)
	disabled := startWorkers(ctx, false, time.Millisecond, time.Second, logger, jobs...)
	disabled.Wait()
	if s.calls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatal("disabled sent work")
	}
	a := startWorkers(ctx, true, 10*time.Millisecond, time.Second, logger, jobs...)
	b := startWorkers(ctx, true, 10*time.Millisecond, time.Second, logger, jobs...)
	deadline := time.Now().Add(3 * time.Second)
	var pending, remaining int
	for {
		err = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM recurring_draft_deliveries WHERE delivered_at IS NULL AND attempts=1 AND next_attempt_at>now()),(SELECT count(*) FROM hosting_restore_intents)`).Scan(&pending, &remaining)
		if err != nil {
			t.Fatal(err)
		}
		if s.calls.Load() >= 1 && provider.calls.Load() >= 1 && pending == 1 && remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue did not settle: email=%d hosting=%d pending=%d remaining=%d", s.calls.Load(), provider.calls.Load(), pending, remaining)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	a.Wait()
	b.Wait()
	if s.calls.Load() != 1 || provider.calls.Load() != 1 {
		t.Fatalf("duplicate or missing calls email=%d hosting=%d", s.calls.Load(), provider.calls.Load())
	}
	err = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM recurring_draft_deliveries WHERE delivered_at IS NULL AND attempts=1 AND next_attempt_at>now()),(SELECT count(*) FROM hosting_restore_intents)`).Scan(&pending, &remaining)
	if err != nil || pending != 1 || remaining != 0 {
		t.Fatalf("queue state %d %d %v", pending, remaining, err)
	}
	// Separate OS processes share the same durable claim table.
	_, err = p.Exec(context.Background(), `CREATE TABLE lifecycle_calls (id serial); UPDATE recurring_draft_deliveries SET delivered_at=NULL, next_attempt_at=now(), lease_token=NULL, lease_until=NULL`)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerProcessHelper$", "-test.count=1")
			cmd.Env = append(os.Environ(), "TASK69_CHILD_DB="+db.URL())
			out, e := cmd.CombinedOutput()
			if e != nil {
				failures <- errors.New(string(out))
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	var calls int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM lifecycle_calls`).Scan(&calls); err != nil || calls != 1 {
		t.Fatalf("process calls %d: %v", calls, err)
	}

}

type databaseSender struct{ pool *pgxpool.Pool }

func (s databaseSender) Send(ctx context.Context, _ email.Message) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO lifecycle_calls DEFAULT VALUES`)
	return err
}
func TestWorkerProcessHelper(t *testing.T) {
	dsn := os.Getenv("TASK69_CHILD_DB")
	if dsn == "" {
		return
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w := startWorkers(ctx, true, 10*time.Millisecond, time.Second, log.New(io.Discard, "", 0), func(ctx context.Context) error { return platform.DeliverRecurringDrafts(ctx, p, databaseSender{p}, "") })
	w.Wait()
}
