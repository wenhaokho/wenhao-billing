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
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestItemPatchPostgres(t *testing.T) {
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
		r := httptest.NewRequest("PATCH", "/api/v1/items/"+id, strings.NewReader(`{"name":"Renamed"}`))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call("10000000-0000-0000-0000-000000000001", admin)
	var item map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil || item["name"] != "Renamed" {
		t.Fatalf("PATCH item: status %d body %s", w.Code, w.Body.String())
	}

	callBody := func(id, body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "/api/v1/items/"+id, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	id := "10000000-0000-0000-0000-000000000001"
	for _, tc := range []struct {
		id, body string
		cookie   *http.Cookie
		status   int
	}{
		{id, `{}`, nil, 401}, {id, `{}`, user, 403}, {id, `{}`, &http.Cookie{Name: "session", Value: "tampered"}, 401},
		{"bad", `{}`, admin, 422}, {"10000000-0000-0000-0000-000000000099", `{}`, admin, 404},
		{id, ``, admin, 422}, {id, `{`, admin, 422}, {id, `{"default_unit_price":1e1000000000}`, admin, 422},
	} {
		w := callBody(tc.id, tc.body, tc.cookie)
		if w.Code != tc.status {
			t.Errorf("%s %s: %d %s", tc.id, tc.body, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("missing no-store: %v", w.Header())
		}
	}
	// Both endpoints reject oversized streaming bodies before parsing or mutating.
	oversized := `{"description":"` + strings.Repeat("x", (1<<20)+1) + `"}`
	for _, method := range []string{"PATCH", "POST"} {
		path := "/api/v1/items"
		if method == "PATCH" {
			path += "/" + id
		}
		request := httptest.NewRequest(method, path, strings.NewReader(oversized))
		request.Header.Set("Content-Type", "application/json")
		request.ContentLength = -1
		request.AddCookie(admin)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("oversized %s: %d", method, response.Code)
		}
	}
	var afterOversized string
	if err = pool.QueryRow(context.Background(), `SELECT name FROM items WHERE item_id=$1`, id).Scan(&afterOversized); err != nil || afterOversized != "Renamed" {
		t.Fatalf("oversized changed item: %q %v", afterOversized, err)
	}
	// FastAPI validates ItemOut after the router commits, so establish both effects.
	pyEmpty := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.items import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ["ITEM_TEST_DATABASE_URL"].replace("postgres://","postgresql+psycopg://",1))
def database():
 with Session(engine) as db: yield db
app=FastAPI(); app.include_router(router,prefix="/api/v1"); app.dependency_overrides[get_db]=database; app.dependency_overrides[current_admin]=lambda: None
r=TestClient(app,raise_server_exceptions=False).patch("/api/v1/items/10000000-0000-0000-0000-000000000003",json={"name":""})
with engine.connect() as db: name=db.execute(text("SELECT name FROM items WHERE item_id='10000000-0000-0000-0000-000000000003'")).scalar_one()
print(json.dumps({"status":r.status_code,"body":r.text,"name":name}))`)
	pyEmpty.Env = append(os.Environ(), "ITEM_TEST_DATABASE_URL="+db.URL())
	out, err := pyEmpty.Output()
	if err != nil {
		t.Fatalf("Python empty name: %v", err)
	}
	var pythonEmpty struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
		Name   string `json:"name"`
	}
	if err = json.Unmarshal(out, &pythonEmpty); err != nil || pythonEmpty.Status != 500 || pythonEmpty.Body != "Internal Server Error" || pythonEmpty.Name != "" {
		t.Fatalf("Python empty name result: %s %v", out, err)
	}
	if _, err = pool.Exec(context.Background(), `UPDATE items SET name='Other' WHERE item_id='10000000-0000-0000-0000-000000000003'`); err != nil {
		t.Fatal(err)
	}
	empty := callBody("10000000-0000-0000-0000-000000000003", `{"name":"","description":"must rollback"}`, admin)
	var nameAfter string
	var descAfter *string
	if err = pool.QueryRow(context.Background(), `SELECT name,description FROM items WHERE item_id='10000000-0000-0000-0000-000000000003'`).Scan(&nameAfter, &descAfter); err != nil {
		t.Fatal(err)
	}
	if empty.Code != pythonEmpty.Status || strings.TrimSpace(empty.Body.String()) != pythonEmpty.Body || nameAfter != "Other" || descAfter != nil {
		t.Fatalf("empty name HTTP/state: %d %s %q %v", empty.Code, empty.Body.String(), nameAfter, descAfter)
	}
	// Failed authentication must not change persisted data even if a response was written.
	callBody(id, `{"name":"unauthorized"}`, user)
	var authorizedName string
	if err = pool.QueryRow(context.Background(), `SELECT name FROM items WHERE item_id=$1`, id).Scan(&authorizedName); err != nil || authorizedName != "Renamed" {
		t.Fatalf("unauthorized mutation: %q %v", authorizedName, err)
	}
	// A target read failure after the item update must roll back that update.
	if _, err = pool.Exec(context.Background(), `ALTER TABLE cloudflare_targets RENAME TO hidden_targets`); err != nil {
		t.Fatal(err)
	}
	failedRead := callBody(id, `{"name":"read failure"}`, admin)
	if _, err = pool.Exec(context.Background(), `ALTER TABLE hidden_targets RENAME TO cloudflare_targets`); err != nil {
		t.Fatal(err)
	}
	if failedRead.Code != 500 {
		t.Fatalf("target read failure: %d", failedRead.Code)
	}
	if err = pool.QueryRow(context.Background(), `SELECT name FROM items WHERE item_id=$1`, id).Scan(&authorizedName); err != nil || authorizedName != "Renamed" {
		t.Fatalf("read failure rollback: %q %v", authorizedName, err)
	}
	w = callBody(id, `{"sku":"x","description":"d","default_unit_price":"12.34","is_hosting":true,"hosting_domain":" EXAMPLE.ORG ","hosting_grace_days":5,"hosting_suspension_enabled":true,"cloudflare_target":{"zone_id":" z ","record_id":" r ","record_name":" EXAMPLE.ORG ","record_type":"aaaa","live_content":" 1 ","maintenance_content":" 2 ","proxied":false}}`, admin)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	target := item["cloudflare_target"].(map[string]any)
	if item["hosting_domain"] != "example.org" || item["hosting_status"] != "ACTIVE" || target["record_name"] != "example.org" || target["record_type"] != "AAAA" || target["proxied"] != false {
		t.Fatalf("normalization: %s", w.Body.String())
	}
	w = callBody(id, `{"sku":null,"description":null,"default_unit_price":null,"hosting_domain":null,"hosting_grace_days":null,"hosting_suspension_enabled":null,"cloudflare_target":null}`, admin)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil || item["sku"] != nil || item["default_unit_price"] != nil || item["cloudflare_target"] == nil {
		t.Fatalf("null/retention: %d %s", w.Code, w.Body.String())
	}
	w = callBody(id, `{"cloudflare_target":{"zone_id":"z","record_id":"r","record_name":"name","record_type":"a","live_content":"1","maintenance_content":"2"}}`, admin)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil || item["cloudflare_target"].(map[string]any)["proxied"] != false {
		t.Fatalf("proxied retention: %d %s", w.Code, w.Body.String())
	}
	// Every mutable column and explicit nullable fields, then an empty update retaining them.
	w = callBody(id, `{"sku":"s","name":"all fields","item_type":"USAGE","description":"details","default_currency":"USD","default_unit_price":"12.3400","default_purchase_price":"5.6700","revenue_account_id":null,"expense_account_id":null,"is_sold":false,"is_purchased":true,"active":false,"is_hosting":false,"hosting_domain":" DOMAIN.TEST ","hosting_grace_days":12,"hosting_suspension_enabled":false,"hosting_status":"SUSPENDED"}`, admin)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil {
		t.Fatalf("all fields: %s", w.Body.String())
	}
	for k, v := range map[string]any{"sku": "s", "name": "all fields", "item_type": "USAGE", "description": "details", "default_currency": "USD", "default_unit_price": "12.3400", "default_purchase_price": "5.6700", "is_sold": false, "is_purchased": true, "active": false, "is_hosting": false, "hosting_domain": "domain.test", "hosting_grace_days": float64(12), "hosting_suspension_enabled": false, "hosting_status": "SUSPENDED"} {
		if item[k] != v {
			t.Errorf("field %s: %v != %v", k, item[k], v)
		}
	}
	previous := item
	w = callBody(id, `{}`, admin)
	item = map[string]any{}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil || !reflect.DeepEqual(previous, item) {
		t.Fatalf("omission did not retain: %s", w.Body.String())
	}
	w = callBody(id, `{"sku":null,"description":null,"default_unit_price":null,"default_purchase_price":null,"revenue_account_id":null,"expense_account_id":null,"hosting_domain":null,"hosting_grace_days":null,"hosting_suspension_enabled":null,"hosting_status":null}`, admin)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil {
		t.Fatalf("nullable columns: %s", w.Body.String())
	}
	for _, k := range []string{"sku", "description", "default_unit_price", "default_purchase_price", "revenue_account_id", "expense_account_id", "hosting_domain", "hosting_grace_days", "hosting_suspension_enabled", "hosting_status"} {
		if item[k] != nil {
			t.Errorf("%s not null: %v", k, item[k])
		}
	}
	for _, k := range []string{"name", "item_type", "default_currency", "is_sold", "is_purchased", "active", "is_hosting"} {
		result := callBody(id, `{"`+k+`":null}`, admin)
		if result.Code != 500 {
			t.Errorf("non-null %s: %d", k, result.Code)
		}
	}
	var accountIDs []int
	rows, err := pool.Query(context.Background(), `SELECT account_id FROM chart_of_accounts ORDER BY account_id LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var account int
		if err = rows.Scan(&account); err != nil {
			t.Fatal(err)
		}
		accountIDs = append(accountIDs, account)
	}
	rows.Close()
	if len(accountIDs) != 2 {
		t.Fatal("expected seeded accounts")
	}
	accountBody := fmt.Sprintf(`{"revenue_account_id":%d,"expense_account_id":%d}`, accountIDs[0], accountIDs[1])
	w = callBody(id, accountBody, admin)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil || item["revenue_account_id"] != float64(accountIDs[0]) || item["expense_account_id"] != float64(accountIDs[1]) {
		t.Fatalf("valid accounts: %s", w.Body.String())
	}
	for _, field := range []string{"revenue_account_id", "expense_account_id"} {
		w = callBody(id, `{"name":"invalid account", "`+field+`":2147483647}`, admin)
		if w.Code != 500 {
			t.Errorf("invalid account %s: %d", field, w.Code)
		}
		w = callBody(id, `{}`, admin)
		if json.Unmarshal(w.Body.Bytes(), &item) != nil || item["name"] != "all fields" || item["revenue_account_id"] != float64(accountIDs[0]) || item["expense_account_id"] != float64(accountIDs[1]) {
			t.Fatalf("account rollback: %s", w.Body.String())
		}
	}
	// Compare representative invalid request bodies through FastAPI and actual PATCH.
	for _, body := range []string{`{"name":1,"item_type":"BAD","hosting_grace_days":366}`, `{"default_unit_price":"0.00001"}`, `{"cloudflare_target":{}}`, "", `{`} {
		py := exec.Command("python", "-c", `import sys
from fastapi import FastAPI
from fastapi.testclient import TestClient
from app.schemas.item import ItemUpdate
a=FastAPI()
@a.patch("/")
def patch(item: ItemUpdate): return {}
r=TestClient(a).patch("/",content=sys.argv[1],headers={"content-type":"application/json"}); print(r.text)`, body)
		out, e := py.Output()
		if e != nil {
			t.Fatalf("Python validation: %v %s", e, out)
		}
		result := callBody(id, body, admin)
		var want, got any
		json.Unmarshal(out, &want)
		json.Unmarshal(result.Body.Bytes(), &got)
		if result.Code != 422 || !reflect.DeepEqual(want, got) {
			t.Errorf("validation %s: python %s go %s", body, out, result.Body.String())
		}
	}
	// Compare full output to Python service on the same row and identical starting state.
	py := exec.Command("python", "-c", `import os
from uuid import UUID
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.services.items import update_item
from app.schemas.item import ItemUpdate, ItemOut
engine=create_engine(os.environ["ITEM_TEST_DATABASE_URL"].replace("postgres://","postgresql+psycopg://",1))
with Session(engine) as db:
 item=update_item(db,UUID("10000000-0000-0000-0000-000000000002"),ItemUpdate(name="Python parity", description="hello", default_unit_price="15.2300", is_sold=False, revenue_account_id=int(os.environ["ACCOUNT_1"]), expense_account_id=int(os.environ["ACCOUNT_2"])))
 db.commit(); db.refresh(item); print(ItemOut.model_validate(item).model_dump_json())`)
	py.Env = append(os.Environ(), "ITEM_TEST_DATABASE_URL="+db.URL(), fmt.Sprintf("ACCOUNT_1=%d", accountIDs[0]), fmt.Sprintf("ACCOUNT_2=%d", accountIDs[1]))
	expected, err := py.CombinedOutput()
	if err != nil {
		t.Fatalf("Python fixture: %v %s", err, expected)
	}
	_, err = pool.Exec(context.Background(), `UPDATE items SET name='Inactive',description=NULL,default_unit_price=NULL,is_sold=true,revenue_account_id=NULL,expense_account_id=NULL WHERE item_id='10000000-0000-0000-0000-000000000002'`)
	if err != nil {
		t.Fatal(err)
	}
	w = callBody("10000000-0000-0000-0000-000000000002", fmt.Sprintf(`{"name":"Python parity","description":"hello","default_unit_price":"15.2300","is_sold":false,"revenue_account_id":%d,"expense_account_id":%d}`, accountIDs[0], accountIDs[1]), admin)
	var want, got map[string]any
	if err = json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("Python output: want %s got %s", expected, w.Body.String())
	}
	doc := httptest.NewRecorder()
	h.ServeHTTP(doc, httptest.NewRequest("GET", "/openapi.json", nil))
	var spec map[string]any
	if json.Unmarshal(doc.Body.Bytes(), &spec) != nil {
		t.Fatal("invalid OpenAPI")
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	updateSchema := schemas["ItemUpdateSchema"].(map[string]any)
	if required, ok := updateSchema["required"].([]any); ok && len(required) > 0 {
		t.Errorf("update fields required: %v", required)
	}
	props := updateSchema["properties"].(map[string]any)
	if props["default_unit_price"].(map[string]any)["anyOf"] == nil || props["hosting_status"].(map[string]any)["enum"] == nil {
		t.Errorf("update schema: %v", props)
	}
	for _, body := range []string{`{"default_unit_price":NaN}`, `{"default_unit_price":1e-1000000000}`, `{"hosting_grace_days":1e1000000000}`, `{"active":1e1000000000}`} {
		result := callBody(id, body, admin)
		if result.Code != 422 {
			t.Errorf("unsafe number %s: %d %s", body, result.Code, result.Body.String())
		}
	}
	nullable := func(prop map[string]any) bool {
		if typ, ok := prop["type"].([]any); ok && containsValue(typ, "null") {
			return true
		}
		if variants, ok := prop["anyOf"].([]any); ok {
			for _, v := range variants {
				if v.(map[string]any)["type"] == "null" {
					return true
				}
			}
		}
		return false
	}
	for _, field := range []string{"sku", "name", "item_type", "description", "default_currency", "default_unit_price", "default_purchase_price", "revenue_account_id", "expense_account_id", "is_sold", "is_purchased", "active", "is_hosting", "hosting_domain", "hosting_grace_days", "hosting_suspension_enabled", "hosting_status", "cloudflare_target"} {
		if !nullable(props[field].(map[string]any)) {
			t.Errorf("%s missing null in OpenAPI: %v", field, props[field])
		}
	}
	for _, field := range []string{"item_type", "hosting_status"} {
		enum := props[field].(map[string]any)["enum"].([]any)
		hasNull := false
		for _, v := range enum {
			if v == nil {
				hasNull = true
			}
		}
		if !hasNull {
			t.Errorf("%s enum excludes null", field)
		}
	}
	targetSchema := schemas["ItemCreateTargetSchema"].(map[string]any)
	targetRequired := targetSchema["required"].([]any)
	for _, field := range []string{"zone_id", "record_id", "record_name", "record_type", "live_content", "maintenance_content"} {
		if !containsValue(targetRequired, field) {
			t.Errorf("nested target missing required %s", field)
		}
	}
	if containsValue(targetRequired, "proxied") {
		t.Error("target proxied must be optional")
	}
	if nullable(targetSchema["properties"].(map[string]any)["proxied"].(map[string]any)) {
		t.Error("target proxied must reject null")
	}
	paths := spec["paths"].(map[string]any)
	patch := paths["/api/v1/items/{item_id}"].(map[string]any)["patch"].(map[string]any)
	if patch["security"] == nil || patch["requestBody"] == nil || patch["responses"] == nil {
		t.Fatalf("incomplete OpenAPI: %v", patch)
	}
	// A held row lock must block PATCH; simultaneous disjoint writes must survive.
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `SELECT item_id FROM items WHERE item_id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callBody(id, `{"name":"concurrent"}`, admin) }()
	select {
	case <-done:
		t.Fatal("PATCH bypassed row lock")
	case <-time.After(150 * time.Millisecond):
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := <-done; result.Code != 200 {
		t.Fatalf("blocked PATCH: %s", result.Body.String())
	}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, body := range []string{`{"name":"parallel"}`, `{"description":"parallel description"}`} {
		wg.Add(1)
		go func(body string) { defer wg.Done(); results <- callBody(id, body, admin) }(body)
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result.Code != 200 {
			t.Fatalf("concurrent update: %s", result.Body.String())
		}
	}
	w = callBody(id, `{}`, admin)
	if json.Unmarshal(w.Body.Bytes(), &item) != nil || item["name"] != "parallel" || item["description"] != "parallel description" {
		t.Fatalf("lost update: %s", w.Body.String())
	}
	other := "10000000-0000-0000-0000-000000000003"
	w = callBody(other, `{"cloudflare_target":{"zone_id":" z ","record_id":" r ","record_name":" EXAMPLE.COM ","record_type":"a","live_content":" 1 ","maintenance_content":" 2 "}}`, admin)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil || item["name"] != "Other" || item["cloudflare_target"].(map[string]any)["proxied"] != true {
		t.Fatalf("target create: %d %s", w.Code, w.Body.String())
	}
	deleted := token("00000000-0000-0000-0000-000000000099")
	if result := callBody(id, `{}`, deleted); result.Code != 403 {
		t.Fatalf("deleted user: %d", result.Code)
	}
	_, err = pool.Exec(context.Background(), `CREATE FUNCTION fail_target() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced'; END $$; CREATE TRIGGER fail_target BEFORE UPDATE ON cloudflare_targets FOR EACH ROW EXECUTE FUNCTION fail_target()`)
	if err != nil {
		t.Fatal(err)
	}
	w = callBody(id, `{"name":"must rollback","cloudflare_target":{"zone_id":"z","record_id":"r","record_name":"n","record_type":"a","live_content":"1","maintenance_content":"2"}}`, admin)
	if w.Code != 500 {
		t.Fatalf("forced failure: %d %s", w.Code, w.Body.String())
	}
	var name string
	if err = pool.QueryRow(context.Background(), `SELECT name FROM items WHERE item_id=$1`, id).Scan(&name); err != nil || name != "parallel" {
		t.Fatalf("rollback: %s %v", name, err)
	}

}
