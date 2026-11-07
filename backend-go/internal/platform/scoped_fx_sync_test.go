package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedfx"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestScopedFXSyncPostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	migrationDB, err := sql.Open("pgx", db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer migrationDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, migrationDB, os.DirFS("../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	oneConfig, err := pgxpool.ParseConfig(db.URL())
	if err != nil {
		t.Fatal(err)
	}
	oneConfig.MaxConns = 1
	one, err := pgxpool.NewWithConfig(ctx, oneConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const tenantA = "10000000-0000-0000-0000-000000000001"
	const tenantB = "10000000-0000-0000-0000-000000000002"
	const userA = "11000000-0000-0000-0000-000000000001"
	const userB = "11000000-0000-0000-0000-000000000002"
	a, _ := business.Parse(tenantA)
	b, _ := business.Parse(tenantB)
	exec("INSERT INTO businesses(business_id) VALUES ($1),($2)", tenantA, tenantB)
	exec("INSERT INTO users(business_id,user_id,email,password_hash,role) VALUES ($1,$2,'fx-a@example.test','x','admin'),($3,$4,'fx-b@example.test','x','admin')", tenantA, userA, tenantB, userB)
	exec(`INSERT INTO fx_rates(business_id,from_currency,to_currency,as_of_date,rate,source)
	VALUES ($1,'USD','IDR','2025-01-02',111.1,'MANUAL'),($2,'USD','IDR','2025-01-02',222.2,'MANUAL')`, tenantA, tenantB)

	var mode, calls, blockedCall atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		base := r.URL.Query().Get("base")
		if r.URL.Path != "/latest" || r.URL.Query().Get("symbols") != "IDR" {
			t.Error("unexpected provider path or target")
		}
		switch {
		case mode.Load() == 1 && base == "SGD":
			http.Error(w, "provider secret", http.StatusBadGateway)
			return
		case mode.Load() == 3 && base == "SGD":
			fmt.Fprint(w, `{"base":"EUR","date":"2025-01-02","rates":{"IDR":5678}}`)
			return
		case mode.Load() == 2:
			<-r.Context().Done()
			return
		case blockedCall.Load() == call && base == "USD":
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprintf(w, `{"base":%q,"date":"2025-01-02","rates":{"IDR":1234.12345678}}`, base)
	}))
	defer server.Close()
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, FXProviderURL: server.URL, FXProviderTimeout: time.Second, FXSyncCurrencies: []string{"USD", "SGD"}}
	h, err := NewScopedProfileRouter(one, cfg, a)
	if err != nil {
		t.Fatal(err)
	}
	token := func(user, tenant string) string {
		t.Helper()
		value, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": user, "business_id": tenant})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	good := token(userA, tenantA)
	request := func(requestCtx context.Context, method, suffix, cookie, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/accounting/fx-rates"+suffix, strings.NewReader(body)).WithContext(requestCtx)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int, fragment string) {
		t.Helper()
		if w.Code != status || !strings.Contains(w.Body.String(), fragment) {
			t.Fatalf("got %d %s, want %d containing %q", w.Code, w.Body, status, fragment)
		}
	}
	snapshot := func(owner string) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(string_agg(concat_ws(' ',from_currency,to_currency,as_of_date,rate,source),'|' ORDER BY from_currency),'') FROM fx_rates WHERE business_id=$1`, owner).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	beforeA, beforeB := snapshot(tenantA), snapshot(tenantB)
	expect(request(ctx, "POST", "/sync", token(userB, tenantB), ""), 401, "")
	expect(request(ctx, "POST", "/sync", token(userB, tenantA), ""), 403, "")
	if err := RunScopedWeeklyFXSync(ctx, one, business.ID{}, cfg); err == nil || calls.Load() != 0 {
		t.Fatalf("invalid scope contacted provider or was accepted: %v, %d calls", err, calls.Load())
	}

	mode.Store(1)
	expect(request(ctx, "POST", "/sync", good, ""), 502, `fx provider request failed for SGD-\u003eIDR`)
	if snapshot(tenantA) != beforeA || snapshot(tenantB) != beforeB {
		t.Fatal("partial provider response changed rows")
	}
	mode.Store(3)
	expect(request(ctx, "POST", "/sync", good, ""), 502, `fx provider returned invalid base for SGD-\u003eIDR`)
	if err := RunScopedWeeklyFXSync(ctx, one, a, cfg); err == nil || err.Error() != "fx provider returned invalid base for SGD->IDR" {
		t.Fatalf("worker accepted invalid provider base: %v", err)
	}
	if snapshot(tenantA) != beforeA || snapshot(tenantB) != beforeB {
		t.Fatal("unverified provider response changed rows")
	}
	mode.Store(2)
	fast := cfg
	fast.FXProviderTimeout = 30 * time.Millisecond
	if err := RunScopedWeeklyFXSync(ctx, one, a, fast); err == nil || err.Error() != "fx provider request failed for USD->IDR" {
		t.Fatalf("provider timeout: %v", err)
	}
	short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	if err := RunScopedWeeklyFXSync(short, one, a, cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("provider cancellation: %v", err)
	}
	stop()
	mode.Store(0)

	exec(`ALTER TABLE fx_rates ADD CONSTRAINT test_reject_sgd CHECK (from_currency <> 'SGD')`)
	expect(request(ctx, "POST", "/sync", good, ""), 500, "internal server error")
	if err := RunScopedWeeklyFXSync(ctx, one, a, cfg); err == nil {
		t.Fatal("worker unexpectedly accepted failed batch")
	}
	if snapshot(tenantA) != beforeA || snapshot(tenantB) != beforeB {
		t.Fatal("failed write leaked a partial batch")
	}
	exec(`ALTER TABLE fx_rates DROP CONSTRAINT test_reject_sgd`)
	expect(request(ctx, "POST", "/sync", good, ""), 200, `"created":1,"updated":1,"fetched":2,"pairs":["USD-\u003eIDR","SGD-\u003eIDR"],"source":"FRANKFURTER"`)
	expect(request(ctx, "POST", "/sync", good, ""), 200, `"created":0,"updated":0`)
	const synced = "SGD IDR 2025-01-02 1234.12345678 FRANKFURTER|USD IDR 2025-01-02 1234.12345678 FRANKFURTER"
	if snapshot(tenantA) != synced || snapshot(tenantB) != beforeB {
		t.Fatal("sync stored an incorrect rate, date, source or owner")
	}

	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if err := scopedfx.LockSync(ctx, lock, a); err != nil {
		t.Fatal(err)
	}
	deadline, end := context.WithTimeout(ctx, 100*time.Millisecond)
	if err := RunScopedWeeklyFXSync(deadline, one, a, cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("worker bypassed A writer lock: %v", err)
	}
	end()
	if err := RunScopedWeeklyFXSync(ctx, one, b, cfg); err != nil || snapshot(tenantB) != synced {
		t.Fatalf("B worker could not proceed under A writer lock: %v", err)
	}
	deadline, end = context.WithTimeout(ctx, 100*time.Millisecond)
	expect(request(deadline, "POST", "/sync", good, ""), 500, "internal server error")
	end()
	const manual = `{"from_currency":"USD","to_currency":"IDR","rate":"17.12","as_of_date":"2025-01-02"}`
	deadline, end = context.WithTimeout(ctx, 100*time.Millisecond)
	expect(request(deadline, "POST", "", good, manual), 500, "internal server error")
	end()
	var rateID int64
	if err := pool.QueryRow(ctx, `SELECT rate_id FROM fx_rates WHERE business_id=$1 AND from_currency='USD'`, tenantA).Scan(&rateID); err != nil {
		t.Fatal(err)
	}
	deadline, end = context.WithTimeout(ctx, 100*time.Millisecond)
	expect(request(deadline, "DELETE", fmt.Sprintf("/%d", rateID), good, ""), 500, "internal server error")
	end()
	if snapshot(tenantA) != synced {
		t.Fatal("manual writer bypassed A writer lock")
	}
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	blockedCall.Store(calls.Load() + 1)
	done := make(chan error, 1)
	go func() { done <- RunScopedWeeklyFXSync(ctx, pool, a, cfg) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("A worker did not reach provider")
	}
	if err := RunScopedWeeklyFXSync(ctx, one, a, cfg); !errors.Is(err, ErrFXJobAlreadyRunning) {
		t.Fatalf("duplicate A worker: %v", err)
	}
	if err := RunScopedWeeklyFXSync(ctx, one, b, cfg); err != nil {
		t.Fatalf("A job lock blocked B: %v", err)
	}
	expect(request(ctx, "DELETE", fmt.Sprintf("/%d", rateID), good, ""), 204, "")
	expect(request(ctx, "POST", "", good, manual), 201, `"rate":"17.12000000"`)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if snapshot(tenantB) != synced || snapshot(tenantA) != synced {
		t.Fatal("parallel manual write and workers changed the wrong rows")
	}
}
