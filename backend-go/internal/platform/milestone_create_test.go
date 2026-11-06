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

func TestMilestoneCreatePostgres(t *testing.T) {
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
	r := httptest.NewRequest("POST", "/api/v1/invoices/milestone", strings.NewReader(`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","amount":"6.5000","milestone_ref":"Service"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"amount":"6.5000"`) {
		t.Fatalf("create draft: %d %s", w.Code, w.Body.String())
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/invoices/milestone", strings.NewReader(`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","amount":1,"milestone_ref":"race"}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			if out.Code != 201 {
				t.Errorf("concurrent: %d %s", out.Code, out.Body)
			}
		}()
	}
	wg.Wait()
	var invoices, lines, journals int
	if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM invoices),(SELECT count(*) FROM invoice_line_items),(SELECT count(*) FROM journal_entries)`).Scan(&invoices, &lines, &journals); e != nil || invoices != 10 || lines != 0 || journals != 0 {
		t.Fatalf("unexpected rows: %d %d %d %v", invoices, lines, journals, e)
	}
	req := httptest.NewRequest("POST", "/api/v1/invoices/milestone", strings.NewReader(`{"customer_id":"10000000-0000-0000-0000-000000000001","project_id":"99999999-9999-9999-9999-999999999999","currency":"USD","amount":1,"milestone_ref":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 500 || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bad project: %d %s", out.Code, out.Body)
	}
	var after int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&after); e != nil || after != invoices {
		t.Fatalf("rollback: %d %v", after, e)
	}
	if _, e = p.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("POST", "/api/v1/invoices/milestone", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	out = httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 403 {
		t.Fatalf("demoted: %d", out.Code)
	}
	if _, e = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'; CREATE FUNCTION bad_milestone_response() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.created_at='infinity'; RETURN NEW; END $$; CREATE TRIGGER bad_milestone BEFORE INSERT ON invoices FOR EACH ROW EXECUTE FUNCTION bad_milestone_response()`); e != nil {
		t.Fatal(e)
	}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/invoices/milestone", strings.NewReader(`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","amount":1,"milestone_ref":"rollback"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		o := httptest.NewRecorder()
		h.ServeHTTP(o, r)
		return o
	}
	o := request()
	if o.Code != 500 {
		t.Fatalf("invalid response: %d %s", o.Code, o.Body)
	}
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&after); e != nil || after != invoices {
		t.Fatalf("response rollback: %d %v", after, e)
	}
	if _, e = p.Exec(context.Background(), `DROP TRIGGER bad_milestone ON invoices; DROP FUNCTION bad_milestone_response()`); e != nil {
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
	go func() { result <- request() }()
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
			t.Fatalf("completed before demotion: %d", early.Code)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Error("admin lock not observed")
	}
	if e = demote.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case res := <-result:
		if res.Code != 403 {
			t.Fatalf("demotion race: %d", res.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request stuck")
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	if spec.Code != 200 || !strings.Contains(spec.Body.String(), `"create-milestone-invoice"`) || !strings.Contains(spec.Body.String(), `"MilestoneSchema"`) {
		t.Errorf("openapi: %d", spec.Code)
	}

	if _, e = p.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		body, ctype string
		status      int
	}{{`{"bad":`, "application/json", 422}, {`[]`, "application/json", 422}, {`{}`, "text/plain", 422}, {strings.Repeat("x", 2<<20), "application/json", 413}} {
		r := httptest.NewRequest("POST", "/api/v1/invoices/milestone", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.ctype)
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		o := httptest.NewRecorder()
		h.ServeHTTP(o, r)
		if o.Code != tc.status || o.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("body/media %d wanted %d", o.Code, tc.status)
		}
	}
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest("POST", "/api/v1/invoices/milestone", strings.NewReader(`{}`)))
	if unauth.Code != 401 {
		t.Errorf("unauthenticated: %d", unauth.Code)
	}
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	paths := doc["paths"].(map[string]any)
	op := paths["/api/v1/invoices/milestone"].(map[string]any)["post"].(map[string]any)
	responses := op["responses"].(map[string]any)
	for _, status := range []string{"201", "401", "403", "413", "422", "500"} {
		if responses[status] == nil {
			t.Errorf("missing response %s", status)
		}
	}
	if op["security"] == nil || op["requestBody"] == nil {
		t.Error("missing security or request schema")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	schema := schemas["MilestoneSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	for _, name := range []string{"customer_id", "project_id", "currency", "amount", "milestone_ref", "issue_date", "due_in_days"} {
		if props[name] == nil {
			t.Errorf("missing property %s", name)
		}
	}
	amountSchema := props["amount"].(map[string]any)
	variants := amountSchema["anyOf"].([]any)
	positiveNumber := false
	for _, variant := range variants {
		v := variant.(map[string]any)
		if v["type"] == "number" && v["exclusiveMinimum"] == float64(0) {
			positiveNumber = true
		}
	}
	if !positiveNumber {
		t.Error("amount numeric schema must have exclusiveMinimum 0")
	}
	related, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer related.Rollback(context.Background())
	if _, e = related.Exec(context.Background(), `UPDATE customers SET default_wht_type='PPH23',default_wht_rate=2,default_wht_gross_up=true WHERE customer_id='10000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	pending := make(chan *httptest.ResponseRecorder, 1)
	go func() { pending <- request() }()
	deadline = time.Now().Add(3 * time.Second)
	blocked = false
	for time.Now().Before(deadline) {
		var n int
		_ = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%LockMilestoneCustomer%'`).Scan(&n)
		if n > 0 {
			blocked = true
			break
		}
		select {
		case early := <-pending:
			t.Fatalf("customer read before update commit: %d", early.Code)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Error("customer lock not observed")
	}
	if e = related.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case res := <-pending:
		if res.Code != 201 || !strings.Contains(res.Body.String(), `"wht_gross_up":true`) {
			t.Errorf("related race %d %s", res.Code, res.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("related race stuck")
	}

}
