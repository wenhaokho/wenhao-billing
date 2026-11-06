package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestHostingRestoreSuccess(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','MILESTONE','USD',100,0,'PAID'); INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status,hosting_suspension_enabled) VALUES ('50000000-0000-0000-0000-000000000001','Hosting','SERVICE',true,'SUSPENDED',true); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'Hosting',1,100,100); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','record','example.com','A','192.0.2.1','192.0.2.2','SUSPENDED'); INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	handler, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	callAPI := func(path, body string) (int, []byte) {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code, w.Body.Bytes()
	}
	createOpen := func() string {
		code, body := callAPI("/api/v1/invoices", `{"customer_id":"40000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"item_id":"50000000-0000-0000-0000-000000000001","description":"Hosting","quantity":1,"unit_price":10}]}`)
		if code != 201 {
			t.Errorf("create invoice %d %s", code, body)
			return ""
		}
		var result struct {
			InvoiceID string `json:"invoice_id"`
		}
		if e := json.Unmarshal(body, &result); e != nil {
			t.Error(e)
			return ""
		}
		code, body = callAPI("/api/v1/invoices/"+result.InvoiceID+"/finalize", "")
		if code != 200 {
			t.Errorf("finalize invoice %d %s", code, body)
		}
		return result.InvoiceID
	}
	calls := 0
	var providerFails atomic.Bool
	var duringRequest func()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if duringRequest != nil {
			duringRequest()
		}
		if providerFails.Load() {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	provider, err := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	var item, target string
	var n int
	err = p.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status,(SELECT count(*) FROM hosting_restore_intents) FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&item, &target, &n)
	if err != nil {
		t.Fatal(err)
	}
	if item != "ACTIVE" || target != "ACTIVE" || n != 0 || calls != 1 {
		t.Fatalf("item=%s target=%s pending=%d calls=%d", item, target, n, calls)
	}
	// A second open invoice prevents restoration even though the triggering invoice is paid.
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000002','40000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10); INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("restored despite outstanding invoice: %d", calls)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_restore_intents`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("blocked intent must remain retryable: %d %v", n, err)
	}

	_, err = p.Exec(context.Background(), `UPDATE invoices SET status='PAID',balance_due=0 WHERE invoice_id='30000000-0000-0000-0000-000000000002'; UPDATE items SET hosting_status='CANCELLED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("restored cancelled item: %d", calls)
	}

	// Failures persist a redacted error and a retryable lease; retry is idempotent.
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	providerFails.Store(true)
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	var failure string
	if err = p.QueryRow(context.Background(), `SELECT last_error FROM cloudflare_targets`).Scan(&failure); err != nil || failure != "Cloudflare request failed" {
		t.Fatalf("failure=%q err=%v", failure, err)
	}
	if calls != 2 {
		t.Fatalf("failed attempt calls=%d", calls)
	}
	providerFails.Store(false)
	_, err = p.Exec(context.Background(), `UPDATE hosting_restore_intents SET next_attempt_at=now()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("retry/repeat calls=%d", calls)
	}

	// A balance introduced during the provider call must not be marked restored.
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	duringRequest = func() { createOpen() }
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	duringRequest = nil
	if err = p.QueryRow(context.Background(), `SELECT hosting_status FROM items`).Scan(&item); err != nil || item != "SUSPENDED" {
		t.Fatalf("stale completion: %s %v", item, err)
	}

	// While a restore is in flight, another invoice can be finalized and paid.
	// Its payment creates a second durable intent and the final recheck sees no balance.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET status='PAID',balance_due=0; UPDATE hosting_restore_intents SET next_attempt_at=now()`)
	if err != nil {
		t.Fatal(err)
	}
	duringRequest = func() {
		id := createOpen()
		code, body := callAPI("/api/v1/invoices/"+id+"/record-payment", `{"amount":10,"payment_date":"2026-01-01"}`)
		if code != 201 {
			t.Errorf("payment %d %s", code, body)
		}
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	duringRequest = nil
	if err = p.QueryRow(context.Background(), `SELECT hosting_status FROM items`).Scan(&item); err != nil || item != "ACTIVE" {
		t.Fatalf("payment race: %s %v", item, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	// Unknown item state fails closed instead of activating a stale target.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET status='PAID',balance_due=0; UPDATE items SET hosting_status='UNKNOWN'; UPDATE hosting_restore_intents SET next_attempt_at=now()`)
	if err != nil {
		t.Fatal(err)
	}
	previousCalls := calls
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	if calls != previousCalls {
		t.Fatal("unknown item status was restored")
	}

	// Missing targets do not cause a provider action or discard pending work.
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; DELETE FROM cloudflare_targets; INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_restore_intents`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("missing target lost intent: %d %v", n, err)
	}
	_, err = p.Exec(context.Background(), `DELETE FROM hosting_restore_intents; INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','record','example.com','A','192.0.2.1','192.0.2.2','SUSPENDED')`)
	if err != nil {
		t.Fatal(err)
	}
	// A crash after provider success is retried after lease expiry with the same payload.
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := claimHostingRestore(context.Background(), p)
	if err != nil || !found || !claim.token.Valid {
		t.Fatalf("claim: %v %v", found, err)
	}
	if err = provider.Restore(context.Background(), claim.target); err != nil {
		t.Fatal(err)
	}
	beforeRetry := calls
	_, err = p.Exec(context.Background(), `UPDATE hosting_restore_intents SET lease_until=now()-interval '1 second'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = finishHostingRestore(context.Background(), p, claim, nil); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(context.Background(), `SELECT hosting_status FROM items`).Scan(&item); err != nil || item != "SUSPENDED" {
		t.Fatalf("expired lease finalized: %s %v", item, err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	if calls != beforeRetry+1 {
		t.Fatalf("expired lease did not retry: %d", calls)
	}
	// A database failure after an accepted PATCH rolls back both state changes.
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001'); CREATE FUNCTION task68_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$; CREATE TRIGGER task68_fail BEFORE UPDATE ON cloudflare_targets FOR EACH ROW EXECUTE FUNCTION task68_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err == nil {
		t.Fatal("expected finalization failure")
	}
	if err = p.QueryRow(context.Background(), `SELECT hosting_status FROM items`).Scan(&item); err != nil || item != "SUSPENDED" {
		t.Fatalf("non-atomic finish: %s %v", item, err)
	}
	_, err = p.Exec(context.Background(), `DROP TRIGGER task68_fail ON cloudflare_targets; DROP FUNCTION task68_fail(); UPDATE hosting_restore_intents SET lease_until=now()-interval '1 second'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}

	// Distinct workers cannot claim the same item while one PATCH is in flight.
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001'),('30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001')`)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	duringRequest = func() { close(entered); <-release }
	go func() { done <- ConsumeHostingRestoreIntents(context.Background(), p, provider) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not entered")
	}
	var leased int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_restore_intents WHERE lease_token IS NOT NULL`).Scan(&leased); err != nil || leased != 1 {
		t.Fatalf("lease count %d: %v", leased, err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	duringRequest = nil
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_restore_intents`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("remaining intents %d: %v", n, err)
	}

	// Exercise Python's real service against the same isolated DB and fake HTTP server.
	for _, tc := range []struct {
		name         string
		open, fail   bool
		item, target string
	}{
		{"success", false, false, "ACTIVE", "ACTIVE"}, {"open balance", true, false, "SUSPENDED", "SUSPENDED"}, {"provider error", false, true, "SUSPENDED", "ERROR"},
	} {
		_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED',last_error=NULL; UPDATE invoices SET status='PAID',balance_due=0`)
		if err != nil {
			t.Fatal(err)
		}
		if tc.open {
			if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='OPEN',balance_due=10 WHERE invoice_id='30000000-0000-0000-0000-000000000002'`); err != nil {
				t.Fatal(err)
			}
		}
		providerFails.Store(tc.fail)
		cmd := exec.Command("python", "-c", `import os
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session
from app.models.item import Item
from app.services.hosting import restore_hosting_if_eligible
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
with Session(engine) as s:
 item=s.scalars(select(Item)).one()
 try: restore_hosting_if_eligible(s,item)
 except Exception: pass
 s.commit()
`)
		cmd.Dir = "../../../backend"
		cmd.Env = append(os.Environ(), "DB="+db.URL(), "CLOUDFLARE_API_BASE_URL="+s.URL, "CLOUDFLARE_API_TOKEN=secret")
		if output, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("Python %s: %v: %s", tc.name, e, output)
		}
		if err = p.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&item, &target); err != nil || item != tc.item || target != tc.target {
			t.Fatalf("Python %s: %s %s %v", tc.name, item, target, err)
		}
	}

	// A leased sibling must not starve another item's due work.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET status='PAID',balance_due=0; UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status) VALUES ('50000000-0000-0000-0000-000000000002','Hosting 2','SERVICE',true,'SUSPENDED'); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002','z','r','example.org','A','192.0.2.1','192.0.2.2','SUSPENDED'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000003','30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002',1,'Hosting',1,10,10); INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001'),('30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001'),('30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002')`)
	if err != nil {
		t.Fatal(err)
	}
	first, found, err := claimHostingRestore(context.Background(), p)
	if err != nil || !found || !first.token.Valid {
		t.Fatalf("first claim: %v %v", found, err)
	}
	second, found, err := claimHostingRestore(context.Background(), p)
	if err != nil || !found || !second.token.Valid || first.item == second.item {
		t.Fatalf("other item starved: found=%v err=%v", found, err)
	}

}
