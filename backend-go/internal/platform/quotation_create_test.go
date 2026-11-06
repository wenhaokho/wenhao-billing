package platform

import (
	"context"
	"encoding/json"
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

func TestQuotationCreateDraftPostgres(t *testing.T) {
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
	r := httptest.NewRequest("POST", "/api/v1/quotations", strings.NewReader(`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"Service","quantity":"2","unit_price":"3.2500"}]}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"amount":"6.5000"`) {
		t.Fatalf("create draft: %d %s", w.Code, w.Body.String())
	}
	request := func(payload, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/quotations", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	payload := `{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1}]}`
	if out := request(payload, ""); out.Code != 401 {
		t.Fatalf("unauthenticated: %d", out.Code)
	}

	prefix := fmt.Sprintf("Q%d", time.Now().Year())
	if _, e = p.Exec(context.Background(), `INSERT INTO quotations(quotation_id,customer_id,quotation_number,currency,amount,status) VALUES ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001',$1,'USD',0,'DRAFT'),('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001',$2,'USD',0,'DRAFT')`, prefix+"0009", prefix+"+99"); e != nil {
		t.Fatal(e)
	}
	if out := request(payload, token); out.Code != 201 || !strings.Contains(out.Body.String(), `"quotation_number":"`+prefix+`0010"`) {
		t.Fatalf("number scan: %d %s", out.Code, out.Body)
	}
	var wg sync.WaitGroup
	failures := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := request(payload, token)
			if out.Code != 201 {
				failures <- fmt.Sprintf("%d %s", out.Code, out.Body)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}

	// Two distinct administrators and customers must both wait at the allocator.
	if _, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin'),('00000000-0000-0000-0000-000000000003','c@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000002','B'),('10000000-0000-0000-0000-000000000003','C')`); e != nil {
		t.Fatal(e)
	}
	hold, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer hold.Rollback(context.Background())
	if _, e = hold.Exec(context.Background(), `SELECT pg_advisory_xact_lock(472691842002::bigint)`); e != nil {
		t.Fatal(e)
	}
	blocked := make(chan int, 2)
	for i := 2; i <= 3; i++ {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		tok, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Replace(payload, "10000000-0000-0000-0000-000000000001", fmt.Sprintf("10000000-0000-0000-0000-%012d", i), 1)
		go func() { blocked <- request(body, tok).Code }()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		e = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory'`).Scan(&waiting)
		if e != nil {
			t.Fatal(e)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("number allocation did not block both requests: %d", waiting)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = hold.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if code := <-blocked; code != 201 {
			t.Fatalf("blocked create: %d", code)
		}
	}

	if _, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000004','d@example.com','x','admin')`); e != nil {
		t.Fatal(e)
	}
	demotion, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer demotion.Rollback(context.Background())
	if _, e = demotion.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000004'`); e != nil {
		t.Fatal(e)
	}
	demotedToken, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000004"})
	if e != nil {
		t.Fatal(e)
	}
	demotedResult := make(chan int, 1)
	go func() { demotedResult <- request(payload, demotedToken).Code }()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting int
		e = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='transactionid'`).Scan(&waiting)
		if e != nil {
			t.Fatal(e)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("create did not wait for demotion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = demotion.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if code := <-demotedResult; code != 403 {
		t.Fatalf("concurrent demotion: %d", code)
	}

	// Alternate UUID spellings must converge to one bytewise item lock order.
	if _, e = p.Exec(context.Background(), `INSERT INTO items(item_id,name,item_type) VALUES ('aaaaaaaa-0000-0000-0000-000000000001','first','SERVICE'),('bbbbbbbb-0000-0000-0000-000000000002','second','SERVICE')`); e != nil {
		t.Fatal(e)
	}
	itemHold, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer itemHold.Rollback(context.Background())
	if _, e = itemHold.Exec(context.Background(), `SELECT item_id FROM items WHERE item_id='aaaaaaaa-0000-0000-0000-000000000001' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	itemResults := make(chan int, 2)
	for i := 2; i <= 3; i++ {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		tok, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if err != nil {
			t.Fatal(err)
		}
		a, b := "aaaaaaaa-0000-0000-0000-000000000001", "BBBBBBBB-0000-0000-0000-000000000002"
		if i == 3 {
			a, b = "AAAAAAAA-0000-0000-0000-000000000001", "bbbbbbbb-0000-0000-0000-000000000002"
		}
		body := fmt.Sprintf(`{"customer_id":"10000000-0000-0000-0000-%012d","currency":"USD","line_items":[{"item_id":"%s","description":"a","quantity":1,"unit_price":1},{"item_id":"%s","description":"b","quantity":1,"unit_price":1},{"item_id":"%s","description":"duplicate","quantity":1,"unit_price":1}]}`, i, a, b, strings.ToUpper(b))
		go func() { itemResults <- request(body, tok).Code }()
		if i == 2 {
			until := time.Now().Add(5 * time.Second)
			for {
				var waiting int
				if e = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&waiting); e != nil {
					t.Fatal(e)
				}
				if waiting >= 1 {
					break
				}
				if time.Now().After(until) {
					t.Fatal("first item lock did not block")
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting int
		e = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&waiting)
		if e != nil {
			t.Fatal(e)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("item locks did not block both requests: %d", waiting)
		}
		time.Sleep(10 * time.Millisecond)
	}
	probe, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	_, probeErr := probe.Exec(context.Background(), `SELECT item_id FROM items WHERE item_id='bbbbbbbb-0000-0000-0000-000000000002' FOR UPDATE NOWAIT`)
	_ = probe.Rollback(context.Background())
	if probeErr != nil {
		t.Errorf("higher item locked before lower item: %v", probeErr)
	}
	if e = itemHold.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if code := <-itemResults; code != 201 {
			t.Errorf("item-order create: %d", code)
		}
	}
	var count, distinct int
	if e = p.QueryRow(context.Background(), `SELECT count(*),count(DISTINCT quotation_number) FROM quotations`).Scan(&count, &distinct); e != nil || count != distinct {
		t.Fatalf("number uniqueness: %d %d %v", count, distinct, e)
	}
	snapshot := func() string {
		var value string
		e := p.QueryRow(context.Background(), `SELECT jsonb_build_object('quotes',(SELECT jsonb_agg(to_jsonb(q) ORDER BY quotation_id) FROM quotations q),'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM quotation_line_items l),'invoices',(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i),'ledger',(SELECT jsonb_agg(to_jsonb(j)) FROM journal_entries j),'journal_lines',(SELECT jsonb_agg(to_jsonb(l)) FROM journal_lines l))::text`).Scan(&value)
		if e != nil {
			t.Fatal(e)
		}
		return value
	}
	before := snapshot()
	bad := `{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"ok","quantity":1,"unit_price":1},{"item_id":"99999999-9999-9999-9999-999999999999","description":"bad","quantity":1,"unit_price":1}]}`
	if out := request(bad, token); out.Code != 500 {
		t.Errorf("bad reference: %d %s", out.Code, out.Body)
	}
	if after := snapshot(); before != after {
		t.Fatal("failed insert changed persisted rows")
	}
	if _, e = p.Exec(context.Background(), `CREATE FUNCTION fail_quotation_line() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$; CREATE TRIGGER fail_quotation_line BEFORE INSERT ON quotation_line_items FOR EACH ROW EXECUTE FUNCTION fail_quotation_line()`); e != nil {
		t.Fatal(e)
	}
	if out := request(payload, token); out.Code != 500 {
		t.Errorf("trigger failure: %d", out.Code)
	}
	if after := snapshot(); before != after {
		t.Fatal("trigger failure changed persisted rows")
	}
	if _, e = p.Exec(context.Background(), `DROP TRIGGER fail_quotation_line ON quotation_line_items`); e != nil {
		t.Fatal(e)
	}

	for _, tc := range []struct {
		body, media string
		code        int
	}{{`{"secret":"do-not-leak"`, "application/json", 422}, {payload, "text/plain", 422}, {`{"customer_id":"bad","currency":"USD","notes":"do-not-leak"}`, "application/json", 422}} {
		req := httptest.NewRequest("POST", "/api/v1/quotations", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.media)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != tc.code || strings.Contains(out.Body.String(), "do-not-leak") || out.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("invalid request: %d %s", out.Code, out.Body)
		}
	}

	oversized := request(`{"notes":"`+strings.Repeat("x", 1<<20)+`"}`, token)
	if oversized.Code != 413 || oversized.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("oversized body: %d", oversized.Code)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	op := doc["paths"].(map[string]any)["/api/v1/quotations"].(map[string]any)["post"].(map[string]any)
	if op["security"] == nil {
		t.Error("missing security")
	}
	responses := op["responses"].(map[string]any)
	for _, code := range []string{"201", "400", "401", "403", "413", "422", "500"} {
		if responses[code] == nil {
			t.Errorf("missing response %s", code)
		}
	}

	if responses["500"].(map[string]any)["content"].(map[string]any)["text/plain"] == nil {
		t.Error("missing text/plain 500 schema")
	}
	schema := doc["components"].(map[string]any)["schemas"].(map[string]any)["QuotationCreateSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	for _, name := range []string{"customer_id", "currency", "quotation_number", "valid_until", "line_items", "discount_value"} {
		if props[name] == nil {
			t.Errorf("missing property %s", name)
		}
	}
	if props["business_id"] != nil {
		t.Error("unexpected business_id")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("missing no-store")
	}
	if _, e = p.Exec(context.Background(), `CREATE FUNCTION invalid_quotation_response() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.amount='NaN'; RETURN NEW; END $$; CREATE TRIGGER invalid_quotation_response BEFORE INSERT ON quotations FOR EACH ROW EXECUTE FUNCTION invalid_quotation_response()`); e != nil {
		t.Fatal(e)
	}
	if out := request(payload, token); out.Code != 500 {
		t.Errorf("response failure: %d %s", out.Code, out.Body)
	}
	if after := snapshot(); before != after {
		t.Fatal("response failure changed persisted rows")
	}
	if _, e = p.Exec(context.Background(), `DROP TRIGGER invalid_quotation_response ON quotations`); e != nil {
		t.Fatal(e)
	}
	duplicate := strings.Replace(payload, `"currency":"USD"`, `"quotation_number":"`+fmt.Sprintf("Q%d0001", time.Now().Year())+`","currency":"USD"`, 1)
	if out := request(duplicate, token); out.Code != 500 {
		t.Errorf("number conflict: %d %s", out.Code, out.Body)
	}
	if after := snapshot(); before != after {
		t.Fatal("number conflict changed persisted rows")
	}
	if _, e = p.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if out := request(payload, token); out.Code != 403 {
		t.Errorf("demoted user: %d", out.Code)
	}
	if after := snapshot(); before != after {
		t.Fatal("demoted user changed persisted rows")
	}

}
