package platform

import (
	"context"
	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUsageLockPostgres(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,billing_cycle_ref) VALUES ('30000000-0000-0000-0000-000000000001','USAGE','USD',10,10,'DRAFT','PPH23',2,true,'{"nested":{"n":12345678901234567890}}')`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/invoices/usage/30000000-0000-0000-0000-000000000001/lock", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, body := range []string{`{"accrued_amount":"100.0000","cutoff_date":"2026-10-31"}`, `{"accrued_amount":"100.0000","cutoff_date":"2026-10-31"}`} {
		w := call(body)
		if w.Code != 200 {
			t.Fatalf("lock: %d %s", w.Code, w.Body)
		}
	}
	var amount, base, wht, ref string
	err = p.QueryRow(context.Background(), `SELECT amount::text,wht_base::text,wht_amount::text,billing_cycle_ref::text FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&amount, &base, &wht, &ref)
	if err != nil {
		t.Fatal(err)
	}
	if amount != "102.0400" || base != "100.0000" || wht != "2.0400" || !strings.Contains(ref, "12345678901234567890") || !strings.Contains(ref, `"locked": true`) {
		t.Fatalf("unexpected stored state: %s %s %s %s", amount, base, wht, ref)
	}

	// A database error during update must leave the invoice unchanged.
	_, err = p.Exec(context.Background(), `CREATE FUNCTION usage_test_error() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$; CREATE TRIGGER usage_test_error BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION usage_test_error()`)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(`{"accrued_amount":50,"cutoff_date":"2026-11-01"}`); w.Code != 500 {
		t.Fatalf("database error: %d %s", w.Code, w.Body)
	}
	if err = p.QueryRow(context.Background(), `SELECT amount::text FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&amount); err != nil || amount != "102.0400" {
		t.Fatalf("database error rollback amount=%s err=%v", amount, err)
	}
	if _, err = p.Exec(context.Background(), `DROP TRIGGER usage_test_error ON invoices; DROP FUNCTION usage_test_error()`); err != nil {
		t.Fatal(err)
	}
	// An invalid stored response must roll back the update.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET created_at='infinity' WHERE invoice_id='30000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(`{"accrued_amount":50,"cutoff_date":"2026-11-01"}`); w.Code != 500 {
		t.Fatalf("invalid output: %d %s", w.Code, w.Body)
	}
	if err = p.QueryRow(context.Background(), `SELECT amount::text FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&amount); err != nil || amount != "102.0400" {
		t.Fatalf("rollback amount=%s err=%v", amount, err)
	}
	_, err = p.Exec(context.Background(), `UPDATE invoices SET created_at='2020-01-01' WHERE invoice_id='30000000-0000-0000-0000-000000000001'; UPDATE users SET role='viewer' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(`{"accrued_amount":50,"cutoff_date":"2026-11-01"}`); w.Code != 403 {
		t.Fatalf("demotion: %d %s", w.Code, w.Body)
	}
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest("POST", "/api/v1/invoices/usage/30000000-0000-0000-0000-000000000001/lock", nil))
	if unauth.Code != 401 || unauth.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauth: %d %v", unauth.Code, unauth.Header())
	}
	doc := httptest.NewRecorder()
	h.ServeHTTP(doc, httptest.NewRequest("GET", "/openapi.json", nil))
	if !strings.Contains(doc.Body.String(), `"/api/v1/invoices/usage/{invoice_id}/lock"`) {
		t.Fatal("OpenAPI route missing")
	}

	// Queue real update/finalize requests ahead of usage lock while holding the row.
	_, err = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`)
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"update", "finalize"} {
		if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT' WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
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
		first := make(chan *httptest.ResponseRecorder, 1)
		second := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			method, path, body := "PATCH", "/api/v1/invoices/30000000-0000-0000-0000-000000000001", `{"notes":"concurrent"}`
			if action == "finalize" {
				method = "POST"
				path += "/finalize"
				body = ""
			}
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "session", Value: tok2})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			first <- w
		}()
		wait := func(want int) {
			deadline := time.Now().Add(5 * time.Second)
			for {
				var n int
				if err := p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%FOR UPDATE%' AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n >= want {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s: %d requests waiting", action, n)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		wait(1)
		go func() { second <- call(`{"accrued_amount":50,"cutoff_date":"2026-11-01"}`) }()
		wait(2)
		if err = tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if w := <-first; w.Code != 200 {
			t.Fatalf("%s race: %d %s", action, w.Code, w.Body)
		}
		w := <-second
		want := 200
		if action == "finalize" {
			want = 400
		}
		if w.Code != want {
			t.Fatalf("lock after %s: %d %s", action, w.Code, w.Body)
		}
	}

	// Queue the actual manual endpoint and finalization ahead of the scheduler.
	for _, action := range []string{"manual", "finalize"} {
		_, err = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT',billing_cycle_ref='{"cut_off_day":31}' WHERE invoice_id='30000000-0000-0000-0000-000000000001'`)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := p.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
			t.Fatal(err)
		}
		requests := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			if action == "manual" {
				requests <- call(`{"accrued_amount":50,"cutoff_date":"2026-10-31"}`)
				return
			}
			r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/finalize", nil)
			r.AddCookie(&http.Cookie{Name: "session", Value: tok2})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			requests <- w
		}()
		waitLocks := func(want int) {
			deadline := time.Now().Add(5 * time.Second)
			for {
				var n int
				e := p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%FOR UPDATE%' AND pid <> pg_backend_pid()`).Scan(&n)
				if e != nil {
					t.Fatal(e)
				}
				if n >= want {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s: only %d waiters", action, n)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		waitLocks(1)
		type result struct {
			n   int
			err error
		}
		scans := make(chan result, 1)
		go func() {
			n, e := ScanUsageCutoff(context.Background(), p, time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC), UsageMeterFunc(func(context.Context, pgtype.UUID) (decimal.Decimal, error) { return decimal.NewFromInt(100), nil }))
			scans <- result{n, e}
		}()
		waitLocks(2)
		if err = tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if w := <-requests; w.Code != 200 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body)
		}
		if res := <-scans; res.err != nil || res.n != 0 {
			t.Fatalf("scan after %s: %+v", action, res)
		}
	}

	// An actual payment waits for the scan's row lock and cannot pay a draft.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT',billing_cycle_ref='{"cut_off_day":31}' WHERE invoice_id='30000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	scanDone := make(chan error, 1)
	go func() {
		_, e := ScanUsageCutoff(context.Background(), p, time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC), UsageMeterFunc(func(context.Context, pgtype.UUID) (decimal.Decimal, error) {
			close(entered)
			<-release
			return decimal.NewFromInt(100), nil
		}))
		scanDone <- e
	}()
	<-entered
	paymentDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":1,"payment_date":"2026-01-01"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: tok2})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		paymentDone <- w
	}()
	deadlinePayment := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%FOR UPDATE%' AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadlinePayment) {
			close(release)
			t.Fatal("payment did not wait for scan")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if err = <-scanDone; err != nil {
		t.Fatal(err)
	}
	if w := <-paymentDone; w.Code != 400 {
		t.Fatalf("payment after scan: %d %s", w.Code, w.Body)
	}

	// The persisted role is rechecked after waiting for a concurrent demotion.
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='viewer' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	pending := make(chan *httptest.ResponseRecorder, 1)
	go func() { pending <- call(`{"accrued_amount":1,"cutoff_date":"2026-01-01"}`) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM users%FOR UPDATE%' AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("usage lock did not wait for admin row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w := <-pending; w.Code != 403 {
		t.Fatalf("concurrent demotion: %d %s", w.Code, w.Body)
	}

}
