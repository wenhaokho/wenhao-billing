package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
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

func TestFXSyncPostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`)
	if e != nil {
		t.Fatal(e)
	}
	var mode atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" || r.URL.Query().Get("symbols") != "IDR" {
			t.Errorf("request %s", r.URL)
		}
		if r.URL.Query().Get("base") == "USD" && mode.Load() == 5 {
			entered <- struct{}{}
			<-release
		}
		if r.URL.Query().Get("base") == "SGD" {
			switch mode.Load() {
			case 1:
				w.WriteHeader(503)
				return
			case 2:
				w.Write([]byte(`not json`))
				return
			case 3:
				w.Write([]byte(`{"date":"bad","rates":{"IDR":1}}`))
				return
			case 4:
				w.Write([]byte(`{"date":"2025-01-02","rates":{"IDR":0.000000001}}`))
				return
			}
		}
		switch r.URL.Query().Get("base") {
		case "USD":
			w.Write([]byte(`{"date":"2025-01-02","rates":{"IDR":1234.12345678}}`))
		case "SGD":
			w.Write([]byte(`{"date":"2025-01-02","rates":{}}`))
		default:
			t.Errorf("unexpected base %s", r.URL.Query().Get("base"))
		}
	}))
	defer provider.Close()
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, FXProviderURL: provider.URL, FXSyncCurrencies: []string{"usd", "SGD", "IDR"}, FXProviderTimeout: time.Second}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	call := func(authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/accounting/fx-rates/sync", nil)
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call(false); w.Code != 401 {
		t.Fatalf("unauth %d %s", w.Code, w.Body)
	}
	for i := 0; i < 2; i++ {
		w := call(true)
		if w.Code != 200 {
			t.Fatalf("sync %d %s", w.Code, w.Body)
		}
		var got struct {
			Created, Updated, Fetched int
			Pairs                     []string
			Source                    string
		}
		if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatal(e)
		}
		if got.Created != 1-i || got.Updated != 0 || got.Fetched != 1 || len(got.Pairs) != 1 || got.Pairs[0] != "USD->IDR" || got.Source != "FRANKFURTER" {
			t.Fatalf("result %+v", got)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache")
		}
	}
	for _, m := range []int32{1, 2, 3, 4} {
		mode.Store(m)
		w := call(true)
		if w.Code != 502 {
			t.Fatalf("mode %d status %d %s", m, w.Code, w.Body)
		}
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM fx_rates WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2025-01-02' AND rate=1234.12345678`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("partial change mode %d: %d %v", m, n, err)
		}
	}
	mode.Store(5)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- call(true) }()
	<-entered
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	close(release)
	if w := <-result; w.Code != 403 {
		t.Fatalf("demotion %d %s", w.Code, w.Body)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	mode.Store(0)
	pyDB := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pyPool, err := pgxpool.New(context.Background(), pyDB.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pyPool.Close()
	if _, err = pyPool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python", "-c", `import os,json,base64,time
from itsdangerous import TimestampSigner
from starlette.middleware.sessions import SessionMiddleware
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.accounting import router
from app.db.session import get_db
engine=create_engine(os.environ['FX_DATABASE'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI();app.add_middleware(SessionMiddleware,secret_key='secret');app.include_router(router,prefix='/api/v1')
def db():
 with Session(engine) as s: yield s
app.dependency_overrides[get_db]=db
session=base64.b64encode(json.dumps({'user_id':'00000000-0000-0000-0000-000000000001','exp':int(time.time())+3600}).encode())
with TestClient(app,raise_server_exceptions=False) as c:
 c.cookies.set('session',TimestampSigner('secret').sign(session).decode());print(json.dumps([[r.status_code,r.json()] for r in [c.post('/api/v1/accounting/fx-rates/sync'),c.post('/api/v1/accounting/fx-rates/sync')]]))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "FX_DATABASE="+pyDB.URL(), "FX_PROVIDER_BASE_URL="+provider.URL, `FX_SYNC_CURRENCIES=["usd","SGD","IDR"]`)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("python %v %s", err, output)
	}
	var want [][]any
	if err = json.Unmarshal(output, &want); err != nil {
		t.Fatalf("python JSON %v %s", err, output)
	}
	if _, err = pool.Exec(context.Background(), `DELETE FROM fx_rates WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2025-01-02'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w := call(true)
		var body any
		if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != int(want[i][0].(float64)) || !reflect.DeepEqual(body, want[i][1]) {
			t.Fatalf("Python mismatch got %d %s want %v", w.Code, w.Body, want[i])
		}
	}
	if _, err := pool.Exec(context.Background(), `UPDATE fx_rates SET source='MANUAL',rate=1234 WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2025-01-02'`); err != nil {
		t.Fatal(err)
	}
	w := call(true)
	var updated fxSyncResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &updated) != nil || updated.Updated != 1 {
		t.Fatalf("update %d %s", w.Code, w.Body)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM fx_rates WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2025-01-02'`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); responses <- call(true) }()
	}
	wg.Wait()
	close(responses)
	created := 0
	for w := range responses {
		var v fxSyncResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatalf("concurrent %d %s", w.Code, w.Body)
		}
		created += v.Created
	}
	if created != 1 {
		t.Fatalf("concurrent created %d", created)
	}

}

func TestFXProviderTimeoutAndConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.Write([]byte(`{"date":"2025-01-02","rates":{"IDR":1}}`))
	}))
	defer server.Close()
	s := sessionHandler{config: SessionConfig{FXProviderURL: server.URL, FXSyncCurrencies: []string{"USD"}, FXProviderTimeout: 10 * time.Millisecond}}
	if _, err := fetchFX(context.Background(), s.config); err == nil {
		t.Fatal("timeout accepted")
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, FXProviderURL: "http://user:secret@example.com"}
	if err := cfg.validate(); err == nil {
		t.Fatal("provider URL credentials accepted")
	}
	cfg.FXProviderURL = server.URL
	cfg.FXProviderTimeout = -time.Second
	if err := cfg.validate(); err == nil {
		t.Fatal("negative timeout accepted")
	}
}

func TestFXSyncOpenAPI(t *testing.T) {
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(nil, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/openapi.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	path := paths["/api/v1/accounting/fx-rates/sync"].(map[string]any)
	op := path["post"].(map[string]any)
	if len(op["security"].([]any)) == 0 {
		t.Fatal("missing security")
	}
	responses := op["responses"].(map[string]any)
	for _, code := range []string{"200", "401", "403", "500", "502"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("missing %s", code)
		}
	}
}

func TestFXSyncCoordinatesDistinctAdminWrites(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	ctx := context.Background()
	_, e = pool.Exec(ctx, `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`)
	if e != nil {
		t.Fatal(e)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"date":"2025-01-02","rates":{"IDR":1234}}`))
	}))
	defer provider.Close()
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, FXProviderURL: provider.URL, FXSyncCurrencies: []string{"USD"}, FXProviderTimeout: time.Second}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token := func(id string) string {
		v, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	a := token("00000000-0000-0000-0000-000000000001")
	b := token("00000000-0000-0000-0000-000000000002")
	call := func(path, body, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	waiters := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var n int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n >= want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("expected %d advisory lock waiters", want)
	}
	hold := func() func() {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(76920174933512)`); err != nil {
			t.Fatal(err)
		}
		var once sync.Once
		release := func() { once.Do(func() { tx.Rollback(ctx) }) }
		t.Cleanup(release)
		return release
	}
	reset := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `DELETE FROM fx_rates WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2025-01-02'`); err != nil {
			t.Fatal(err)
		}
	}
	syncPath := "/api/v1/accounting/fx-rates/sync"
	postPath := "/api/v1/accounting/fx-rates"
	manual := `{"from_currency":"USD","to_currency":"IDR","rate":"1000","as_of_date":"2025-01-02"}`
	reset()
	release := hold()
	defer release()
	first := make(chan *httptest.ResponseRecorder, 1)
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- call(syncPath, "", a) }()
	waiters(1)
	go func() { second <- call(postPath, manual, b) }()
	waiters(2)
	release()
	sw, mw := <-first, <-second
	var syncResult fxSyncResult
	if sw.Code != 200 || json.Unmarshal(sw.Body.Bytes(), &syncResult) != nil {
		t.Fatalf("sync %d %s", sw.Code, sw.Body)
	}
	if !((mw.Code == 201 && syncResult.Created == 0 && syncResult.Updated == 1) || (mw.Code == 409 && syncResult.Created == 1 && syncResult.Updated == 0)) {
		t.Fatalf("manual %d sync %+v", mw.Code, syncResult)
	}
	var source, rate string
	if err := pool.QueryRow(ctx, `SELECT source,rate::text FROM fx_rates WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2025-01-02'`).Scan(&source, &rate); err != nil || source != "FRANKFURTER" || rate != "1234.00000000" {
		t.Fatalf("stored %s %s %v", source, rate, err)
	}
	reset()
	release = hold()
	defer release()
	go func() { first <- call(postPath, manual, a) }()
	waiters(1)
	go func() { second <- call(syncPath, "", b) }()
	waiters(2)
	release()
	mw, sw = <-first, <-second
	if sw.Code != 200 || json.Unmarshal(sw.Body.Bytes(), &syncResult) != nil || mw.Code != 201 || syncResult.Created != 0 || syncResult.Updated != 1 {
		t.Fatalf("manual-first manual %d sync %d %s", mw.Code, sw.Code, sw.Body)
	}

	reset()
	release = hold()
	defer release()
	go func() { first <- call(syncPath, "", a) }()
	waiters(1)
	go func() { second <- call(syncPath, "", b) }()
	waiters(2)
	release()
	x, y := <-first, <-second
	var xr, yr fxSyncResult
	if x.Code != 200 || y.Code != 200 || json.Unmarshal(x.Body.Bytes(), &xr) != nil || json.Unmarshal(y.Body.Bytes(), &yr) != nil || xr.Created+yr.Created != 1 || xr.Updated+yr.Updated != 0 {
		t.Fatalf("two syncs %d %s / %d %s", x.Code, x.Body, y.Code, y.Body)
	}

	// Queue scheduled sync and each manual writer behind the same transaction lock.
	for _, operation := range []string{"sync", "create", "delete"} {
		reset()
		if operation == "delete" {
			if _, err := pool.Exec(ctx, `INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date,source) VALUES ('USD','IDR',1000,'2025-01-02','MANUAL')`); err != nil {
				t.Fatal(err)
			}
		}
		var id int64
		if operation == "delete" {
			if err := pool.QueryRow(ctx, `SELECT rate_id FROM fx_rates WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2025-01-02'`).Scan(&id); err != nil {
				t.Fatal(err)
			}
		}
		release = hold()
		scheduled := make(chan error, 1)
		go func() { scheduled <- RunWeeklyFXSync(ctx, pool, cfg) }()
		waiters(1)
		manualDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			if operation == "sync" {
				manualDone <- call(syncPath, "", a)
				return
			}
			if operation == "create" {
				manualDone <- call(postPath, manual, a)
				return
			}
			req := httptest.NewRequest("DELETE", fmt.Sprintf("%s/%d", postPath, id), nil)
			req.AddCookie(&http.Cookie{Name: "session", Value: a})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			manualDone <- w
		}()
		waiters(2)
		release()
		if err := <-scheduled; err != nil {
			t.Fatalf("%s scheduler: %v", operation, err)
		}
		response := <-manualDone
		if operation == "sync" && response.Code != 200 || operation == "create" && response.Code != 201 && response.Code != 409 || operation == "delete" && response.Code != 204 {
			t.Fatalf("%s: %d %s", operation, response.Code, response.Body)
		}
	}
}

func TestWeeklyFXSyncPoolOneAndCancellation(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pc, e := pgxpool.ParseConfig(db.URL())
	if e != nil {
		t.Fatal(e)
	}
	pc.MaxConns = 1
	pool, e := pgxpool.NewWithConfig(context.Background(), pc)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	var rate atomic.Int32
	rate.Store(100)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"date": "2026-11-02", "rates": map[string]int32{"IDR": rate.Load()}})
	}))
	defer provider.Close()
	cfg := SessionConfig{FXProviderURL: provider.URL, FXSyncCurrencies: []string{"USD"}, FXProviderTimeout: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		if i == 1 {
			rate.Store(200)
		}
		if e = RunWeeklyFXSync(ctx, pool, cfg); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	var value string
	if e = pool.QueryRow(ctx, `SELECT count(*), max(rate)::text FROM fx_rates WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2026-11-02'`).Scan(&count, &value); e != nil || count != 1 || !strings.HasPrefix(value, "200") {
		t.Fatalf("count=%d rate=%s error=%v", count, value, e)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if e = RunWeeklyFXSync(cancelled, pool, cfg); e == nil {
		t.Fatal("expected cancellation")
	}
}

func TestWeeklyFXSyncPythonWorkerAndReplicaLock(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	config := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	a := pgtestdb.Custom(t, config, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	b := pgtestdb.Custom(t, config, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), a.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	pyPool, e := pgxpool.New(context.Background(), b.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pyPool.Close()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("base") == "SGD" {
			w.Write([]byte(`{"date":"2026-11-02","rates":{}}`))
			return
		}
		w.Write([]byte(`{"date":"2026-11-02","rates":{"IDR":1234}}`))
	}))
	defer provider.Close()
	cfg := SessionConfig{FXProviderURL: provider.URL, FXSyncCurrencies: []string{"USD", "SGD", "IDR"}, FXProviderTimeout: time.Second}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if e = RunWeeklyFXSync(ctx, pool, cfg); e != nil {
			t.Fatal(e)
		}
	}
	py := exec.Command("python", "-c", `import json
from app.workers.tasks.fx_sync import sync_fx_rates
print(json.dumps([sync_fx_rates(),sync_fx_rates()]))`)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "DATABASE_URL="+strings.Replace(b.URL(), "postgres://", "postgresql+psycopg://", 1), "FX_PROVIDER_BASE_URL="+provider.URL, `FX_SYNC_CURRENCIES=["USD","SGD","IDR"]`)
	out, e := py.CombinedOutput()
	if e != nil {
		t.Fatalf("python worker: %v %s", e, out)
	}
	var values []map[string]any
	if e = json.Unmarshal(out, &values); e != nil || len(values) != 2 || values[0]["created"] != float64(1) || values[1]["created"] != float64(0) {
		t.Fatalf("python worker result %s %v", out, e)
	}
	query := `SELECT rate::text,source FROM fx_rates WHERE from_currency='USD' AND to_currency='IDR' AND as_of_date='2026-11-02'`
	var ar, as, br, bs string
	if e = pool.QueryRow(ctx, query).Scan(&ar, &as); e != nil {
		t.Fatal(e)
	}
	if e = pyPool.QueryRow(ctx, query).Scan(&br, &bs); e != nil || ar != br || as != bs {
		t.Fatalf("worker mismatch %s %s / %s %s %v", ar, as, br, bs, e)
	}
	// A separate Python process holds the exact Python scheduler key.
	lock := exec.Command("python", "-u", "-c", `import os,sys,zlib,psycopg
with psycopg.connect(os.environ['LOCK_DB'],autocommit=True) as c:
 c.execute('select pg_advisory_lock(%s)',(zlib.crc32(b'fx-rates-weekly-sync'),)); print('locked',flush=True); sys.stdin.read()`)
	lock.Env = append(os.Environ(), "LOCK_DB="+a.URL())
	stdin, e := lock.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := lock.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = lock.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { stdin.Close(); lock.Wait() }()
	buf := make([]byte, 7)
	if _, e = io.ReadFull(stdout, buf); e != nil || string(buf) != "locked\n" {
		t.Fatalf("lock process %q %v", buf, e)
	}
	before := calls.Load()
	if e = RunWeeklyFXSync(ctx, pool, cfg); !errors.Is(e, ErrFXJobAlreadyRunning) || calls.Load() != before {
		t.Fatalf("replica was not skipped: %v", e)
	}
}

func TestWeeklyFXSyncRollbackAndSharedWriterLock(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	ctx := context.Background()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"date":"2026-11-02","rates":{"IDR":1234}}`))
	}))
	defer provider.Close()
	cfg := SessionConfig{FXProviderURL: provider.URL, FXSyncCurrencies: []string{"USD", "SGD"}, FXProviderTimeout: time.Second}
	_, e = pool.Exec(ctx, `CREATE FUNCTION fx_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.from_currency='SGD' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$; CREATE TRIGGER fx_test_fail BEFORE INSERT ON fx_rates FOR EACH ROW EXECUTE FUNCTION fx_test_fail()`)
	if e != nil {
		t.Fatal(e)
	}
	if e = RunWeeklyFXSync(ctx, pool, cfg); e == nil {
		t.Fatal("expected write failure")
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM fx_rates WHERE as_of_date='2026-11-02'`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("partial batch %d %v", count, e)
	}
	if _, e = pool.Exec(ctx, `DROP TRIGGER fx_test_fail ON fx_rates`); e != nil {
		t.Fatal(e)
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(76920174933512)`); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- RunWeeklyFXSync(ctx, pool, cfg) }()
	deadline := time.Now().Add(3 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("scheduler did not wait on shared writer lock")
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestWeeklyFXSyncProviderFailureAndCancellation(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	var mode atomic.Int32
	entered := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("base") == "USD" {
			w.Write([]byte(`{"date":"2026-11-02","rates":{"IDR":1234}}`))
			return
		}
		switch mode.Load() {
		case 0:
			w.WriteHeader(503)
		case 1:
			w.Write([]byte(`{"date":"2026-11-02","rates":{"IDR":-1}}`))
		case 2:
			entered <- struct{}{}
			<-r.Context().Done()
		}
	}))
	defer provider.Close()
	cfg := SessionConfig{FXProviderURL: provider.URL, FXSyncCurrencies: []string{"USD", "SGD"}, FXProviderTimeout: 100 * time.Millisecond}
	for _, m := range []int32{0, 1, 2} {
		mode.Store(m)
		if e = RunWeeklyFXSync(context.Background(), pool, cfg); e == nil {
			t.Fatalf("mode %d expected failure", m)
		}
		var n int
		if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM fx_rates WHERE as_of_date='2026-11-02'`).Scan(&n); e != nil || n != 0 {
			t.Fatalf("mode %d partial write %d %v", m, n, e)
		}
	}
	<-entered // Consume the timed-out request before waiting for the next one.
	mode.Store(2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunWeeklyFXSync(ctx, pool, cfg) }()
	<-entered
	cancel()
	if e = <-done; e == nil {
		t.Fatal("expected cancellation")
	}
}

func TestWeeklyFXBlockedWriterDeadlineReleasesPool(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	blocker, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Close()
	tx, e := blocker.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock(76920174933512)`); e != nil {
		t.Fatal(e)
	}
	pc, e := pgxpool.ParseConfig(db.URL())
	if e != nil {
		t.Fatal(e)
	}
	pc.MaxConns = 1
	pool, e := pgxpool.NewWithConfig(context.Background(), pc)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"date":"2026-11-02","rates":{"IDR":1234}}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	e = RunWeeklyFXSync(ctx, pool, SessionConfig{FXProviderURL: server.URL, FXSyncCurrencies: []string{"USD"}})
	if e == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("deadline not honored: %v", e)
	}
	probe, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	var n int
	if e = pool.QueryRow(probe, `SELECT count(*) FROM fx_rates WHERE as_of_date='2026-11-02'`).Scan(&n); e != nil || n != 0 {
		t.Fatalf("connection or atomicity: %d %v", n, e)
	}
}
