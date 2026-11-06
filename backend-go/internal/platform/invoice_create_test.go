package platform

import (
	"context"
	"fmt"
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
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInvoiceCreateDraftPostgres(t *testing.T) {
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
	r := httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(`{"currency":"USD","line_items":[{"description":"Service","quantity":"2","unit_price":"3.2500"}]}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"amount":"6.5000"`) {
		t.Fatalf("create draft: %d %s", w.Code, w.Body.String())
	}
	prefix := fmt.Sprintf("WH%d", time.Now().Year())
	if _, e = p.Exec(context.Background(), `UPDATE invoices SET invoice_number=$1 WHERE invoice_id='20000000-0000-0000-0000-000000000001'`, prefix+"+99"); e != nil {
		t.Fatal(e)
	}
	numberReq := httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(`{"currency":"USD","line_items":[{"description":"number","quantity":1,"unit_price":1}]}`))
	numberReq.Header.Set("Content-Type", "application/json")
	numberReq.AddCookie(&http.Cookie{Name: "session", Value: token})
	numberOut := httptest.NewRecorder()
	h.ServeHTTP(numberOut, numberReq)
	if numberOut.Code != 201 || !strings.Contains(numberOut.Body.String(), `"invoice_number":"`+prefix+`0002"`) {
		t.Errorf("number suffix: %d %s", numberOut.Code, numberOut.Body)
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"currency":"USD","wht_type":"PPH23","wht_rate":"-1","line_items":[{"description":"x","quantity":1,"unit_price":1}]}`, 400},
		{`{"currency":"USD","line_items":null}`, 422},
		{`{"currency":"USD","invoice_type":null,"line_items":[{"description":"x","quantity":1,"unit_price":1}]}`, 422},
	} {
		req := httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != tc.status {
			t.Errorf("body %s: got %d want %d: %s", tc.body, out.Code, tc.status, out.Body)
		}
	}

	if _, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`); e != nil {
		t.Fatal(e)
	}
	token2, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	failures := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(`{"currency":"USD","line_items":[{"description":"concurrent","quantity":1,"unit_price":1}]}`))
			req.Header.Set("Content-Type", "application/json")
			v := token
			if i%2 == 0 {
				v = token2
			}
			req.AddCookie(&http.Cookie{Name: "session", Value: v})
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			if out.Code != 201 {
				failures <- fmt.Sprintf("%d %s", out.Code, out.Body)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
	var count, distinct int
	if e = p.QueryRow(context.Background(), `SELECT count(*),count(DISTINCT invoice_number) FROM invoices WHERE invoice_number IS NOT NULL`).Scan(&count, &distinct); e != nil || count != distinct {
		t.Fatalf("number collision count=%d distinct=%d err=%v", count, distinct, e)
	}
	var journals int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM journal_entries`).Scan(&journals); e != nil || journals != 0 {
		t.Fatalf("draft creation posted ledger entries: %d %v", journals, e)
	}
	var beforeInvoices, beforeLines, afterInvoices, afterLines int
	if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM invoices),(SELECT count(*) FROM invoice_line_items)`).Scan(&beforeInvoices, &beforeLines); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(`{"currency":"USD","line_items":[{"description":"valid","quantity":1,"unit_price":1},{"item_id":"99999999-9999-9999-9999-999999999999","description":"bad FK","quantity":1,"unit_price":1}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 500 {
		t.Errorf("line FK: %d %s", out.Code, out.Body)
	}
	if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM invoices),(SELECT count(*) FROM invoice_line_items)`).Scan(&afterInvoices, &afterLines); e != nil || beforeInvoices != afterInvoices || beforeLines != afterLines {
		t.Fatalf("rollback before=%d/%d after=%d/%d err=%v", beforeInvoices, beforeLines, afterInvoices, afterLines, e)
	}

	for _, tc := range []struct {
		body, ctype string
		code        int
	}{{strings.Repeat("x", 2<<20), "application/json", 413}, {`{"currency":"USD","line_items":[]}`, "text/plain", 422}, {`{"currency":"USD","line_items":[]}`, "application/json", 400}} {
		req := httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.ctype)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != tc.code || out.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("media/limit got %d want %d", out.Code, tc.code)
		}
	}
	if _, e = p.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	out = httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 403 {
		t.Errorf("demotion: %d %s", out.Code, out.Body)
	}

	if _, e = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	demote, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer demote.Rollback(context.Background())
	if _, e = demote.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(`{"currency":"USD","line_items":[{"description":"race","quantity":1,"unit_price":1}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		result <- out
	}()
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		_ = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%LockSessionUserByID%'`).Scan(&n)
		if n > 0 {
			blocked = true
			break
		}
		select {
		case early := <-result:
			t.Errorf("create completed before demotion committed: %d", early.Code)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Error("admin lock was not observed")
	}
	if e = demote.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case res := <-result:
		if res.Code != 403 {
			t.Errorf("concurrent demotion: %d %s", res.Code, res.Body)
		}
	case <-time.After(3 * time.Second):
		t.Error("request did not finish")
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	if spec.Code != 200 || !strings.Contains(spec.Body.String(), `"create-invoice"`) || !strings.Contains(spec.Body.String(), `"InvoiceCreateSchema"`) {
		t.Errorf("openapi: %d", spec.Code)
	}

}
