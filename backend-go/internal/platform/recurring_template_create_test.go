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
	"testing"
	"time"
)

func TestRecurringTemplateCreateTransaction(t *testing.T) {
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

	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	op := doc["paths"].(map[string]any)["/api/v1/invoices/recurring-template"].(map[string]any)["post"].(map[string]any)
	if op["security"] == nil || op["requestBody"] == nil || op["responses"].(map[string]any)["201"] == nil {
		t.Fatalf("incomplete OpenAPI operation: %#v", op)
	}
	schema := doc["components"].(map[string]any)["schemas"].(map[string]any)["RecurringTemplateSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	for _, name := range []string{"currency", "payment_terms", "schedule", "line_items"} {
		if props[name] == nil {
			t.Errorf("missing schema property %s", name)
		}
	}
	for _, name := range []string{"invoice_number", "issue_date", "due_date", "invoice_type"} {
		if props[name] != nil {
			t.Errorf("unexpected schema property %s", name)
		}
	}

	snapshot := func() string {
		var v string
		e := p.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]') FROM payments x),(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]') FROM journal_entries x),(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]') FROM journal_lines x),(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]') FROM payment_account x))::text`).Scan(&v)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	beforeFinancial := snapshot()

	body := `{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","payment_terms":"Net 30","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":"2026-01-01"}}`
	call := func(content string, ctype string, cookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring-template", strings.NewReader(content))
		req.Header.Set("Content-Type", ctype)
		if cookie {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	for _, tc := range []struct {
		content, ctype string
		cookie         bool
		status         int
	}{{body, "application/json", true, 201}, {body, "application/json", false, 401}, {body, "text/plain", true, 422}, {strings.Repeat("x", 2<<20), "application/json", true, 413}} {
		out := call(tc.content, tc.ctype, tc.cookie)
		if out.Code != tc.status || out.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("status=%d expected=%d %s", out.Code, tc.status, out.Body)
		}
	}
	var before, after int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	bad := strings.Replace(body, `"description":"a"`, `"item_id":"99999999-9999-9999-9999-999999999999","description":"a"`, 1)
	if out := call(bad, "application/json", true); out.Code != 500 {
		t.Fatalf("bad FK: %d %s", out.Code, out.Body)
	}
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&after); e != nil || before != after {
		t.Fatalf("rollback: %d %d %v", before, after, e)
	}
	if _, e = p.Exec(context.Background(), `CREATE FUNCTION corrupt_template_output() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.amount='NaN'; RETURN NEW; END $$; CREATE TRIGGER corrupt_template_output BEFORE INSERT ON invoices FOR EACH ROW EXECUTE FUNCTION corrupt_template_output()`); e != nil {
		t.Fatal(e)
	}
	if out := call(body, "application/json", true); out.Code != 500 {
		t.Errorf("invalid output: %d %s", out.Code, out.Body)
	}
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM invoices`).Scan(&after); e != nil || before != after {
		t.Fatalf("output rollback: %d %d %v", before, after, e)
	}
	if _, e = p.Exec(context.Background(), `DROP TRIGGER corrupt_template_output ON invoices; DROP FUNCTION corrupt_template_output()`); e != nil {
		t.Fatal(e)
	}

	var journals int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM journal_entries`).Scan(&journals); e != nil || journals != 0 {
		t.Fatalf("ledger changed: %d %v", journals, e)
	}
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- call(body, "application/json", true).Code }()
	}
	for i := 0; i < 8; i++ {
		if got := <-results; got != 201 {
			t.Errorf("concurrent create: %d", got)
		}
	}
	tx, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(context.Background(), `UPDATE customers SET default_wht_type='PPH23', default_wht_rate=2 WHERE customer_id='10000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	waiting := make(chan *httptest.ResponseRecorder, 1)
	go func() { waiting <- call(body, "application/json", true) }()
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		_ = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%LockCustomer%'`).Scan(&n)
		if n > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Error("customer lock not observed")
	}
	if e = tx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case out := <-waiting:
		if out.Code != 201 || !strings.Contains(out.Body.String(), `"wht_type":"PPH23"`) {
			t.Errorf("customer lock/defaults: %d %s", out.Code, out.Body)
		}
	case <-time.After(3 * time.Second):
		t.Error("blocked request did not finish")
	}
	if _, e = p.Exec(context.Background(), `INSERT INTO projects(project_id,customer_id,code,name,currency,status) VALUES ('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P','Project','USD','ACTIVE'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000001','Item','SERVICE')`); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ update, lock, body string }{
		{`UPDATE projects SET name='Changed' WHERE project_id='30000000-0000-0000-0000-000000000001'`, `LockProject`, strings.Replace(body, `"currency":"USD"`, `"project_id":"30000000-0000-0000-0000-000000000001","currency":"USD"`, 1)},
		{`UPDATE items SET name='Changed' WHERE item_id='40000000-0000-0000-0000-000000000001'`, `LockItem`, strings.Replace(body, `"description":"a"`, `"item_id":"40000000-0000-0000-0000-000000000001","description":"a"`, 1)},
	} {
		tx, e := p.Begin(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(context.Background(), tc.update); e != nil {
			t.Fatal(e)
		}
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- call(tc.body, "application/json", true) }()
		deadline := time.Now().Add(3 * time.Second)
		blocked := false
		for time.Now().Before(deadline) {
			var n int
			_ = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE $1`, "%"+tc.lock+"%").Scan(&n)
			if n > 0 {
				blocked = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !blocked {
			t.Errorf("%s lock not observed", tc.lock)
		}
		if e = tx.Commit(context.Background()); e != nil {
			t.Fatal(e)
		}
		select {
		case out := <-done:
			if out.Code != 201 {
				t.Errorf("%s: %d %s", tc.lock, out.Code, out.Body)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("%s request blocked", tc.lock)
		}
	}

	if _, e = p.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if out := call(body, "application/json", true); out.Code != 403 {
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
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring-template", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		result <- out
	}()
	deadline = time.Now().Add(3 * time.Second)
	blocked = false
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
	if afterFinancial := snapshot(); afterFinancial != beforeFinancial {
		t.Errorf("unrelated financial data changed: before=%s after=%s", beforeFinancial, afterFinancial)
	}

}
