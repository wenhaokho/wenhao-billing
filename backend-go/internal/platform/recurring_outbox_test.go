package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type outboxSender struct {
	sync.Mutex
	messages []email.Message
	failures map[string]bool
}

func (s *outboxSender) Send(_ context.Context, m email.Message) error {
	s.Lock()
	defer s.Unlock()
	s.messages = append(s.messages, m)
	if s.failures[m.To[0]] {
		return errors.New("secret provider detail")
	}
	return nil
}
func TestRecurringOutboxRetryAndIsolation(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','staff'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Customer & Co'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,invoice_number) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','RECURRING','USD',1234.5,1234.5,'DRAFT',false,'INV-1'); INSERT INTO recurring_draft_events(invoice_id,cycle_key,invoice_number,customer_name,amount,currency) VALUES ('20000000-0000-0000-0000-000000000001','2025-02-01','INV-1','Customer & Co',1234.5,'USD'); INSERT INTO recurring_draft_deliveries(invoice_id,user_id,email) VALUES ('20000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','a@example.com'),('20000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','b@example.com')`)
	if err != nil {
		t.Fatal(err)
	}
	sender := &outboxSender{failures: map[string]bool{"a@example.com": true}}
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 2 {
		t.Fatalf("messages %d", len(sender.messages))
	}
	for _, m := range sender.messages {
		if !strings.Contains(m.Text, "USD 1,234.50") || !strings.Contains(m.Text, "https://example.com/invoices/20000000-0000-0000-0000-000000000001/edit") || !strings.Contains(m.HTML, "Customer &amp; Co") {
			t.Fatalf("content %#v", m)
		}
	}
	var delivered, failed int
	var detail string
	if err = p.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE delivered_at IS NOT NULL),count(*) FILTER (WHERE delivered_at IS NULL AND attempts=1 AND next_attempt_at>now()),coalesce(max(last_error),'') FROM recurring_draft_deliveries`).Scan(&delivered, &failed, &detail); err != nil || delivered != 1 || failed != 1 || strings.Contains(detail, "secret") {
		t.Fatalf("state %d %d %q %v", delivered, failed, detail, err)
	}
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com"); err != nil || len(sender.messages) != 2 {
		t.Fatalf("premature retry: %v %d", err, len(sender.messages))
	}
	_, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET next_attempt_at=now()-interval '1 second' WHERE delivered_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	sender.failures["a@example.com"] = false
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com"); err != nil || len(sender.messages) != 3 {
		t.Fatalf("retry %v %d", err, len(sender.messages))
	}

	// A competing worker skips a live lease, and a crashed worker's lease expires.
	_, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET delivered_at=NULL, next_attempt_at=now()-interval '1 second', lease_until=now()-interval '1 second' WHERE email='a@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	waiter := &waitingOutboxSender{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- DeliverRecurringDrafts(context.Background(), p, waiter, "https://example.com") }()
	<-waiter.entered
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com"); err != nil || len(sender.messages) != 3 {
		t.Fatalf("competing worker sent leased row: %v", err)
	}
	close(waiter.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	// Cancellation during sending leaves the lease recoverable instead of marking success.
	_, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET delivered_at=NULL, next_attempt_at=now()-interval '1 second' WHERE email='a@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	accepted := &acceptingCancelSender{cancel: cancel}
	if err = DeliverRecurringDrafts(cctx, p, accepted, "https://example.com"); !errors.Is(err, context.Canceled) || !accepted.accepted {
		t.Fatalf("accepted then canceled: %v", err)
	}
	var leased bool
	if err = p.QueryRow(context.Background(), `SELECT delivered_at IS NULL AND lease_until>now() FROM recurring_draft_deliveries WHERE email='a@example.com'`).Scan(&leased); err != nil || !leased {
		t.Fatalf("cancelled lease %v %v", leased, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET lease_until=now()-interval '1 second' WHERE email='a@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com"); err != nil || len(sender.messages) != 4 {
		t.Fatalf("lease recovery %v", err)
	}

	// Exercise the configured email abstraction against a local provider.
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("provider request")
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"local"}`))
	}))
	defer srv.Close()
	provider, err := email.New(email.Config{ResendKey: "test-key", ResendURL: srv.URL, ResendFrom: "from@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET delivered_at=NULL,next_attempt_at=now()-interval '1 second' WHERE email='a@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = DeliverRecurringDrafts(context.Background(), p, provider, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if payload["subject"] != "1 draft invoice awaiting your approval" || !strings.Contains(payload["text"].(string), "Customer & Co") {
		t.Fatalf("provider payload %#v", payload)
	}
	// Claims use current user state, and cancel deleted users without exposing old addresses.
	_, err = p.Exec(context.Background(), `UPDATE users SET email='current@example.com' WHERE user_id='00000000-0000-0000-0000-000000000001'; DELETE FROM users WHERE user_id='00000000-0000-0000-0000-000000000002'; UPDATE recurring_draft_deliveries SET delivered_at=NULL,next_attempt_at=now()-interval '1 second'`)
	if err != nil {
		t.Fatal(err)
	}
	before := len(sender.messages)
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != before+1 || sender.messages[before].To[0] != "current@example.com" {
		t.Fatalf("stale or deleted recipient: %#v", sender.messages[before:])
	}
	var cancelled bool
	if err = p.QueryRow(context.Background(), `SELECT cancelled_at IS NOT NULL AND delivered_at IS NULL FROM recurring_draft_deliveries WHERE user_id='00000000-0000-0000-0000-000000000002'`).Scan(&cancelled); err != nil || !cancelled {
		t.Fatalf("deleted recipient not cancelled: %v", err)
	}

	// An uncommitted lifecycle change must finish before the claim resolves.
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `UPDATE users SET email='raced@example.com' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET delivered_at=NULL,next_attempt_at=now()-interval '1 second' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	before = len(sender.messages)
	go func() { done <- DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com") }()
	select {
	case err = <-done:
		t.Fatalf("claim did not wait for lifecycle change: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil || len(sender.messages) != before+1 || sender.messages[before].To[0] != "raced@example.com" {
		t.Fatalf("claim race: %v", err)
	}

	// Retries revalidate again and follow a changed address.
	_, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET delivered_at=NULL,next_attempt_at=now()-interval '1 second' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	sender.failures["raced@example.com"] = true
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(context.Background(), `UPDATE users SET email='retry@example.com' WHERE user_id='00000000-0000-0000-0000-000000000001'; UPDATE recurring_draft_deliveries SET next_attempt_at=now()-interval '1 second' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	before = len(sender.messages)
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com"); err != nil || len(sender.messages) != before+1 || sender.messages[before].To[0] != "retry@example.com" {
		t.Fatalf("retry address: %v", err)
	}

	// A delete in progress must win over a later claim.
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','staff'); UPDATE recurring_draft_deliveries SET cancelled_at=NULL,delivered_at=NULL,next_attempt_at=now()-interval '1 second' WHERE user_id='00000000-0000-0000-0000-000000000002'`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `DELETE FROM users WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	before = len(sender.messages)
	go func() { done <- DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com") }()
	select {
	case err = <-done:
		t.Fatalf("claim did not wait for delete: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil || len(sender.messages) != before {
		t.Fatalf("delete race sent email: %v", err)
	}
	if err = p.QueryRow(context.Background(), `SELECT cancelled_at IS NOT NULL FROM recurring_draft_deliveries WHERE user_id='00000000-0000-0000-0000-000000000002'`).Scan(&cancelled); err != nil || !cancelled {
		t.Fatalf("delete race not cancelled: %v", err)
	}

	// Provider payloads use Python Decimal's half-even formatting, including signed zero.
	for _, tc := range []struct{ amount, want string }{{"1.0050", "1.00"}, {"1.0150", "1.02"}, {"-1.0050", "-1.00"}, {"-1.0150", "-1.02"}, {"-0.0050", "-0.00"}} {
		_, err = p.Exec(context.Background(), `UPDATE recurring_draft_events SET amount=$1`, tc.amount)
		if err == nil {
			_, err = p.Exec(context.Background(), `UPDATE recurring_draft_deliveries SET delivered_at=NULL,next_attempt_at=now()-interval '1 second' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = DeliverRecurringDrafts(context.Background(), p, provider, "https://example.com"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload["text"].(string), "USD "+tc.want+" ·") {
			t.Fatalf("amount %s: %v", tc.amount, payload["text"])
		}
	}

	// The snapshot survives later invoice edits or deletion without blocking deletion.
	_, err = p.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000001'; UPDATE recurring_draft_deliveries SET delivered_at=NULL,next_attempt_at=now()-interval '1 second' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = DeliverRecurringDrafts(context.Background(), p, sender, "https://example.com"); err != nil || len(sender.messages) != before+1 || !strings.Contains(sender.messages[len(sender.messages)-1].Text, "INV-1") {
		t.Fatalf("snapshot after deletion: %v", err)
	}
}

type waitingOutboxSender struct {
	entered chan struct{}
	release chan struct{}
}

func (s *waitingOutboxSender) Send(ctx context.Context, m email.Message) error {
	close(s.entered)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Simulates provider acceptance followed by process cancellation before recording success.
type acceptingCancelSender struct {
	cancel   context.CancelFunc
	accepted bool
}

func (s *acceptingCancelSender) Send(context.Context, email.Message) error {
	s.accepted = true
	s.cancel()
	return nil
}
