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

func TestRecurringTemplateUpdateReplacesFields(t *testing.T) {
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

	body := `{"currency":"USD","payment_terms":"Net 30","line_items":[{"description":"original","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":"2026-01-01"}}`
	req := httptest.NewRequest("POST", "/api/v1/invoices/recurring-template", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	created := httptest.NewRecorder()
	h.ServeHTTP(created, req)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var invoice map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &invoice); err != nil {
		t.Fatal(err)
	}
	body = `{"currency":"SGD","payment_terms":"Cash","line_items":[{"description":"replacement","quantity":2,"unit_price":3}],"schedule":{"frequency":"WEEKLY","start_date":"2026-01-01"}}`
	req = httptest.NewRequest("PUT", "/api/v1/invoices/recurring-templates/"+invoice["invoice_id"].(string), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	updated := httptest.NewRecorder()
	h.ServeHTTP(updated, req)
	if updated.Code != 200 {
		t.Fatalf("update: %d %s", updated.Code, updated.Body)
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &invoice); err != nil {
		t.Fatal(err)
	}
	if invoice["currency"] != "SGD" || invoice["amount"] != "6.0000" || invoice["line_items"].([]any)[0].(map[string]any)["description"] != "replacement" {
		t.Fatalf("incorrect update: %#v", invoice)
	}
	id := invoice["invoice_id"].(string)
	_, e = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","interval":1,"start_date":"2026-01-01","end_mode":"NEVER","paused":true}',wht_type='PPH23',wht_rate=3,wht_gross_up=true WHERE invoice_id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("PUT", "/api/v1/invoices/recurring-templates/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	updated = httptest.NewRecorder()
	h.ServeHTTP(updated, req)
	if updated.Code != 200 {
		t.Fatalf("paused update: %d %s", updated.Code, updated.Body)
	}
	if e = json.Unmarshal(updated.Body.Bytes(), &invoice); e != nil {
		t.Fatal(e)
	}
	if invoice["billing_cycle_ref"].(map[string]any)["paused"] != true || invoice["wht_rate"] != "3.0000" || invoice["wht_gross_up"] != true {
		t.Fatalf("overlay lost: %#v", invoice)
	}
	_, e = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","interval":1,"start_date":"2026-01-01","end_mode":"ON_DATE","end_date":"2025-12-31"}' WHERE invoice_id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("PUT", "/api/v1/invoices/recurring-templates/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	updated = httptest.NewRecorder()
	h.ServeHTTP(updated, req)
	if updated.Code != 409 {
		t.Fatalf("ended: %d %s", updated.Code, updated.Body)
	}

	// A malformed stored schedule is editable, and existing issue/due dates are preserved.
	_, e = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{}',issue_date='2026-01-01',due_date=NULL WHERE invoice_id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("PUT", "/api/v1/invoices/recurring-templates/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	updated = httptest.NewRecorder()
	h.ServeHTTP(updated, req)
	if updated.Code != 200 {
		t.Fatalf("misconfigured schedule: %d %s", updated.Code, updated.Body)
	}
	if e = json.Unmarshal(updated.Body.Bytes(), &invoice); e != nil {
		t.Fatal(e)
	}
	if invoice["issue_date"] != "2026-01-01" || invoice["due_date"] != nil {
		t.Fatalf("dates changed: %#v", invoice)
	}
	// A response serialization failure must roll back the complete edit.
	_, e = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","interval":1,"start_date":"2026-01-01","end_mode":"NEVER"}',created_at='infinity' WHERE invoice_id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("PUT", "/api/v1/invoices/recurring-templates/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	updated = httptest.NewRecorder()
	h.ServeHTTP(updated, req)
	if updated.Code != 500 {
		t.Fatalf("serialization: %d %s", updated.Code, updated.Body)
	}
	var stored string
	if e = p.QueryRow(context.Background(), `SELECT billing_cycle_ref->>'frequency' FROM invoices WHERE invoice_id=$1`, id).Scan(&stored); e != nil || stored != "DAILY" {
		t.Fatalf("serialization rollback: %s %v", stored, e)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	op := doc["paths"].(map[string]any)["/api/v1/invoices/recurring-templates/{template_id}"].(map[string]any)["put"].(map[string]any)
	if op["security"] == nil || op["requestBody"] == nil || op["responses"].(map[string]any)["200"] == nil {
		t.Fatalf("incomplete operation: %#v", op)
	}

	_, e = p.Exec(context.Background(), `UPDATE invoices SET created_at='2026-01-01' WHERE invoice_id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	snapshot := func() string {
		var v string
		err := p.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM invoices x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM invoice_line_items x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM journal_entries x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM journal_lines x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM recurring_draft_events x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM recurring_draft_deliveries x))::text`).Scan(&v)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := snapshot()
	_, e = p.Exec(context.Background(), `CREATE FUNCTION task63_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced'; END $$; CREATE TRIGGER task63_fail BEFORE INSERT ON invoice_line_items FOR EACH ROW EXECUTE FUNCTION task63_fail()`)
	if e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("PUT", "/api/v1/invoices/recurring-templates/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	updated = httptest.NewRecorder()
	h.ServeHTTP(updated, req)
	if updated.Code != 500 {
		t.Fatalf("database rollback: %d %s", updated.Code, updated.Body)
	}
	if after := snapshot(); after != before {
		t.Fatalf("database failure changed data: %s", after)
	}
	_, e = p.Exec(context.Background(), `DROP TRIGGER task63_fail ON invoice_line_items; DROP FUNCTION task63_fail()`)
	if e != nil {
		t.Fatal(e)
	}

	for _, tc := range []struct {
		id, content   string
		authenticated bool
		status        int
	}{{"bad", body, true, 422}, {"20000000-0000-0000-0000-000000000099", body, true, 400}, {"20000000-0000-0000-0000-000000000001", body, true, 400}, {id, strings.Repeat("x", (1<<20)+1), true, 413}, {id, body, false, 401}, {id, `{"currency":"secret-data"}`, true, 422}} {
		req = httptest.NewRequest("PUT", "/api/v1/invoices/recurring-templates/"+tc.id, strings.NewReader(tc.content))
		req.Header.Set("Content-Type", "application/json")
		if tc.authenticated {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		updated = httptest.NewRecorder()
		h.ServeHTTP(updated, req)
		if updated.Code != tc.status || updated.Header().Get("Cache-Control") != "no-store" || strings.Contains(updated.Body.String(), "secret-data") {
			t.Errorf("invalid request: %d %s", updated.Code, updated.Body)
		}
	}

}
