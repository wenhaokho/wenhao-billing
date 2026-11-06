package platform

import (
	"context"
	"encoding/json"

	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

func TestRecurringTriggerCreatesOneDraft(t *testing.T) {
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

	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','RECURRING','USD',10,10,'DRAFT',true,'{"frequency":"MONTHLY","start_date":"2025-01-01"}'); INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001',0,'Service',1,10,10)`)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = p.Exec(context.Background(), `INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000002','Unrelated'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000002','Unrelated item','SERVICE')`); err != nil {
		t.Fatal(err)
	}
	var unrelatedBefore string
	if err = p.QueryRow(context.Background(), `SELECT json_build_array((SELECT row_to_json(c) FROM customers c WHERE customer_id='10000000-0000-0000-0000-000000000002'),(SELECT row_to_json(i) FROM items i WHERE item_id='40000000-0000-0000-0000-000000000002'))::text`).Scan(&unrelatedBefore); err != nil {
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
	malformed := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{`))
	malformed.Header.Set("Content-Type", "application/json")
	malformed.AddCookie(&http.Cookie{Name: "session", Value: token})
	malformedOut := httptest.NewRecorder()
	h.ServeHTTP(malformedOut, malformed)
	if malformedOut.Code != 422 || !strings.Contains(malformedOut.Body.String(), "json_invalid") {
		t.Fatalf("malformed: %d %s", malformedOut.Code, malformedOut.Body.String())
	}

	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"bad", `{"cycle_key":"2025-01-01"}`, 422},
		{"20000000-0000-0000-0000-000000000099", `{"cycle_key":"2025-01-01"}`, 400},
		{"20000000-0000-0000-0000-000000000001", strings.Repeat("x", (1<<20)+1), 413},
	} {
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/"+tc.path+"/trigger", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("case %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	badReq := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-02"}`))
	badReq.Header.Set("Content-Type", "application/json")
	badReq.AddCookie(&http.Cookie{Name: "session", Value: token})
	badOut := httptest.NewRecorder()
	h.ServeHTTP(badOut, badReq)
	if badOut.Code != 400 || !strings.Contains(badOut.Body.String(), "got '2025-02'") {
		t.Fatalf("invalid date: %d %s", badOut.Code, badOut.Body.String())
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	operation := doc["paths"].(map[string]any)["/api/v1/invoices/recurring/{template_id}/trigger"].(map[string]any)["post"].(map[string]any)
	if operation["requestBody"] == nil || operation["security"] == nil {
		t.Fatal("missing OpenAPI request or security")
	}
	for i := 0; i < 2; i++ {
		path := "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger"
		if i == 0 {
			path = "/api/v1/invoices/recurring/20000000000000000000000000000001/trigger"
		}
		key := "2025-02-01"
		if i == 0 {
			key = "20250201"
		}
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"cycle_key":"`+key+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("cache control: %q", out.Header().Get("Cache-Control"))
		}
		if out.Code != 200 {
			t.Fatalf("trigger %d: %d %s", i, out.Code, out.Body.String())
		}
	}

	var eventCount int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM recurring_draft_events`).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("expected one durable event for repeated trigger: count=%d error=%v", eventCount, err)
	}

	// A void child remains the idempotent result for its exact cycle key.
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='VOID' WHERE NOT is_template`); err != nil {
		t.Fatal(err)
	}
	voidReq := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-02-01"}`))
	voidReq.Header.Set("Content-Type", "application/json")
	voidReq.AddCookie(&http.Cookie{Name: "session", Value: token})
	voidOut := httptest.NewRecorder()
	h.ServeHTTP(voidOut, voidReq)
	if voidOut.Code != 200 || !strings.Contains(voidOut.Body.String(), `"status":"VOID"`) {
		t.Fatalf("void rerun %d %s", voidOut.Code, voidOut.Body.String())
	}
	if _, err = p.Exec(context.Background(), `CREATE FUNCTION task62_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'enqueue failed'; END $$; CREATE TRIGGER task62_fail BEFORE INSERT ON recurring_draft_deliveries FOR EACH ROW EXECUTE FUNCTION task62_fail()`); err != nil {
		t.Fatal(err)
	}
	failReq := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-03-01"}`))
	failReq.Header.Set("Content-Type", "application/json")
	failReq.AddCookie(&http.Cookie{Name: "session", Value: token})
	failOut := httptest.NewRecorder()
	h.ServeHTTP(failOut, failReq)
	if failOut.Code != 500 {
		t.Fatalf("enqueue failure: %d %s", failOut.Code, failOut.Body.String())
	}
	if _, err = p.Exec(context.Background(), `DROP TRIGGER task62_fail ON recurring_draft_deliveries; DROP FUNCTION task62_fail()`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE NOT is_template`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("draft count %d, error %v", count, err)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM recurring_draft_events`).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("enqueue failure left events: %d %v", eventCount, err)
	}

	// A database failure after invoice creation and a response serialization failure must both roll back.
	for _, setup := range []string{
		`CREATE FUNCTION task60_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.invoice_id <> '20000000-0000-0000-0000-000000000001' THEN RAISE foreign_key_violation; END IF; RETURN NEW; END $$; CREATE TRIGGER task60_test BEFORE INSERT ON invoice_line_items FOR EACH ROW EXECUTE FUNCTION task60_fail()`,
		`CREATE FUNCTION task60_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NOT NEW.is_template THEN NEW.created_at='infinity'; END IF; RETURN NEW; END $$; CREATE TRIGGER task60_test BEFORE INSERT ON invoices FOR EACH ROW EXECUTE FUNCTION task60_fail()`,
	} {
		if _, err = p.Exec(context.Background(), setup); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-03-01"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 500 {
			t.Fatalf("forced failure %d %s", w.Code, w.Body.String())
		}
		if _, err = p.Exec(context.Background(), `DROP TRIGGER IF EXISTS task60_test ON invoice_line_items; DROP TRIGGER IF EXISTS task60_test ON invoices; DROP FUNCTION task60_fail()`); err != nil {
			t.Fatal(err)
		}
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE NOT is_template`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("failed transaction count %d: %v", count, err)
		}
	}
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`)
	if err != nil {
		t.Fatal(err)
	}
	token2, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	concurrentHandler, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i, tok := range []string{token, token2} {
		wg.Add(1)
		go func(i int, tok string) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader([]string{`{"cycle_key":"2025-03-01"}`, `{"cycle_key":"2025-W09-6"}`}[i]))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: tok})
			w := httptest.NewRecorder()
			concurrentHandler.ServeHTTP(w, req)
			statuses <- w.Code
		}(i, tok)
	}
	wg.Wait()
	close(statuses)
	for code := range statuses {
		if code != 200 {
			t.Fatalf("concurrent status %d", code)
		}
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE NOT is_template`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("concurrent count %d: %v", count, err)
	}
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM recurring_draft_events`).Scan(&eventCount); err != nil || eventCount != 2 {
		t.Fatalf("concurrent events %d: %v", eventCount, err)
	}

	var diffWG sync.WaitGroup
	diffCodes := make(chan int, 2)
	for i, tok := range []string{token, token2} {
		diffWG.Add(1)
		go func(i int, tok string) {
			defer diffWG.Done()
			key := []string{"2025-05-01", "2025-06-01"}[i]
			req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"`+key+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: tok})
			w := httptest.NewRecorder()
			concurrentHandler.ServeHTTP(w, req)
			diffCodes <- w.Code
		}(i, tok)
	}
	diffWG.Wait()
	close(diffCodes)
	for code := range diffCodes {
		if code != 200 {
			t.Fatalf("distinct cycles: %d", code)
		}
	}
	var uniqueNumbers, childCount int
	if err = p.QueryRow(context.Background(), `SELECT count(DISTINCT invoice_number),count(*) FROM invoices WHERE NOT is_template`).Scan(&uniqueNumbers, &childCount); err != nil || uniqueNumbers != 4 || childCount != 4 {
		t.Fatalf("distinct cycles numbers %d count %d: %v", uniqueNumbers, childCount, err)
	}
	unauth := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-07-01"}`))
	unauth.Header.Set("Content-Type", "application/json")
	uout := httptest.NewRecorder()
	concurrentHandler.ServeHTTP(uout, unauth)
	if uout.Code != 401 {
		t.Fatalf("unauthenticated: %d", uout.Code)
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	unauth.AddCookie(&http.Cookie{Name: "session", Value: token2})
	uout = httptest.NewRecorder()
	concurrentHandler.ServeHTTP(uout, unauth)
	if uout.Code != 403 {
		t.Fatalf("demoted: %d", uout.Code)
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}

	// Persisted role is checked under lock after an in-flight demotion commits.
	demotion, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = demotion.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	demotedResult := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-07-01"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token2})
		w := httptest.NewRecorder()
		concurrentHandler.ServeHTTP(w, req)
		demotedResult <- w.Code
	}()
	waited := false
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')`).Scan(&waited); err != nil {
			t.Fatal(err)
		}
		if waited {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = demotion.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !waited {
		t.Fatal("trigger did not wait for demotion")
	}
	if code := <-demotedResult; code != 403 {
		t.Fatalf("persisted demotion: %d", code)
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000002'`); err != nil {
		t.Fatal(err)
	}
	var ledgerCount int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM journal_entries`).Scan(&ledgerCount); err != nil || ledgerCount != 0 {
		t.Fatalf("ledger changed %d: %v", ledgerCount, err)
	}
	// END_NOW must wait for an in-flight trigger holding the template lock.
	blockConn, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer blockConn.Release()
	if _, err = blockConn.Exec(context.Background(), `SELECT pg_advisory_lock(620062); CREATE FUNCTION task62_block() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(620062); RETURN NEW; END $$; CREATE TRIGGER task62_block BEFORE INSERT ON recurring_draft_events FOR EACH ROW EXECUTE FUNCTION task62_block()`); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 2)
	go func() {
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-04-01"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		concurrentHandler.ServeHTTP(w, req)
		result <- w.Code
	}()
	until = time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(until) {
		if err = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("trigger did not reach outbox insert")
	}
	go func() {
		req := httptest.NewRequest("PATCH", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"action":"END_NOW"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token2})
		w := httptest.NewRecorder()
		concurrentHandler.ServeHTTP(w, req)
		result <- w.Code
	}()
	until = time.Now().Add(5 * time.Second)
	blocked = false
	for time.Now().Before(until) {
		var n int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = blockConn.Exec(context.Background(), `SELECT pg_advisory_unlock(620062)`); err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("END_NOW did not wait for trigger")
	}
	for i := 0; i < 2; i++ {
		if code := <-result; code != 200 {
			t.Fatalf("locked action status %d", code)
		}
	}
	if _, err = p.Exec(context.Background(), `DROP TRIGGER task62_block ON recurring_draft_events; DROP FUNCTION task62_block()`); err != nil {
		t.Fatal(err)
	}

	// Legacy compact keys are reused; malformed and different-template references cannot collide.
	_, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES
 ('20000000-0000-0000-0000-000000000010','10000000-0000-0000-0000-000000000001','RECURRING','USD',1,1,'DRAFT',false,'{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":"20250901"}'),
 ('20000000-0000-0000-0000-000000000011','10000000-0000-0000-0000-000000000001','RECURRING','USD',1,1,'DRAFT',false,'{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":"bad"}'),
 ('20000000-0000-0000-0000-000000000012','10000000-0000-0000-0000-000000000001','RECURRING','USD',1,1,'DRAFT',false,'{"template_invoice_id":"20000000-0000-0000-0000-000000000099","cycle_key":"2025-10-01"}'),
 ('20000000-0000-0000-0000-000000000013','10000000-0000-0000-0000-000000000001','RECURRING','USD',1,1,'DRAFT',false,'{"template_invoice_id":"20000000-0000-0000-0000-000000000001","cycle_key":20251001}')`)
	if err != nil {
		t.Fatal(err)
	}

	// An identifiable existing cycle wins even when another legacy key is ambiguous.
	exact := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-W36-1"}`))
	exact.Header.Set("Content-Type", "application/json")
	exact.AddCookie(&http.Cookie{Name: "session", Value: token})
	exactOut := httptest.NewRecorder()
	h.ServeHTTP(exactOut, exact)
	if exactOut.Code != 200 || !strings.Contains(exactOut.Body.String(), "20000000-0000-0000-0000-000000000010") {
		t.Fatalf("recognized legacy: %d %s", exactOut.Code, exactOut.Body.String())
	}
	if _, err = p.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000013'`); err != nil {
		t.Fatal(err)
	}
	var before int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var eventsBefore int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM recurring_draft_events`).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{`"2025-10"`, `"bad"`, `null`, `123`, `""`, `"2025-10-15"`, `"missing"`, `"void"`} {
		_, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref=jsonb_build_object('template_invoice_id','20000000-0000-0000-0000-000000000001','cycle_key',$1::jsonb) WHERE invoice_id='20000000-0000-0000-0000-000000000011'`, legacy)
		if err != nil {
			t.Fatal(err)
		}

		if legacy == `"missing"` {
			if _, err = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref=billing_cycle_ref-'cycle_key' WHERE invoice_id='20000000-0000-0000-0000-000000000011'`); err != nil {
				t.Fatal(err)
			}
		}
		if legacy == `"void"` {
			if _, err = p.Exec(context.Background(), `UPDATE invoices SET status='VOID',billing_cycle_ref=jsonb_set(billing_cycle_ref,'{cycle_key}','"2025-10"') WHERE invoice_id='20000000-0000-0000-0000-000000000011'`); err != nil {
				t.Fatal(err)
			}
		}
		var snapshotBefore string
		if err = p.QueryRow(context.Background(), `SELECT json_build_array((SELECT json_agg(row_to_json(i) ORDER BY invoice_id) FROM invoices i),(SELECT json_agg(row_to_json(l) ORDER BY line_item_id) FROM invoice_line_items l),(SELECT count(*) FROM journal_entries))::text`).Scan(&snapshotBefore); err != nil {
			t.Fatal(err)
		}
		results := make(chan int, 2)
		var wait sync.WaitGroup
		for _, tok := range []string{token, token2} {
			wait.Add(1)
			go func(tok string) {
				defer wait.Done()
				req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-10-01"}`))
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(&http.Cookie{Name: "session", Value: tok})
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if !strings.Contains(w.Body.String(), "manual review") {
					results <- 0
					return
				}
				results <- w.Code
			}(tok)
		}
		wait.Wait()
		close(results)
		for code := range results {
			if code != 409 {
				t.Fatalf("ambiguous %s: status %d", legacy, code)
			}
		}

		var snapshotAfter string
		if err = p.QueryRow(context.Background(), `SELECT json_build_array((SELECT json_agg(row_to_json(i) ORDER BY invoice_id) FROM invoices i),(SELECT json_agg(row_to_json(l) ORDER BY line_item_id) FROM invoice_line_items l),(SELECT count(*) FROM journal_entries))::text`).Scan(&snapshotAfter); err != nil || snapshotAfter != snapshotBefore {
			t.Fatalf("ambiguous %s changed data: %v", legacy, err)
		}
		var after int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&after); err != nil || after != before {
			t.Fatalf("ambiguous %s mutated state: count %d", legacy, after)
		}
		var eventsAfter int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM recurring_draft_events`).Scan(&eventsAfter); err != nil || eventsAfter != eventsBefore {
			t.Fatalf("ambiguous %s changed event count", legacy)
		}
	}
	// An ambiguous key from another template does not block this template.
	if _, err = p.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_id='20000000-0000-0000-0000-000000000011'; UPDATE invoices SET billing_cycle_ref=jsonb_set(billing_cycle_ref,'{cycle_key}','"bad"') WHERE invoice_id='20000000-0000-0000-0000-000000000012'`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-10-01"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	concurrentHandler.ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), "20000000-0000-0000-0000-000000000012") {
		t.Fatalf("unrelated legacy: %d %s", w.Code, w.Body.String())
	}
	var unrelatedAfter string
	if err = p.QueryRow(context.Background(), `SELECT json_build_array((SELECT row_to_json(c) FROM customers c WHERE customer_id='10000000-0000-0000-0000-000000000002'),(SELECT row_to_json(i) FROM items i WHERE item_id='40000000-0000-0000-0000-000000000002'))::text`).Scan(&unrelatedAfter); err != nil || unrelatedAfter != unrelatedBefore {
		t.Fatalf("unrelated data changed: %v", err)
	}

}
