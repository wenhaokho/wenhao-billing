package platform

import (
	"context"
	"encoding/json"
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

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type customerLockTracer struct {
	reached     chan uint32
	release     chan struct{}
	releaseOnce sync.Once
}

func (tr *customerLockTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "-- name: LockSessionUserByID") {
		tr.reached <- conn.PgConn().PID()
		<-tr.release
	}
	return ctx
}
func (*customerLockTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (tr *customerLockTracer) unblock() { tr.releaseOnce.Do(func() { close(tr.release) }) }

func TestCustomerLockTracerRelease(t *testing.T) {
	tr := &customerLockTracer{release: make(chan struct{})}
	tr.unblock()
	tr.unblock()
	select {
	case <-tr.release:
	default:
		t.Fatal("tracer release remained blocked")
	}
}

func TestCustomerDeactivatePostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO customers(customer_id,name,active,matching_aliases,quotation_cc_emails,default_wht_rate) VALUES ('10000000-0000-0000-0000-000000000001','Active',true,'["alias"]','["cc@example.com"]',2.5000),('10000000-0000-0000-0000-000000000002','Inactive',false,'[]','[]',NULL),('10000000-0000-0000-0000-000000000003','Other',true,'[]','[]',NULL)`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(context.Background(), `INSERT INTO projects(project_id,customer_id,code,name,currency) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P1','Project','IDR'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,project_id) VALUES ('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','MILESTONE','IDR',100,100,'DRAFT','20000000-0000-0000-0000-000000000001'); INSERT INTO payments(payment_id,invoice_id,customer_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('40000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001',100,'IDR','Payer','2026-01-01','manual','PENDING')`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var out string
		e := pool.QueryRow(context.Background(), `SELECT jsonb_build_object('projects',(SELECT jsonb_agg(to_jsonb(p)) FROM projects p),'invoices',(SELECT jsonb_agg(to_jsonb(i)) FROM invoices i),'payments',(SELECT jsonb_agg(to_jsonb(p)) FROM payments p))::text`).Scan(&out)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	relatedBefore := snapshot()
	otherSnapshot := func() string {
		var out string
		if e := pool.QueryRow(context.Background(), `SELECT to_jsonb(c)::text FROM customers c WHERE customer_id='10000000-0000-0000-0000-000000000003'`).Scan(&out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	otherBefore := otherSnapshot()
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token := func(id string) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	admin := token("00000000-0000-0000-0000-000000000001")
	user := token("00000000-0000-0000-0000-000000000002")
	deleted := token("00000000-0000-0000-0000-000000000099")
	call := func(method, path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	create := func(body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/customers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)
		return resp
	}
	for _, tc := range []struct {
		body   string
		cookie *http.Cookie
		status int
	}{
		{`{"name":"x"}`, nil, 401}, {`{"name":"x"}`, &http.Cookie{Name: "session", Value: "tampered"}, 401},
		{`{"name":"x"}`, user, 403}, {`{"name":"x"}`, deleted, 403},
		{`{`, nil, 422}, {`{}`, admin, 422}, {`{"name":"x","default_wht_type":"PPH23","default_wht_rate":"0.00001"}`, admin, 422},
		{strings.Repeat("x", (1<<20)+1), admin, 413},
	} {
		resp := create(tc.body, tc.cookie)
		if resp.Code != tc.status || resp.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("create %q: %d %s", tc.body[:min(len(tc.body), 100)], resp.Code, resp.Body.String())
		}
	}
	created := create(`{"name":"Created"}`, admin)
	var createdBody map[string]any
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &createdBody) != nil || createdBody["default_currency"] != "IDR" || createdBody["active"] != true || createdBody["default_wht_type"] != "NONE" {
		t.Fatalf("create defaults: %d %s", created.Code, created.Body.String())
	}
	// Omitted values are supplied by PostgreSQL, not copied into Go constants.
	if _, err = pool.Exec(context.Background(), `ALTER TABLE customers ALTER COLUMN active SET DEFAULT false, ALTER COLUMN default_currency SET DEFAULT 'SGD'`); err != nil {
		t.Fatal(err)
	}
	storageDefaults := create(`{"name":"Storage defaults"}`, admin)
	var storageBody map[string]any
	if storageDefaults.Code != 201 || json.Unmarshal(storageDefaults.Body.Bytes(), &storageBody) != nil || storageBody["active"] != false || storageBody["default_currency"] != "SGD" {
		t.Fatalf("storage defaults: %d %s", storageDefaults.Code, storageDefaults.Body.String())
	}
	if _, err = pool.Exec(context.Background(), `ALTER TABLE customers ALTER COLUMN active SET DEFAULT true, ALTER COLUMN default_currency SET DEFAULT 'IDR'`); err != nil {
		t.Fatal(err)
	}
	created = create(`{"name":"WHT","matching_aliases":["alias"],"quotation_cc_emails":["A@example.com","a@example.com"],"default_wht_type":"PPH21","default_wht_gross_up":true}`, admin)
	createdBody = nil
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &createdBody) != nil || createdBody["default_wht_rate"] != "2.5000" || createdBody["default_wht_gross_up"] != true || len(createdBody["quotation_cc_emails"].([]any)) != 1 {
		t.Fatalf("create WHT: %d %s", created.Code, created.Body.String())
	}
	_, err = pool.Exec(context.Background(), `CREATE FUNCTION fail_create_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private create failure'; END $$; CREATE CONSTRAINT TRIGGER fail_create_commit AFTER INSERT ON customers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_create_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	failedCreate := create(`{"name":"Must rollback"}`, admin)
	if failedCreate.Code != 500 || strings.Contains(failedCreate.Body.String(), "private create failure") {
		t.Fatalf("create commit failure: %d %s", failedCreate.Code, failedCreate.Body.String())
	}
	_, err = pool.Exec(context.Background(), `DROP TRIGGER fail_create_commit ON customers; DROP FUNCTION fail_create_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	var rollbackCount int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM customers WHERE name='Must rollback'`).Scan(&rollbackCount); err != nil || rollbackCount != 0 {
		t.Fatalf("create rollback: %d %v", rollbackCount, err)
	}
	base := "/api/v1/customers/"
	id := "10000000-0000-0000-0000-000000000001"
	for _, tc := range []struct {
		id     string
		c      *http.Cookie
		status int
	}{{id, nil, 401}, {id, &http.Cookie{Name: "session", Value: "tampered"}, 401}, {id, user, 403}, {id, deleted, 403}, {"bad", admin, 422}, {"{10000000000000000000000000000001}", admin, 422}, {"10000000-0000-0000-0000-000000000099", admin, 404}} {
		w := call("DELETE", base+tc.id, tc.c)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", tc.id, w.Code, w.Body.String())
		}
	}

	// A deferred constraint trigger fails at commit after the UPDATE succeeds.
	_, err = pool.Exec(context.Background(), `CREATE FUNCTION fail_customer_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private failure'; END $$; CREATE CONSTRAINT TRIGGER fail_customer_commit AFTER UPDATE ON customers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_customer_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	failed := call("DELETE", base+id, admin)
	if failed.Code != 500 || strings.Contains(failed.Body.String(), "private failure") {
		t.Fatalf("commit failure: %d %s", failed.Code, failed.Body.String())
	}
	_, err = pool.Exec(context.Background(), `DROP TRIGGER fail_customer_commit ON customers; DROP FUNCTION fail_customer_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	var stillActive bool
	if err = pool.QueryRow(context.Background(), `SELECT active FROM customers WHERE customer_id=$1`, id).Scan(&stillActive); err != nil || !stillActive {
		t.Fatalf("commit failure changed customer: %v %v", stillActive, err)
	}

	// The tracer pauses immediately before the transactional SELECT FOR UPDATE.
	// Reaching it proves the initial persisted-admin lookup has already passed.
	trace := &customerLockTracer{reached: make(chan uint32, 1), release: make(chan struct{})}
	tracedConfig, err := pgxpool.ParseConfig(db.URL())
	if err != nil {
		t.Fatal(err)
	}
	tracedConfig.ConnConfig.Tracer = trace
	tracedPool, err := pgxpool.NewWithConfig(context.Background(), tracedConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer tracedPool.Close()
	defer trace.unblock()
	tracedHandler, err := NewHandlerWithSessions(tracedPool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("DELETE", base+id, nil)
		r.AddCookie(admin)
		w := httptest.NewRecorder()
		tracedHandler.ServeHTTP(w, r)
		done <- w
	}()
	var pid uint32
	select {
	case pid = <-trace.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not pass initial admin check")
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	trace.unblock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("transactional admin query did not wait for demotion lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	var demoted *httptest.ResponseRecorder
	select {
	case demoted = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if demoted.Code != 403 {
		t.Fatalf("demotion: %d %s", demoted.Code, demoted.Body.String())
	}
	if err = pool.QueryRow(context.Background(), `SELECT active FROM customers WHERE customer_id=$1`, id).Scan(&stillActive); err != nil || !stillActive {
		t.Fatalf("demotion changed customer: %v %v", stillActive, err)
	}
	if _, err = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	// The tracer pauses immediately before the transactional SELECT FOR UPDATE.
	// Reaching it proves the initial persisted-admin lookup has already passed.
	trace2 := &customerLockTracer{reached: make(chan uint32, 1), release: make(chan struct{})}
	tracedConfig2, err := pgxpool.ParseConfig(db.URL())
	if err != nil {
		t.Fatal(err)
	}
	tracedConfig2.ConnConfig.Tracer = trace2
	tracedPool2, err := pgxpool.NewWithConfig(context.Background(), tracedConfig2)
	if err != nil {
		t.Fatal(err)
	}
	defer tracedPool2.Close()
	defer trace2.unblock()
	tracedHandler2, err := NewHandlerWithSessions(tracedPool2, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	done2 := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/v1/customers", strings.NewReader(`{"name":"Demotion blocked"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(admin)
		w := httptest.NewRecorder()
		tracedHandler2.ServeHTTP(w, r)
		done2 <- w
	}()
	var pid2 uint32
	select {
	case pid2 = <-trace2.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not pass initial admin check")
	}
	tx2, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback(context.Background())
	if _, err = tx2.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	trace2.unblock()
	deadline2 := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')`, pid2).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline2) {
			t.Fatal("transactional admin query did not wait for demotion lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx2.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	var demoted2 *httptest.ResponseRecorder
	select {
	case demoted2 = <-done2:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if demoted2.Code != 403 {
		t.Fatalf("demotion: %d %s", demoted2.Code, demoted2.Body.String())
	}
	if err = pool.QueryRow(context.Background(), `SELECT active FROM customers WHERE customer_id=$1`, id).Scan(&stillActive); err != nil || !stillActive {
		t.Fatalf("demotion changed customer: %v %v", stillActive, err)
	}
	if _, err = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	var countDemoted int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM customers WHERE name='Demotion blocked'`).Scan(&countDemoted); err != nil || countDemoted != 0 {
		t.Fatalf("demoted create inserted row: %d %v", countDemoted, err)
	}
	before := call("GET", base+id, admin)
	var want map[string]any
	if before.Code != 200 || json.Unmarshal(before.Body.Bytes(), &want) != nil {
		t.Fatal(before.Body.String())
	}
	want["active"] = false
	for _, target := range []string{id, id, "10000000-0000-0000-0000-000000000002"} {
		w := call("DELETE", base+target, admin)
		var got map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got["active"] != false || got["customer_id"] != target {
			t.Fatalf("delete %d %s", w.Code, w.Body.String())
		}
		if target == id && !reflect.DeepEqual(got, want) {
			t.Fatalf("output changed: %v vs %v", got, want)
		}
	}

	for _, wrapped := range []string{"{" + id + "}", "urn:uuid:" + id} {
		w := call("DELETE", base+wrapped, admin)
		var body map[string]any
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &body) != nil || !reflect.DeepEqual(body, want) {
			t.Fatalf("wrapped UUID %s: %d %s", wrapped, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct{ method, path string }{{"DELETE", "/api/v1/customers"}, {"PUT", base + id}} {
		w := call(tc.method, tc.path, admin)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("invalid route %s %s: %d", tc.method, tc.path, w.Code)
		}
	}
	// Compare real Python create responses using a separate disposable database.
	pythonDB := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	payloads := []string{`{"name":"Differential bad type","quotation_cc_emails":null,"default_wht_type":"BAD"}`, `{"name":"Differential default"}`, `{"name":"Differential nulls","active":null,"default_currency":null,"contact_email":null,"quotation_cc_emails":null,"billing_cc_emails":null}`, `{"name":"Differential full","matching_aliases":["A","B"],"active":false,"contact_first_name":"First","contact_last_name":"Last","contact_email":"me@example.com","contact_phone":"1","contact_phone_2":"2","account_number":"3","website":"https://example.com","notes":"n","default_currency":"USD","billing_address":"legacy","billing_address1":"a1","billing_address2":"a2","billing_country":"ID","billing_state":"s","billing_city":"c","billing_postal_code":"p","ship_to_name":"recipient","shipping_address1":"s1","shipping_address2":"s2","shipping_country":"SG","shipping_state":"st","shipping_city":"city","shipping_postal_code":"123","shipping_phone":"4","shipping_delivery_instructions":"door","quotation_cc_emails":["A@example.com","a@example.com"],"billing_cc_emails":["b@example.com"],"default_wht_type":"PPH23","default_wht_gross_up":true}`}
	payloadJSON, _ := json.Marshal(payloads)
	pyCreate := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.customers import router
from app.api.deps import current_admin
from app.db.session import get_db
engine = create_engine(os.environ["CUSTOMER_TEST_DATABASE_URL"].replace("postgres://", "postgresql+psycopg://", 1))
app = FastAPI(); app.include_router(router, prefix="/api/v1"); app.dependency_overrides[current_admin] = lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db] = session
with TestClient(app, raise_server_exceptions=False) as client:
    results = []
    for payload in json.loads(os.environ["PAYLOADS"]):
        r = client.post("/api/v1/customers", content=payload, headers={"content-type":"application/json"})
        results.append([r.status_code, r.json() if r.status_code != 500 else None])
    print(json.dumps(results))
`)
	pyCreate.Dir = "../../../backend"
	pyCreate.Env = append(os.Environ(), "CUSTOMER_TEST_DATABASE_URL="+pythonDB.URL(), "PAYLOADS="+string(payloadJSON))
	pyCreated, err := pyCreate.Output()
	if err != nil {
		t.Fatalf("Python create: %v %s", err, pyCreated)
	}
	pythonPool, err := pgxpool.New(context.Background(), pythonDB.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pythonPool.Close()
	var pythonBadCount int
	if err = pythonPool.QueryRow(context.Background(), `SELECT count(*) FROM customers WHERE name='Differential bad type'`).Scan(&pythonBadCount); err != nil || pythonBadCount != 0 {
		t.Fatalf("Python bad type changed database: %d %v", pythonBadCount, err)
	}
	var pyResults [][]any
	if err = json.Unmarshal(pyCreated, &pyResults); err != nil {
		t.Fatal(err)
	}
	for i, payload := range payloads {
		got := create(payload, admin)
		if i == 0 {
			var count int
			if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM customers WHERE name='Differential bad type'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("bad type changed database: %d %v", count, err)
			}
		}
		var body map[string]any
		if got.Code != 500 {
			if err = json.Unmarshal(got.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
		}
		if expected, ok := pyResults[i][1].(map[string]any); ok {
			delete(expected, "customer_id")
			delete(body, "customer_id")
		}
		if float64(got.Code) != pyResults[i][0] || got.Code != 500 && !reflect.DeepEqual(body, pyResults[i][1]) {
			t.Fatalf("create parity %d: Go %d %s Python %s", i, got.Code, got.Body.String(), pyCreated)
		}
	}
	// Compare an actual Python DELETE response against Go on the same inactive row.
	py := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.customers import router
from app.api.deps import current_admin
from app.db.session import get_db
engine = create_engine(os.environ["CUSTOMER_TEST_DATABASE_URL"].replace("postgres://", "postgresql+psycopg://", 1))
app = FastAPI()
app.include_router(router, prefix="/api/v1")
app.dependency_overrides[current_admin] = lambda: object()
def session():
    with Session(engine) as db:
        yield db
app.dependency_overrides[get_db] = session
with TestClient(app) as client:
    r = client.delete("/api/v1/customers/10000000-0000-0000-0000-000000000001")
    print(json.dumps([r.status_code, r.json()]))
`)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "CUSTOMER_TEST_DATABASE_URL="+db.URL())
	pyOutput, err := py.Output()
	if err != nil {
		t.Fatalf("Python DELETE: %v %s", err, pyOutput)
	}
	var expected []any
	if err = json.Unmarshal(pyOutput, &expected); err != nil {
		t.Fatal(err, string(pyOutput))
	}
	goResult := call("DELETE", base+id, admin)
	var goBody any
	if err = json.Unmarshal(goResult.Body.Bytes(), &goBody); err != nil {
		t.Fatal(err)
	}
	if float64(goResult.Code) != expected[0] || !reflect.DeepEqual(goBody, expected[1]) {
		t.Fatalf("Python parity: Go %d %s Python %s", goResult.Code, goResult.Body.String(), pyOutput)
	}
	if after := snapshot(); after != relatedBefore {
		t.Fatalf("related rows changed: %s vs %s", after, relatedBefore)
	}
	if after := otherSnapshot(); after != otherBefore {
		t.Fatalf("other customer changed: %s vs %s", after, otherBefore)
	}
}
func TestCustomerDeactivateOpenAPI(t *testing.T) {
	h, e := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if json.Unmarshal(w.Body.Bytes(), &doc) != nil {
		t.Fatal(w.Body.String())
	}
	post, ok := doc["paths"].(map[string]any)["/api/v1/customers"].(map[string]any)["post"].(map[string]any)
	if !ok || len(post["security"].([]any)) == 0 || post["requestBody"] == nil {
		t.Fatal("POST OpenAPI contract missing")
	}
	postResponses := post["responses"].(map[string]any)
	for _, code := range []string{"201", "401", "403", "413", "422", "500"} {
		if postResponses[code] == nil {
			t.Fatalf("POST response %s missing", code)
		}
	}
	// Compare each request property with Python's authoritative Pydantic schema.
	cmd := exec.Command("python", "-c", `import json; from app.schemas.customer import CustomerCreate; print(json.dumps(CustomerCreate.model_json_schema()))`)
	cmd.Dir = "../../../backend"
	pySchema, err := cmd.Output()
	if err != nil {
		t.Fatalf("Python schema: %v", err)
	}
	var expectedSchema map[string]any
	if err := json.Unmarshal(pySchema, &expectedSchema); err != nil {
		t.Fatal(err)
	}
	actualSchema := doc["components"].(map[string]any)["schemas"].(map[string]any)["CustomerCreateSchema"].(map[string]any)
	if !reflect.DeepEqual(actualSchema["required"], expectedSchema["required"]) {
		t.Fatalf("required: %v expected %v", actualSchema["required"], expectedSchema["required"])
	}
	expectedProps := expectedSchema["properties"].(map[string]any)
	actualProps := actualSchema["properties"].(map[string]any)
	delete(actualProps, "$schema")
	if len(actualProps) != len(expectedProps) {
		t.Fatalf("property count: %d expected %d, got %v", len(actualProps), len(expectedProps), actualProps)
	}
	for key, raw := range expectedProps {
		want := raw.(map[string]any)
		delete(want, "title")
		got, ok := actualProps[key].(map[string]any)
		if !ok {
			t.Fatalf("missing property %s", key)
		}
		delete(got, "title")
		if !reflect.DeepEqual(got, want) {
			t.Errorf("property %s: got %v want %v", key, got, want)
		}
	}
	op, ok := doc["paths"].(map[string]any)["/api/v1/customers/{customer_id}"].(map[string]any)["delete"].(map[string]any)
	if !ok || len(op["security"].([]any)) == 0 {
		t.Fatal("DELETE security missing")
	}
	if op["requestBody"] != nil {
		t.Fatal("DELETE must not describe a request body")
	}
	params, ok := op["parameters"].([]any)
	if !ok || len(params) != 1 {
		t.Fatalf("parameters: %v", op["parameters"])
	}
	param := params[0].(map[string]any)
	ps := param["schema"].(map[string]any)
	if param["name"] != "customer_id" || param["in"] != "path" || param["required"] != true || ps["type"] != "string" || ps["format"] != "uuid" {
		t.Fatalf("path parameter: %v", param)
	}
	security := op["security"].([]any)
	if len(security) != 1 || security[0].(map[string]any)["sessionCookie"] == nil {
		t.Fatalf("security: %v", security)
	}
	components := doc["components"].(map[string]any)
	scheme := components["securitySchemes"].(map[string]any)["sessionCookie"].(map[string]any)
	if scheme["type"] != "apiKey" || scheme["in"] != "cookie" || scheme["name"] != "session" {
		t.Fatalf("cookie scheme: %v", scheme)
	}
	responses := op["responses"].(map[string]any)
	for status, ref := range map[string]string{"200": "CustomerOutputSchema", "401": "AuthErrorBody", "403": "AuthErrorBody", "404": "AuthErrorBody", "422": "ValidationErrorBody", "500": "AuthErrorBody"} {
		response, ok := responses[status].(map[string]any)
		if !ok {
			t.Fatalf("missing %s", status)
		}
		content, ok := response["content"].(map[string]any)
		if !ok {
			t.Fatalf("%s content: %v", status, response)
		}
		media, ok := content["application/json"].(map[string]any)
		if !ok {
			t.Fatalf("%s JSON: %v", status, content)
		}
		schema, ok := media["schema"].(map[string]any)
		if !ok || schema["$ref"] != "#/components/schemas/"+ref {
			t.Fatalf("%s schema: %v", status, media)
		}
	}
	schemas := components["schemas"].(map[string]any)
	customer := schemas["CustomerOutputSchema"].(map[string]any)
	props := customer["properties"].(map[string]any)
	for _, name := range []string{"customer_id", "name", "matching_aliases", "active", "contact_email", "contact_phone", "billing_address", "contact_first_name", "contact_last_name", "contact_phone_2", "account_number", "website", "notes", "default_currency", "billing_address1", "billing_address2", "billing_country", "billing_state", "billing_city", "billing_postal_code", "ship_to_name", "shipping_address1", "shipping_address2", "shipping_country", "shipping_state", "shipping_city", "shipping_postal_code", "shipping_phone", "shipping_delivery_instructions", "quotation_cc_emails", "billing_cc_emails", "default_wht_type", "default_wht_rate", "default_wht_gross_up"} {
		if props[name] == nil {
			t.Errorf("customer schema missing %s", name)
		}
	}
	for _, name := range []string{"matching_aliases", "quotation_cc_emails", "billing_cc_emails"} {
		p := props[name].(map[string]any)
		if p["type"] != "array" || p["items"].(map[string]any)["type"] != "string" {
			t.Errorf("%s schema: %v", name, p)
		}
	}
	if props["customer_id"].(map[string]any)["format"] != "uuid" || props["active"].(map[string]any)["type"] != "boolean" {
		t.Fatal("customer ID or active schema invalid")
	}
	authProps := schemas["AuthErrorBody"].(map[string]any)["properties"].(map[string]any)
	if authProps["detail"].(map[string]any)["type"] != "string" {
		t.Fatal("auth error schema")
	}
	validationProps := schemas["ValidationErrorBody"].(map[string]any)["properties"].(map[string]any)
	if validationProps["detail"] == nil {
		t.Fatal("validation error schema")
	}
}
