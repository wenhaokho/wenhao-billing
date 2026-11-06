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

type customerUpdateOrderTracer struct {
	mu    sync.Mutex
	names []string
}

func (tr *customerUpdateOrderTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, name := range []string{"LockSessionUserByID", "LockCustomer"} {
		if strings.Contains(d.SQL, "-- name: "+name) {
			tr.names = append(tr.names, name)
		}
	}
	return ctx
}
func (*customerUpdateOrderTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestCustomerUpdateHTTP(t *testing.T) {
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
	_, err = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Before'),('10000000-0000-0000-0000-000000000002','Other')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(context.Background(), `INSERT INTO projects(project_id,customer_id,code,name,currency) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P1','Project','IDR'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,project_id) VALUES ('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','MILESTONE','IDR',100,100,'DRAFT','20000000-0000-0000-0000-000000000001'); INSERT INTO payments(payment_id,invoice_id,customer_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('40000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001',100,'IDR','Payer','2026-01-01','manual','PENDING')`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var value string
		if err := pool.QueryRow(context.Background(), `SELECT jsonb_build_object('projects',(SELECT jsonb_agg(to_jsonb(p) ORDER BY project_id) FROM projects p),'invoices',(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i),'payments',(SELECT jsonb_agg(to_jsonb(p) ORDER BY payment_id) FROM payments p),'other',(SELECT to_jsonb(c) FROM customers c WHERE customer_id='10000000-0000-0000-0000-000000000002'))::text`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	relatedBefore := snapshot()
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "/api/v1/customers/10000000-0000-0000-0000-000000000001", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call(`{"name":"After","contact_phone":"123","default_wht_type":"PPH23"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"After"`) || !strings.Contains(w.Body.String(), `"default_wht_rate":"2.0000"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), w.Header())
	}
	w = call(`{"name":null}`)
	if w.Code != 500 {
		t.Fatalf("expected rollback: %d %s", w.Code, w.Body.String())
	}
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM customers WHERE customer_id='10000000-0000-0000-0000-000000000001'`).Scan(&name); err != nil || name != "After" {
		t.Fatalf("name %q: %v", name, err)
	}
	for _, tc := range []struct {
		body     string
		status   int
		fragment string
	}{
		{`{}`, 200, `"name":"After"`},
		{`{"contact_phone":null,"matching_aliases":["One","Two"],"quotation_cc_emails":[" A@example.com ","a@EXAMPLE.COM"]}`, 200, `"contact_phone":null`},
		{`{"default_wht_type":"NONE"}`, 200, `"default_wht_rate":null`},
		{`{"default_wht_type":"PPH21","default_wht_rate":"0.0001"}`, 200, `"default_wht_rate":"0.0001"`},
		{`{"default_wht_gross_up":true}`, 200, `"default_wht_rate":"0.0001"`},
		{`{"default_wht_type":"BAD"}`, 422, `Unknown withholding`},
		{`{"default_wht_rate":"0.00001"}`, 422, `decimal_precision`},
		{`{"default_wht_rate":0.1234567890123456789}`, 422, `decimal_inexact`},
		{`{"name":""}`, 422, `string_too_short`},
		{`{"matching_aliases":null}`, 500, `internal server error`},
		{`{"quotation_cc_emails":null}`, 500, `internal server error`},
		{`{"billing_cc_emails":null}`, 500, `internal server error`},
		{`{"active":null}`, 500, `internal server error`},
	} {
		w := call(tc.body)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.fragment) {
			t.Errorf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
	var arraysValid bool
	if err := pool.QueryRow(context.Background(), `SELECT matching_aliases IS NOT NULL AND quotation_cc_emails IS NOT NULL AND billing_cc_emails IS NOT NULL FROM customers WHERE customer_id='10000000-0000-0000-0000-000000000001'`).Scan(&arraysValid); err != nil || !arraysValid {
		t.Fatalf("null arrays persisted: %v", err)
	}
	callPath := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	userToken, _ := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	deletedToken, _ := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000099"})
	adminCookie := &http.Cookie{Name: "session", Value: token}
	for _, tc := range []struct {
		path, body string
		cookie     *http.Cookie
		status     int
		fragment   string
	}{
		{"/api/v1/customers/{10000000-0000-0000-0000-000000000001}", `{}`, adminCookie, 200, `"name":"After"`},
		{"/api/v1/customers/urn:uuid:10000000-0000-0000-0000-000000000001", `{}`, adminCookie, 200, `"name":"After"`},
		{"/api/v1/customers/bad", `{`, nil, 422, `json_invalid`},
		{"/api/v1/customers/bad", `{}`, nil, 401, `not authenticated`},
		{"/api/v1/customers/bad", `{}`, adminCookie, 422, `uuid_parsing`},
		{"/api/v1/customers/bad", `{"name":""}`, adminCookie, 422, `uuid_parsing`},
		{"/api/v1/customers/bad", `{`, adminCookie, 422, `json_invalid`},
		{"/api/v1/customers/10000000-0000-0000-0000-000000000099", `{}`, adminCookie, 404, `customer not found`},
		{"/api/v1/customers/10000000-0000-0000-0000-000000000001", `{}`, nil, 401, `not authenticated`},
		{"/api/v1/customers/10000000-0000-0000-0000-000000000001", `{}`, &http.Cookie{Name: "session", Value: token + "x"}, 401, `not authenticated`},
		{"/api/v1/customers/10000000-0000-0000-0000-000000000001", `{}`, &http.Cookie{Name: "session", Value: userToken}, 403, `admin required`},
		{"/api/v1/customers/10000000-0000-0000-0000-000000000001", `{}`, &http.Cookie{Name: "session", Value: deletedToken}, 403, `admin required`},
		{"/api/v1/customers/10000000-0000-0000-0000-000000000001", strings.Repeat(" ", 1<<20+1), adminCookie, 413, `large`},
	} {
		w := callPath(tc.path, tc.body, tc.cookie)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.fragment) || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	var before string
	if err := pool.QueryRow(context.Background(), `SELECT to_jsonb(c)::text FROM customers c WHERE name='Other'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, body := range []string{`{"contact_phone":"concurrent"}`, `{"notes":"concurrent"}`} {
		wg.Add(1)
		go func(body string) { defer wg.Done(); codes <- call(body).Code }(body)
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Errorf("concurrent code %d", code)
		}
	}
	w = call(`{}`)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result["contact_phone"] != "concurrent" || result["notes"] != "concurrent" {
		t.Errorf("lost update: %s %v", w.Body.String(), err)
	}
	var after string
	if err := pool.QueryRow(context.Background(), `SELECT to_jsonb(c)::text FROM customers c WHERE name='Other'`).Scan(&after); err != nil || before != after {
		t.Errorf("other row changed: %v", err)
	}

	openAPI := httptest.NewRecorder()
	h.ServeHTTP(openAPI, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err := json.Unmarshal(openAPI.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	patch := paths["/api/v1/customers/{customer_id}"].(map[string]any)["patch"].(map[string]any)
	if patch["requestBody"] == nil || patch["security"] == nil || patch["responses"].(map[string]any)["200"] == nil {
		t.Error("PATCH OpenAPI contract incomplete")
	}
	schema := doc["components"].(map[string]any)["schemas"].(map[string]any)["CustomerUpdateSchema"].(map[string]any)
	if required, ok := schema["required"].([]any); ok && len(required) > 0 {
		t.Errorf("update schema requires fields: %v", required)
	}
	if len(schema["properties"].(map[string]any)) < 30 {
		t.Error("update schema missing properties")
	}

	// A malformed persisted array makes output encoding fail; the write must roll back.
	if _, err = pool.Exec(context.Background(), `INSERT INTO customers(customer_id,name,matching_aliases) VALUES ('10000000-0000-0000-0000-000000000004','Malformed','{}')`); err != nil {
		t.Fatal(err)
	}
	badOutput := callPath("/api/v1/customers/10000000-0000-0000-0000-000000000004", `{"name":"Must roll back"}`, adminCookie)
	if badOutput.Code != 500 {
		t.Errorf("invalid output: %d %s", badOutput.Code, badOutput.Body.String())
	}
	var badName string
	if err = pool.QueryRow(context.Background(), `SELECT name FROM customers WHERE customer_id='10000000-0000-0000-0000-000000000004'`).Scan(&badName); err != nil || badName != "Malformed" {
		t.Errorf("invalid output committed: %q %v", badName, err)
	}
	// Hold the row while two different admins update separate withholding fields.
	// Without SELECT FOR UPDATE both overlays read the old row before their writes wait.
	if _, err = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000003','b@example.com','x','admin'); UPDATE customers SET default_wht_type='PPH21',default_wht_rate=3,default_wht_gross_up=false WHERE customer_id='10000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	token2, _ := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000003"})
	held, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.Background())
	if _, err = held.Exec(context.Background(), `SELECT customer_id FROM customers WHERE customer_id='10000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	concurrent := make(chan int, 2)
	for _, request := range []struct{ body, token string }{{`{"default_wht_rate":"4"}`, token}, {`{"default_wht_gross_up":true}`, token2}} {
		go func(body, token string) {
			r := httptest.NewRequest("PATCH", "/api/v1/customers/10000000-0000-0000-0000-000000000001", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			concurrent <- w.Code
		}(request.body, request.token)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		err = pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%customers%' AND pid<>pg_backend_pid()`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("updates did not wait on held customer row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = held.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case code := <-concurrent:
			if code != 200 {
				t.Errorf("held row update status %d", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("held row update timed out")
		}
	}
	w = call(`{}`)
	var lockedResult map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &lockedResult); err != nil || lockedResult["default_wht_rate"] != "4.0000" || lockedResult["default_wht_gross_up"] != true {
		t.Errorf("lost withholding update: %s %v", w.Body.String(), err)
	}
	// A database failure is sanitized and leaves the target unchanged.
	if _, err = pool.Exec(context.Background(), `CREATE FUNCTION reject_customer_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'secret database error'; END $$; CREATE TRIGGER reject_customer_update BEFORE UPDATE ON customers FOR EACH ROW EXECUTE FUNCTION reject_customer_update()`); err != nil {
		t.Fatal(err)
	}
	w = call(`{"notes":"must roll back"}`)
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret database error") {
		t.Errorf("database failure: %d %s", w.Code, w.Body.String())
	}
	if _, err = pool.Exec(context.Background(), `DROP TRIGGER reject_customer_update ON customers; DROP FUNCTION reject_customer_update()`); err != nil {
		t.Fatal(err)
	}
	w = call(`{}`)
	if strings.Contains(w.Body.String(), "must roll back") {
		t.Error("failed database update persisted")
	}

	// Deferred constraint trigger fails at commit, after output encoding.
	if _, err = pool.Exec(context.Background(), `CREATE FUNCTION reject_customer_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'secret commit failure'; END $$; CREATE CONSTRAINT TRIGGER reject_customer_commit AFTER UPDATE ON customers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_customer_commit()`); err != nil {
		t.Fatal(err)
	}
	w = call(`{"notes":"commit failure"}`)
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret commit failure") {
		t.Errorf("commit failure: %d %s", w.Code, w.Body.String())
	}
	if _, err = pool.Exec(context.Background(), `DROP TRIGGER reject_customer_commit ON customers; DROP FUNCTION reject_customer_commit()`); err != nil {
		t.Fatal(err)
	}
	w = call(`{}`)
	if strings.Contains(w.Body.String(), "commit failure") {
		t.Error("failed commit persisted")
	}

	orderTracer := &customerUpdateOrderTracer{}
	orderConfig, err := pgxpool.ParseConfig(db.URL())
	if err != nil {
		t.Fatal(err)
	}
	orderConfig.ConnConfig.Tracer = orderTracer
	orderPool, err := pgxpool.NewWithConfig(context.Background(), orderConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer orderPool.Close()
	orderHandler, err := NewHandlerWithSessions(orderPool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	orderRequest := httptest.NewRequest("PATCH", "/api/v1/customers/10000000-0000-0000-0000-000000000001", strings.NewReader(`{}`))
	orderRequest.Header.Set("Content-Type", "application/json")
	orderRequest.AddCookie(adminCookie)
	orderResponse := httptest.NewRecorder()
	orderHandler.ServeHTTP(orderResponse, orderRequest)
	if orderResponse.Code != 200 || !reflect.DeepEqual(orderTracer.names, []string{"LockSessionUserByID", "LockCustomer"}) {
		t.Errorf("lock order: %d %v", orderResponse.Code, orderTracer.names)
	}
	// Pause after the initial admin lookup, then demote before the transactional recheck.
	tracer := &customerLockTracer{reached: make(chan uint32, 1), release: make(chan struct{})}
	tracedConfig, err := pgxpool.ParseConfig(db.URL())
	if err != nil {
		t.Fatal(err)
	}
	tracedConfig.ConnConfig.Tracer = tracer
	tracedPool, err := pgxpool.NewWithConfig(context.Background(), tracedConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer tracedPool.Close()
	defer tracer.unblock()
	tracedHandler, err := NewHandlerWithSessions(tracedPool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("PATCH", "/api/v1/customers/10000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"demoted"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(adminCookie)
		w := httptest.NewRecorder()
		tracedHandler.ServeHTTP(w, r)
		done <- w
	}()
	select {
	case <-tracer.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("admin lock not reached")
	}
	if _, err = pool.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	tracer.unblock()
	select {
	case response := <-done:
		if response.Code != 403 {
			t.Errorf("demotion: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("demotion request timed out")
	}
	if _, err = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	w = call(`{}`)
	if strings.Contains(w.Body.String(), `"notes":"demoted"`) {
		t.Error("demoted request changed customer")
	}
	// Compare complete responses against the real Python router on a separate database.
	pythonDB := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pyPool, err := pgxpool.New(context.Background(), pythonDB.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pyPool.Close()
	seed := `INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000003','Differential')`
	if _, err = pool.Exec(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	if _, err = pyPool.Exec(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	payloads := []string{`{"name":"Full","active":false,"contact_first_name":"First","contact_last_name":"Last","contact_phone":"1","contact_phone_2":"2","account_number":"acct","website":"https://example.com","notes":"note","default_currency":"USD","billing_address":"legacy","billing_address1":"a1","billing_address2":"a2","billing_country":"ID","billing_state":"state","billing_city":"city","billing_postal_code":"123","ship_to_name":"recipient","shipping_address1":"s1","shipping_address2":"s2","shipping_country":"SG","shipping_state":"state","shipping_city":"city","shipping_postal_code":"456","shipping_phone":"3","shipping_delivery_instructions":"door","billing_cc_emails":["B@example.com"]}`, `{}`, `{"name":"Changed","contact_email":"Test@EXAMPLE.com","matching_aliases":["A","B"],"quotation_cc_emails":[" A@example.com ","a@EXAMPLE.COM"],"default_wht_type":"PPH23"}`, `{"notes":"hello","default_wht_type":"PPH21","default_wht_rate":"3.2500"}`, `{"name":""}`, `{"default_wht_type":"BAD"}`, `{"contact_email":null,"default_wht_type":"NONE"}`}
	payloadJSON, _ := json.Marshal(payloads)
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.customers import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['CUSTOMER_TEST_DATABASE_URL'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app,raise_server_exceptions=False) as client:
 results=[]
 for payload in json.loads(os.environ['PAYLOADS']):
  r=client.patch('/api/v1/customers/10000000-0000-0000-0000-000000000003',content=payload,headers={'content-type':'application/json'})
  results.append([r.status_code,r.json()])
 print(json.dumps(results))`)
	cmd.Env = append(os.Environ(), "CUSTOMER_TEST_DATABASE_URL="+pythonDB.URL(), "PAYLOADS="+string(payloadJSON))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python: %v %s", err, out)
	}
	var expected []any
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatalf("python output: %s: %v", out, err)
	}
	for i, body := range payloads {
		w := callPath("/api/v1/customers/10000000-0000-0000-0000-000000000003", body, adminCookie)
		var result any
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		actual := []any{float64(w.Code), result}
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Errorf("differential %s: got %v want %v", body, actual, expected[i])
		}
	}

	if after := snapshot(); after != relatedBefore {
		t.Errorf("related rows changed: %s vs %s", relatedBefore, after)
	}

}
