package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"encoding/json"
	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func reviewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	_, e = p.Exec(context.Background(), `INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status,hosting_suspension_enabled,hosting_grace_days) VALUES ('50000000-0000-0000-0000-000000000001','Host','SERVICE',true,'ACTIVE',true,0); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('70000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','zone','record','example.com','A','192.0.2.1','192.0.2.2','ACTIVE'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,due_date) VALUES ('30000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN','2026-01-01'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10)`)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func reviewManual(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	var n int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases WHERE manual_review_required`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("manual review: %d %v", n, e)
	}
}
func TestHostingReviewAmbiguousSuspend(t *testing.T) {
	for _, change := range []string{`UPDATE items SET hosting_grace_days=10`, `UPDATE invoices SET due_date='2026-01-10'`, `UPDATE invoices SET status='DRAFT'; INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,due_date) VALUES ('30000000-0000-0000-0000-000000000002','MILESTONE','USD',10,10,'OPEN','2026-01-10'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10)`} {
		t.Run(change, func(t *testing.T) {
			p := reviewPool(t)
			entered, release := make(chan struct{}), make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			defer s.Close()
			provider, e := NewCloudflareRestoreProvider(s.URL, "secret", time.Second*10, s.Client())
			if e != nil {
				t.Fatal(e)
			}
			done := make(chan HostingEnforcementCounts, 1)
			go func() {
				c, _ := RunHostingEnforcement(context.Background(), p, provider, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
				done <- c
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("no provider call")
			}
			if _, e = p.Exec(context.Background(), change); e != nil {
				close(release)
				t.Fatal(e)
			}
			close(release)
			c := <-done
			if c.Errors != 1 || c.Suspended != 0 {
				t.Fatalf("counts: %+v", c)
			}
			reviewManual(t, p)
		})
	}
}
func TestHostingReviewExpiredClaimStateDrift(t *testing.T) {
	for _, change := range []string{`UPDATE items SET hosting_suspension_enabled=false`, `UPDATE items SET hosting_status='CANCELLED'`, `UPDATE items SET hosting_status='ACTIVE'`, `UPDATE items SET is_hosting=false`} {
		t.Run(change, func(t *testing.T) {
			p := reviewPool(t)
			conn, e := p.Acquire(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			var id string
			e = p.QueryRow(context.Background(), `SELECT item_id::text FROM items`).Scan(&id)
			if e != nil {
				t.Fatal(e)
			}
			var item pgtype.UUID
			if e = item.Scan(id); e != nil {
				t.Fatal(e)
			}
			c, action, e := claimEnforcement(context.Background(), conn, item, "2026-01-04")
			conn.Release()
			if e != nil || !action || !c.token.Valid {
				t.Fatalf("claim: %v %v", action, e)
			}
			_, e = p.Exec(context.Background(), `UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second'`)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.Exec(context.Background(), change); e != nil {
				t.Fatal(e)
			}
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{"success":true}`)) }))
			defer s.Close()
			provider, e := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
			if e != nil {
				t.Fatal(e)
			}
			counts, e := RunHostingEnforcement(context.Background(), p, provider, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
			if e != nil || counts.Errors != 1 || calls != 0 {
				t.Fatalf("recovery: %+v calls=%d %v", counts, calls, e)
			}
			reviewManual(t, p)
		})
	}
}
func TestHostingReviewRestoreMaintenanceDrift(t *testing.T) {
	p := reviewPool(t)
	_, e := p.Exec(context.Background(), `UPDATE invoices SET status='PAID',balance_due=0; UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) SELECT invoice_id,'50000000-0000-0000-0000-000000000001' FROM invoices`)
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	provider, e := NewCloudflareRestoreProvider(s.URL, "secret", 10*time.Second, s.Client())
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- ConsumeHostingRestoreIntents(context.Background(), p, provider) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no restore call")
	}
	if _, e = p.Exec(context.Background(), `UPDATE cloudflare_targets SET maintenance_content='192.0.2.3'`); e != nil {
		close(release)
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	reviewManual(t, p)
	var status string
	if e = p.QueryRow(context.Background(), `SELECT hosting_status FROM items`).Scan(&status); e != nil || status != "SUSPENDED" {
		t.Fatalf("status: %s %v", status, e)
	}
}

func TestHostingReviewExpiredAmbiguousSuspension(t *testing.T) {
	p := reviewPool(t)
	conn, e := p.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var id pgtype.UUID
	if e = p.QueryRow(context.Background(), `SELECT item_id FROM items`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	_, action, e := claimEnforcement(context.Background(), conn, id, "2026-01-04")
	conn.Release()
	if e != nil || !action {
		t.Fatalf("claim: %v %v", action, e)
	}
	_, e = p.Exec(context.Background(), `UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second'; UPDATE invoices SET due_date='2026-01-10'`)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{"success":true}`)) }))
	defer s.Close()
	provider, e := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if e != nil {
		t.Fatal(e)
	}
	c, e := RunHostingEnforcement(context.Background(), p, provider, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
	if e != nil || c.Errors != 1 || calls != 0 {
		t.Fatalf("expired ambiguity: %+v calls=%d %v", c, calls, e)
	}
	reviewManual(t, p)
}

func TestHostingReviewExpiredRestoreStateDrift(t *testing.T) {
	for _, change := range []string{`UPDATE items SET hosting_suspension_enabled=false`, `UPDATE items SET hosting_status='CANCELLED'`, `UPDATE items SET hosting_status='ACTIVE'`} {
		t.Run(change, func(t *testing.T) {
			p := reviewPool(t)
			_, e := p.Exec(context.Background(), `UPDATE invoices SET status='PAID',balance_due=0; UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) SELECT invoice_id,'50000000-0000-0000-0000-000000000001' FROM invoices`)
			if e != nil {
				t.Fatal(e)
			}
			c, found, e := claimHostingRestore(context.Background(), p)
			if e != nil || !found || !c.token.Valid {
				t.Fatalf("claim: %v %v", found, e)
			}
			_, e = p.Exec(context.Background(), `UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second'; UPDATE hosting_restore_intents SET lease_until=now()-interval '1 second'`)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.Exec(context.Background(), change); e != nil {
				t.Fatal(e)
			}
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{"success":true}`)) }))
			defer s.Close()
			provider, e := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
			if e != nil {
				t.Fatal(e)
			}
			if e = ConsumeHostingRestoreIntents(context.Background(), p, provider); e != nil {
				t.Fatal(e)
			}
			if calls != 0 {
				t.Fatalf("unexpected provider calls: %d", calls)
			}
			reviewManual(t, p)
		})
	}
}

func TestHostingReviewExpiredRestoreOriginallyDisabled(t *testing.T) {
	p := reviewPool(t)
	_, e := p.Exec(context.Background(), `UPDATE invoices SET status='PAID',balance_due=0; UPDATE items SET hosting_status='SUSPENDED',hosting_suspension_enabled=false; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) SELECT invoice_id,'50000000-0000-0000-0000-000000000001' FROM invoices`)
	if e != nil {
		t.Fatal(e)
	}
	c, found, e := claimHostingRestore(context.Background(), p)
	if e != nil || !found || !c.token.Valid {
		t.Fatalf("claim: %v %v", found, e)
	}
	_, e = p.Exec(context.Background(), `UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second'; UPDATE hosting_restore_intents SET lease_until=now()-interval '1 second'`)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{"success":true}`)) }))
	defer s.Close()
	provider, e := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if e != nil {
		t.Fatal(e)
	}
	if e = ConsumeHostingRestoreIntents(context.Background(), p, provider); e != nil {
		t.Fatal(e)
	}
	if calls != 0 {
		t.Fatalf("disabled recovery called provider %d times", calls)
	}
	reviewManual(t, p)
}

func TestHostingReviewCrashProcess(t *testing.T) {
	dbURL := os.Getenv("HOSTING_REVIEW_CRASH_DB")
	if dbURL == "" {
		return
	}
	p, e := pgxpool.New(context.Background(), dbURL)
	if e != nil {
		t.Fatal(e)
	}
	conn, e := p.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var id pgtype.UUID
	if e = p.QueryRow(context.Background(), `SELECT item_id FROM items`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	c, action, e := claimEnforcement(context.Background(), conn, id, "2026-01-04")
	if e != nil || !action {
		t.Fatalf("claim %v %v", action, e)
	}
	provider, e := NewCloudflareRestoreProvider(os.Getenv("HOSTING_REVIEW_CRASH_SERVER"), "secret", time.Second, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = provider.Suspend(context.Background(), c.target); e != nil {
		t.Fatal(e)
	}
	os.Exit(0)
}
func TestHostingReviewRealCrashAndAdminEdit(t *testing.T) {
	p := reviewPool(t)
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{"success":true}`)) }))
	defer s.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHostingReviewCrashProcess$")
	cmd.Env = append(os.Environ(), "HOSTING_REVIEW_CRASH_DB="+p.Config().ConnString(), "HOSTING_REVIEW_CRASH_SERVER="+s.URL)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("crash process: %v %s", e, out)
	}
	if calls != 1 {
		t.Fatalf("provider calls before crash: %d", calls)
	}
	_, e := p.Exec(context.Background(), `UPDATE hosting_operation_leases SET lease_until=now()-interval '1 second'; UPDATE items SET hosting_status='ACTIVE'`)
	if e != nil {
		t.Fatal(e)
	}
	provider, e := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if e != nil {
		t.Fatal(e)
	}
	c, e := RunHostingEnforcement(context.Background(), p, provider, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
	if e != nil || c.Errors != 1 || calls != 1 {
		t.Fatalf("crash recovery: %+v calls=%d %v", c, calls, e)
	}
	reviewManual(t, p)
}

func TestHostingReviewUncertainSuspendError(t *testing.T) {
	p := reviewPool(t)
	fail := true
	calls := 0
	remote := "192.0.2.1"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b struct {
			Content string `json:"content"`
		}
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Error(e)
		}
		remote = b.Content
		if fail {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	provider, e := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if e != nil {
		t.Fatal(e)
	}
	day := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	c, e := RunHostingEnforcement(context.Background(), p, provider, day)
	if e != nil || c.Errors != 1 || remote != "192.0.2.2" {
		t.Fatalf("accepted error: %+v %s %v", c, remote, e)
	}
	var n int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases WHERE target_fingerprint IS NOT NULL AND item_state_fingerprint IS NOT NULL`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("lost uncertain suspension: %d %v", n, e)
	}
	if _, e = p.Exec(context.Background(), `UPDATE items SET hosting_grace_days=10`); e != nil {
		t.Fatal(e)
	}
	fail = false
	c, e = RunHostingEnforcement(context.Background(), p, provider, day)
	if e != nil || c.Errors != 1 || calls != 1 {
		t.Fatalf("ambiguous retry: %+v calls=%d %v", c, calls, e)
	}
	reviewManual(t, p)
	if _, e = p.Exec(context.Background(), `UPDATE hosting_operation_leases SET manual_review_required=false; UPDATE items SET hosting_grace_days=0`); e != nil {
		t.Fatal(e)
	}
	c, e = RunHostingEnforcement(context.Background(), p, provider, day)
	if e != nil || c.Suspended != 1 || calls != 2 || remote != "192.0.2.2" {
		t.Fatalf("safe retry: %+v calls=%d remote=%s %v", c, calls, remote, e)
	}
}
func TestHostingReviewUncertainRestoreError(t *testing.T) {
	p := reviewPool(t)
	_, e := p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); UPDATE invoices SET status='PAID',balance_due=0; UPDATE items SET hosting_status='SUSPENDED'; UPDATE cloudflare_targets SET provider_status='SUSPENDED'; INSERT INTO hosting_restore_intents(invoice_id,item_id) SELECT invoice_id,'50000000-0000-0000-0000-000000000001' FROM invoices; INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,due_date) VALUES ('30000000-0000-0000-0000-000000000002','MILESTONE','USD',10,10,'DRAFT','2026-01-10'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10)`)
	if e != nil {
		t.Fatal(e)
	}
	fail := true
	calls := 0
	remote := "192.0.2.2"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b struct {
			Content string `json:"content"`
		}
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Error(e)
		}
		remote = b.Content
		if fail {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	provider, e := NewCloudflareRestoreProvider(s.URL, "secret", time.Second, s.Client())
	if e != nil {
		t.Fatal(e)
	}
	if e = ConsumeHostingRestoreIntents(context.Background(), p, provider); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || remote != "192.0.2.1" {
		t.Fatalf("accepted error: calls=%d remote=%s", calls, remote)
	}
	var n int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases WHERE target_fingerprint IS NOT NULL AND item_state_fingerprint IS NOT NULL`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("lost uncertain restore: %d %v", n, e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000002/finalize", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("finalize %d %s", res.Code, res.Body.String())
	}
	fail = false
	day := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	c, e := RunHostingEnforcement(context.Background(), p, provider, day)
	if e != nil || c.Errors != 1 || calls != 1 {
		t.Fatalf("open balance: %+v calls=%d %v", c, calls, e)
	}
	reviewManual(t, p)
	if _, e = p.Exec(context.Background(), `UPDATE hosting_operation_leases SET manual_review_required=false; UPDATE invoices SET status='PAID',balance_due=0 WHERE invoice_id='30000000-0000-0000-0000-000000000002'`); e != nil {
		t.Fatal(e)
	}
	c, e = RunHostingEnforcement(context.Background(), p, provider, day)
	if e != nil || c.Restored != 1 || calls != 2 || remote != "192.0.2.1" {
		t.Fatalf("safe restore: %+v calls=%d remote=%s %v", c, calls, remote, e)
	}
}

func TestHostingReviewUncertainAcceptedTimeout(t *testing.T) {
	p := reviewPool(t)
	accepted := make(chan string, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Content string `json:"content"`
		}
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Error(e)
		}
		accepted <- b.Content
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer s.Close()
	provider, e := NewCloudflareRestoreProvider(s.URL, "secret", 20*time.Millisecond, s.Client())
	if e != nil {
		t.Fatal(e)
	}
	day := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	c, e := RunHostingEnforcement(context.Background(), p, provider, day)
	if e != nil || c.Errors != 1 {
		t.Fatalf("timeout: %+v %v", c, e)
	}
	if got := <-accepted; got != "192.0.2.2" {
		t.Fatalf("accepted content: %q", got)
	}
	var n int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM hosting_operation_leases WHERE repair_required AND target_fingerprint IS NOT NULL AND item_state_fingerprint IS NOT NULL`).Scan(&n); e != nil || n != 1 {
		t.Fatalf("lost timed-out lease: %d %v", n, e)
	}
	if _, e = p.Exec(context.Background(), `UPDATE items SET hosting_grace_days=10`); e != nil {
		t.Fatal(e)
	}
	c, e = RunHostingEnforcement(context.Background(), p, provider, day)
	if e != nil || c.Errors != 1 {
		t.Fatalf("timeout ambiguity: %+v %v", c, e)
	}
	reviewManual(t, p)
	select {
	case extra := <-accepted:
		t.Fatalf("unexpected retry: %s", extra)
	default:
	}
}
