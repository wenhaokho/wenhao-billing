package platform

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestRecurringScanCatchupAndIdempotency(t *testing.T) {
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
	if n, err := ScanRecurring(context.Background(), p, time.Date(2025, 2, 15, 2, 0, 0, 0, time.UTC)); err != nil || n != 0 {
		t.Fatalf("empty scan: %d %v", n, err)
	}
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','RECURRING','USD',10,10,'DRAFT',true,'{"frequency":"MONTHLY","start_date":"2025-01-01"}'); INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001',0,'Service',1,10,10)`)
	if err != nil {
		t.Fatal(err)
	}

	// The scanner must not require a second connection while holding the job lock.
	oneCfg, err := pgxpool.ParseConfig(db.URL())
	if err != nil {
		t.Fatal(err)
	}
	oneCfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(context.Background(), oneCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	bounded, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	singleCount, singleErr := ScanRecurring(bounded, one, time.Date(2025, 2, 15, 2, 0, 0, 0, time.UTC))
	cancel()
	if singleErr != nil || singleCount != 1 {
		t.Fatalf("one-connection scan: %d %v", singleCount, singleErr)
	}
	now := time.Date(2025, 2, 15, 2, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		n, err := ScanRecurring(context.Background(), p, now)
		if err != nil || n != 1 {
			t.Fatalf("scan %d: count=%d err=%v", i, n, err)
		}
	}
	var n int
	var key string
	if err = p.QueryRow(context.Background(), `SELECT count(*),min(billing_cycle_ref->>'cycle_key') FROM invoices WHERE NOT is_template`).Scan(&n, &key); err != nil || n != 1 || key != "2025-02-01" {
		t.Fatalf("children %d %s: %v", n, key, err)
	}
	sender := &outboxSender{failures: map[string]bool{}}
	if err = DeliverRecurringDrafts(context.Background(), p, sender, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','staff')`); err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','RECURRING','USD',10,10,'DRAFT',true,'{"frequency":"MONTHLY","start_date":"2025-01-01"}'),('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','RECURRING','USD',10,10,'DRAFT',true,'{"frequency":"MONTHLY","start_date":"2025-01-01"}')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ScanRecurring(context.Background(), p, now); err != nil {
		t.Fatal(err)
	}
	before := len(sender.messages)
	if err = DeliverRecurringDrafts(context.Background(), p, sender, ""); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != before+2 || !strings.Contains(sender.messages[before].Subject, "2 draft") || !strings.Contains(sender.messages[before+1].Subject, "2 draft") || sender.messages[before].To[0] == sender.messages[before+1].To[0] {
		t.Fatalf("expected one two-invoice digest, got %#v", sender.messages[before:])
	}

	if _, err = p.Exec(context.Background(), `DELETE FROM users WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	// Pause, end and malformed schedules all suppress generation.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref=CASE invoice_id WHEN '20000000-0000-0000-0000-000000000001' THEN '{"frequency":"MONTHLY","start_date":"2025-01-01","paused":true}'::jsonb WHEN '20000000-0000-0000-0000-000000000002' THEN '{"frequency":"BAD","start_date":"2025-01-01"}'::jsonb ELSE '{"frequency":"MONTHLY","start_date":"2025-01-01","end_mode":"AFTER_N","end_after_cycles":2}'::jsonb END WHERE is_template`)
	if err != nil {
		t.Fatal(err)
	}
	march := time.Date(2025, 3, 15, 2, 0, 0, 0, time.UTC)
	if n, err := ScanRecurring(context.Background(), p, march); err != nil || n != 0 {
		t.Fatalf("suppressed: %d %v", n, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"MONTHLY","start_date":"2025-01-01"}' WHERE is_template; CREATE FUNCTION scan_fail_one() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF (SELECT billing_cycle_ref->>'template_invoice_id' FROM invoices WHERE invoice_id=NEW.invoice_id)='20000000-0000-0000-0000-000000000002' THEN RAISE EXCEPTION 'private failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER scan_fail_one BEFORE INSERT ON recurring_draft_events FOR EACH ROW EXECUTE FUNCTION scan_fail_one()`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ScanRecurring(context.Background(), p, march); err == nil || n != 2 {
		t.Fatalf("isolated failure: %d %v", n, err)
	}
	var failedChildren int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE billing_cycle_ref->>'template_invoice_id'='20000000-0000-0000-0000-000000000002' AND billing_cycle_ref->>'cycle_key'='2025-03-01'`).Scan(&failedChildren); err != nil || failedChildren != 0 {
		t.Fatalf("failed transaction left child: %d %v", failedChildren, err)
	}
	if _, err = p.Exec(context.Background(), `DROP TRIGGER scan_fail_one ON recurring_draft_events; DROP FUNCTION scan_fail_one()`); err != nil {
		t.Fatal(err)
	}
	if n, err := ScanRecurring(context.Background(), p, march); err != nil || n != 3 {
		t.Fatalf("retry: %d %v", n, err)
	}

	// Failed digests retain the same members for a grouped retry.
	sender.failures["a@example.com"] = true
	before = len(sender.messages)
	if err = DeliverRecurringDrafts(context.Background(), p, sender, ""); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != before+2 {
		t.Fatalf("expected two batches, got %d", len(sender.messages)-before)
	}
	subjectCounts := map[string]int{}
	for _, m := range sender.messages[before:] {
		subjectCounts[m.Subject]++
	}
	if subjectCounts["2 draft invoices awaiting your approval"] != 1 || subjectCounts["1 draft invoice awaiting your approval"] != 1 {
		t.Fatalf("unexpected batches %#v", subjectCounts)
	}
	if _, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET next_attempt_at=now()-interval '1 second' WHERE delivered_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	sender.failures["a@example.com"] = false
	before = len(sender.messages)
	if err = DeliverRecurringDrafts(context.Background(), p, sender, ""); err != nil {
		t.Fatal(err)
	}
	subjectCounts = map[string]int{}
	for _, m := range sender.messages[before:] {
		subjectCounts[m.Subject]++
	}
	if subjectCounts["2 draft invoices awaiting your approval"] != 1 || subjectCounts["1 draft invoice awaiting your approval"] != 1 {
		t.Fatalf("retry regrouped batches %#v", subjectCounts)
	}
	// An independently held job lock causes another process to skip immediately.
	conn, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	jobKey := int64(crc32.ChecksumIEEE([]byte("recurring-scan-and-generate")))
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, jobKey)
		conn.Release()
	}()
	if _, err = conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, jobKey); err != nil {
		t.Fatal(err)
	}
	if n, err := ScanRecurring(context.Background(), p, march); err != nil || n != 0 {
		t.Fatalf("lock did not skip: %d %v", n, err)
	}
	if _, err = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, jobKey); err != nil {
		t.Fatal(err)
	}
	// A separate OS process owns the same advisory lock as another replica.
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecurringScanLockProcess$")
	cmd.Env = append(os.Environ(), "SCAN_LOCK_DB="+db.URL())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "READY" {
		t.Fatalf("lock process: %q %v", line, err)
	}
	if n, err := ScanRecurring(context.Background(), p, march); err != nil || n != 0 {
		t.Fatalf("process lock: %d %v", n, err)
	}
	stdin.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	april := time.Date(2025, 4, 15, 2, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := ScanRecurring(context.Background(), p, april); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var aprilChildren int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE NOT is_template AND billing_cycle_ref->>'cycle_key'='2025-04-01'`).Scan(&aprilChildren); err != nil || aprilChildren != 3 {
		t.Fatalf("concurrent children %d: %v", aprilChildren, err)
	}

	// A real PAUSE request holds the template row while the scan waits.
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	block, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer block.Release()
	_, err = block.Exec(context.Background(), `SELECT pg_advisory_lock(707070); CREATE FUNCTION scan_pause_block() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.is_template AND NEW.billing_cycle_ref->>'paused'='true' THEN PERFORM pg_advisory_xact_lock(707070); END IF; RETURN NEW; END $$; CREATE TRIGGER scan_pause_block BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION scan_pause_block()`)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() {
		result <- request("PATCH", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", `{"action":"PAUSE"}`)
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("pause did not reach lock")
	}
	scanDone := make(chan error, 1)
	may := time.Date(2025, 5, 15, 2, 0, 0, 0, time.UTC)
	go func() { _, err := ScanRecurring(context.Background(), p, may); scanDone <- err }()
	time.Sleep(50 * time.Millisecond)
	if _, err = block.Exec(context.Background(), `SELECT pg_advisory_unlock(707070)`); err != nil {
		t.Fatal(err)
	}
	if status := <-result; status != 200 {
		t.Fatalf("pause status %d", status)
	}
	if err = <-scanDone; err != nil {
		t.Fatal(err)
	}
	var pausedChildren int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE billing_cycle_ref->>'template_invoice_id'='20000000-0000-0000-0000-000000000001' AND billing_cycle_ref->>'cycle_key'='2025-05-01'`).Scan(&pausedChildren); err != nil || pausedChildren != 0 {
		t.Fatalf("paused scan created invoice: %d %v", pausedChildren, err)
	}
	if _, err = p.Exec(context.Background(), `DROP TRIGGER scan_pause_block ON invoices; DROP FUNCTION scan_pause_block()`); err != nil {
		t.Fatal(err)
	}
	if status := request("PATCH", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", `{"action":"RESUME"}`); status != 200 {
		t.Fatalf("resume status %d", status)
	}

	// A manual trigger racing the same cycle completes before the scanner, which reuses it.
	_, err = block.Exec(context.Background(), `SELECT pg_advisory_lock(707071); CREATE FUNCTION scan_manual_block() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF (SELECT billing_cycle_ref->>'template_invoice_id' FROM invoices WHERE invoice_id=NEW.invoice_id)='20000000-0000-0000-0000-000000000001' THEN PERFORM pg_advisory_xact_lock(707071); END IF; RETURN NEW; END $$; CREATE TRIGGER scan_manual_block BEFORE INSERT ON recurring_draft_events FOR EACH ROW EXECUTE FUNCTION scan_manual_block()`)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		result <- request("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", `{"cycle_key":"2025-06-01"}`)
	}()
	deadline = time.Now().Add(5 * time.Second)
	blocked = false
	for time.Now().Before(deadline) {
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("manual trigger did not reach lock")
	}
	june := time.Date(2025, 6, 15, 2, 0, 0, 0, time.UTC)
	go func() { _, err := ScanRecurring(context.Background(), p, june); scanDone <- err }()
	time.Sleep(50 * time.Millisecond)
	if _, err = block.Exec(context.Background(), `SELECT pg_advisory_unlock(707071)`); err != nil {
		t.Fatal(err)
	}
	if status := <-result; status != 200 {
		t.Fatalf("trigger status %d", status)
	}
	if err = <-scanDone; err != nil {
		t.Fatal(err)
	}
	var juneChildren int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE billing_cycle_ref->>'template_invoice_id'='20000000-0000-0000-0000-000000000001' AND billing_cycle_ref->>'cycle_key'='2025-06-01'`).Scan(&juneChildren); err != nil || juneChildren != 1 {
		t.Fatalf("manual scan race: %d %v", juneChildren, err)
	}
	if _, err = p.Exec(context.Background(), `DROP TRIGGER scan_manual_block ON recurring_draft_events; DROP FUNCTION scan_manual_block()`); err != nil {
		t.Fatal(err)
	}

	// An ambiguous legacy key blocks only its own template, without an extra draft.
	_, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000099','10000000-0000-0000-0000-000000000001','RECURRING','USD',0,0,'DRAFT',false,'{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":"bad"}')`)
	if err != nil {
		t.Fatal(err)
	}
	july := time.Date(2025, 7, 15, 2, 0, 0, 0, time.UTC)
	if n, err := ScanRecurring(context.Background(), p, july); err == nil || n != 2 {
		t.Fatalf("ambiguous cycle: %d %v", n, err)
	}
	var unsafeChildren int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE billing_cycle_ref->>'template_invoice_id'='20000000-0000-0000-0000-000000000001' AND billing_cycle_ref->>'cycle_key'='2025-07-01'`).Scan(&unsafeChildren); err != nil || unsafeChildren != 0 {
		t.Fatalf("ambiguous generated child: %d %v", unsafeChildren, err)
	}

}

func TestRecurringScanLockProcess(t *testing.T) {
	raw := os.Getenv("SCAN_LOCK_DB")
	if raw == "" {
		return
	}
	p, err := pgxpool.New(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	conn, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	key := int64(crc32.ChecksumIEEE([]byte("recurring-scan-and-generate")))
	if _, err = conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestRecurringScanPythonDifferential(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfg := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	migrator := goosemigrator.New(".", goosemigrator.WithFS(migrations.Files))
	goDB := pgtestdb.Custom(t, cfg, migrator)
	pyDB := pgtestdb.Custom(t, cfg, migrator)
	gp, err := pgxpool.New(context.Background(), goDB.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer gp.Close()
	pp, err := pgxpool.New(context.Background(), pyDB.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pp.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES
 ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','RECURRING','IDR',0,0,'DRAFT',true,'{"frequency":"DAILY","interval":3,"start_date":"2025-01-01"}'),
 ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','RECURRING','IDR',0,0,'DRAFT',true,'{"frequency":"WEEKLY","interval":2,"start_date":"2025-01-01"}'),
 ('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','RECURRING','IDR',0,0,'DRAFT',true,'{"frequency":"MONTHLY","start_date":"2025-01-31"}'),
 ('20000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001','RECURRING','IDR',0,0,'DRAFT',true,'{"frequency":"YEARLY","start_date":"2024-02-29"}'),
 ('20000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001','RECURRING','IDR',0,0,'DRAFT',true,'{"frequency":"DAILY","start_date":"2025-01-01","paused":true}'),
 ('20000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000001','RECURRING','IDR',0,0,'DRAFT',true,'{"frequency":"BAD","start_date":"2025-01-01"}')`
	for _, p := range []*pgxpool.Pool{gp, pp} {
		if _, err = p.Exec(context.Background(), seed); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2025, 3, 15, 2, 0, 0, 0, time.UTC)
	var counts []int
	for i := 0; i < 2; i++ {
		n, err := ScanRecurring(context.Background(), gp, now)
		if err != nil {
			t.Fatal(err)
		}
		counts = append(counts, n)
	}
	sender := &outboxSender{failures: map[string]bool{}}
	if err = DeliverRecurringDrafts(context.Background(), gp, sender, ""); err != nil {
		t.Fatal(err)
	}
	rows, err := gp.Query(context.Background(), `SELECT billing_cycle_ref->>'cycle_key' FROM invoices WHERE NOT is_template ORDER BY billing_cycle_ref->>'cycle_key'`)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for rows.Next() {
		var k string
		if err = rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	script := `import json,os
from datetime import date
from sqlalchemy import create_engine, text
from sqlalchemy.orm import sessionmaker
from app.workers.tasks import recurring
from app.services import invoicing
class Clock(date):
 @classmethod
 def today(cls): return cls(2025,3,15)
recurring.date=Clock
invoicing.date=Clock
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
recurring.SessionLocal=sessionmaker(engine)
messages=[]
recurring.notify_users_of_new_drafts=lambda db,drafts: messages.append(len(drafts)) if drafts else None
counts=[recurring.scan_and_generate(),recurring.scan_and_generate()]
with engine.connect() as c: keys=list(c.execute(text("SELECT billing_cycle_ref->>'cycle_key' FROM invoices WHERE NOT is_template ORDER BY billing_cycle_ref->>'cycle_key'")).scalars())
print(json.dumps({'counts':counts,'keys':keys,'messages':messages}))`
	cmd := exec.Command("python", "-c", script)
	cmd.Env = append(os.Environ(), "DB="+pyDB.URL())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v: %s", err, out)
	}
	var result struct {
		Counts   []int    `json:"counts"`
		Keys     []string `json:"keys"`
		Messages []int    `json:"messages"`
	}
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatalf("python output %q: %v", out, err)
	}
	if !reflect.DeepEqual(counts, result.Counts) || !reflect.DeepEqual(keys, result.Keys) || len(result.Messages) != 1 || result.Messages[0] != len(keys) || len(sender.messages) != 1 {
		t.Fatalf("Go %v %v %d; Python %+v", counts, keys, len(sender.messages), result)
	}
}

func TestRecurringScanSnapshotsRecipients(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','staff'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','RECURRING','USD',0,0,'DRAFT',true,'{"frequency":"MONTHLY","start_date":"2025-01-01"}'),('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','RECURRING','USD',0,0,'DRAFT',true,'{"frequency":"MONTHLY","start_date":"2025-01-01"}')`)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	_, err = conn.Exec(context.Background(), `SELECT pg_advisory_lock(707072); CREATE FUNCTION scan_recipient_block() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF (SELECT billing_cycle_ref->>'template_invoice_id' FROM invoices WHERE invoice_id=NEW.invoice_id)='20000000-0000-0000-0000-000000000002' THEN PERFORM pg_advisory_xact_lock(707072); END IF; RETURN NEW; END $$; CREATE TRIGGER scan_recipient_block BEFORE INSERT ON recurring_draft_events FOR EACH ROW EXECUTE FUNCTION scan_recipient_block()`)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(707072)`)
	done := make(chan error, 1)
	go func() {
		_, err := ScanRecurring(context.Background(), p, time.Date(2025, 2, 15, 2, 0, 0, 0, time.UTC))
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("scan did not reach second template")
	}
	_, err = p.Exec(context.Background(), `DELETE FROM users WHERE user_id='00000000-0000-0000-0000-000000000002'; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000003','c@example.com','x','staff')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(707072)`); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	var original, newcomer int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE user_id='00000000-0000-0000-0000-000000000002'),count(*) FILTER (WHERE user_id='00000000-0000-0000-0000-000000000003') FROM recurring_draft_deliveries`).Scan(&original, &newcomer); err != nil || original != 2 || newcomer != 0 {
		t.Fatalf("snapshot deliveries original=%d newcomer=%d: %v", original, newcomer, err)
	}
	sender := &outboxSender{failures: map[string]bool{}}
	if err = DeliverRecurringDrafts(context.Background(), p, sender, ""); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0].To[0] != "a@example.com" || !strings.HasPrefix(sender.messages[0].Subject, "2 draft") {
		t.Fatalf("revalidated digest: %#v", sender.messages)
	}
}
