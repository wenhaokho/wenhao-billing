package platform

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestHostingEnforcementPythonDifferential(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfg := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	migrator := goosemigrator.New(".", goosemigrator.WithFS(migrations.Files))
	goDB := pgtestdb.Custom(t, cfg, migrator)
	pyDB := pgtestdb.Custom(t, cfg, migrator)
	goPool, err := pgxpool.New(context.Background(), goDB.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer goPool.Close()
	pyPool, err := pgxpool.New(context.Background(), pyDB.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pyPool.Close()
	seed := `INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status,hosting_suspension_enabled,hosting_grace_days) VALUES ('50000000-0000-0000-0000-000000000001','Host','SERVICE',true,'ACTIVE',true,2); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','record','example.com','A','192.0.2.1','192.0.2.2','ACTIVE'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,due_date) VALUES ('30000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN','2026-01-01'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10)`
	for _, p := range []*pgxpool.Pool{goPool, pyPool} {
		if _, err = p.Exec(context.Background(), seed); err != nil {
			t.Fatal(err)
		}
	}
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(b)
		bodies = append(bodies, string(raw))
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	provider, err := NewCloudflareRestoreProvider(server.URL, "secret", time.Second, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range []int{3, 4, 4} {
		counts, err := RunHostingEnforcement(context.Background(), goPool, provider, time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("python", "-c", `import json,os
from datetime import date
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.workers.tasks.hosting_enforcement import run_hosting_enforcement
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
with Session(engine) as db:
 result=run_hosting_enforcement(db,date(2026,1,int(os.environ['DAY'])))
 db.commit()
 print(json.dumps(result))
`)
		cmd.Dir = "../../../backend"
		cmd.Env = append(os.Environ(), "DB="+pyDB.URL(), "DAY="+fmt.Sprint(day), "CLOUDFLARE_API_BASE_URL="+server.URL, "CLOUDFLARE_API_TOKEN=secret")
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("python: %v %s", e, out)
		}
		var got struct{ Suspended, Restored, Errors int }
		if e = json.Unmarshal(out, &got); e != nil {
			t.Fatalf("python output %s: %v", out, e)
		}
		if counts.Suspended != got.Suspended || counts.Restored != got.Restored || counts.Errors != got.Errors {
			t.Fatalf("day %d Go=%+v Python=%+v", day, counts, got)
		}
		var gs, gt, ps, pt string
		if e = goPool.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&gs, &gt); e != nil {
			t.Fatal(e)
		}
		if e = pyPool.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&ps, &pt); e != nil {
			t.Fatal(e)
		}
		if gs != ps || gt != pt {
			t.Fatalf("day %d Go %s/%s Python %s/%s", day, gs, gt, ps, pt)
		}
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("provider payloads: %v", bodies)
	}
	cases := []struct {
		name, invoiceStatus, itemStatus string
		balance                         int
		template, enabled, hosting      bool
		due                             *string
		grace                           int
	}{
		{"open", "OPEN", "ACTIVE", 10, false, true, true, ptrString("2026-01-01"), 0},
		{"sent", "SENT", "ACTIVE", 10, false, true, true, ptrString("2026-01-01"), 0},
		{"partial", "PARTIAL", "ACTIVE", 1, false, true, true, ptrString("2026-01-01"), 0},
		{"paid", "PAID", "ACTIVE", 0, false, true, true, ptrString("2026-01-01"), 0},
		{"draft", "DRAFT", "ACTIVE", 10, false, true, true, ptrString("2026-01-01"), 0},
		{"void", "VOID", "ACTIVE", 10, false, true, true, ptrString("2026-01-01"), 0},
		{"zero", "OPEN", "ACTIVE", 0, false, true, true, ptrString("2026-01-01"), 0},
		{"template", "OPEN", "ACTIVE", 10, true, true, true, ptrString("2026-01-01"), 0},
		{"no due", "OPEN", "ACTIVE", 10, false, true, true, nil, 0},
		{"disabled", "OPEN", "ACTIVE", 10, false, false, true, ptrString("2026-01-01"), 0},
		{"cancelled", "OPEN", "CANCELLED", 10, false, true, true, ptrString("2026-01-01"), 0},
		{"not hosting", "OPEN", "ACTIVE", 10, false, true, false, ptrString("2026-01-01"), 0},
		{"restore paid", "PAID", "SUSPENDED", 0, false, true, true, ptrString("2026-01-01"), 0},
		{"pending overdue", "OPEN", "SUSPEND_PENDING", 10, false, true, true, ptrString("2026-01-01"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range []*pgxpool.Pool{goPool, pyPool} {
				_, e := p.Exec(context.Background(), `UPDATE items SET hosting_status=$1,hosting_grace_days=$2,hosting_suspension_enabled=$3,is_hosting=$4`, tc.itemStatus, tc.grace, tc.enabled, tc.hosting)
				if e != nil {
					t.Fatal(e)
				}
				_, e = p.Exec(context.Background(), `UPDATE invoices SET status=$1,balance_due=$2,is_template=$3,due_date=$4`, tc.invoiceStatus, tc.balance, tc.template, tc.due)
				if e != nil {
					t.Fatal(e)
				}
				_, e = p.Exec(context.Background(), `UPDATE cloudflare_targets SET provider_status='ACTIVE'; DELETE FROM hosting_operation_leases`)
				if e != nil {
					t.Fatal(e)
				}
			}
			counts, e := RunHostingEnforcement(context.Background(), goPool, provider, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
			if e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command("python", "-c", `import json,os
from datetime import date
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.workers.tasks.hosting_enforcement import run_hosting_enforcement
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
with Session(engine) as db:
 result=run_hosting_enforcement(db,date(2026,1,4)); db.commit(); print(json.dumps(result))
`)
			cmd.Dir = "../../../backend"
			cmd.Env = append(os.Environ(), "DB="+pyDB.URL(), "CLOUDFLARE_API_BASE_URL="+server.URL, "CLOUDFLARE_API_TOKEN=secret")
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("python: %v %s", e, out)
			}
			var expected struct{ Suspended, Restored, Errors int }
			if e = json.Unmarshal(out, &expected); e != nil {
				t.Fatal(e)
			}
			if counts.Suspended != expected.Suspended || counts.Restored != expected.Restored || counts.Errors != expected.Errors {
				t.Fatalf("Go %+v Python %+v", counts, expected)
			}
			var gs, gt, ps, pt string
			if e = goPool.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&gs, &gt); e != nil {
				t.Fatal(e)
			}
			if e = pyPool.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&ps, &pt); e != nil {
				t.Fatal(e)
			}
			if gs != ps || gt != pt {
				t.Fatalf("Go %s/%s Python %s/%s", gs, gt, ps, pt)
			}
		})
	}
}

func ptrString(v string) *string { return &v }

func TestHostingEnforcementGraceAndIdempotency(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status,hosting_suspension_enabled,hosting_grace_days) VALUES ('50000000-0000-0000-0000-000000000001','Host','SERVICE',true,'ACTIVE',true,2); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','record','example.com','A','192.0.2.1','192.0.2.2','ACTIVE'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,due_date) VALUES ('30000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN','2026-01-01'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10)`)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	lastContent := ""
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		lastContent = payload.Content
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	provider, err := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	day := func(n int) time.Time { return time.Date(2026, 1, n, 0, 0, 0, 0, time.UTC) }
	// Python holds exactly the scheduler's session lock in a separate process.
	cmd := exec.Command("python", "-u", "-c", `import os,sys,zlib,psycopg
with psycopg.connect(os.environ['DB'],autocommit=True) as c:
 c.execute('SELECT pg_advisory_lock(%s)',(zlib.crc32(b'hosting-overdue-enforcement'),))
 print('READY',flush=True)
 sys.stdin.read()
`)
	cmd.Env = append(os.Environ(), "DB="+db.URL())
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "READY" {
		t.Fatalf("lock process: %q %v", line, err)
	}
	_, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if !errors.Is(err, ErrHostingEnforcementAlreadyRunning) {
		t.Fatalf("job lock: %v", err)
	}
	_ = in.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	counts, err := RunHostingEnforcement(context.Background(), p, provider, day(3))
	if err != nil || counts.Suspended != 0 || calls != 0 {
		t.Fatalf("boundary: %+v %d %v", counts, calls, err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if err != nil || counts.Suspended != 1 || calls != 1 {
		t.Fatalf("overdue: %+v %d %v", counts, calls, err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if err != nil || counts.Suspended != 0 || calls != 1 {
		t.Fatalf("repeat: %+v %d %v", counts, calls, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE invoices SET balance_due=0,status='PAID'; INSERT INTO hosting_operation_leases(item_id,operation,lease_token,lease_until,target_fingerprint,item_state_fingerprint) SELECT i.item_id,'RESTORE','80000000-0000-0000-0000-000000000001',now()+interval '2 minutes',md5(jsonb_build_array(t.target_id,t.zone_id,t.record_id,t.record_name,t.record_type,t.live_content,t.maintenance_content,t.proxied)::text),md5(jsonb_build_array(i.is_hosting,i.hosting_status,i.hosting_suspension_enabled)::text) FROM items i JOIN cloudflare_targets t USING(item_id)`)
	if err != nil {
		t.Fatal(err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if err != nil || counts.Restored != 0 || calls != 1 {
		t.Fatalf("leased: %+v %d %v", counts, calls, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second'`)
	if err != nil {
		t.Fatal(err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if err != nil || counts.Restored != 1 || calls != 2 {
		t.Fatalf("expired lease: %+v %d %v", counts, calls, err)
	}
	// A real payment during the external PATCH prevents stale suspension state;
	// its restore intent remains until the in-flight operation releases the lease.
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); UPDATE invoices SET customer_id='40000000-0000-0000-0000-000000000001',status='OPEN',balance_due=10`)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer blocked.Close()
	blockingProvider, err := NewCloudflareRestoreProvider(blocked.URL, "secret", time.Second*10, blocked.Client())
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		counts HostingEnforcementCounts
		err    error
	}
	finished := make(chan result, 1)
	go func() {
		c, e := RunHostingEnforcement(context.Background(), p, blockingProvider, day(4))
		finished <- result{c, e}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("suspend PATCH not reached")
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":"10","payment_date":"2026-01-04"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 201 {
		close(release)
		t.Fatalf("payment: %d %s", res.Code, res.Body.String())
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		close(release)
		t.Fatal(err)
	}
	var n int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_restore_intents`).Scan(&n); err != nil || n != 1 {
		close(release)
		t.Fatalf("intent while suspended: %d %v", n, err)
	}
	close(release)
	r := <-finished
	if r.err != nil || r.counts.Suspended != 0 {
		t.Fatalf("stale suspension: %+v %v", r.counts, r.err)
	}
	_, err = p.Exec(context.Background(), `UPDATE hosting_restore_intents SET next_attempt_at=now()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsumeHostingRestoreIntents(context.Background(), p, provider); err != nil {
		t.Fatal(err)
	}
	var itemStatus, providerStatus string
	if err = p.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&itemStatus, &providerStatus); err != nil || itemStatus != "ACTIVE" || providerStatus != "ACTIVE" {
		t.Fatalf("restoration: %s/%s %v", itemStatus, providerStatus, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000002','MILESTONE','USD',10,10,'DRAFT'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10)`)
	if err != nil {
		t.Fatal(err)
	}
	entered2, release2 := make(chan struct{}), make(chan struct{})
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered2)
		<-release2
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server2.Close()
	provider2, err := NewCloudflareRestoreProvider(server2.URL, "secret", 10*time.Second, server2.Client())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, e := RunHostingEnforcement(context.Background(), p, provider2, day(4))
		finished <- result{c, e}
	}()
	select {
	case <-entered2:
	case <-time.After(5 * time.Second):
		t.Fatal("restore PATCH not reached")
	}
	finalize := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000002/finalize", nil)
	finalize.AddCookie(&http.Cookie{Name: "session", Value: token})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, finalize)
	if response.Code != 200 {
		close(release2)
		t.Fatalf("finalize: %d %s", response.Code, response.Body.String())
	}
	close(release2)
	r = <-finished
	if r.err != nil || r.counts.Restored != 0 {
		t.Fatalf("stale restore: %+v %v", r.counts, r.err)
	}
	if err = p.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&itemStatus, &providerStatus); err != nil || itemStatus != "SUSPENDED" || providerStatus != "SUSPENDED" {
		t.Fatalf("finalize recheck: %s/%s %v", itemStatus, providerStatus, err)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases WHERE operation='SUSPEND' AND repair_required`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("missing durable compensation: %d %v", n, err)
	}
	before := calls
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if err != nil || counts.Errors != 1 || calls != before {
		t.Fatalf("ambiguous compensation: %+v calls=%d err=%v", counts, calls, err)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases WHERE manual_review_required`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ambiguous compensation review: %d %v", n, err)
	}
	// Simulate a crash after a restore PATCH but before recording its result.
	_, err = p.Exec(context.Background(), `DELETE FROM hosting_operation_leases; UPDATE invoices SET due_date='2026-01-01' WHERE status='OPEN'; INSERT INTO hosting_operation_leases(item_id,operation,lease_token,lease_until,target_fingerprint,item_state_fingerprint) SELECT i.item_id,'RESTORE','80000000-0000-0000-0000-000000000001',now()-interval '1 second',md5(jsonb_build_array(t.target_id,t.zone_id,t.record_id,t.record_name,t.record_type,t.live_content,t.maintenance_content,t.proxied)::text),md5(jsonb_build_array(i.is_hosting,i.hosting_status,i.hosting_suspension_enabled)::text) FROM items i JOIN cloudflare_targets t USING(item_id)`)
	if err != nil {
		t.Fatal(err)
	}
	before = calls
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if err != nil || counts.Suspended != 1 || calls != before+1 || lastContent != "192.0.2.2" {
		t.Fatalf("crash recovery: %+v calls=%d err=%v", counts, calls, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='ACTIVE'; UPDATE cloudflare_targets SET provider_status='ACTIVE'; UPDATE invoices SET due_date='2026-01-01' WHERE invoice_id='30000000-0000-0000-0000-000000000002'`)
	if err != nil {
		t.Fatal(err)
	}
	entered3, release3 := make(chan struct{}), make(chan struct{})
	server3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered3)
		<-release3
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server3.Close()
	provider3, err := NewCloudflareRestoreProvider(server3.URL, "secret", 10*time.Second, server3.Client())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, e := RunHostingEnforcement(context.Background(), p, provider3, day(4))
		finished <- result{c, e}
	}()
	select {
	case <-entered3:
	case <-time.After(5 * time.Second):
		t.Fatal("target change PATCH not reached")
	}
	_, err = p.Exec(context.Background(), `UPDATE cloudflare_targets SET record_id='changed'`)
	if err != nil {
		close(release3)
		t.Fatal(err)
	}
	close(release3)
	r = <-finished
	if r.err != nil || r.counts.Errors != 1 {
		t.Fatalf("target change: %+v %v", r.counts, r.err)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases WHERE manual_review_required`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("target change escalation: %d %v", n, err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if err != nil || counts.Errors != 1 {
		t.Fatalf("unresolved target escalation: %+v %v", counts, err)
	}
	// Crash before completion followed by a target edit must not retry against
	// the replacement record using the old operation.
	_, err = p.Exec(context.Background(), `DELETE FROM hosting_operation_leases; UPDATE items SET hosting_status='SUSPEND_PENDING'; UPDATE cloudflare_targets SET record_id='old',provider_status='ACTIVE'; INSERT INTO hosting_operation_leases(item_id,operation,lease_token,lease_until,target_fingerprint) SELECT item_id,'SUSPEND','80000000-0000-0000-0000-000000000001',now()-interval '1 second',md5(jsonb_build_array(target_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,proxied)::text) FROM cloudflare_targets; UPDATE cloudflare_targets SET record_id='replacement'`)
	if err != nil {
		t.Fatal(err)
	}
	before = calls
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day(4))
	if err != nil || counts.Errors != 1 || calls != before {
		t.Fatalf("crash target drift: %+v calls=%d %v", counts, calls, err)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases WHERE manual_review_required`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("crash drift escalation: %d %v", n, err)
	}
}

func TestHostingEnforcementIndependentFailuresAndRetry(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status,hosting_suspension_enabled) VALUES ('50000000-0000-0000-0000-000000000001','Missing','SERVICE',true,'ACTIVE',true),('50000000-0000-0000-0000-000000000002','Configured','SERVICE',true,'ACTIVE',true); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002','zone','record','example.com','A','192.0.2.1','192.0.2.2','ACTIVE'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,due_date) VALUES ('30000000-0000-0000-0000-000000000001','MILESTONE','USD',20,20,'OPEN','2026-01-01'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'Missing',1,10,10),('60000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000002',1,'Configured',1,10,10)`)
	if err != nil {
		t.Fatal(err)
	}
	fail := true
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"secret":"sensitive"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	provider, err := NewCloudflareRestoreProvider(s.URL, "sensitive", time.Second, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	counts, err := RunHostingEnforcement(context.Background(), p, provider, day)
	if err != nil || counts.Errors != 2 || calls != 1 {
		t.Fatalf("failed: %+v calls=%d err=%v", counts, calls, err)
	}
	var status, providerStatus, lastError string
	err = p.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status,t.last_error FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&status, &providerStatus, &lastError)
	if err != nil || status != "SUSPEND_PENDING" || providerStatus != "ERROR" || strings.Contains(lastError, "sensitive") {
		t.Fatalf("unsafe failure: %s %s %q %v", status, providerStatus, lastError, err)
	}
	fail = false
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day)
	if err != nil || counts.Errors != 1 || counts.Suspended != 1 || calls != 2 {
		t.Fatalf("retry: %+v calls=%d err=%v", counts, calls, err)
	}
	err = p.QueryRow(context.Background(), `SELECT hosting_status FROM items WHERE item_id='50000000-0000-0000-0000-000000000001'`).Scan(&status)
	if err != nil || status != "ACTIVE" {
		t.Fatalf("other item changed: %s %v", status, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = RunHostingEnforcement(ctx, p, provider, day); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='ACTIVE' WHERE item_id='50000000-0000-0000-0000-000000000002'; UPDATE cloudflare_targets SET provider_status='ACTIVE'`)
	if err != nil {
		t.Fatal(err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(200 * time.Millisecond) }))
	defer slow.Close()
	timed, err := NewCloudflareRestoreProvider(slow.URL, "sensitive", 20*time.Millisecond, slow.Client())
	if err != nil {
		t.Fatal(err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, timed, day)
	if err != nil || counts.Errors != 2 {
		t.Fatalf("provider timeout: %+v %v", counts, err)
	}
	if err = p.QueryRow(context.Background(), `SELECT last_error FROM cloudflare_targets`).Scan(&lastError); err != nil || strings.Contains(lastError, "sensitive") {
		t.Fatalf("timeout error: %q %v", lastError, err)
	}
	_, err = p.Exec(context.Background(), `CREATE FUNCTION task73_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$; CREATE TRIGGER task73_fail BEFORE UPDATE ON cloudflare_targets FOR EACH ROW EXECUTE FUNCTION task73_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day)
	if err != nil || counts.Errors != 2 {
		t.Fatalf("injected failure: %+v %v", counts, err)
	}
	if err = p.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t USING(item_id)`).Scan(&status, &providerStatus); err != nil || status != "SUSPEND_PENDING" || providerStatus != "ERROR" {
		t.Fatalf("atomic rollback: %s/%s %v", status, providerStatus, err)
	}
	_, err = p.Exec(context.Background(), `DROP TRIGGER task73_fail ON cloudflare_targets; DROP FUNCTION task73_fail(); UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second'`)
	if err != nil {
		t.Fatal(err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day)
	if err != nil || counts.Suspended != 1 || counts.Errors != 1 {
		t.Fatalf("retry after rollback: %+v %v", counts, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE items SET hosting_status='ACTIVE' WHERE item_id='50000000-0000-0000-0000-000000000002'; UPDATE cloudflare_targets SET provider_status='ACTIVE'`)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }))
	defer blocked.Close()
	blockedProvider, err := NewCloudflareRestoreProvider(blocked.URL, "secret", 10*time.Second, blocked.Client())
	if err != nil {
		t.Fatal(err)
	}
	callCtx, cancelCall := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := RunHostingEnforcement(callCtx, p, blockedProvider, day); done <- e }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel PATCH not reached")
	}
	cancelCall()
	if err = <-done; !errors.Is(err, context.Canceled) {
		close(release)
		t.Fatalf("in-flight cancellation: %v", err)
	}
	close(release)
	var pending int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("cancel lost lease: %d %v", pending, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second'`)
	if err != nil {
		t.Fatal(err)
	}
	counts, err = RunHostingEnforcement(context.Background(), p, provider, day)
	if err != nil || counts.Suspended != 1 || counts.Errors != 1 {
		t.Fatalf("cancel retry: %+v %v", counts, err)
	}
}

func TestHostingEnforcementPythonMultipleItemsAndTimeout(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint("timeout=", timeout), func(t *testing.T) {
			u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
			if err != nil || u.User == nil {
				t.Fatal("PGTESTDB_URL required")
			}
			pw, _ := u.User.Password()
			cfg := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
			m := goosemigrator.New(".", goosemigrator.WithFS(migrations.Files))
			gd := pgtestdb.Custom(t, cfg, m)
			pd := pgtestdb.Custom(t, cfg, m)
			gp, err := pgxpool.New(context.Background(), gd.URL())
			if err != nil {
				t.Fatal(err)
			}
			defer gp.Close()
			pp, err := pgxpool.New(context.Background(), pd.URL())
			if err != nil {
				t.Fatal(err)
			}
			defer pp.Close()
			seed := `INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status,hosting_suspension_enabled,hosting_grace_days) VALUES ('50000000-0000-0000-0000-000000000001','Configured','SERVICE',true,'ACTIVE',true,0),('50000000-0000-0000-0000-000000000002','Missing','SERVICE',true,'ACTIVE',true,0); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','record','example.com','A','192.0.2.1','192.0.2.2','ACTIVE'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,due_date,created_at) VALUES ('30000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN','2026-01-02','2026-01-02'),('30000000-0000-0000-0000-000000000002','MILESTONE','USD',10,10,'PARTIAL','2026-01-01','2026-01-01'),('30000000-0000-0000-0000-000000000003','MILESTONE','USD',10,10,'SENT','2026-01-01','2026-01-01'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10),('60000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10),('60000000-0000-0000-0000-000000000003','30000000-0000-0000-0000-000000000003','50000000-0000-0000-0000-000000000002',0,'Missing',1,10,10)`
			for _, p := range []*pgxpool.Pool{gp, pp} {
				if _, err = p.Exec(context.Background(), seed); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if timeout {
					time.Sleep(200 * time.Millisecond)
				}
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			defer server.Close()
			d := time.Second
			if timeout {
				d = 20 * time.Millisecond
			}
			provider, err := NewCloudflareRestoreProvider(server.URL, "secret", d, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			counts, err := RunHostingEnforcement(context.Background(), gp, provider, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("python", "-c", `import json,os
from datetime import date
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.workers.tasks.hosting_enforcement import run_hosting_enforcement
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
with Session(engine) as db:
 result=run_hosting_enforcement(db,date(2026,1,4)); db.commit(); print(json.dumps(result))
`)
			cmd.Dir = "../../../backend"
			seconds := "1"
			if timeout {
				seconds = "0.02"
			}
			cmd.Env = append(os.Environ(), "DB="+pd.URL(), "CLOUDFLARE_API_BASE_URL="+server.URL, "CLOUDFLARE_API_TOKEN=secret", "CLOUDFLARE_TIMEOUT_SECONDS="+seconds)
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("python %v %s", e, out)
			}
			var expected struct{ Suspended, Restored, Errors int }
			if e = json.Unmarshal(out, &expected); e != nil {
				t.Fatal(e)
			}
			if counts.Suspended != expected.Suspended || counts.Restored != expected.Restored || counts.Errors != expected.Errors {
				t.Fatalf("Go %+v Python %+v", counts, expected)
			}
			for _, id := range []string{"50000000-0000-0000-0000-000000000001", "50000000-0000-0000-0000-000000000002"} {
				var gs, ps string
				if err = gp.QueryRow(context.Background(), `SELECT hosting_status FROM items WHERE item_id=$1`, id).Scan(&gs); err != nil {
					t.Fatal(err)
				}
				if err = pp.QueryRow(context.Background(), `SELECT hosting_status FROM items WHERE item_id=$1`, id).Scan(&ps); err != nil {
					t.Fatal(err)
				}
				if gs != ps {
					t.Fatalf("item %s Go=%s Python=%s", id, gs, ps)
				}
			}
		})
	}
}

func TestHostingEnforcementSerializesRealPayment(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status,hosting_suspension_enabled,hosting_grace_days) VALUES ('50000000-0000-0000-0000-000000000001','Host','SERVICE',true,'ACTIVE',true,0); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','record','example.com','A','192.0.2.1','192.0.2.2','ACTIVE'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,due_date) VALUES ('30000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN','2026-01-01'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10)`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"success":true}`)) }))
	defer s.Close()
	provider, err := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(context.Background(), `SELECT target_id FROM cloudflare_targets FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	scanDone := make(chan error, 1)
	go func() {
		_, e := RunHostingEnforcement(context.Background(), p, provider, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
		scanDone <- e
	}()
	waitFor := func(pattern string) bool {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var waiting bool
			if e := p.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting); e != nil {
				t.Error(e)
				return false
			}
			if waiting {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}
	if !waitFor("%FROM cloudflare_targets WHERE item_id=$1 FOR UPDATE%") {
		t.Fatal("scan did not wait on locked target")
	}
	paymentDone := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":"10","payment_date":"2026-01-04"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		paymentDone <- w.Code
	}()
	observed := waitFor("%SELECT i.item_id FROM items i WHERE i.is_hosting%FOR UPDATE%")
	if err = blocker.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-paymentDone; code != 201 {
		t.Fatalf("payment code %d", code)
	}
	if err = <-scanDone; err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("real payment did not wait for scan item lock")
	}
}
