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

func TestQuotationStatusPostgres(t *testing.T) {
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
		req := httptest.NewRequest("POST", path, nil)
		if cookie {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	path := "/api/v1/quotations/20000000-0000-0000-0000-000000000001/decline"
	if w := call(path, false); w.Code != 401 {
		t.Fatalf("auth: %d %s", w.Code, w.Body)
	}
	if w := call("/api/v1/quotations/bad/decline", true); w.Code != 422 {
		t.Fatalf("uuid: %d %s", w.Code, w.Body)
	}
	for _, status := range []string{"DRAFT", "SENT", "ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET status=$1`, status); err != nil {
			t.Fatal(err)
		}
		w := call(path, true)
		expected := 400
		if status == "DRAFT" || status == "SENT" {
			expected = 200
		}
		if w.Code != expected {
			t.Fatalf("%s: %d %s", status, w.Code, w.Body)
		}
	}
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT'`); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 2)
	go func() { done <- call(path, true).Code }()
	go func() { done <- call(path, true).Code }()
	a, b := <-done, <-done
	if !((a == 200 && b == 400) || (a == 400 && b == 200)) {
		t.Fatalf("concurrent transitions: %d %d", a, b)
	}
	// A failed database write rolls the transaction back.
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT'; CREATE FUNCTION reject_quotation_transition() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'rejected'; END $$; CREATE TRIGGER reject_quotation_transition BEFORE UPDATE ON quotations FOR EACH ROW EXECUTE FUNCTION reject_quotation_transition()`); err != nil {
		t.Fatal(err)
	}
	if w := call(path, true); w.Code != 500 {
		t.Fatalf("rollback response: %d %s", w.Code, w.Body)
	}
	var status string
	if err = p.QueryRow(context.Background(), `SELECT status FROM quotations`).Scan(&status); err != nil || status != "DRAFT" {
		t.Fatalf("rollback status: %s %v", status, err)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"mark-sent", "accept", "decline", "void"} {
		op := doc["paths"].(map[string]any)["/api/v1/quotations/{quotation_id}/"+action].(map[string]any)["post"].(map[string]any)
		if op["security"] == nil {
			t.Fatal(action)
		}
		responses := op["responses"].(map[string]any)
		_, has413 := responses["413"]
		if has413 != (action == "accept") {
			t.Fatalf("%s 413: %t", action, has413)
		}
		content := responses["500"].(map[string]any)["content"].(map[string]any)
		_, hasText := content["text/plain"]
		if hasText != (action == "accept") {
			t.Fatalf("%s text/plain 500: %t", action, hasText)
		}
		if content["application/json"] == nil {
			t.Fatalf("%s missing JSON 500", action)
		}
	}
}
