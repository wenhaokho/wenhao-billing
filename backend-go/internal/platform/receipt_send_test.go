package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	pdf "github.com/ledongthuc/pdf"
	"image"
	"image/color"
	"image/png"
	"io"
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

func TestReceiptSendPostgres(t *testing.T) {
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
	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','admin@example.com','x','admin'); INSERT INTO customers(customer_id,name,contact_email) VALUES ('10000000-0000-0000-0000-000000000001','Customer','customer@example.com'); UPDATE customers SET billing_address1='Structured address',billing_address='Legacy address'; INSERT INTO invoices(invoice_id,customer_id,invoice_number,currency,amount,balance_due,invoice_type,status) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','I-1','USD',100,20,'MILESTONE','PARTIAL'); INSERT INTO payments(payment_id,invoice_id,customer_id,amount,currency,payer_name,payer_reference,payment_date,intake_source,status) VALUES ('abcdef01-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001',1000.5,'IDR','Payer','Bank ref','2026-03-01','MANUAL','CLEARED')`)
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
	path := "/api/v1/invoices/20000000-0000-0000-0000-000000000001/payments/abcdef01-0000-0000-0000-000000000001/send-receipt"
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// Compare request validation and successful response against the Python router.
	bodies := []string{`{}`, `{"to_email":"other@example.com"}`, `{"cc_email":"bad"}`, `{"to_email":42}`, `[]`}
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
import app.api.v1.routers.invoices as q
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI();app.include_router(q.router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
q.render_receipt_pdf=lambda *a:b'%PDF-fake'
q.send_email=lambda **kw:None
with TestClient(app,raise_server_exceptions=False) as client:
 print(json.dumps([[r.status_code,r.json()] for r in (client.post('/api/v1/invoices/20000000-0000-0000-0000-000000000001/payments/abcdef01-0000-0000-0000-000000000001/send-receipt',content=b,headers={'content-type':'application/json'}) for b in json.loads(os.environ['BODIES']))]))`)
	encoded, _ := json.Marshal(bodies)
	cmd.Env = append(os.Environ(), "DB="+d.URL(), "BODIES="+string(encoded))
	cmd.Dir = "../../../backend"
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v", e)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	for i, body := range bodies {
		w := call(body)
		var actual any
		if e = json.Unmarshal(w.Body.Bytes(), &actual); e != nil {
			t.Fatal(e)
		}
		if w.Code != int(expected[i][0].(float64)) || !reflect.DeepEqual(actual, expected[i][1]) {
			t.Fatalf("%s: go %d %v python %v", body, w.Code, actual, expected[i])
		}
	}
	// FastAPI authenticates before treating non-JSON text as an invalid model.
	r := httptest.NewRequest("POST", path, strings.NewReader("not json"))
	r.Header.Set("Content-Type", "text/plain")
	wNoAuth := httptest.NewRecorder()
	h.ServeHTTP(wNoAuth, r)
	if wNoAuth.Code != 401 {
		t.Fatalf("unauthenticated text: %d %s", wNoAuth.Code, wNoAuth.Body.String())
	}
	r = httptest.NewRequest("POST", path, strings.NewReader("not json"))
	r.Header.Set("Content-Type", "text/plain")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	wText := httptest.NewRecorder()
	h.ServeHTTP(wText, r)
	if wText.Code != 422 || !strings.Contains(wText.Body.String(), "model_attributes_type") {
		t.Fatalf("text: %d %s", wText.Code, wText.Body.String())
	}
	// Invalid persisted financial values and dates must never reach the provider.
	for _, tc := range []struct{ update, restore string }{
		{"UPDATE payments SET amount='NaN'", "UPDATE payments SET amount=1000.5"},
		{"UPDATE invoices SET amount='NaN'", "UPDATE invoices SET amount=100"},
		{"UPDATE invoices SET balance_due='NaN'", "UPDATE invoices SET balance_due=20"},
		{"UPDATE payments SET payment_date='infinity'", "UPDATE payments SET payment_date='2026-03-01'"},
		{"UPDATE payments SET payment_date='-infinity'", "UPDATE payments SET payment_date='2026-03-01'"},
		{"UPDATE payments SET payment_date='10000-01-01'", "UPDATE payments SET payment_date='2026-03-01'"},
	} {
		if _, e = p.Exec(context.Background(), tc.update); e != nil {
			t.Fatal(e)
		}
		before := len(sender.messages)
		if w := call(`{}`); w.Code != 500 || len(sender.messages) != before {
			t.Fatalf("%s: %d %s", tc.update, w.Code, w.Body.String())
		}
		if _, e = p.Exec(context.Background(), tc.restore); e != nil {
			t.Fatal(e)
		}
	}
	sender.err = errors.New("failed")
	if w := call(`{}`); w.Code != 502 {
		t.Fatalf("provider: %d %s", w.Code, w.Body.String())
	}
	sender.err = nil
	w := call(`{"cc_email":"cc@example.com"}`)
	if w.Code != 200 {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	m := sender.messages[len(sender.messages)-1]
	if m.To[0] != "customer@example.com" || m.Subject != "Payment receipt for invoice I-1" || !strings.Contains(m.Text, "1,000 IDR") || !strings.Contains(m.Text, "Remaining balance: 20.00 USD.") || m.Attachments[0].Filename != "receipt-RCT-ABCDEF01.pdf" {
		t.Fatalf("message %+v", m)
	}
	inspectPDF := func(content []byte) (string, bool) {
		t.Helper()
		reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			t.Fatal(err)
		}
		textReader, err := reader.GetPlainText()
		if err != nil {
			t.Fatal(err)
		}
		text, err := io.ReadAll(textReader)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for i := 1; i <= reader.NumPage(); i++ {
			objects := reader.Page(i).Resources().Key("XObject")
			for _, key := range objects.Keys() {
				obj := objects.Key(key)
				if obj.Key("Subtype").Name() != "Image" || obj.Key("Width").Int64() != 7 || obj.Key("Height").Int64() != 5 {
					continue
				}
				found = true
			}
		}
		return string(text), found
	}

	pdfText, _ := inspectPDF(m.Attachments[0].Content)
	for _, v := range []string{"RCT-ABCDEF01", "1,000 IDR", "100.00 USD", "20.00 USD", "Bank ref", "Structured address"} {
		if !strings.Contains(pdfText, v) {
			t.Fatalf("PDF missing %q: %s", v, pdfText)
		}
	}
	if strings.Contains(pdfText, "Legacy address") {
		t.Fatalf("unexpected legacy address: %s", pdfText)
	}
	if _, e = p.Exec(context.Background(), `UPDATE customers SET billing_address1=NULL`); e != nil {
		t.Fatal(e)
	}
	if w = call(`{}`); w.Code != 200 {
		t.Fatalf("legacy send: %d %s", w.Code, w.Body.String())
	}
	pdfText, _ = inspectPDF(sender.messages[len(sender.messages)-1].Attachments[0].Content)
	if !strings.Contains(pdfText, "Legacy address") || strings.Contains(pdfText, "Structured address") {
		t.Fatalf("legacy fallback: %s", pdfText)
	}
	img := image.NewRGBA(image.Rect(0, 0, 7, 5))
	for y := 0; y < 5; y++ {
		for x := 0; x < 7; x++ {
			img.Set(x, y, color.RGBA{R: 231, G: 42, B: 17, A: 255})
		}
	}
	var data bytes.Buffer
	if e = png.Encode(&data, img); e != nil {
		t.Fatal(e)
	}
	logo := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())
	if _, e = p.Exec(context.Background(), `INSERT INTO business_profile(id,logo_url) VALUES (1,$1) ON CONFLICT (id) DO UPDATE SET logo_url=EXCLUDED.logo_url`, logo); e != nil {
		t.Fatal(e)
	}
	if w = call(`{}`); w.Code != 200 {
		t.Fatalf("logo send: %d %s", w.Code, w.Body.String())
	}
	_, found := inspectPDF(sender.messages[len(sender.messages)-1].Attachments[0].Content)
	if !found {
		t.Fatal("configured logo dimensions not found in receipt PDF")
	}
	t.Log("verified receipt text, address fallback, and configured logo dimensions with Go PDF parser")

	sender.entered = make(chan struct{})
	sender.release = make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(`{}`) }()
	<-sender.entered
	updated := make(chan error, 1)
	go func() { _, err := p.Exec(context.Background(), `UPDATE payments SET amount=2`); updated <- err }()
	select {
	case <-updated:
		t.Fatal("payment lock bypassed")
	case <-time.After(100 * time.Millisecond):

	}
	close(sender.release)
	if w = <-done; w.Code != 200 {
		t.Fatalf("send during lock: %d", w.Code)
	}
	if e = <-updated; e != nil {
		t.Fatal(e)
	}
}
