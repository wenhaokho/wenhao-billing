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
	"sync"
	"testing"
	"time"
)

func TestBillPaymentAndVoid(t *testing.T) {
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
	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO vendors(vendor_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Vendor'); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','USD',10,10,'OPEN')`)
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
	for _, tc := range []struct {
		path, body string
		status     int
		contains   string
	}{{"record-payment", `{"amount":"4","payment_date":"2026-01-01"}`, 200, `"status":"PARTIAL"`}, {"void", `{"reason":"mistake"}`, 200, `"status":"VOID"`}, {"void", `{}`, 400, `bill is VOID and cannot be voided`}} {
		r := httptest.NewRequest("POST", "/api/v1/bills/50000000-0000-0000-0000-000000000001/"+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Errorf("%s: got %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	_, e = p.Exec(context.Background(), `UPDATE bills SET status='OPEN', balance_due=5 WHERE bill_id='50000000-0000-0000-0000-000000000001'`)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/api/v1/bills/50000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":"10.0000","payment_date":"2026-01-01"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "amount 10.0000 exceeds balance due 5.0000") {
		t.Errorf("overpayment: %d %s", w.Code, w.Body.String())
	}

	// Compare actual HTTP responses from the Python router against a separate database.
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pp, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	_, e = pp.Exec(context.Background(), `INSERT INTO vendors(vendor_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Vendor'); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','USD',10,10,'OPEN')`)
	if e != nil {
		t.Fatal(e)
	}
	cases := [][3]string{{"OPEN", "record-payment", `{"amount":"4","payment_date":"2026-01-01"}`}, {"OPEN", "record-payment", `{"amount":"10","payment_date":"2026-01-01"}`}, {"PARTIAL", "record-payment", `{"amount":"11.0000","payment_date":"2026-01-01"}`}, {"DRAFT", "record-payment", `{"amount":1,"payment_date":"2026-01-01"}`}, {"VOID", "record-payment", `{"amount":1,"payment_date":"2026-01-01"}`}, {"PAID", "record-payment", `{"amount":1,"payment_date":"2026-01-01"}`}, {"OPEN", "record-payment", `{}`}, {"OPEN", "record-payment", `{"amount":0,"payment_date":"2026-01-01"}`}, {"OPEN", "record-payment", `{"amount":null,"payment_date":null}`}, {"OPEN", "record-payment", `{"amount":"0.00001","payment_date":"2026-01-01"}`}, {"OPEN", "void", `{"reason":"mistake"}`}, {"DRAFT", "void", `{}`}, {"PARTIAL", "void", `{}`}, {"PAID", "void", `{}`}, {"VOID", "void", `{}`}, {"OPEN", "void", `null`}}
	cases = append(cases, [][3]string{
		{"OPEN", "record-payment", `{"amount":-1,"payment_date":"x"}`},
		{"OPEN", "record-payment", `{"amount":"1.00000","payment_date":"2026-01-01","extra":true}`},
		{"OPEN", "record-payment", `{"amount":"1000000000000000","payment_date":"2026-01-01"}`},
		{"OPEN", "record-payment", `{"amount":"abc","payment_date":"2026-01-01"}`},
		{"OPEN", "record-payment", `{"amount":true,"payment_date":"2026-01-01"}`},
		{"OPEN", "record-payment", `{"amount":1,"payment_date":"2026-01-01","payer_name":5}`},
		{"OPEN", "record-payment", `[]`}, {"OPEN", "record-payment", `null`}, {"OPEN", "record-payment", `{`},
		{"OPEN", "void", `[]`}, {"OPEN", "void", `{"reason":true}`}, {"OPEN", "void", `{"reason":0}`}, {"OPEN", "void", `{"reason":[1,2]}`},
	}...)
	cases = append(cases, [][3]string{{"OPEN", "record-payment", ``}, {"OPEN", "void", ``}, {"OPEN", "record-payment", `{"amount":"-1.00001","payment_date":"2026-01-01"}`}, {"OPEN", "record-payment", `{"amount":"-999999999999999999999","payment_date":"2026-01-01"}`}, {"OPEN", "record-payment", `{"amount":"1e2","payment_date":"2026-01-01"}`}}...)
	cases = append(cases, [3]string{"OPEN", "record-payment", `{"amount":0}`}, [3]string{"OPEN", "record-payment", `{"amount":"1.2e2","payment_date":"2026-01-01"}`})
	cases = append(cases, [][3]string{
		{"OPEN", "void", `{"reason":1e2}`}, {"OPEN", "void", `{"reason":1e-5}`}, {"OPEN", "void", `{"reason":1.0}`},
		{"OPEN", "void", `{"reason":{"z":1,"a":2,"x":{"two":true,"one":[null,"a\nb","it's", "\u0001"]}}}`},
		{"OPEN", "void", `{"reason":[]}`}, {"OPEN", "void", `{"reason":{}}`}, {"OPEN", "void", `{"reason":false}`}, {"OPEN", "void", `{"reason":""}`},
		{"OPEN", "void", `{"reason":["é", "a\tb", "it's", "\u200b"]}`},
		{"OPEN", "void", `{"reason":{"z":1,"a":2,"z":3}}`},
		{"OPEN", "void", `{"reason":[1e20,1e-4,-0.0,"a\"b'c"]}`},
	}...)
	encoded, _ := json.Marshal(cases)
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
from app.api.v1.routers.bills import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1));app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
out=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for status,path,body in json.loads(os.environ['CASES']):
  with engine.begin() as db: db.execute(text("UPDATE bills SET status=:s,balance_due=10,notes=NULL"),{'s':status})
  r=c.post('/api/v1/bills/50000000-0000-0000-0000-000000000001/'+path,content=body,headers={'content-type':'application/json'})
  try: result=r.json()
  except ValueError: result=r.text
  if isinstance(result,dict): result.pop('created_at',None)
  with engine.connect() as db: notes=db.execute(text("SELECT notes FROM bills")).scalar_one()
  out.append([r.status_code,result,notes])
print(json.dumps(out))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pydb.URL(), "CASES="+string(encoded))
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, output)
	}
	var want [][]any
	if e = json.Unmarshal(output, &want); e != nil {
		t.Fatal(e)
	}
	for i, tc := range cases {
		_, e = p.Exec(context.Background(), `UPDATE bills SET status=$1,balance_due=10,notes=NULL`, tc[0])
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("POST", "/api/v1/bills/50000000-0000-0000-0000-000000000001/"+tc[1], strings.NewReader(tc[2]))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		if json.Unmarshal(w.Body.Bytes(), &got) != nil {
			got = w.Body.String()
		}
		if m, ok := got.(map[string]any); ok {
			delete(m, "created_at")
		}
		var notes *string
		if e = p.QueryRow(context.Background(), `SELECT notes FROM bills`).Scan(&notes); e != nil {
			t.Fatal(e)
		}
		var goNotes any
		if notes != nil {
			goNotes = *notes
		}
		if !reflect.DeepEqual(goNotes, want[i][2]) {
			t.Errorf("differential %d persisted notes: %#v python %#v", i, goNotes, want[i][2])
		}
		if float64(w.Code) != want[i][0] || !reflect.DeepEqual(got, want[i][1]) {
			t.Errorf("differential %d %v: got %d %#v; python %#v", i, tc, w.Code, got, want[i])
		}
	}

	// Python parses JSON numeric literals through binary float; Go validates the exact token.
	precise := [][3]string{{"OPEN", "record-payment", `{"amount":1.0000000000000001,"payment_date":"2026-01-01"}`}}
	rawCases, _ := json.Marshal(precise)
	py := exec.Command("python", "-c", cmd.Args[2])
	py.Dir = cmd.Dir
	py.Env = append(os.Environ(), "DB="+pydb.URL(), "CASES="+string(rawCases))
	pyOutput, err := py.Output()
	if err != nil {
		t.Fatalf("python precision: %v", err)
	}
	var pyResult [][]any
	if err = json.Unmarshal(pyOutput, &pyResult); err != nil {
		t.Fatal(err)
	}
	if pyResult[0][0] != float64(200) {
		t.Fatalf("python precision response %s", pyOutput)
	}
	if _, e = p.Exec(context.Background(), `UPDATE bills SET status='OPEN',balance_due=10,notes=NULL`); e != nil {
		t.Fatal(e)
	}
	precisionReq := httptest.NewRequest("POST", "/api/v1/bills/50000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(precise[0][2]))
	precisionReq.Header.Set("Content-Type", "application/json")
	precisionReq.AddCookie(&http.Cookie{Name: "session", Value: token})
	precisionResp := httptest.NewRecorder()
	h.ServeHTTP(precisionResp, precisionReq)
	if precisionResp.Code != 422 || !strings.Contains(precisionResp.Body.String(), "decimal_max_places") {
		t.Errorf("exact payment precision: %d %s", precisionResp.Code, precisionResp.Body.String())
	}
	var remaining string
	if e = p.QueryRow(context.Background(), `SELECT balance_due::text FROM bills`).Scan(&remaining); e != nil || remaining != "10.0000" {
		t.Errorf("exact payment changed balance: %s %v", remaining, e)
	}

	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var document map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &document); e != nil {
		t.Fatal(e)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	amountSchema := schemas["BillPaymentSchema"].(map[string]any)["properties"].(map[string]any)["amount"].(map[string]any)
	numeric := amountSchema["anyOf"].([]any)[0].(map[string]any)
	if numeric["exclusiveMinimum"] != float64(0) {
		t.Errorf("amount numeric exclusiveMinimum: %#v", numeric)
	}
	for _, path := range []string{"record-payment", "void"} {
		op := document["paths"].(map[string]any)["/api/v1/bills/{bill_id}/"+path].(map[string]any)["post"].(map[string]any)
		if op["security"] == nil || op["requestBody"] == nil {
			t.Errorf("%s missing schema", path)
		}
		responses := op["responses"].(map[string]any)
		for _, code := range []string{"200", "400", "401", "403", "404", "413", "422", "500"} {
			if responses[code] == nil {
				t.Errorf("%s missing response %s", path, code)
			}
		}
	}
	r = httptest.NewRequest("POST", "/api/v1/bills/50000000-0000-0000-0000-000000000001/void", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Errorf("missing no-store: %v", w.Header())
	}

	call := func(method, path, body, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/bills/50000000-0000-0000-0000-000000000001"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	reset := func() {
		t.Helper()
		if _, err := p.Exec(context.Background(), `UPDATE bills SET status='OPEN',balance_due=10,notes=NULL`); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var v string
		if err := p.QueryRow(context.Background(), `SELECT to_jsonb(b)::text FROM bills b`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, trigger := range []struct{ name, sql string }{{"fail_pay", `CREATE FUNCTION fail_pay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced'; END $$`}, {"corrupt_pay", `CREATE FUNCTION corrupt_pay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.created_at='infinity'; RETURN NEW; END $$`}} {
		reset()
		if _, e = p.Exec(context.Background(), trigger.sql+`; CREATE TRIGGER `+trigger.name+` BEFORE UPDATE ON bills FOR EACH ROW EXECUTE FUNCTION `+trigger.name+`() `); e != nil {
			t.Fatal(e)
		}
		before := snapshot()
		for _, op := range []struct{ path, body string }{{"/record-payment", `{"amount":4,"payment_date":"2026-01-01"}`}, {"/void", `{}`}} {
			if w := call("POST", op.path, op.body, token); w.Code != 500 {
				t.Errorf("%s %s got %d", trigger.name, op.path, w.Code)
			}
			if snapshot() != before {
				t.Errorf("%s %s committed", trigger.name, op.path)
			}
		}
		if _, e = p.Exec(context.Background(), `DROP TRIGGER `+trigger.name+` ON bills; DROP FUNCTION `+trigger.name+`() `); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`); e != nil {
		t.Fatal(e)
	}
	token2, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	reset()
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, tok := range []string{token, token2} {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			codes <- call("POST", "/record-payment", `{"amount":6,"payment_date":"2026-01-01"}`, tok).Code
		}(tok)
	}
	wg.Wait()
	close(codes)
	gotCodes := map[int]int{}
	for c := range codes {
		gotCodes[c]++
	}
	if gotCodes[200] != 1 || gotCodes[400] != 1 {
		t.Errorf("concurrent payment outcomes %v", gotCodes)
	}
	var status, balance string
	if e = p.QueryRow(context.Background(), `SELECT status,balance_due::text FROM bills`).Scan(&status, &balance); e != nil || status != "PARTIAL" || balance != "4.0000" {
		t.Errorf("concurrent balance %s %s %v", status, balance, e)
	}
	for _, op := range []string{"/void", "/record-payment"} {
		reset()
		lock, err := p.Begin(context.Background())
		if err == nil {
			defer lock.Rollback(context.Background())
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = lock.Exec(context.Background(), `SELECT 1 FROM bills WHERE bill_id='50000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
			t.Fatal(err)
		}
		result := make(chan int, 1)
		body := `{}`
		if op == "/record-payment" {
			body = `{"amount":1,"payment_date":"2026-01-01"}`
		}
		go func() { result <- call("POST", op, body, token).Code }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiting bool
			if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM bills WHERE bill_id%FOR UPDATE%' AND pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("bill lock not observed")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err = lock.Exec(context.Background(), `UPDATE bills SET status='PAID'`); err != nil {
			t.Fatal(err)
		}
		if err = lock.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if code := <-result; code != 400 {
			t.Errorf("%s did not observe locked status: %d", op, code)
		}
	}

	for _, ops := range [][2]string{{"/void", "/record-payment"}, {"", "/void"}, {"", "/record-payment"}} {
		reset()
		start := make(chan struct{})
		out := make(chan int, 2)
		for i, op := range ops {
			wg.Add(1)
			go func(i int, op string) {
				defer wg.Done()
				<-start
				method, body := "POST", `{}`
				if op == "" {
					method = "PATCH"
					body = `{"notes":"patch"}`
				} else if op == "/record-payment" {
					body = `{"amount":4,"payment_date":"2026-01-01"}`
				}
				tok := token
				if i == 1 {
					tok = token2
				}
				out <- call(method, op, body, tok).Code
			}(i, op)
		}
		close(start)
		wg.Wait()
		close(out)
		for c := range out {
			if c != 200 && c != 400 {
				t.Errorf("race %v unexpected %d", ops, c)
			}
		}
		if e = p.QueryRow(context.Background(), `SELECT status,balance_due::text FROM bills`).Scan(&status, &balance); e != nil {
			t.Fatal(e)
		}
		if status != "VOID" && !(status == "PARTIAL" && balance == "6.0000") {
			t.Errorf("race %v invalid result %s %s", ops, status, balance)
		}
	}
	reset()
	lock, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(context.Background(), `SELECT 1 FROM users WHERE user_id='00000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() { result <- call("POST", "/void", `{}`, token).Code }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM users%' AND pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("admin lock not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = lock.Exec(context.Background(), `UPDATE users SET role='staff' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err = lock.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 403 {
		t.Errorf("demotion: %d", code)
	}
	if e = p.QueryRow(context.Background(), `SELECT status FROM bills`).Scan(&status); e != nil || status != "OPEN" {
		t.Errorf("demotion wrote %s %v", status, e)
	}

	if _, e = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	reset()
	for _, op := range []string{"/record-payment", "/void"} {
		body := `{}`
		if op == "/record-payment" {
			body = `{"amount":10,"payment_date":"2026-01-01"}`
		}
		r := httptest.NewRequest("POST", "/api/v1/bills/50000000-0000-0000-0000-000000000001"+op, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("unauthenticated %s: %d", op, w.Code)
		}
		if _, e = p.Exec(context.Background(), `UPDATE users SET role='staff' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
			t.Fatal(e)
		}
		if w = call("POST", op, body, token); w.Code != 403 {
			t.Errorf("non-admin %s: %d", op, w.Code)
		}
		if _, e = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
			t.Fatal(e)
		}
		if w = call("POST", op, body, token); w.Code != 200 {
			t.Errorf("first %s: %d", op, w.Code)
		}
		if w = call("POST", op, body, token); w.Code != 400 {
			t.Errorf("repeated %s: %d", op, w.Code)
		}
		reset()
	}
	for _, id := range []struct {
		value string
		code  int
	}{{"bad", 422}, {"50000000-0000-0000-0000-000000000099", 404}} {
		for _, op := range []string{"record-payment", "void"} {
			body := `{}`
			if op == "record-payment" {
				body = `{"amount":1,"payment_date":"2026-01-01"}`
			}
			r := httptest.NewRequest("POST", "/api/v1/bills/"+id.value+"/"+op, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != id.code {
				t.Errorf("id %s %s: %d", id.value, op, w.Code)
			}
		}
	}

	_, e = p.Exec(context.Background(), `INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','USD',8,8,'OPEN'); INSERT INTO bill_line_items(line_item_id,bill_id,position,description,quantity,unit_price,amount) VALUES ('60000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001',0,'line',1,10,10),('60000000-0000-0000-0000-000000000002','50000000-0000-0000-0000-000000000002',0,'other',1,8,8); INSERT INTO payments(payment_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('70000000-0000-0000-0000-000000000001',3,'USD','other','2026-01-01','manual','PENDING'); INSERT INTO chart_of_accounts(account_id,code,name,type) VALUES (98765,'999999','Expense','EXPENSE'); INSERT INTO journal_entries(entry_id,source_type,source_id) VALUES ('80000000-0000-0000-0000-000000000001','OTHER','70000000-0000-0000-0000-000000000001'); INSERT INTO journal_lines(line_id,entry_id,account_id,debit,currency) VALUES ('90000000-0000-0000-0000-000000000001','80000000-0000-0000-0000-000000000001',98765,3,'USD')`)
	if e != nil {
		t.Fatal(e)
	}
	unrelated := func() string {
		t.Helper()
		var v string
		if e := p.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT to_jsonb(b) FROM bills b WHERE bill_id='50000000-0000-0000-0000-000000000002'),(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM bill_line_items l),(SELECT jsonb_agg(to_jsonb(p)) FROM payments p),(SELECT jsonb_agg(to_jsonb(j)) FROM journal_entries j),(SELECT jsonb_agg(to_jsonb(l)) FROM journal_lines l))::text`).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	before := unrelated()
	if w := call("POST", "/record-payment", `{"amount":4,"payment_date":"2026-01-01"}`, token); w.Code != 200 {
		t.Errorf("snapshot payment %d", w.Code)
	}
	if w := call("POST", "/void", `{}`, token); w.Code != 200 {
		t.Errorf("snapshot void %d", w.Code)
	}
	if unrelated() != before {
		t.Error("unrelated bill, lines, payment or ledger changed")
	}

	for _, op := range []struct{ path, typ string }{{"record-payment", "model_attributes_type"}, {"void", "dict_type"}} {
		r := httptest.NewRequest("POST", "/api/v1/bills/50000000-0000-0000-0000-000000000001/"+op.path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "text/plain")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 422 || !strings.Contains(w.Body.String(), op.typ) {
			t.Errorf("media %s: %d %s", op.path, w.Code, w.Body.String())
		}
	}

	for _, op := range []string{"record-payment", "void"} {
		r := httptest.NewRequest("POST", "/api/v1/bills/50000000-0000-0000-0000-000000000001/"+op, strings.NewReader(`{`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 422 {
			t.Errorf("malformed unauthenticated %s: %d", op, w.Code)
		}
	}

}
