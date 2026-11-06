package platform

import (
	"context"
	"encoding/json"
	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRecurringActionPause(t *testing.T) {
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
	defer p.Close()
	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,created_at) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'DRAFT',false,'2025-01-01')`)
	if e != nil {
		t.Fatal(e)
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

	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var document map[string]any
	if err := json.Unmarshal(spec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	operation := document["paths"].(map[string]any)["/api/v1/invoices/recurring-templates/{template_id}"].(map[string]any)["patch"].(map[string]any)
	if operation["requestBody"] == nil || operation["security"] == nil || operation["responses"].(map[string]any)["409"] == nil {
		t.Fatal("incomplete action schema")
	}

	_, e = p.Exec(context.Background(), `UPDATE invoices SET is_template=true, invoice_type='RECURRING', billing_cycle_ref='{"frequency":"DAILY","start_date":"2030-01-01","end_mode":"NEVER"}' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`)
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("PATCH", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"action":"pause"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"paused":true`) {
		t.Fatalf("pause status %d: %s", out.Code, out.Body.String())
	}
	// Compare successive actions against the Python HTTP handler on an independent database.
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pp, err := pgxpool.New(context.Background(), pydb.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pp.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,created_at,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','RECURRING','USD',10,10,'DRAFT',true,'2025-01-01','{"frequency":"DAILY","start_date":"2020-01-01","end_mode":"NEVER","extra":123456789012345678901234567890}')`
	if _, err = pp.Exec(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","start_date":"2020-01-01","end_mode":"NEVER","extra":123456789012345678901234567890}' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
result=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for action in ['PAUSE','resume','END_NOW','end_now','PAUSE']:
  r=c.patch('/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001',json={'action':action}); result.append([r.status_code,r.json().get('billing_cycle_ref') if r.status_code==200 else r.json().get('detail')])
print(json.dumps(result))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pydb.URL())
	pyout, err := cmd.Output()
	if err != nil {
		t.Fatalf("python %v: %s", err, pyout)
	}
	var expected [][]any
	if err = json.Unmarshal(pyout, &expected); err != nil {
		t.Fatal(err)
	}
	for i, action := range []string{"PAUSE", "resume", "END_NOW", "end_now", "PAUSE"} {
		req = httptest.NewRequest("PATCH", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"action":"`+action+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		out = httptest.NewRecorder()
		h.ServeHTTP(out, req)
		var result map[string]any
		if err = json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		value := result["billing_cycle_ref"]
		if out.Code != 200 {
			value = result["detail"]
		}
		if out.Code != int(expected[i][0].(float64)) || !reflect.DeepEqual(value, expected[i][1]) {
			t.Fatalf("%s: Go %d %#v Python %#v", action, out.Code, value, expected[i])
		}
	}

	// Reset each case independently and compare actual Python and Go HTTP behavior.
	type matrixCase struct {
		Schedule string `json:"schedule"`
		Body     string `json:"body"`
		Child    string `json:"child"`
		Template bool   `json:"template"`
		Missing  bool   `json:"missing"`
	}
	matrix := []matrixCase{}
	for _, schedule := range []string{`{"frequency":"DAILY","start_date":"2030-01-01","end_mode":"NEVER"}`, `{"frequency":"DAILY","start_date":"2020-01-01","end_mode":"ON_DATE","end_date":"2020-01-02","paused":true}`, `{"frequency":"DAILY","start_date":"2020-01-01","end_mode":"AFTER_N","end_after_cycles":2}`, `{"frequency":"BAD","paused":true}`, `{"frequency":"DAILY","start_date":"2020-01-01","paused":true}`, `{"frequency":"DAILY","start_date":"20200101","end_mode":"ON_DATE","end_date":"20191231","paused":true}`, `{"frequency":"DAILY","start_date":"2020-W01-1","end_mode":"AFTER_N","end_after_cycles":true}`, `{"frequency":"DAILY","start_date":"20200101","interval":true}`} {
		for _, action := range []string{"PAUSE", "pause", "RESUME", "resume", "END_NOW", "end_now"} {
			matrix = append(matrix, matrixCase{Schedule: schedule, Body: `{"action":"` + action + `"}`, Template: true})
		}
	}
	for _, child := range []string{`{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":"2020-01-02"}`, `{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":"bad"}`} {
		for _, action := range []string{"PAUSE", "RESUME", "END_NOW"} {
			matrix = append(matrix, matrixCase{Schedule: `{"frequency":"DAILY","start_date":"2020-01-01","end_mode":"AFTER_N","end_after_cycles":2}`, Body: `{"action":"` + action + `"}`, Child: child, Template: true})
		}
	}
	for _, body := range []string{`{}`, `{"action":null}`, `{"action":12}`, `[]`, `null`, `{"action":"INVALID"}`, `{`} {
		matrix = append(matrix, matrixCase{Schedule: `{}`, Body: body, Template: true})
	}
	matrix = append(matrix, matrixCase{Schedule: `{}`, Body: `{"action":"PAUSE"}`, Template: false}, matrixCase{Schedule: `{}`, Body: `{"action":"PAUSE"}`, Template: true, Missing: true})
	matrixJSON, _ := json.Marshal(matrix)
	matrixCmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
results=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for case in json.loads(os.environ['CASES']):
  with engine.begin() as db:
   db.execute(text("DELETE FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000002'"))
   db.execute(text("UPDATE invoices SET billing_cycle_ref=CAST(:s AS jsonb),is_template=:t WHERE invoice_id='20000000-0000-0000-0000-000000000001'"),dict(s=case['schedule'],t=case['template']))
   if case['child']: db.execute(text("INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000002','RECURRING','USD',1,1,'DRAFT',false,'2020-01-05',CAST(:c AS jsonb))"),dict(c=case['child']))
  ident='20000000-0000-0000-0000-000000000099' if case['missing'] else '20000000-0000-0000-0000-000000000001'
  r=c.patch('/api/v1/invoices/recurring-templates/'+ident,content=case['body'],headers={'content-type':'application/json'})
  results.append([r.status_code,r.json().get('billing_cycle_ref') if r.status_code==200 else None])
print(json.dumps(results))`)
	matrixCmd.Dir = "../../../backend"
	matrixCmd.Env = append(os.Environ(), "DB="+pydb.URL(), "CASES="+string(matrixJSON))
	matrixOut, err := matrixCmd.Output()
	if err != nil {
		t.Fatalf("python matrix: %v", err)
	}
	var matrixExpected [][]any
	if err = json.Unmarshal(matrixOut, &matrixExpected); err != nil {
		t.Fatal(err)
	}
	for i, tc := range matrix {
		if _, err = p.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000002'`); err != nil {
			t.Fatal(err)
		}
		if _, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref=$1::jsonb,is_template=$2 WHERE invoice_id='20000000-0000-0000-0000-000000000001'`, tc.Schedule, tc.Template); err != nil {
			t.Fatal(err)
		}
		if tc.Child != "" {
			if _, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000002','RECURRING','USD',1,1,'DRAFT',false,'2020-01-05',$1::jsonb)`, tc.Child); err != nil {
				t.Fatal(err)
			}
		}
		id := "20000000-0000-0000-0000-000000000001"
		if tc.Missing {
			id = "20000000-0000-0000-0000-000000000099"
		}
		req := httptest.NewRequest("PATCH", "/api/v1/invoices/recurring-templates/"+id, strings.NewReader(tc.Body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		result := httptest.NewRecorder()
		h.ServeHTTP(result, req)
		var value any
		if result.Code == 200 {
			var payload map[string]any
			if err = json.Unmarshal(result.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			value = payload["billing_cycle_ref"]
		}
		if result.Code != int(matrixExpected[i][0].(float64)) || !reflect.DeepEqual(value, matrixExpected[i][1]) {
			t.Errorf("matrix %d %#v: Go %d %#v Python %#v", i, tc, result.Code, value, matrixExpected[i])
		}
	}
	if _, err = p.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000002'; UPDATE invoices SET is_template=true WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}

	call := func(action string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"action":"`+action+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		schedule, action string
		status           int
		want             string
	}{
		{`{"frequency":"DAILY","start_date":"2030-01-01","end_mode":"AFTER_N","end_after_cycles":2}`, "PAUSE", 200, `"paused":true`},
		{`{"frequency":"DAILY","start_date":"2030-01-01","end_mode":"ON_DATE","end_date":"2029-12-31","paused":true}`, "RESUME", 409, "view-only"},
		{`{"frequency":"BAD","paused":true}`, "RESUME", 200, `"frequency":"BAD"`},
		{`{"frequency":"BAD","paused":true}`, "END_NOW", 200, `"end_mode":"ON_DATE"`},
		{`{"frequency":"DAILY","start_date":"2020-01-01"}`, "END_NOW", 200, `"end_date":"2019-12-31"`},
		{`{"frequency":"DAILY","start_date":"2030-01-01"}`, "unknown", 400, "unknown action"},
	} {
		if _, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref=$1::jsonb WHERE invoice_id='20000000-0000-0000-0000-000000000001'`, tc.schedule); err != nil {
			t.Fatal(err)
		}
		w := call(tc.action)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s %s: %d %s", tc.schedule, tc.action, w.Code, w.Body.String())
		}
	}
	// Rows and actions agree that a basic-date schedule with no cycles is ended.
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","start_date":"20200101","end_mode":"ON_DATE","end_date":"20191231","paused":true}' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	rowsReq := httptest.NewRequest("GET", "/api/v1/invoices/recurring-templates/rows", nil)
	rowsReq.AddCookie(&http.Cookie{Name: "session", Value: token})
	rowsOut := httptest.NewRecorder()
	h.ServeHTTP(rowsOut, rowsReq)
	if rowsOut.Code != 200 || !strings.Contains(rowsOut.Body.String(), `"status":"ENDED"`) {
		t.Fatalf("basic date rows: %d %s", rowsOut.Code, rowsOut.Body.String())
	}
	if w := call("PAUSE"); w.Code != 409 {
		t.Fatalf("ended pause: %d %s", w.Code, w.Body.String())
	}
	if w := call("END_NOW"); w.Code != 200 || !strings.Contains(w.Body.String(), `"end_date":"20191231"`) {
		t.Fatalf("ended end_now: %d %s", w.Code, w.Body.String())
	}

	// Boundary failures do not reveal submitted action data and are never cacheable.
	for _, tc := range []struct {
		path, body string
		cookie     bool
		status     int
	}{
		{"/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", `{"action":"PAUSE"}`, false, 401},
		{"/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000099", `{"action":"PAUSE"}`, true, 404},
		{"/api/v1/invoices/recurring-templates/bad", `{"action":"PAUSE"}`, true, 422},
		{"/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", `{}`, true, 422},
		{"/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", `{"action":null}`, true, 422},
		{"/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", `{"action":"secret-value"}`, true, 400},
		{"/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", strings.Repeat("x", (1<<20)+1), true, 413},
	} {
		req := httptest.NewRequest("PATCH", tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if tc.cookie {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "secret-value") {
			t.Errorf("boundary %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='viewer' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if w := call("PAUSE"); w.Code != 403 {
		t.Fatalf("demoted: %d %s", w.Code, w.Body.String())
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}

	// Demotion committed while a request is waiting must be observed under the user lock.
	adminTx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer adminTx.Rollback(context.Background())
	if _, err = adminTx.Exec(context.Background(), `UPDATE users SET role='viewer' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	adminDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { adminDone <- call("PAUSE") }()
	authDeadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-adminDone:
			t.Fatal("request did not wait for administrator lock")
		default:
		}
		if time.Now().After(authDeadline) {
			t.Fatal("administrator lock wait missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = adminTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w := <-adminDone; w.Code != 403 {
		t.Fatalf("concurrent demotion: %d %s", w.Code, w.Body.String())
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}

	// A live child uses its cycle key instead of the later issue date; VOID children are ignored.
	_, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","start_date":"2020-01-01"}' WHERE invoice_id='20000000-0000-0000-0000-000000000001'; INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000002','RECURRING','USD',1,1,'DRAFT',false,'2020-02-10','{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":"2020-01-03"}'), ('20000000-0000-0000-0000-000000000003','RECURRING','USD',1,1,'VOID',false,'2020-03-10','{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":"2020-03-10"}')`)
	if err != nil {
		t.Fatal(err)
	}
	childResult := call("END_NOW")
	if childResult.Code != 200 || !strings.Contains(childResult.Body.String(), `"end_date":"2020-01-03"`) {
		t.Fatalf("child cycle: %d %s", childResult.Code, childResult.Body.String())
	}

	// A child-like transaction holds the template lock while inserting its child.
	if _, err = p.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_id IN ('20000000-0000-0000-0000-000000000002','20000000-0000-0000-0000-000000000003'); UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","start_date":"2020-01-01"}' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT invoice_id FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call("END_NOW") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-done:
			t.Fatal("action did not wait for template lock")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("action did not reach template lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000002','RECURRING','USD',1,1,'DRAFT',false,'2020-01-05','{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":"2020-01-05"}')`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Code != 200 || !strings.Contains(result.Body.String(), `"end_date":"2020-01-05"`) {
			t.Fatalf("concurrent child: %d %s", result.Code, result.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("action did not finish")
	}

	// Output validation must happen before the schedule mutation commits.
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET created_at='infinity', billing_cycle_ref='{"frequency":"DAILY","start_date":"2030-01-01"}' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	w := call("PAUSE")
	if w.Code != 500 {
		t.Errorf("invalid output: %d %s", w.Code, w.Body.String())
	}
	var paused bool
	if err = p.QueryRow(context.Background(), `SELECT billing_cycle_ref ? 'paused' FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000001'`).Scan(&paused); err != nil || paused {
		t.Fatalf("mutation was committed: %v %v", paused, err)
	}

}
