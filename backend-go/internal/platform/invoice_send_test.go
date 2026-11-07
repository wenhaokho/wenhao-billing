package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestInvoiceSendPostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	d := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, e := pgxpool.New(context.Background(), d.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com','x','admin'); INSERT INTO customers(customer_id,name,contact_email) VALUES ('10000000-0000-0000-0000-000000000001','Customer','customer@example.com'); INSERT INTO invoices(invoice_id,customer_id,invoice_number,currency,amount,balance_due,invoice_type,status,due_date) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','I-1','USD',10,10,'MILESTONE','DRAFT','2026-03-01')`)
	if e != nil {
		t.Fatal(e)
	}
	sender := &quotationFakeSender{}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, Sender: sender}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/invoices/20000000-0000-0000-0000-000000000001/send", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	sender.err = errors.New("provider failed")
	if w := call(`{}`); w.Code != 500 {
		t.Fatalf("failure: %d %s", w.Code, w.Body.String())
	}
	var status string
	if e = p.QueryRow(context.Background(), `SELECT status FROM invoices`).Scan(&status); e != nil || status != "DRAFT" {
		t.Fatalf("rollback %s %v", status, e)
	}
	sender.err = nil
	w := call(`{"cc_email":"cc@example.com","To_Email":"ignored@example.com"}`)
	if w.Code != 200 {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"SENT"`) {
		t.Fatal(w.Body.String())
	}
	m := sender.messages[len(sender.messages)-1]
	if m.To[0] != "customer@example.com" || len(m.CC) != 1 || m.Subject != "Invoice I-1" || m.Text != "Hi Customer,\n\nPlease find attached invoice I-1 for 10.00 USD.\nDue date: 2026-03-01.\n\nThank you." || len(m.Attachments) != 1 || !strings.HasPrefix(string(m.Attachments[0].Content), "%PDF-") {
		t.Fatalf("message: %+v", m)
	}
	_, e = p.Exec(context.Background(), `UPDATE invoices SET status='VOID'`)
	if e != nil {
		t.Fatal(e)
	}
	n := len(sender.messages)
	if w = call(`{}`); w.Code != 400 || len(sender.messages) != n {
		t.Fatalf("void %d %s", w.Code, w.Body.String())
	}
	// The row lock prevents a void from overtaking a send in progress.
	_, e = p.Exec(context.Background(), `UPDATE invoices SET status='OPEN'`)
	if e != nil {
		t.Fatal(e)
	}
	sender.entered = make(chan struct{})
	sender.release = make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(`{}`) }()
	<-sender.entered
	updated := make(chan error, 1)
	go func() { _, err := p.Exec(context.Background(), `UPDATE invoices SET status='VOID'`); updated <- err }()
	select {
	case <-updated:
		t.Fatal("status update bypassed invoice lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(sender.release)
	if w = <-done; w.Code != 200 {
		t.Fatalf("concurrent send: %d %s", w.Code, w.Body.String())
	}
	if e = <-updated; e != nil {
		t.Fatal(e)
	}

	// Provider acceptance survives client cancellation, with currency rounding
	// and normalized CC addresses in the delivered snapshot.
	_, e = p.Exec(context.Background(), `UPDATE invoices SET status='OPEN',currency='IDR',amount=1000.5,balance_due=1000.5,wht_type='PPH_23',wht_amount=100.5,wht_booked=0`)
	if e != nil {
		t.Fatal(e)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelling := &quotationFakeSender{afterSend: cancel}
	cfg.Sender = cancelling
	hc, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	rc := httptest.NewRequest("POST", "/api/v1/invoices/20000000-0000-0000-0000-000000000001/send", strings.NewReader(`{"cc_email":"Name<CC@EXAMPLE.COM>"}`)).WithContext(cancelCtx)
	rc.Header.Set("Content-Type", "application/json")
	rc.AddCookie(&http.Cookie{Name: "session", Value: token})
	wc := httptest.NewRecorder()
	hc.ServeHTTP(wc, rc)
	if wc.Code != 200 {
		t.Fatalf("cancel after send: %d %s", wc.Code, wc.Body.String())
	}
	if e = p.QueryRow(context.Background(), `SELECT status FROM invoices`).Scan(&status); e != nil || status != "SENT" {
		t.Fatalf("cancel commit: %s %v", status, e)
	}
	message := cancelling.messages[0]
	if len(message.CC) != 1 || message.CC[0] != "CC@example.com" || !strings.Contains(message.Text, "1,000 IDR") || !strings.Contains(message.Text, "900 IDR") {
		t.Fatalf("currency/cc: %+v", message)
	}

	waitForInvoiceLock := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for ctx.Err() == nil {
			var n int
			if err := p.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%-- name: LockInvoice :one%'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n > 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("invoice lock wait not observed")
	}
	// A second administrator's overlapping send retries its stale snapshot and
	// performs an intentional resend after the first transaction commits.
	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','admin2@example.com','x','admin'); UPDATE invoices SET status='DRAFT'`)
	if e != nil {
		t.Fatal(e)
	}
	secondToken, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	sender2 := &quotationFakeSender{entered: make(chan struct{}), release: make(chan struct{})}
	cfg.Sender = sender2
	h2, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	callAs := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/invoices/20000000-0000-0000-0000-000000000001/send", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h2.ServeHTTP(w, r)
		return w
	}
	first, second := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- callAs(token) }()
	<-sender2.entered
	go func() { second <- callAs(secondToken) }()
	waitForInvoiceLock()
	close(sender2.release)
	if w = <-first; w.Code != 200 {
		t.Fatalf("first send: %d %s", w.Code, w.Body.String())
	}
	if w = <-second; w.Code != 200 {
		t.Fatalf("second send: %d %s", w.Code, w.Body.String())
	}
	if len(sender2.messages) != 2 {
		t.Fatalf("expected two sends, got %d", len(sender2.messages))
	}
	// A void committed while a sender waits is observed on its retry.
	_, e = p.Exec(context.Background(), `UPDATE invoices SET status='OPEN'`)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(context.Background(), `UPDATE invoices SET status='VOID'`); e != nil {
		t.Fatal(e)
	}
	waiting := make(chan *httptest.ResponseRecorder, 1)
	go func() { waiting <- callAs(secondToken) }()
	waitForInvoiceLock()
	if e = tx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if w = <-waiting; w.Code != 400 || !strings.Contains(w.Body.String(), "cannot send a voided invoice") {
		t.Fatalf("void race: %d %s", w.Code, w.Body.String())
	}
	if len(sender2.messages) != 2 {
		t.Fatal("void race sent a message")
	}

}

func TestInvoiceSendDifferential(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	c := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	g := pgtestdb.Custom(t, c, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	py := pgtestdb.Custom(t, c, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, e := pgxpool.New(context.Background(), g.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	pp, e := pgxpool.New(context.Background(), py.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com','x','admin'); INSERT INTO customers(customer_id,name,contact_email) VALUES ('10000000-0000-0000-0000-000000000001','Customer','customer@example.com'); INSERT INTO invoices(invoice_id,customer_id,invoice_number,currency,subtotal,amount,balance_due,invoice_type,status) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','I1','USD',10,10,10,'MILESTONE','DRAFT')`
	for _, db := range []*pgxpool.Pool{p, pp} {
		if _, e = db.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	bodies := []string{`{}`, `{"to_email":"other@example.com","cc_email":"a@example.com; b@example.com","subject":"Custom","message":"Hi"}`, `{"to_email":null,"subject":"","message":""}`, `{"subject":12}`, `{"cc_email":"a@b"}`, `{"cc_email":"bad"}`, `{"TO_EMAIL":"wrong@example.com"}`, `{"To_Email":"wrong@example.com"}`, `{"message":42,"subject":false}`, `[]`, `["private@example.com"]`}
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
import app.api.v1.routers.invoices as q
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI();app.include_router(q.router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
q.render_invoice_pdf=lambda *args: b'%PDF-fake'
q.send_email=lambda **kwargs: None
result=[]
with TestClient(app,raise_server_exceptions=False) as client:
 for body in json.loads(os.environ['BODIES']):
  with engine.begin() as conn: conn.execute(text("UPDATE invoices SET status='DRAFT'"))
  r=client.post('/api/v1/invoices/20000000-0000-0000-0000-000000000001/send',content=body,headers={'content-type':'application/json'})
  result.append([r.status_code,r.json()])
 r=client.post('/api/v1/invoices/bad/send',json={'message':42})
 result.append([r.status_code,r.json()])
 # Python disabled delivery reports success.
 from app.services.email import send_email
 q.send_email=send_email
 os.environ.pop('RESEND_API_KEY',None);os.environ.pop('SMTP_HOST',None)
 with engine.begin() as conn: conn.execute(text("UPDATE invoices SET status='DRAFT'"))
 r=client.post('/api/v1/invoices/20000000-0000-0000-0000-000000000001/send',json={})
 result.append([r.status_code,r.json().get('status')])
print(json.dumps(result))`)
	cmd.Dir = "../../../backend"
	b, _ := json.Marshal(bodies)
	cmd.Env = append(os.Environ(), "DB="+py.URL(), "BODIES="+string(b))
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, output)
	}
	var expected [][]any
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e)
	}
	fake := &quotationFakeSender{}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, Sender: fake}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	for i, body := range bodies {
		if _, e = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT'`); e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("POST", "/api/v1/invoices/20000000-0000-0000-0000-000000000001/send", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != int(expected[i][0].(float64)) {
			t.Fatalf("%s: Go %d %s Python %v", body, w.Code, w.Body.String(), expected[i])
		}
		if strings.Contains(body, "TO_EMAIL") || strings.Contains(body, "To_Email") {
			if got := fake.messages[len(fake.messages)-1].To[0]; got != "customer@example.com" {
				t.Fatalf("alias changed recipient: %s", got)
			}
		}
		if w.Code == 422 {
			var actual any
			if e = json.Unmarshal(w.Body.Bytes(), &actual); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(actual, expected[i][1]) {
				t.Fatalf("%s: Go %v Python %v", body, actual, expected[i][1])
			}
		}
		if w.Code == 200 {
			var actual map[string]any
			if e = json.Unmarshal(w.Body.Bytes(), &actual); e != nil {
				t.Fatal(e)
			}
			want := expected[i][1].(map[string]any)
			delete(actual, "created_at")
			delete(want, "created_at")
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("%s: Go %v Python %v", body, actual, want)
			}
		}
	}
	bad := httptest.NewRequest("POST", "/api/v1/invoices/bad/send", strings.NewReader(`{"message":42}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.AddCookie(&http.Cookie{Name: "session", Value: token})
	bw := httptest.NewRecorder()
	h.ServeHTTP(bw, bad)
	var got any
	if e = json.Unmarshal(bw.Body.Bytes(), &got); e != nil {
		t.Fatal(e)
	}
	if bw.Code != int(expected[len(bodies)][0].(float64)) || !reflect.DeepEqual(got, expected[len(bodies)][1]) {
		t.Fatalf("invalid UUID: %d %v vs %v", bw.Code, got, expected[len(bodies)])
	}
	if !reflect.DeepEqual(expected[len(bodies)+1], []any{float64(200), "SENT"}) {
		t.Fatalf("Python disabled: %v", expected[len(bodies)+1])
	}
	fake.err = errors.New("disabled")
	if _, e = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT'`); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/api/v1/invoices/20000000-0000-0000-0000-000000000001/send", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var status string
	if e = p.QueryRow(context.Background(), `SELECT status FROM invoices`).Scan(&status); e != nil || w.Code != 500 || status != "DRAFT" {
		t.Fatalf("Go disabled: %d %s %v", w.Code, status, e)
	}

}
