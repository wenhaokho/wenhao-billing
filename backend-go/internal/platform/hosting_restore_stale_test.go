package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestHostingRestoreStaleCandidateDoesNotStarveOtherItem(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	password, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: password, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000001','MILESTONE','USD',10,0,'PAID'),('30000000-0000-0000-0000-000000000002','MILESTONE','USD',10,0,'PAID'); INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status) VALUES ('50000000-0000-0000-0000-000000000001','A','SERVICE',true,'SUSPENDED'),('50000000-0000-0000-0000-000000000002','B','SERVICE',true,'SUSPENDED'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'A',1,10,10),('60000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002',0,'B',1,10,10); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','a','a.example','A','192.0.2.1','192.0.2.2','SUSPENDED'),('70000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002','zone','b','b.example','A','192.0.2.1','192.0.2.2','SUSPENDED'); INSERT INTO hosting_restore_intents(invoice_id,item_id,created_at) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',now()-interval '1 minute'),('30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002',now())`)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan string, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.URL.Path
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	provider, err := NewCloudflareRestoreProvider(server.URL, "secret", 8*time.Second, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(context.Background(), `SELECT item_id FROM items WHERE item_id='50000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- ConsumeHostingRestoreIntents(ctx, pool, provider) }()
	}
	deadline := time.Now().Add(5 * time.Second)
	waiting := 0
	for time.Now().Before(deadline) {
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%SELECT is_hosting,coalesce(hosting_status%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiting < 2 {
		cancel()
		close(release)
		t.Fatalf("workers did not both select A before waiting: %d", waiting)
	}
	if err = lock.Commit(ctx); err != nil {
		cancel()
		close(release)
		t.Fatal(err)
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case path := <-entered:
			got[path] = true
		case <-time.After(2 * time.Second):
			close(release)
			cancel()
			<-done
			<-done
			t.Fatalf("second item starved; provider calls: %v", got)
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	if !got["/zones/zone/dns_records/a"] || !got["/zones/zone/dns_records/b"] {
		t.Fatalf("unexpected calls: %v", got)
	}
	var pending, active int
	if err = pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM hosting_restore_intents),(SELECT count(*) FROM items WHERE hosting_status='ACTIVE')`).Scan(&pending, &active); err != nil || pending != 0 || active != 2 {
		t.Fatalf("pending=%d active=%d err=%v", pending, active, err)
	}
}
