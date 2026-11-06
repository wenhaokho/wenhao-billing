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
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInvoiceUpdateDraftPostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	d := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), d.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','update@example.com','x','admin'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'DRAFT'); INSERT INTO invoice_line_items(line_item_id,invoice_id,description,quantity,unit_price,amount,position) VALUES ('40000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','Service',2,5,10,0)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,notes) VALUES ('30000000-0000-0000-0000-000000000002','MILESTONE','USD',42,42,'DRAFT','untouched'); INSERT INTO invoice_line_items(line_item_id,invoice_id,description,quantity,unit_price,amount,position) VALUES ('40000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000002','unrelated',2,21,42,0); INSERT INTO payments(payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status) VALUES ('50000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000002',1,'USD','payer','2026-01-01','MANUAL','CLEARED'); INSERT INTO journal_entries(entry_id,source_type,source_id,memo) VALUES ('60000000-0000-0000-0000-000000000001','PAYMENT','50000000-0000-0000-0000-000000000001','untouched')`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		err := p.QueryRow(context.Background(), `SELECT json_build_array((SELECT row_to_json(i) FROM invoices i WHERE invoice_id='30000000-0000-0000-0000-000000000002'),(SELECT json_agg(row_to_json(l) ORDER BY line_item_id) FROM invoice_line_items l WHERE invoice_id='30000000-0000-0000-0000-000000000002'),(SELECT json_agg(row_to_json(p) ORDER BY payment_id) FROM payments p),(SELECT json_agg(row_to_json(j) ORDER BY entry_id) FROM journal_entries j),(SELECT json_agg(row_to_json(j) ORDER BY line_id) FROM journal_lines j))::text`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	originalSnapshot := snapshot()
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PATCH", "/api/v1/invoices/30000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"updated"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"notes":"updated"`) || !strings.Contains(out.Body.String(), `"amount":"10.0000"`) {
		t.Fatalf("update draft: %d %s", out.Code, out.Body)
	}
	if got := snapshot(); got != originalSnapshot {
		t.Fatalf("unrelated rows changed after update: %s", got)
	}
	request := func(tok, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "/api/v1/invoices/30000000-0000-0000-0000-000000000001", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if tok != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: tok})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, body := range []string{`{"notes":`, `{} trailing-secret`} {
		if w := request(token, body); w.Code != 422 || strings.Contains(w.Body.String(), "trailing-secret") {
			t.Errorf("malformed body: %d %s", w.Code, w.Body)
		}
	}
	media := httptest.NewRequest("PATCH", "/api/v1/invoices/30000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"private-value"}`))
	media.Header.Set("Content-Type", "text/plain")
	media.AddCookie(&http.Cookie{Name: "session", Value: token})
	mediaOut := httptest.NewRecorder()
	h.ServeHTTP(mediaOut, media)
	if mediaOut.Code != 422 || strings.Contains(mediaOut.Body.String(), "private-value") {
		t.Errorf("media: %d %s", mediaOut.Code, mediaOut.Body)
	}
	if w := request("", `{}`); w.Code != 401 || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("unauth: %d", w.Code)
	}
	if w := request(token, `{"line_items":[{"description":"overflow","quantity":1,"unit_price":1,"position":2147483648}]}`); w.Code != 500 {
		t.Errorf("position overflow: %d %s", w.Code, w.Body)
	}
	if w := request(token, `{"notes":123,"secret":"private-value"}`); w.Code != 422 || strings.Contains(w.Body.String(), "private-value") {
		t.Errorf("validation: %d %s", w.Code, w.Body)
	}
	if w := request(token, strings.Repeat("x", (1<<20)+1)); w.Code != 413 {
		t.Errorf("large body: %d", w.Code)
	}
	doc := httptest.NewRecorder()
	h.ServeHTTP(doc, httptest.NewRequest("GET", "/openapi.json", nil))
	var spec map[string]any
	if err = json.Unmarshal(doc.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if _, ok := spec["paths"].(map[string]any)["/api/v1/invoices/{invoice_id}"].(map[string]any)["patch"]; !ok {
		t.Error("missing PATCH schema")
	}
	for _, status := range []string{"OPEN", "PARTIAL", "PAID", "VOID"} {
		if _, err = p.Exec(context.Background(), `UPDATE invoices SET status=$1 WHERE invoice_id='30000000-0000-0000-0000-000000000001'`, status); err != nil {
			t.Fatal(err)
		}
		if w := request(token, `{"notes":"bad"}`); w.Code != 400 || !strings.Contains(w.Body.String(), status) {
			t.Errorf("status %s: %d %s", status, w.Code, w.Body)
		}
	}
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT',created_at='infinity' WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if w := request(token, `{"notes":"must rollback"}`); w.Code != 500 {
		t.Errorf("invalid response: %d", w.Code)
	}
	var note string
	if err = p.QueryRow(context.Background(), `SELECT notes FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&note); err != nil || note != "updated" {
		t.Fatalf("serialization rollback: %q %v", note, err)
	}
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET created_at='2026-01-01' WHERE invoice_id='30000000-0000-0000-0000-000000000001'; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','second@example.com','x','admin')`); err != nil {
		t.Fatal(err)
	}
	token2, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, x := range []struct{ tok, body string }{{token, `{"notes":"concurrent"}`}, {token2, `{"footer":"other"}`}} {
		wg.Add(1)
		go func(i int, tok, body string) { defer wg.Done(); codes[i] = request(tok, body).Code }(i, x.tok, x.body)
	}
	wait := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var count int
			if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%' AND pid<>pg_backend_pid()`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count >= n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("only %d requests waiting", count)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait(2)
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("concurrent updates: %v", codes)
	}
	var footer string
	if err = p.QueryRow(context.Background(), `SELECT notes,footer FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&note, &footer); err != nil || note != "concurrent" || footer != "other" {
		t.Fatalf("lost update: %s %s %v", note, footer, err)
	}
	tx, err = p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() { result <- request(token2, `{"notes":"must not overwrite"}`).Code }()
	wait(1)
	if _, err = tx.Exec(context.Background(), `UPDATE invoices SET status='PARTIAL',balance_due=3 WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 400 {
		t.Fatalf("state race: %d", code)
	}
	var status, balance string
	if err = p.QueryRow(context.Background(), `SELECT status,balance_due::text,notes FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001'`).Scan(&status, &balance, &note); err != nil || status != "PARTIAL" || balance != "3.0000" || note != "concurrent" {
		t.Fatalf("state race changed invoice: %s %s %s %v", status, balance, note, err)
	}

	if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='DRAFT' WHERE invoice_id='30000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if w := request(token, `{"notes":"bad fk","line_items":[{"item_id":"99999999-9999-9999-9999-999999999999","description":"bad","quantity":1,"unit_price":1}]}`); w.Code != 500 {
		t.Fatalf("FK failure: %d %s", w.Code, w.Body)
	}
	var lines, journals, payments int
	var other string
	if err = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM invoice_line_items WHERE invoice_id='30000000-0000-0000-0000-000000000001'),(SELECT count(*) FROM journal_entries),(SELECT count(*) FROM payments),(SELECT notes FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000002'),(SELECT notes FROM invoices WHERE invoice_id='30000000-0000-0000-0000-000000000001')`).Scan(&lines, &journals, &payments, &other, &note); err != nil || lines != 1 || journals != 1 || payments != 1 || other != "untouched" || note != "concurrent" {
		t.Fatalf("FK rollback or unrelated rows: %d %d %d %q %q %v", lines, journals, payments, other, note, err)
	}
	if got := snapshot(); got != originalSnapshot {
		t.Fatalf("unrelated rows changed after failed update: %s", got)
	}
	tx, err = p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	demotion := make(chan int, 1)
	go func() { demotion <- request(token2, `{"notes":"demoted"}`).Code }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err = p.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM users%' AND pid<>pg_backend_pid())`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("demotion did not block")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-demotion; code != 403 {
		t.Fatalf("demoted request: %d", code)
	}
	missing := httptest.NewRequest("PATCH", "/api/v1/invoices/99999999-9999-9999-9999-999999999999", strings.NewReader(`{}`))
	missing.Header.Set("Content-Type", "application/json")
	missing.AddCookie(&http.Cookie{Name: "session", Value: token})
	missingOut := httptest.NewRecorder()
	h.ServeHTTP(missingOut, missing)
	if missingOut.Code != 400 || !strings.Contains(missingOut.Body.String(), "not found") {
		t.Errorf("missing invoice: %d %s", missingOut.Code, missingOut.Body)
	}

}
