package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

func TestQuotationUpdatePostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO quotations(quotation_id,customer_id,currency,amount,status) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','USD',0,'DRAFT')`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"updated","discount_type":"PERCENT","discount_value":"10","line_items":[{"description":"Work","quantity":"2","unit_price":"3.25"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"amount":"5.8500"`) || !strings.Contains(w.Body.String(), `"notes":"updated"`) {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	for _, status := range []string{"DRAFT", "SENT", "ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET status=$1 WHERE quotation_id='20000000-0000-0000-0000-000000000001'`, status); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"status edit"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		expected := 400
		if status == "DRAFT" || status == "SENT" {
			expected = 200
		}
		if w.Code != expected {
			t.Errorf("status %s: %d %s", status, w.Code, w.Body)
		}
	}

	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT' WHERE quotation_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	overflow := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":2147483648}]}`))
	overflow.Header.Set("Content-Type", "application/json")
	overflow.AddCookie(&http.Cookie{Name: "session", Value: token})
	ow := httptest.NewRecorder()
	h.ServeHTTP(ow, overflow)
	if ow.Code != 500 || ow.Body.String() != "Internal Server Error" || ow.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("overflow: %d %q %q", ow.Code, ow.Body.String(), ow.Header().Get("Content-Type"))
	}

	// Exercise a near-limit body with many errors to keep validation practical.
	const lineCount = 14000
	line := `{"description":1,"quantity":1,"unit_price":1,"position":"bad"}`
	largeBody := `{"line_items":[` + strings.Repeat(line+",", lineCount-1) + line + `]}`
	largeReq := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(largeBody))
	largeReq.Header.Set("Content-Type", "application/json")
	largeReq.AddCookie(&http.Cookie{Name: "session", Value: token})
	largeResult := httptest.NewRecorder()
	h.ServeHTTP(largeResult, largeReq)
	var largeErrors struct {
		Detail []json.RawMessage `json:"detail"`
	}
	if largeResult.Code != 422 || json.Unmarshal(largeResult.Body.Bytes(), &largeErrors) != nil || len(largeErrors.Detail) != 2*lineCount {
		t.Fatalf("large invalid body: status %d, errors %d", largeResult.Code, len(largeErrors.Detail))
	}

	// Distinct administrators editing distinct quotations acquire item locks in UUID-byte order.
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin'),('00000000-0000-0000-0000-000000000003','c@example.com','x','admin'); INSERT INTO quotations(quotation_id,customer_id,currency,amount,status) VALUES ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','USD',0,'DRAFT'),('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','USD',0,'DRAFT'); INSERT INTO items(item_id,name,item_type) VALUES ('aaaaaaaa-0000-0000-0000-000000000001','first','SERVICE'),('bbbbbbbb-0000-0000-0000-000000000002','second','SERVICE')`)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(context.Background())
	if _, err = hold.Exec(context.Background(), `SELECT item_id FROM items WHERE item_id='aaaaaaaa-0000-0000-0000-000000000001' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	results := make(chan int, 2)
	for i := 2; i <= 3; i++ {
		tok, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": fmt.Sprintf("00000000-0000-0000-0000-%012d", i)})
		if e != nil {
			t.Fatal(e)
		}
		a, b := "aaaaaaaa-0000-0000-0000-000000000001", "BBBBBBBB-0000-0000-0000-000000000002"
		if i == 3 {
			a, b = strings.ToUpper(a), strings.ToLower(b)
		}
		body := fmt.Sprintf(`{"line_items":[{"item_id":"%s","description":"a","quantity":1,"unit_price":1},{"item_id":"%s","description":"b","quantity":1,"unit_price":1},{"item_id":"%s","description":"duplicate","quantity":1,"unit_price":1}]}`, a, b, strings.ToUpper(b))
		url := fmt.Sprintf("/api/v1/quotations/20000000-0000-0000-0000-%012d", i)
		go func() {
			req := httptest.NewRequest("PATCH", url, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: tok})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			results <- w.Code
		}()
		until := time.Now().Add(5 * time.Second)
		for {
			var n int
			if e = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&n); e != nil {
				t.Fatal(e)
			}
			if n >= i-1 {
				break
			}
			if time.Now().After(until) {
				t.Fatal("item locks did not block")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	probe, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, probeErr := probe.Exec(context.Background(), `SELECT item_id FROM items WHERE item_id='bbbbbbbb-0000-0000-0000-000000000002' FOR UPDATE NOWAIT`)
	_ = probe.Rollback(context.Background())
	if probeErr != nil {
		t.Errorf("higher item locked first: %v", probeErr)
	}
	if err = hold.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if code := <-results; code != 200 {
			t.Errorf("item order update: %d", code)
		}
	}

	// Two administrators serialize edits to the same quotation and retain independent changes.
	quoteHold, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer quoteHold.Rollback(context.Background())
	if _, err = quoteHold.Exec(context.Background(), `UPDATE quotations SET status='DRAFT' WHERE quotation_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	updates := make(chan int, 2)
	for i, body := range []string{`{"notes":"admin two"}`, `{"footer":"admin three"}`} {
		tok, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": fmt.Sprintf("00000000-0000-0000-0000-%012d", i+2)})
		if e != nil {
			t.Fatal(e)
		}
		go func() {
			req := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: tok})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			updates <- w.Code
		}()
	}
	waitUntil := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			break
		}
		if time.Now().After(waitUntil) {
			t.Fatal("updates did not both wait for quotation lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = quoteHold.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if code := <-updates; code != 200 {
			t.Fatalf("concurrent update: %d", code)
		}
	}
	var notes, footer string
	if err = p.QueryRow(context.Background(), `SELECT notes,footer FROM quotations WHERE quotation_id='20000000-0000-0000-0000-000000000001'`).Scan(&notes, &footer); err != nil || notes != "admin two" || footer != "admin three" {
		t.Fatalf("lost update: %q %q %v", notes, footer, err)
	}

	// Authorization is rechecked under the persisted user lock after an in-flight demotion.
	demotion, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer demotion.Rollback(context.Background())
	if _, err = demotion.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	demoted := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"demoted"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		demoted <- w.Code
	}()
	deadlineAuth := time.Now().Add(5 * time.Second)
	for {
		var n int
		err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadlineAuth) {
			t.Fatal("demotion did not block update")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = demotion.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-demoted; code != 403 {
		t.Fatalf("demotion: %d", code)
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body, media string
		code        int
	}{{`{"secret":"do-not-leak"`, "application/json", 422}, {`{}`, "text/plain", 422}, {`{"notes":"` + strings.Repeat("x", 1<<20) + `"}`, "application/json", 413}} {
		req := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.media)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "do-not-leak") || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("invalid request: %d %s", w.Code, w.Body)
		}
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(map[string]any)["/api/v1/quotations/{quotation_id}"].(map[string]any)["patch"].(map[string]any)
	if op["security"] == nil {
		t.Error("missing security")
	}
	schema := doc["components"].(map[string]any)["schemas"].(map[string]any)["QuotationUpdateSchema"].(map[string]any)["properties"].(map[string]any)
	if schema["business_id"] != nil || schema["line_items"] == nil || schema["discount_value"] == nil {
		t.Error("incorrect update schema")
	}

	// A status-changing transaction must serialize with edits and have its status rechecked.
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `UPDATE quotations SET status='ACCEPTED' WHERE quotation_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"blocked"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		done <- w.Code
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("update did not wait for quotation lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 400 {
		t.Fatalf("status race: %d", code)
	}
	// Replacing lines must roll back when inserting a new line fails.
	_, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT' WHERE quotation_id='20000000-0000-0000-0000-000000000001'; CREATE FUNCTION fail_quote_line() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$; CREATE TRIGGER fail_quote_line BEFORE INSERT ON quotation_line_items FOR EACH ROW EXECUTE FUNCTION fail_quote_line()`)
	if err != nil {
		t.Fatal(err)
	}
	var before, after string
	err = p.QueryRow(context.Background(), `SELECT row_to_json(q)::text || (SELECT coalesce(json_agg(l)::text,'[]') FROM quotation_line_items l WHERE l.quotation_id=q.quotation_id) FROM quotations q WHERE quotation_id='20000000-0000-0000-0000-000000000001'`).Scan(&before)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"failure","line_items":[{"description":"x","quantity":1,"unit_price":1}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("injected failure: %d %s", w.Code, w.Body)
	}
	err = p.QueryRow(context.Background(), `SELECT row_to_json(q)::text || (SELECT coalesce(json_agg(l)::text,'[]') FROM quotation_line_items l WHERE l.quotation_id=q.quotation_id) FROM quotations q WHERE quotation_id='20000000-0000-0000-0000-000000000001'`).Scan(&after)
	if err != nil || before != after {
		t.Fatalf("rollback: %v before=%s after=%s", err, before, after)
	}

	if _, err = p.Exec(context.Background(), `DROP TRIGGER fail_quote_line ON quotation_line_items; CREATE FUNCTION invalid_quote_update_response() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.amount='NaN'; RETURN NEW; END $$; CREATE TRIGGER invalid_quote_update_response BEFORE UPDATE ON quotations FOR EACH ROW EXECUTE FUNCTION invalid_quote_update_response()`); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var v string
		e := p.QueryRow(context.Background(), `SELECT jsonb_build_object('quotes',(SELECT jsonb_agg(to_jsonb(q) ORDER BY quotation_id) FROM quotations q),'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM quotation_line_items l),'invoices',(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i),'ledger',(SELECT jsonb_agg(to_jsonb(j)) FROM journal_entries j),'journal_lines',(SELECT jsonb_agg(to_jsonb(l)) FROM journal_lines l))::text`).Scan(&v)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	saved := snapshot()
	req = httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"response failure","line_items":[{"description":"x","quantity":2,"unit_price":3}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("response failure: %d %s", w.Code, w.Body)
	}
	if saved != snapshot() {
		t.Fatal("response conversion failure changed persisted rows")
	}

}
