package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
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

type accountLockTraceKey struct{}
type accountLockTracer struct {
	enabled atomic.Bool
	reached chan struct{}
	release chan struct{}
}

func (tr *accountLockTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, accountLockTraceKey{}, strings.Contains(d.SQL, "-- name: LockAccount"))
}
func (tr *accountLockTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if ctx.Value(accountLockTraceKey{}) == true && tr.enabled.CompareAndSwap(true, false) {
		close(tr.reached)
		<-tr.release
	}
}

func TestAccountWrites(t *testing.T) {
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
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user')`)
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
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
	deleted := token("00000000-0000-0000-0000-000000000003")
	call := func(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("missing no-store")
		}
		return w
	}
	base := "/api/v1/accounting/chart-of-accounts"
	for _, tc := range []struct {
		method, path, body string
		c                  *http.Cookie
		want               int
	}{
		{"POST", base, `{"code":"Z999","name":"Test","type":"ASSET"}`, nil, 401},
		{"POST", base, `{"code":"Z999","name":"Test","type":"ASSET"}`, user, 403},
		{"POST", base, `{"code":"Z999","name":"Test","type":"ASSET"}`, admin, 201},
		{"POST", base, `{"code":"Z999","name":"Test","type":"ASSET"}`, admin, 409},
		{"POST", base, `{"code":"","name":"Test","type":"BAD"}`, admin, 422},
		{"PATCH", base + "/999999", `{}`, admin, 404},
		{"DELETE", base + "/999999", "", admin, 404},
	} {
		w := call(tc.method, tc.path, tc.body, tc.c)
		if w.Code != tc.want {
			t.Errorf("%s %s: want %d got %d %s", tc.method, tc.path, tc.want, w.Code, w.Body.String())
		}
	}

	tampered := call("PATCH", base+"/999999", `{}`, &http.Cookie{Name: "session", Value: admin.Value + "x"})
	if tampered.Code != 401 || strings.TrimSpace(tampered.Body.String()) != `{"detail":"not authenticated"}` {
		t.Errorf("tampered token: %d %s", tampered.Code, tampered.Body.String())
	}
	var id int32
	if e := pool.QueryRow(context.Background(), `SELECT account_id FROM chart_of_accounts WHERE code='Z999'`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	path := fmt.Sprintf("%s/%d", base, id)
	for _, tc := range []struct {
		method, path, body string
		c                  *http.Cookie
		want               int
	}{
		{"PATCH", path, `{"name":"Changed","active":false}`, admin, 200},
		{"PATCH", path, `{}`, admin, 200},
		{"PATCH", path, `{"name":null}`, admin, 409},
		{"PATCH", path, `{"code":"1000","name":"Should roll back"}`, admin, 409},
		{"PATCH", path, `{"code":""}`, admin, 422},
		{"PATCH", path, `{"name":""}`, admin, 422},
		{"PATCH", path, fmt.Sprintf(`{"name":%q}`, strings.Repeat("a", 101)), admin, 422},
		{"PATCH", path, `{"code":42}`, admin, 422},
		{"PATCH", path, `{"name":42}`, admin, 422},
		{"PATCH", path, `{"code":"12345678901"}`, admin, 422},
		{"PATCH", path, `{"type":"UNKNOWN"}`, admin, 422},
		{"PATCH", path, `{"active":"maybe"}`, admin, 422},
		{"PATCH", path, `{"active":null}`, admin, 409},
		{"PATCH", path, `{"code":null}`, admin, 409},
		{"PATCH", path, `{"type":null}`, admin, 409},
		{"PATCH", base + "/nope", `{}`, admin, 422},
		{"DELETE", path, "", nil, 401},
		{"DELETE", path, "", user, 403},
		{"DELETE", path, "", deleted, 403},
		{"PATCH", path, `{}`, &http.Cookie{Name: "session", Value: admin.Value + "x"}, 401},
		{"DELETE", path, "", admin, 200},
		{"DELETE", path, "", admin, 200},
	} {
		w := call(tc.method, tc.path, tc.body, tc.c)
		if w.Code != tc.want {
			t.Errorf("%s %s: want %d got %d %s", tc.method, tc.path, tc.want, w.Code, w.Body.String())
		}
	}

	// Pause immediately after the first SELECT FOR UPDATE returns, before its UPDATE.
	// A second request must wait until the first transaction commits, then read its change.
	trace := &accountLockTracer{reached: make(chan struct{}), release: make(chan struct{})}
	config, e := pgxpool.ParseConfig(db.URL())
	if e != nil {
		t.Fatal(e)
	}
	config.ConnConfig.Tracer = trace
	tracedPool, e := pgxpool.NewWithConfig(context.Background(), config)
	if e != nil {
		t.Fatal(e)
	}
	defer tracedPool.Close()
	tracedHandler, e := NewHandlerWithSessions(tracedPool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	tracedCall := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(admin)
		w := httptest.NewRecorder()
		tracedHandler.ServeHTTP(w, r)
		return w
	}
	trace.enabled.Store(true)
	first := make(chan *httptest.ResponseRecorder, 1)
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- tracedCall(`{"name":"Concurrent"}`) }()
	select {
	case <-trace.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("first update did not acquire lock")
	}
	go func() { second <- tracedCall(`{"type":"EQUITY"}`) }()
	// Observe the second query blocked by a database lock, or its premature completion.
	blocked := false
	var early *httptest.ResponseRecorder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case early = <-second:
		default:
		}
		if early != nil {
			break
		}
		var waiting bool
		e = pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%-- name: LockAccount%')`).Scan(&waiting)
		if e != nil {
			close(trace.release)
			t.Fatal(e)
		}
		if waiting {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(trace.release)
	if !blocked {
		t.Error("second update did not wait for first transaction row lock")
	}
	var firstResult, secondResult *httptest.ResponseRecorder
	select {
	case firstResult = <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("first update stuck")
	}
	if early != nil {
		secondResult = early
	} else {
		select {
		case secondResult = <-second:
		case <-time.After(10 * time.Second):
			t.Fatal("second update stuck")
		}
	}
	if firstResult.Code != 200 || secondResult.Code != 200 {
		t.Errorf("concurrent responses: %d %s / %d %s", firstResult.Code, firstResult.Body.String(), secondResult.Code, secondResult.Body.String())
	}
	var concurrentName, concurrentType string
	e = pool.QueryRow(context.Background(), `SELECT name,type FROM chart_of_accounts WHERE account_id=$1`, id).Scan(&concurrentName, &concurrentType)
	if e != nil || concurrentName != "Concurrent" || concurrentType != "EQUITY" {
		t.Errorf("lost concurrent update: %q %q %v", concurrentName, concurrentType, e)
	}
	_, e = pool.Exec(context.Background(), `UPDATE chart_of_accounts SET name='Changed',type='ASSET' WHERE account_id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		w := call(method, base+"/2147483648", `{}`, admin)
		if w.Code != 500 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || w.Body.String() != "Internal Server Error" {
			t.Errorf("%s overflow: %d %v %s", method, w.Code, w.Header(), w.Body.String())
		}
	}
	// Deferred constraint trigger fails at commit, after the UPDATE statement has succeeded.
	var snapshotBefore, snapshotAfter string
	snapshot := func() string {
		var v string
		if e := pool.QueryRow(context.Background(), `SELECT string_agg(row_to_json(a)::text, ',' ORDER BY account_id) FROM chart_of_accounts a`).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	snapshotBefore = snapshot()
	_, e = pool.Exec(context.Background(), `CREATE FUNCTION account_commit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private commit failure'; END $$; CREATE CONSTRAINT TRIGGER account_commit_fail AFTER UPDATE ON chart_of_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.code='Z999') EXECUTE FUNCTION account_commit_fail()`)
	if e != nil {
		t.Fatal(e)
	}
	commitFailure := call("PATCH", path, `{"name":"Commit failure"}`, admin)
	if commitFailure.Code != 500 || strings.Contains(commitFailure.Body.String(), "private commit failure") {
		t.Errorf("commit failure: %d %s", commitFailure.Code, commitFailure.Body.String())
	}
	snapshotAfter = snapshot()
	if snapshotBefore != snapshotAfter {
		t.Errorf("commit failure changed account snapshot: %s != %s", snapshotBefore, snapshotAfter)
	}
	_, e = pool.Exec(context.Background(), `DROP TRIGGER account_commit_fail ON chart_of_accounts; DROP FUNCTION account_commit_fail()`)
	if e != nil {
		t.Fatal(e)
	}
	// A failing database trigger mutates a separate row before raising; the write must leave both rows unchanged.
	_, e = pool.Exec(context.Background(), `CREATE FUNCTION account_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE chart_of_accounts SET name='corrupted' WHERE code='1000'; RAISE EXCEPTION 'private database detail'; END $$; CREATE TRIGGER account_test_fail BEFORE UPDATE ON chart_of_accounts FOR EACH ROW WHEN (NEW.code='Z999') EXECUTE FUNCTION account_test_fail()`)
	if e != nil {
		t.Fatal(e)
	}
	failed := call("PATCH", path, `{"name":"Failure"}`, admin)
	if failed.Code != 500 || strings.Contains(failed.Body.String(), "private database detail") {
		t.Errorf("failure leaked or accepted: %d %s", failed.Code, failed.Body.String())
	}
	_, e = pool.Exec(context.Background(), `DROP TRIGGER account_test_fail ON chart_of_accounts; DROP FUNCTION account_test_fail()`)
	if e != nil {
		t.Fatal(e)
	}
	var corrupted bool
	if e = pool.QueryRow(context.Background(), `SELECT name='corrupted' FROM chart_of_accounts WHERE code='1000'`).Scan(&corrupted); e != nil || corrupted {
		t.Errorf("unrelated row changed: %v %v", corrupted, e)
	}
	var name string
	var active bool
	if e := pool.QueryRow(context.Background(), `SELECT name,active FROM chart_of_accounts WHERE account_id=$1`, id).Scan(&name, &active); e != nil || name != "Changed" || active {
		t.Errorf("rollback/deactivation: %q %v %v", name, active, e)
	}
	for i, typ := range []string{"ASSET", "LIABILITY", "EQUITY", "INCOME", "EXPENSE", "COGS"} {
		w := call("POST", base, fmt.Sprintf(`{"code":"T%d","name":"Type","type":%q}`, i, typ), admin)
		if w.Code != 201 {
			t.Errorf("type %s: %d %s", typ, w.Code, w.Body.String())
			continue
		}
		var a accountOutput
		if e := json.Unmarshal(w.Body.Bytes(), &a); e != nil || !a.Active || a.Type != typ {
			t.Errorf("create response: %+v %v", a, e)
		}
	}

	pyWrites := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.accounting import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['ACCOUNT_DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def db_session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=db_session
with TestClient(app,raise_server_exceptions=False) as c:
 p='/api/v1/accounting/chart-of-accounts';a=c.post(p,json={'code':'PY15','name':'Example','type':'EQUITY'});id=a.json()['account_id'];b=c.patch(p+'/'+str(id),json={'name':'Updated','active':False});d=c.delete(p+'/'+str(id));print(json.dumps([[r.status_code,r.json()] for r in (a,b,d)]))`)
	pyWrites.Dir = "../../../backend"
	pyWrites.Env = append(os.Environ(), "ACCOUNT_DB="+db.URL())
	pyOut, e := pyWrites.Output()
	if e != nil {
		t.Fatal(e)
	}
	var pyResults [][]any
	if e = json.Unmarshal(pyOut, &pyResults); e != nil {
		t.Fatal(e)
	}
	created := call("POST", base, `{"code":"GO15","name":"Example","type":"EQUITY"}`, admin)
	var createdBody accountOutput
	if e = json.Unmarshal(created.Body.Bytes(), &createdBody); e != nil {
		t.Fatal(e)
	}
	goPath := fmt.Sprintf("%s/%d", base, createdBody.AccountID)
	goResults := []*httptest.ResponseRecorder{created, call("PATCH", goPath, `{"name":"Updated","active":false}`, admin), call("DELETE", goPath, "", admin)}
	for i, w := range goResults {
		var body map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		want := pyResults[i][1].(map[string]any)
		delete(body, "account_id")
		delete(body, "code")
		delete(want, "account_id")
		delete(want, "code")
		if float64(w.Code) != pyResults[i][0] || !reflect.DeepEqual(body, want) {
			t.Errorf("write parity: Python %v Go %d %v", pyResults[i], w.Code, body)
		}
	}
	cases := []string{`{}`, `{"code":"","name":"x","type":"BAD"}`, `{"code":"a","name":"x","type":"ASSET","active":null}`, `{"code":42,"name":42,"type":"ASSET"}`}
	py := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from app.api.v1.routers.accounting import router
from app.api.deps import current_admin
app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda: object()
with TestClient(app,raise_server_exceptions=False) as c:
 print(json.dumps([[r.status_code,r.json()] for r in (c.post('/api/v1/accounting/chart-of-accounts',content=b,headers={'content-type':'application/json'}) for b in json.loads(os.environ['CASES']))]))`)
	casesJSON, _ := json.Marshal(cases)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "CASES="+string(casesJSON))
	out, e := py.Output()
	if e != nil {
		t.Fatal(e)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	for i, b := range cases {
		w := call("POST", base, b, admin)
		var got any
		if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatal(e)
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(got, expected[i][1]) {
			t.Errorf("Python mismatch %s: want %v got %d %s", b, expected[i], w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	paths := doc["paths"].(map[string]any)
	for _, v := range []struct{ p, method string }{{base, "post"}, {base + "/{account_id}", "patch"}, {base + "/{account_id}", "delete"}} {
		op, ok := paths[v.p].(map[string]any)[v.method].(map[string]any)
		if !ok || op["security"] == nil {
			t.Errorf("missing secured %s %s", v.method, v.p)
		}

		responses := op["responses"].(map[string]any)
		for _, status := range []string{"401", "403", "404", "409", "422", "500"} {
			response, ok := responses[status].(map[string]any)
			if !ok {
				t.Errorf("%s missing %s", v.method, status)
				continue
			}
			content := response["content"].(map[string]any)
			jsonContent, ok := content["application/json"].(map[string]any)
			if !ok {
				t.Errorf("%s %s missing JSON", v.method, status)
				continue
			}
			schema := jsonContent["schema"].(map[string]any)
			want := "#/components/schemas/AuthErrorBody"
			if status == "422" {
				want = "#/components/schemas/ValidationErrorBody"
			}
			if schema["$ref"] != want {
				t.Errorf("%s %s schema %v", v.method, status, schema)
			}
		}
		if v.method != "post" {
			response := responses["500"].(map[string]any)
			content := response["content"].(map[string]any)
			plain, ok := content["text/plain"].(map[string]any)
			if !ok || plain["schema"].(map[string]any)["type"] != "string" {
				t.Errorf("%s 500 missing text/plain string", v.method)
			}
		}
		if v.method != "delete" {
			request := op["requestBody"].(map[string]any)
			schema := request["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
			want := "#/components/schemas/AccountCreateSchema"
			if v.method == "patch" {
				want = "#/components/schemas/AccountUpdateSchema"
			}
			if schema["$ref"] != want {
				t.Errorf("%s body %v", v.method, schema)
			}
		}
	}

	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	createSchema := schemas["AccountCreateSchema"].(map[string]any)
	updateSchema := schemas["AccountUpdateSchema"].(map[string]any)
	if createSchema["additionalProperties"] != true || updateSchema["additionalProperties"] != true {
		t.Error("unknown fields should be accepted")
	}
	if !reflect.DeepEqual(createSchema["required"], []any{"code", "name", "type"}) {
		t.Errorf("create required %v", createSchema["required"])
	}
	for _, v := range []struct {
		name string
		max  float64
	}{{"code", 10}, {"name", 100}} {
		for _, schema := range []map[string]any{createSchema, updateSchema} {
			p := schema["properties"].(map[string]any)[v.name].(map[string]any)
			if p["minLength"] != float64(1) || p["maxLength"] != v.max {
				t.Errorf("%s constraints %v", v.name, p)
			}
		}
	}
	for _, name := range []string{"code", "name", "type", "active"} {
		p := updateSchema["properties"].(map[string]any)[name].(map[string]any)
		types, ok := p["type"].([]any)
		if !ok || len(types) != 2 || types[1] != "null" {
			t.Errorf("%s not nullable: %v", name, p)
		}
	}
	if createSchema["properties"].(map[string]any)["active"].(map[string]any)["default"] != true {
		t.Error("active default missing")
	}
	for _, name := range []string{"AuthErrorBody", "ValidationErrorBody"} {
		if schemas[name].(map[string]any)["properties"].(map[string]any)["detail"] == nil {
			t.Errorf("missing %s detail", name)
		}
	}

}
