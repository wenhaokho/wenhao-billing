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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestInvoiceRecordPayment(t *testing.T) {
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
	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,wht_amount,wht_booked) VALUES ('30000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','MILESTONE','USD',100,100,'OPEN',10,0)`)
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
	r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":"40","wht_amount":"5","payment_date":"2026-01-01"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("expected payment creation, got %d: %s", w.Code, w.Body.String())
	}
	var status string
	var balance, booked string
	if e = p.QueryRow(context.Background(), `SELECT status,balance_due::text,wht_booked::text FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&status, &balance, &booked); e != nil {
		t.Fatal(e)
	}
	if status != "PARTIAL" || balance != "55.0000" || booked != "5.0000" {
		t.Fatalf("unexpected invoice: %s %s %s", status, balance, booked)
	}
	// Compare the actual Python HTTP endpoint using an isolated database.
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pp, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	_, e = pp.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,wht_amount,wht_booked) VALUES ('30000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','MILESTONE','USD',100,100,'OPEN',10,0)`)
	if e != nil {
		t.Fatal(e)
	}
	cases := [][2]string{{"OPEN", `{"amount":"40","wht_amount":"5","payment_date":"2026-01-01","payer_name":"Ada","payer_reference":"ref","notes":"note"}`}, {"SENT", `{"amount":"90","wht_amount":"10","payment_date":"2026-01-01"}`}, {"PARTIAL", `{"amount":"0","wht_amount":"5","payment_date":"2026-01-01"}`}, {"DRAFT", `{"amount":1,"payment_date":"2026-01-01"}`}, {"VOID", `{"amount":1,"payment_date":"2026-01-01"}`}, {"PAID", `{"amount":1,"payment_date":"2026-01-01"}`}, {"OPEN", `{"amount":0,"payment_date":"2026-01-01"}`}, {"OPEN", `{"amount":101,"payment_date":"2026-01-01"}`}, {"OPEN", `{"amount":1,"wht_amount":11,"payment_date":"2026-01-01"}`}}
	cases = append(cases, [][2]string{
		{"OPEN", `{"amount":"0.0001","payment_date":"2026-01-01"}`},
		{"OPEN", `{"amount":"1.00000","payment_date":"2026-01-01"}`},
		{"OPEN", `{"amount":"0.00001","payment_date":"2026-01-01"}`},
		{"OPEN", `{"amount":-1,"payment_date":"2026-01-01"}`},
		{"OPEN", `{}`}, {"OPEN", `null`}, {"OPEN", `[]`},
		{"OPEN", `{"amount":1,"payment_date":"invalid"}`},
	}...)
	cases = append(cases, [][2]string{
		{"BOOKED", `{"amount":1,"wht_amount":5,"payment_date":"2026-01-01"}`},
		{"BOOKED", `{"amount":1,"wht_amount":6,"payment_date":"2026-01-01"}`},
		{"OPEN", `{"amount":null,"payment_date":null}`}, {"OPEN", `{"amount":true,"payment_date":"2026-01-01"}`}, {"OPEN", `{"amount":1,"wht_amount":null,"payment_date":"2026-01-01"}`}, {"OPEN", `{"amount":1}`}, {"OPEN", `{"amount":"1e2","payment_date":"2026-01-01"}`},
		{"OPEN", ``}, {"OPEN", `{`}, {"MEDIA", `{"amount":1,"payment_date":"2026-01-01"}`}, {"MEDIA", `private text`},
	}...)
	encoded, _ := json.Marshal(cases)
	cmd := exec.Command("python", "-c", `import json, os
from types import SimpleNamespace
from uuid import UUID
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.include_router(router,prefix='/api/v1')
app.dependency_overrides[current_admin]=lambda: SimpleNamespace(user_id=UUID('00000000-0000-0000-0000-000000000001'))
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
out=[]
with TestClient(app) as c:
 for status,body in json.loads(os.environ['CASES']):
  media = 'text/plain' if status == 'MEDIA' else 'application/json'
  booked = 5 if status == 'BOOKED' else 0
  if status in ('BOOKED','MEDIA'): status='OPEN'
  with engine.begin() as conn:
   conn.execute(text('DELETE FROM reconciliation_log')); conn.execute(text('DELETE FROM payments')); conn.execute(text("UPDATE invoices SET status=:s,balance_due=100,wht_booked=:booked"),{'s':status,'booked':booked})
  r=c.post('/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment',content=body,headers={'content-type':media})
  data=r.json()
  # Go intentionally replaces Python's amount-bearing business errors.
  if r.status_code==400 and 'exceeds balance due' in data.get('detail',''): data['detail']='Payment exceeds balance due'
  if r.status_code==400 and 'exceeds outstanding withholding' in data.get('detail',''): data['detail']='Withholding exceeds outstanding withholding'
  if r.status_code==422:
   for error in data['detail']: error['input']=None
  if r.status_code==201: data={k:data[k] for k in ('amount','wht_amount','currency','payer_name','payer_reference','payment_date','intake_source','status','adjustment_type')}
  with engine.connect() as conn:
   row=conn.execute(text('SELECT status,balance_due::text,wht_booked::text FROM invoices')).one()
   log=conn.execute(text('SELECT reasons FROM reconciliation_log')).scalar()
  out.append([r.status_code,data,list(row),log])
print(json.dumps(out))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pydb.URL(), "CASES="+string(encoded))
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, output)
	}
	var expected [][]any
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e)
	}
	for i, tc := range cases {
		_, e = p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments`)
		if e != nil {
			t.Fatal(e)
		}
		status, booked := tc[0], 0
		if status == "BOOKED" {
			status, booked = "OPEN", 5
		}
		if status == "MEDIA" {
			status = "OPEN"
		}
		_, e = p.Exec(context.Background(), `UPDATE invoices SET status=$1,balance_due=100,wht_booked=$2`, status, booked)
		if e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(tc[1]))
		media := "application/json"
		if tc[0] == "MEDIA" {
			media = "text/plain"
		}
		req.Header.Set("Content-Type", media)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		var data map[string]any
		if e = json.Unmarshal(res.Body.Bytes(), &data); e != nil {
			t.Fatalf("case %d: %s", i, res.Body)
		}
		if res.Code == 422 {
			if detail, ok := data["detail"].([]any); ok {
				for _, item := range detail {
					if m, ok := item.(map[string]any); ok {
						m["input"] = nil
					}
				}
			}
		}
		if res.Code == 201 {
			filtered := map[string]any{}
			for _, k := range []string{"amount", "wht_amount", "currency", "payer_name", "payer_reference", "payment_date", "intake_source", "status", "adjustment_type"} {
				filtered[k] = data[k]
			}
			data = filtered
		}
		var st, bal, book string
		if e = p.QueryRow(context.Background(), `SELECT status,balance_due::text,wht_booked::text FROM invoices`).Scan(&st, &bal, &book); e != nil {
			t.Fatal(e)
		}
		var logs [][]string
		rows, e := p.Query(context.Background(), `SELECT reasons FROM reconciliation_log`)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var l []string
			_ = rows.Scan(&l)
			logs = append(logs, l)
		}
		rows.Close()
		var log any
		if len(logs) > 0 {
			log = logs[0]
		}
		// Normalize Go slices through JSON to compare with decoded Python output.
		gotBytes, _ := json.Marshal([]any{res.Code, data, []string{st, bal, book}, log})
		var got []any
		_ = json.Unmarshal(gotBytes, &got)
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("case %d: go=%s python=%v", i, gotBytes, expected[i])
		}
	}

	// Business-rule failures redact submitted values and leave financial state untouched.
	for _, tc := range []struct{ name, body, detail string }{
		{"overpayment", `{"amount":"101.1234","wht_amount":"1.2345","payment_date":"2026-01-01","payer_name":"secret-payer","payer_reference":"secret-reference","notes":"secret-note"}`, "Payment exceeds balance due"},
		{"withholding", `{"amount":"1.2345","wht_amount":"6.7890","payment_date":"2026-01-01","payer_name":"secret-payer","payer_reference":"secret-reference","notes":"secret-note"}`, "Withholding exceeds outstanding withholding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='OPEN',balance_due=100,wht_amount=10,wht_booked=5`)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			var result struct {
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if res.Code != 400 || result.Detail != tc.detail {
				t.Fatalf("response: %d %s", res.Code, res.Body.String())
			}
			for _, secret := range []string{"101.1234", "1.2345", "6.7890", "secret-payer", "secret-reference", "secret-note"} {
				if strings.Contains(res.Body.String(), secret) {
					t.Fatalf("response leaked %q: %s", secret, res.Body.String())
				}
			}
			var status, balance, booked, wht string
			var payments, logs int
			err = p.QueryRow(context.Background(), `SELECT status,balance_due::text,wht_booked::text,wht_amount::text,(SELECT count(*) FROM payments),(SELECT count(*) FROM reconciliation_log) FROM invoices`).Scan(&status, &balance, &booked, &wht, &payments, &logs)
			if err != nil {
				t.Fatal(err)
			}
			if status != "OPEN" || balance != "100.0000" || booked != "5.0000" || wht != "10.0000" || payments != 0 || logs != 0 {
				t.Fatalf("financial state: %s %s %s %s payments=%d logs=%d", status, balance, booked, wht, payments, logs)
			}
		})
	}
	// Validation must not echo supplied personal information.
	privateReq := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":1,"payment_date":"2026-01-01","payer_name":{"private":"sensitive-marker"}}`))
	privateReq.Header.Set("Content-Type", "application/json")
	privateReq.AddCookie(&http.Cookie{Name: "session", Value: token})
	privateResponse := httptest.NewRecorder()
	h.ServeHTTP(privateResponse, privateReq)
	if privateResponse.Code != 422 || strings.Contains(privateResponse.Body.String(), "sensitive-marker") {
		t.Fatalf("redaction: %d %s", privateResponse.Code, privateResponse.Body)
	}
	// Distinct persisted admins must serialize on the invoice, so two payments
	// that individually fit cannot jointly overpay it.
	_, e = p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='OPEN',balance_due=100,wht_booked=0; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`)
	if e != nil {
		t.Fatal(e)
	}
	token2, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, tk := range []string{token, token2} {
		wg.Add(1)
		go func(tk string) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":60,"payment_date":"2026-01-01"}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: tk})
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			codes <- res.Code
		}(tk)
	}
	wg.Wait()
	close(codes)
	successes, rejections := 0, 0
	for code := range codes {
		if code == 201 {
			successes++
		}
		if code == 400 {
			rejections++
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("concurrent payments: %d successes, %d rejections", successes, rejections)
	}
	var count int
	var remaining string
	if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM payments),balance_due::text FROM invoices`).Scan(&count, &remaining); e != nil {
		t.Fatal(e)
	}
	if count != 1 || remaining != "40.0000" {
		t.Fatalf("concurrent state %d %s", count, remaining)
	}

	// A payment racing void must serialize on the same invoice row.
	_, e = p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='OPEN',balance_due=100,wht_booked=0`)
	if e != nil {
		t.Fatal(e)
	}
	codes = make(chan int, 2)
	for _, op := range []struct{ path, body, token string }{{"record-payment", `{"amount":60,"payment_date":"2026-01-01"}`, token}, {"void", `{"reason":"test"}`, token2}} {
		wg.Add(1)
		go func(op struct{ path, body, token string }) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/"+op.path, strings.NewReader(op.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: op.token})
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			codes <- res.Code
		}(op)
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 201 && code != 200 && code != 400 {
			t.Errorf("payment/void race status %d", code)
		}
	}
	var status2 string
	if e = p.QueryRow(context.Background(), `SELECT status FROM invoices`).Scan(&status2); e != nil {
		t.Fatal(e)
	}
	if status2 != "VOID" {
		t.Fatalf("payment/void final status %s", status2)
	}
	// Settling hosting records an intent atomically without a provider call.
	_, e = p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='OPEN',balance_due=100,wht_booked=0; INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status) VALUES ('60000000-0000-0000-0000-000000000001','hosting','SERVICE',true,'SUSPENDED'); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('80000000-0000-0000-0000-000000000001','60000000-0000-0000-0000-000000000001','zone','record','example.com','A','1.2.3.4','5.6.7.8','SUSPENDED'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('70000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','60000000-0000-0000-0000-000000000001',0,'hosting',1,100,100)`)
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":100,"payment_date":"2026-01-01"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 201 {
		t.Fatalf("hosting settlement %d %s", res.Code, res.Body)
	}
	var intents int
	var paid bool
	if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM hosting_restore_intents),hosting_last_paid_at IS NOT NULL FROM items`).Scan(&intents, &paid); e != nil {
		t.Fatal(e)
	}
	if intents != 1 || !paid {
		t.Fatalf("hosting intent %d paid %v", intents, paid)
	}

	// Python restores immediately using a fake provider; Go records an intent
	// and leaves both hosting and provider status unchanged until delivery.
	_, e = pp.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='OPEN',balance_due=100,wht_booked=0; INSERT INTO items(item_id,name,item_type,is_hosting,hosting_status) VALUES ('60000000-0000-0000-0000-000000000001','hosting','SERVICE',true,'SUSPENDED'); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status) VALUES ('80000000-0000-0000-0000-000000000001','60000000-0000-0000-0000-000000000001','zone','record','example.com','A','1.2.3.4','5.6.7.8','SUSPENDED'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('70000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','60000000-0000-0000-0000-000000000001',0,'hosting',1,100,100)`)
	if e != nil {
		t.Fatal(e)
	}
	hostingCmd := exec.Command("python", "-c", `import json,os
from types import SimpleNamespace
from uuid import UUID
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
from app.services import cloudflare
calls=[]
cloudflare.restore_target=lambda target: calls.append(str(target.target_id))
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI();app.include_router(router,prefix='/api/v1')
app.dependency_overrides[current_admin]=lambda: SimpleNamespace(user_id=UUID('00000000-0000-0000-0000-000000000001'))
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
 r=c.post('/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment',json={'amount':100,'payment_date':'2026-01-01'})
with engine.connect() as conn:
 row=conn.execute(text('SELECT i.hosting_status,t.provider_status,i.hosting_last_paid_at IS NOT NULL FROM items i JOIN cloudflare_targets t ON t.item_id=i.item_id')).one()
print(json.dumps([r.status_code,list(row),len(calls)]))`)
	hostingCmd.Dir = "../../../backend"
	hostingCmd.Env = append(os.Environ(), "DB="+pydb.URL())
	hostingOut, e := hostingCmd.Output()
	if e != nil {
		t.Fatalf("Python fake hosting: %v", e)
	}
	var pythonHosting []any
	if e = json.Unmarshal(hostingOut, &pythonHosting); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(pythonHosting, []any{float64(201), []any{"ACTIVE", "ACTIVE", true}, float64(1)}) {
		t.Fatalf("Python fake hosting: %s", hostingOut)
	}
	var hostingStatus, providerStatus string
	if e = p.QueryRow(context.Background(), `SELECT i.hosting_status,t.provider_status FROM items i JOIN cloudflare_targets t ON t.item_id=i.item_id`).Scan(&hostingStatus, &providerStatus); e != nil {
		t.Fatal(e)
	}
	if hostingStatus != "SUSPENDED" || providerStatus != "SUSPENDED" {
		t.Fatalf("queued hosting status: %s %s", hostingStatus, providerStatus)
	}

	// A failure after inserting the payment must roll back all financial state.
	_, e = p.Exec(context.Background(), `DELETE FROM hosting_restore_intents; DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='OPEN',balance_due=100,wht_booked=0; CREATE FUNCTION reject_payment_log() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$; CREATE TRIGGER reject_payment_log BEFORE INSERT ON reconciliation_log FOR EACH ROW EXECUTE FUNCTION reject_payment_log()`)
	if e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":40,"payment_date":"2026-01-01"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 500 {
		t.Fatalf("injected failure %d %s", res.Code, res.Body)
	}
	if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM payments),balance_due::text FROM invoices`).Scan(&count, &remaining); e != nil {
		t.Fatal(e)
	}
	if count != 0 || remaining != "100.0000" {
		t.Fatalf("rollback state %d %s", count, remaining)
	}
	_, e = p.Exec(context.Background(), `DROP TRIGGER reject_payment_log ON reconciliation_log; DROP FUNCTION reject_payment_log()`)
	if e != nil {
		t.Fatal(e)
	}

	// Hold the invoice lock, then change its state while the request waits.
	_, e = p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='OPEN',balance_due=100,wht_booked=0`)
	if e != nil {
		t.Fatal(e)
	}
	lock, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Rollback(context.Background())
	if _, e = lock.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	result := make(chan int, 1)
	go func() {
		rq := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":1,"payment_date":"2026-01-01"}`))
		rq.Header.Set("Content-Type", "application/json")
		rq.AddCookie(&http.Cookie{Name: "session", Value: token})
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, rq)
		result <- rw.Code
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		e = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices WHERE invoice_id%FOR UPDATE%' AND pid<>pg_backend_pid())`).Scan(&waiting)
		if e != nil {
			t.Fatal(e)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("invoice lock wait not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, e = lock.Exec(context.Background(), `UPDATE invoices SET status='VOID'`); e != nil {
		t.Fatal(e)
	}
	if e = lock.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if code := <-result; code != 400 {
		t.Fatalf("locked invoice status: %d", code)
	}
	// A role change committed while the request waits must prevent the write.
	_, e = p.Exec(context.Background(), `UPDATE invoices SET status='OPEN',balance_due=100`)
	if e != nil {
		t.Fatal(e)
	}
	lock, e = p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Rollback(context.Background())
	if _, e = lock.Exec(context.Background(), `SELECT 1 FROM users WHERE user_id='00000000-0000-0000-0000-000000000001' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	result = make(chan int, 1)
	go func() {
		rq := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":1,"payment_date":"2026-01-01"}`))
		rq.Header.Set("Content-Type", "application/json")
		rq.AddCookie(&http.Cookie{Name: "session", Value: token})
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, rq)
		result <- rw.Code
	}()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		e = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM users WHERE user_id%FOR UPDATE%' AND pid<>pg_backend_pid())`).Scan(&waiting)
		if e != nil {
			t.Fatal(e)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("user lock wait not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, e = lock.Exec(context.Background(), `UPDATE users SET role='staff' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if e = lock.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if code := <-result; code != 403 {
		t.Fatalf("demoted user status: %d", code)
	}

	// The endpoint has no idempotency key: repeated requests are distinct payments.
	_, e = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'; UPDATE invoices SET status='OPEN',balance_due=100,wht_booked=0; DELETE FROM invoice_line_items`)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		rq := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":10,"payment_date":"2026-01-01"}`))
		rq.Header.Set("Content-Type", "application/json")
		rq.AddCookie(&http.Cookie{Name: "session", Value: token})
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, rq)
		if rw.Code != 201 || rw.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("repeated payment: %d %s", rw.Code, rw.Body)
		}
	}
	var journals int
	if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM payments), balance_due::text, (SELECT count(*) FROM journal_entries) FROM invoices`).Scan(&count, &remaining, &journals); e != nil {
		t.Fatal(e)
	}
	if count != 2 || remaining != "80.0000" || journals != 0 {
		t.Fatalf("repeated state: %d %s journals %d", count, remaining, journals)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	paths := doc["paths"].(map[string]any)
	operation := paths["/api/v1/invoices/{invoice_id}/record-payment"].(map[string]any)["post"].(map[string]any)
	if _, ok := operation["security"]; !ok {
		t.Error("missing security")
	}
	responses := operation["responses"].(map[string]any)
	for _, code := range []string{"201", "400", "401", "403", "404", "413", "422", "500"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("missing response %s", code)
		}
	}

	for _, currency := range []string{"IDR", "SGD"} {
		_, e = p.Exec(context.Background(), `UPDATE invoices SET currency=$1,status='OPEN',balance_due=100,wht_booked=0`, currency)
		if e != nil {
			t.Fatal(e)
		}
		rq := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":1,"currency":"USD","payment_date":"2026-01-01"}`))
		rq.Header.Set("Content-Type", "application/json")
		rq.AddCookie(&http.Cookie{Name: "session", Value: token})
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, rq)
		var payment map[string]any
		_ = json.Unmarshal(rw.Body.Bytes(), &payment)
		if rw.Code != 201 || payment["currency"] != currency {
			t.Fatalf("invoice currency %s: %d %s", currency, rw.Code, rw.Body)
		}
	}
	for _, tc := range []struct {
		cookie *http.Cookie
		want   int
	}{{nil, 401}, {&http.Cookie{Name: "session", Value: token + "bad"}, 401}} {
		rq := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":1,"payment_date":"2026-01-01"}`))
		rq.Header.Set("Content-Type", "application/json")
		if tc.cookie != nil {
			rq.AddCookie(tc.cookie)
		}
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, rq)
		if rw.Code != tc.want {
			t.Fatalf("authentication: %d", rw.Code)
		}
	}

	// An invalid returned timestamp makes JSON serialization fail before commit.
	_, e = p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='OPEN',balance_due=100,wht_booked=0; CREATE FUNCTION invalid_payment_timestamp() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.created_at := 'infinity'; RETURN NEW; END $$; CREATE TRIGGER invalid_payment_timestamp BEFORE INSERT ON payments FOR EACH ROW EXECUTE FUNCTION invalid_payment_timestamp()`)
	if e != nil {
		t.Fatal(e)
	}
	rq := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":40,"payment_date":"2026-01-01"}`))
	rq.Header.Set("Content-Type", "application/json")
	rq.AddCookie(&http.Cookie{Name: "session", Value: token})
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, rq)
	if rw.Code != 500 {
		t.Fatalf("serialization failure: %d %s", rw.Code, rw.Body)
	}
	if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM payments),balance_due::text FROM invoices`).Scan(&count, &remaining); e != nil {
		t.Fatal(e)
	}
	if count != 0 || remaining != "100.0000" {
		t.Fatalf("serialization rollback: %d %s", count, remaining)
	}
	_, e = p.Exec(context.Background(), `DROP TRIGGER invalid_payment_timestamp ON payments; DROP FUNCTION invalid_payment_timestamp()`)
	if e != nil {
		t.Fatal(e)
	}
	// Queue actual finalize and draft update requests alongside a payment, and
	// observe both waiting on the authoritative invoice lock before release.
	for _, action := range []string{"finalize", "update", "void"} {
		_, e = p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments; UPDATE invoices SET status='DRAFT',balance_due=100,wht_booked=0`)
		if e != nil {
			t.Fatal(e)
		}
		if action == "void" {
			if _, e = p.Exec(context.Background(), `UPDATE invoices SET status='OPEN'`); e != nil {
				t.Fatal(e)
			}
		}
		hold, err := p.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer hold.Rollback(context.Background())
		if _, err = hold.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
			t.Fatal(err)
		}
		results := make(chan int, 2)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			r := httptest.NewRequest("POST", "/api/v1/invoices/30000000-0000-0000-0000-000000000001/record-payment", strings.NewReader(`{"amount":10,"payment_date":"2026-01-01"}`))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			results <- w.Code
		}()
		go func() {
			defer group.Done()
			method, path, body := "POST", "/finalize", ""
			if action == "update" {
				method, path, body = "PATCH", "", `{"notes":"raced"}`
			}
			if action == "void" {
				method, path, body = "POST", "/void", `{"reason":"raced"}`
			}
			r := httptest.NewRequest(method, "/api/v1/invoices/30000000-0000-0000-0000-000000000001"+path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "session", Value: token2})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			results <- w.Code
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var n int
			if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices WHERE invoice_id%FOR UPDATE%' AND pid<>pg_backend_pid()`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n >= 2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: only %d requests waiting", action, n)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err = hold.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		group.Wait()
		close(results)
		got := map[int]int{}
		for code := range results {
			got[code]++
		}
		if action == "finalize" || action == "void" {
			if got[200] != 1 || got[201]+got[400] != 1 {
				t.Fatalf("finalize race: %v", got)
			}
		} else if got[200] != 1 || got[400] != 1 {
			t.Fatalf("update race: %v", got)
		}
	}

}
