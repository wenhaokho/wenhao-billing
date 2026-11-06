package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestInvoiceTransitionsPostgres(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'DRAFT')`)
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

	// Finalization must wait for a hosting item locked by a restore recheck.
	_, err = p.Exec(context.Background(), `INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status) VALUES ('50000000-0000-0000-0000-000000000001','Hosting','SERVICE',true,'SUSPENDED'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000002','MILESTONE','USD',10,10,'DRAFT'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000001',0,'Hosting',1,10,10)`)
	if err != nil {
		t.Fatal(err)
	}
	block, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(context.Background())
	if _, err = block.Exec(context.Background(), `SELECT item_id FROM items WHERE item_id='50000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	hostingResult := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000002/finalize", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		hostingResult <- res.Code
	}()
	observed := false
	hostingDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(hostingDeadline) {
		if err = p.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%task68 invoice item lock%')`).Scan(&observed); err != nil {
			t.Fatal(err)
		}
		if observed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		t.Error("finalization did not wait for hosting item lock")
	}
	if err = block.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-hostingResult; code != 200 {
		t.Fatalf("finalize after lock: %d", code)
	}
	r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/finalize", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("finalize: %d %s", w.Code, w.Body)
	}
	var status string
	if err = p.QueryRow(context.Background(), `SELECT status FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&status); err != nil || status != "OPEN" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	for _, action := range []string{"finalize", "mark-paid", "void"} {
		path := "/api/v1/invoices/30000000-0000-0000-0000-000000000001/" + action
		unauth := httptest.NewRecorder()
		h.ServeHTTP(unauth, httptest.NewRequest("POST", path, nil))
		if unauth.Code != 401 || unauth.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s unauth: %d %v", action, unauth.Code, unauth.Header())
		}
		large := httptest.NewRequest("POST", path, strings.NewReader(strings.Repeat("x", (1<<20)+1)))
		large.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, large)
		if w.Code != 413 || strings.Contains(w.Body.String(), strings.Repeat("x", 100)) {
			t.Errorf("%s oversized: %d", action, w.Code)
		}
	}
	redacted := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/void", strings.NewReader(`{"secret":"private-value"}`))
	redacted.Header.Set("Content-Type", "application/json")
	redacted.AddCookie(&http.Cookie{Name: "session", Value: token})
	redactedResponse := httptest.NewRecorder()
	h.ServeHTTP(redactedResponse, redacted)
	if redactedResponse.Code != 422 || strings.Contains(redactedResponse.Body.String(), "private-value") {
		t.Errorf("void validation leaked body: %d %s", redactedResponse.Code, redactedResponse.Body)
	}
	doc := httptest.NewRecorder()
	h.ServeHTTP(doc, httptest.NewRequest("GET", "/openapi.json", nil))
	var spec map[string]any
	if err = json.Unmarshal(doc.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	for _, action := range []string{"finalize", "mark-paid", "void"} {
		path := "/api/v1/invoices/{invoice_id}/" + action
		if _, ok := paths[path].(map[string]any)["post"]; !ok {
			t.Errorf("missing OpenAPI operation %s", path)
		}
	}
	// A serialization failure must roll back a state change.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT',created_at='infinity' WHERE invoice_id='30000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/finalize", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 {
		t.Fatalf("invalid stored timestamp: %d", w.Code)
	}
	if err = p.QueryRow(context.Background(), `SELECT status FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&status); err != nil || status != "DRAFT" {
		t.Fatalf("rollback status=%s err=%v", status, err)
	}
	// Two distinct admins compete to finalize the same invoice.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET created_at='2020-01-01' WHERE invoice_id='30000000-0000-0000-0000-000000000001'; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`)
	if err != nil {
		t.Fatal(err)
	}
	token2, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, tok := range []string{token, token2} {
		wg.Add(1)
		go func(i int, tok string) {
			defer wg.Done()
			r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/finalize", nil)
			r.AddCookie(&http.Cookie{Name: "session", Value: tok})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			codes[i] = w.Code
		}(i, tok)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%' AND pid <> pg_backend_pid()`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d finalize requests waited for invoice lock", waiting)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !(codes[0] == 200 && codes[1] == 400 || codes[1] == 200 && codes[0] == 400) {
		t.Fatalf("concurrent finalize: %v", codes)
	}

	// A payment-like balance update holding the invoice lock must be observed by finalize.
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT',balance_due=10 WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	tx, err = p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	paymentRace := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/finalize", nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: token2})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		paymentRace <- w.Code
	}()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%' AND pid <> pg_backend_pid())`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finalize did not wait for payment lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE invoices SET balance_due=0 WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-paymentRace; code != 200 {
		t.Fatalf("payment race status %d", code)
	}
	var balance string
	if err = p.QueryRow(context.Background(), `SELECT status,balance_due::text FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&status, &balance); err != nil || status != "PAID" || balance != "0.0000" {
		t.Fatalf("payment race: status=%s balance=%s err=%v", status, balance, err)
	}
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='OPEN',balance_due=10 WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}

	// A payment that settles first must prevent a concurrent void.
	tx, err = p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	voidRace := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/void", strings.NewReader(`{"reason":"test"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token2})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		voidRace <- w.Code
	}()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%' AND pid <> pg_backend_pid())`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("void did not wait for payment lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE invoices SET status='PAID',balance_due=0 WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-voidRace; code != 400 {
		t.Fatalf("settled payment vs void: %d", code)
	}
	if err = p.QueryRow(context.Background(), `SELECT status,balance_due::text FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&status, &balance); err != nil || status != "PAID" || balance != "0.0000" {
		t.Fatalf("void overwrote payment: %s %s %v", status, balance, err)
	}
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='OPEN',balance_due=10 WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}

	// A persisted demotion committed while the request waits must win over the session.
	tx, err = p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT 1 FROM users WHERE user_id='00000000-0000-0000-0000-000000000002' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/void", strings.NewReader(`{"reason":"test"}`))
		r.AddCookie(&http.Cookie{Name: "session", Value: token2})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		result <- w.Code
	}()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM users%' AND pid <> pg_backend_pid())`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not wait for user lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='staff' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 403 {
		t.Fatalf("demoted admin: got %d", code)
	}
	if err = p.QueryRow(context.Background(), `SELECT status FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&status); err != nil || status != "OPEN" {
		t.Fatalf("demotion changed invoice: %s %v", status, err)
	}

	// A database error during the update must leave the invoice unchanged.
	_, err = p.Exec(context.Background(), `CREATE FUNCTION fail_transition_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$; CREATE TRIGGER fail_transition_test BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION fail_transition_test()`)
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/void", strings.NewReader(`{"reason":"test"}`))
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 {
		t.Fatalf("database error: %d %s", w.Code, w.Body)
	}
	if err = p.QueryRow(context.Background(), `SELECT status FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&status); err != nil || status != "OPEN" {
		t.Fatalf("database rollback status=%s err=%v", status, err)
	}

}
