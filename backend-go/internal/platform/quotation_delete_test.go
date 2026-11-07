package platform

import (
	"context"
	"encoding/json"
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

func TestQuotationDeletePostgres(t *testing.T) {
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

	call := func(path string, cookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("DELETE", path, nil)
		if cookie {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	path := "/api/v1/quotations/20000000-0000-0000-0000-000000000001"
	if w := call(path, false); w.Code != 401 {
		t.Fatalf("auth: %d %s", w.Code, w.Body)
	}
	if w := call("/api/v1/quotations/bad", true); w.Code != 422 {
		t.Fatalf("uuid: %d %s", w.Code, w.Body)
	}
	for _, status := range []string{"SENT", "ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET status=$1`, status); err != nil {
			t.Fatal(err)
		}
		if w := call(path, true); w.Code != 400 || !strings.Contains(w.Body.String(), "only DRAFT quotations can be deleted") {
			t.Fatalf("status %s: %d %s", status, w.Code, w.Body)
		}
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(map[string]any)["/api/v1/quotations/{quotation_id}"].(map[string]any)["delete"].(map[string]any)
	responses := op["responses"].(map[string]any)
	if op["security"] == nil || responses["204"] == nil || responses["400"] == nil || responses["422"] == nil {
		t.Fatal("incomplete delete OpenAPI contract")
	}
	// Hold a row lock so the delete must observe the committed change.
	race := func(update string, expected int) {
		t.Helper()
		tx, err := p.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(context.Background(), update); err != nil {
			t.Fatal(err)
		}
		done := make(chan int, 1)
		go func() { done <- call(path, true).Code }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var n int
			if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("delete did not wait for lock")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err = tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if code := <-done; code != expected {
			t.Fatalf("locked delete: %d want %d", code, expected)
		}
	}
	race(`UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`, 403)
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='admin'`); err != nil {
		t.Fatal(err)
	}
	race(`UPDATE quotations SET status='ACCEPTED'`, 400)
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT'; INSERT INTO quotation_line_items(line_item_id,quotation_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001',0,'x',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	// A failure after deleting the child must roll back both deletions.
	if _, err = p.Exec(context.Background(), `CREATE FUNCTION reject_quotation_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked'; END $$; CREATE TRIGGER reject_delete BEFORE DELETE ON quotations FOR EACH ROW EXECUTE FUNCTION reject_quotation_delete()`); err != nil {
		t.Fatal(err)
	}
	if w := call(path, true); w.Code != 500 {
		t.Fatalf("rollback response: %d %s", w.Code, w.Body)
	}
	var preserved int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM quotation_line_items`).Scan(&preserved); err != nil || preserved != 1 {
		t.Fatalf("rollback lines: %d %v", preserved, err)
	}
	if _, err = p.Exec(context.Background(), `DROP TRIGGER reject_delete ON quotations; DROP FUNCTION reject_quotation_delete()`); err != nil {
		t.Fatal(err)
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- call(path, true) }()
	}
	codes := map[int]int{}
	for i := 0; i < 2; i++ {
		w := <-results
		codes[w.Code]++
		if w.Code == 204 && w.Body.Len() != 0 {
			t.Fatalf("nonempty 204: %s", w.Body)
		}
	}
	if codes[204] != 1 || codes[400] != 1 {
		t.Fatalf("concurrent deletes: %v", codes)
	}
	var count int
	if err = p.QueryRow(context.Background(), `SELECT count(*) FROM quotation_line_items`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("lines: %d %v", count, err)
	}
	if w := call(path, true); w.Code != 400 || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("missing: %d %s", w.Code, w.Body)
	}
}
