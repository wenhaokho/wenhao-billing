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
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestItemDeactivatePostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(context.Background(), `INSERT INTO users (user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO items(item_id,name,item_type,default_currency,is_sold,is_purchased,active,is_hosting) VALUES ('10000000-0000-0000-0000-000000000001','Hosting','SERVICE','IDR',true,false,true,false),('10000000-0000-0000-0000-000000000002','Inactive','SERVICE','IDR',true,false,false,false),('10000000-0000-0000-0000-000000000003','Other','SERVICE','IDR',true,false,true,false); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,proxied) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','zone','rec','example.com','A','1','2',true)`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token := func(id string) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	admin := token("00000000-0000-0000-0000-000000000001")
	user := token("00000000-0000-0000-0000-000000000002")
	call := func(id string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("DELETE", "/api/v1/items/"+id, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		id     string
		cookie *http.Cookie
		status int
		body   string
	}{{"bad", nil, 401, ""}, {"bad", &http.Cookie{Name: "session", Value: "tampered"}, 401, ""}, {"bad", user, 403, ""}, {"bad", admin, 422, "uuid_parsing"}, {"10000000-0000-0000-0000-000000000099", admin, 404, `"detail":"item not found"`}} {
		w := call(tc.id, tc.cookie)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.body) || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s headers %v", tc.id, w.Code, w.Body.String(), w.Header())
		}
	}
	// A target read error must roll back the preceding item update.
	if _, err := pool.Exec(context.Background(), `REVOKE SELECT ON cloudflare_targets FROM CURRENT_USER`); err != nil {
		t.Fatal(err)
	}
	w := call("10000000-0000-0000-0000-000000000001", admin)
	if _, err := pool.Exec(context.Background(), `GRANT SELECT ON cloudflare_targets TO CURRENT_USER`); err != nil {
		t.Fatal(err)
	}
	if w.Code != 500 {
		t.Fatalf("target read failure: %d %s", w.Code, w.Body.String())
	}
	var active bool
	if err := pool.QueryRow(context.Background(), `SELECT active FROM items WHERE item_id='10000000-0000-0000-0000-000000000001'`).Scan(&active); err != nil || !active {
		t.Fatalf("failed request changed active: %v %v", active, err)
	}
	var targetBefore string
	if err := pool.QueryRow(context.Background(), `SELECT row_to_json(t)::text FROM cloudflare_targets t WHERE item_id='10000000-0000-0000-0000-000000000001'`).Scan(&targetBefore); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest("GET", "/api/v1/items/10000000-0000-0000-0000-000000000001", nil)
	get.AddCookie(admin)
	before := httptest.NewRecorder()
	h.ServeHTTP(before, get)
	var expected map[string]any
	if before.Code != 200 || json.Unmarshal(before.Body.Bytes(), &expected) != nil {
		t.Fatalf("GET: %d %s", before.Code, before.Body.String())
	}
	for _, key := range []string{"item_id", "sku", "name", "item_type", "description", "default_currency", "default_unit_price", "default_purchase_price", "revenue_account_id", "expense_account_id", "is_sold", "is_purchased", "active", "is_hosting", "hosting_domain", "hosting_grace_days", "hosting_suspension_enabled", "hosting_status", "hosting_last_paid_at", "hosting_last_action_at", "hosting_last_error", "created_at", "cloudflare_target"} {
		if _, ok := expected[key]; !ok {
			t.Fatalf("ItemOut missing %s", key)
		}
	}
	expected["active"] = false
	for _, id := range []string{"10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000002"} {
		w := call(id, admin)
		var item map[string]any
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &item) != nil || item["active"] != false || item["item_id"] != id || item["name"] == nil || item["created_at"] == nil {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
		if strings.HasSuffix(id, "001") && !reflect.DeepEqual(item, expected) {
			t.Fatalf("complete ItemOut changed: got %v want %v", item, expected)
		}
	}
	var targetAfter string
	if err := pool.QueryRow(context.Background(), `SELECT row_to_json(t)::text FROM cloudflare_targets t WHERE item_id='10000000-0000-0000-0000-000000000001'`).Scan(&targetAfter); err != nil || targetBefore != targetAfter {
		t.Fatalf("target row changed: before %s after %s err %v", targetBefore, targetAfter, err)
	}
	var count int
	err = pool.QueryRow(context.Background(), `SELECT count(*) FROM items WHERE item_id='10000000-0000-0000-0000-000000000003' AND active`).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("other item changed: %d %v", count, err)
	}
}
func TestItemDeactivateOpenAPI(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	op, ok := doc["paths"].(map[string]any)["/api/v1/items/{item_id}"].(map[string]any)["delete"].(map[string]any)
	if !ok {
		t.Fatal("DELETE missing")
	}
	if len(op["security"].([]any)) == 0 {
		t.Fatal("security missing")
	}
}
