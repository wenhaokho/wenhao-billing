package platform

import (
	"context"
	"encoding/json"
	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
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

type quotationFakeSender struct {
	mu               sync.Mutex
	once             sync.Once
	messages         []email.Message
	err              error
	entered, release chan struct{}
	afterSend        func()
}

func (s *quotationFakeSender) Send(ctx context.Context, m email.Message) error {
	s.mu.Lock()
	s.messages = append(s.messages, m)
	s.mu.Unlock()
	if s.entered != nil {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.afterSend != nil {
		s.afterSend()
	}
	return s.err
}
func TestQuotationSendPostgres(t *testing.T) {
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
	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','user@example.com','x','user'),('00000000-0000-0000-0000-000000000003','admin2@example.com','x','admin'); INSERT INTO customers(customer_id,name,contact_email) VALUES ('10000000-0000-0000-0000-000000000001','Customer','customer@example.com'); INSERT INTO quotations(quotation_id,customer_id,quotation_number,currency,subtotal,amount,status) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','Q1','USD',10,10,'DRAFT')`)
	if e != nil {
		t.Fatal(e)
	}
	sender := &quotationFakeSender{}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, Sender: sender}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	cookie := func(id string) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	admin := cookie("00000000-0000-0000-0000-000000000001")
	user := cookie("00000000-0000-0000-0000-000000000002")
	path := "/api/v1/quotations/20000000-0000-0000-0000-000000000001/send"
	call := func(path, body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		path, body string
		c          *http.Cookie
		want       int
	}{{path, "{}", nil, 401}, {path, "{}", user, 403}, {"/api/v1/quotations/bad/send", "{}", admin, 422}, {"/api/v1/quotations/20000000-0000-0000-0000-000000000099/send", "{}", admin, 404}, {path, `{"to_email":"invalid"}`, admin, 400}, {path, `{"subject":"bad\nheader"}`, admin, 400}} {
		w := call(tc.path, tc.body, tc.c)
		if w.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	for _, column := range []string{"subtotal='NaN'", "created_at='infinity'", "issue_date='infinity'", "issue_date='-infinity'", "issue_date='0001-01-01 BC'", "issue_date='10000-01-01'", "valid_until='infinity'", "valid_until='-infinity'", "valid_until='0001-01-01 BC'", "valid_until='10000-01-01'"} {
		if _, e = p.Exec(context.Background(), `UPDATE quotations SET `+column); e != nil {
			t.Fatal(e)
		}
		before := len(sender.messages)
		w := call(path, "{}", admin)
		if w.Code != 500 || len(sender.messages) != before {
			t.Fatalf("corrupt %s: %d sends %d", column, w.Code, len(sender.messages)-before)
		}
		var unchanged bool
		if e = p.QueryRow(context.Background(), `SELECT status='DRAFT' AND last_sent_at IS NULL FROM quotations`).Scan(&unchanged); e != nil || !unchanged {
			t.Fatalf("corrupt %s changed send state: %v %v", column, unchanged, e)
		}
		if _, e = p.Exec(context.Background(), `UPDATE quotations SET subtotal=10, created_at=now(), issue_date=NULL, valid_until=NULL`); e != nil {
			t.Fatal(e)
		}
	}
	sender.err = email.ErrDisabled
	w := call(path, "{}", admin)
	if w.Code != 500 {
		t.Fatalf("disabled: %d %s", w.Code, w.Body.String())
	}
	var status string
	var sent *time.Time
	e = p.QueryRow(context.Background(), `SELECT status,last_sent_at FROM quotations`).Scan(&status, &sent)
	if e != nil || status != "DRAFT" || sent != nil {
		t.Fatalf("failure changed state: %s %v %v", status, sent, e)
	}
	sender.err = email.ErrDelivery
	w = call(path, "{}", admin)
	if w.Code != 500 {
		t.Fatalf("delivery failure: %d", w.Code)
	}
	sender.err = nil
	w = call(path, `{"cc_email":"a@example.com; b@example.com", "message":"Hello"}`, admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"SENT"`) {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	m := sender.messages[len(sender.messages)-1]
	if m.To[0] != "customer@example.com" || len(m.CC) != 2 || m.Text != "Hello" || len(m.Attachments) != 1 || !strings.HasPrefix(string(m.Attachments[0].Content), "%PDF-") {
		t.Fatalf("bad message: %+v", m)
	}
	// A disconnected client after acceptance must not cancel finalization.
	if _, e = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT',last_sent_at=NULL`); e != nil {
		t.Fatal(e)
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	var acceptedAt time.Time
	sender.afterSend = func() { acceptedAt = time.Now().UTC(); cancelRequest() }
	req := httptest.NewRequest("POST", path, strings.NewReader("{}")).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	sender.afterSend = nil
	if rec.Code != 200 {
		t.Fatalf("cancelled after acceptance: %d %s", rec.Code, rec.Body.String())
	}
	if e = p.QueryRow(context.Background(), `SELECT status,last_sent_at FROM quotations`).Scan(&status, &sent); e != nil || status != "SENT" || sent == nil || sent.Before(acceptedAt.Truncate(time.Microsecond)) {
		t.Fatalf("accepted delivery not finalized after acceptance: %s %v %v accepted %v", status, sent, e, acceptedAt)
	}
	sender.entered = make(chan struct{})
	sender.release = make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(path, "{}", admin) }()
	<-sender.entered
	second := make(chan *httptest.ResponseRecorder, 1)
	admin2 := cookie("00000000-0000-0000-0000-000000000003")
	go func() { second <- call(path, "{}", admin2) }()
	select {
	case w := <-second:
		t.Fatalf("parallel send was not serialized: %d", w.Code)
	case <-time.After(150 * time.Millisecond):
	}
	edit := make(chan error, 1)
	go func() {
		_, e := p.Exec(context.Background(), `UPDATE quotations SET status='VOID' WHERE quotation_id='20000000-0000-0000-0000-000000000001'`)
		edit <- e
	}()
	select {
	case e := <-edit:
		t.Fatalf("edit was not serialized: %v", e)
	case <-time.After(150 * time.Millisecond):
	}
	beforeAcceptance := time.Now().UTC()
	close(sender.release)
	if w = <-done; w.Code != 200 {
		t.Fatalf("concurrent send: %d %s", w.Code, w.Body.String())
	}
	if w = <-second; w.Code != 200 && w.Code != 400 {
		t.Fatalf("second send: %d %s", w.Code, w.Body.String())
	}
	if e = <-edit; e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(context.Background(), `SELECT last_sent_at FROM quotations`).Scan(&sent); e != nil || sent == nil || sent.Before(beforeAcceptance) {
		t.Fatalf("last_sent_at predates delivery: %v %v", sent, e)
	}
	sender.entered = nil
	sender.release = nil
	if w = call(path, "{}", admin); w.Code != 400 {
		t.Fatalf("void send: %d %s", w.Code, w.Body.String())
	}
}

func TestQuotationSendDifferential(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com','x','admin'); INSERT INTO customers(customer_id,name,contact_email) VALUES ('10000000-0000-0000-0000-000000000001','Customer','customer@example.com'); INSERT INTO quotations(quotation_id,customer_id,quotation_number,currency,subtotal,amount,status) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','Q1','USD',10,10,'DRAFT')`
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
import app.api.v1.routers.quotations as q
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI();app.include_router(q.router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
q.render_quotation_pdf=lambda *args: b'%PDF-fake'
q.send_email=lambda **kwargs: None
result=[]
with TestClient(app,raise_server_exceptions=False) as client:
 for body in json.loads(os.environ['BODIES']):
  with engine.begin() as conn: conn.execute(text("UPDATE quotations SET status='DRAFT',last_sent_at=NULL"))
  r=client.post('/api/v1/quotations/20000000-0000-0000-0000-000000000001/send',content=body,headers={'content-type':'application/json'})
  result.append([r.status_code,r.json()])
 r=client.post('/api/v1/quotations/bad/send',json={'message':42})
 result.append([r.status_code,r.json()])
 # Python disabled delivery returns successfully and records a sent timestamp.
 from app.services.email import send_email
 q.send_email=send_email
 os.environ.pop('RESEND_API_KEY',None);os.environ.pop('SMTP_HOST',None)
 with engine.begin() as conn: conn.execute(text("UPDATE quotations SET status='DRAFT',last_sent_at=NULL"))
 r=client.post('/api/v1/quotations/20000000-0000-0000-0000-000000000001/send',json={})
 result.append([r.status_code,r.json().get('status'),r.json().get('last_sent_at') is not None])
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
		if _, e = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT',last_sent_at=NULL`); e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("POST", "/api/v1/quotations/20000000-0000-0000-0000-000000000001/send", strings.NewReader(body))
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
			delete(actual, "last_sent_at")
			delete(want, "last_sent_at")
			delete(actual, "created_at")
			delete(want, "created_at")
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("%s: Go %v Python %v", body, actual, want)
			}
		}
	}
	bad := httptest.NewRequest("POST", "/api/v1/quotations/bad/send", strings.NewReader(`{"message":42}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.AddCookie(&http.Cookie{Name: "session", Value: token})
	bw := httptest.NewRecorder()
	h.ServeHTTP(bw, bad)
	var actualBad any
	if e = json.Unmarshal(bw.Body.Bytes(), &actualBad); e != nil {
		t.Fatal(e)
	}
	if bw.Code != int(expected[len(bodies)][0].(float64)) || !reflect.DeepEqual(actualBad, expected[len(bodies)][1]) {
		t.Fatalf("path and body errors: Go %d %v Python %v", bw.Code, actualBad, expected[len(bodies)])
	}
	missingType := httptest.NewRequest("POST", "/api/v1/quotations/bad/send", strings.NewReader(`{"message":42}`))
	missingType.AddCookie(&http.Cookie{Name: "session", Value: token})
	mw := httptest.NewRecorder()
	h.ServeHTTP(mw, missingType)
	if mw.Code != 422 || !strings.Contains(mw.Body.String(), "model_attributes_type") || !strings.Contains(mw.Body.String(), "[REDACTED]") {
		t.Fatalf("missing content type: %d %s", mw.Code, mw.Body.String())
	}
	if expected[len(bodies)+1][0] != float64(200) || expected[len(bodies)+1][1] != "SENT" || expected[len(bodies)+1][2] != true {
		t.Fatalf("unexpected Python disabled behavior: %v", expected[len(bodies)+1])
	}
}
