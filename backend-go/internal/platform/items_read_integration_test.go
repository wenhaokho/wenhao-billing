package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
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

func TestItemReadPostgres(t *testing.T) {
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
	_, err = pool.Exec(context.Background(), `INSERT INTO users (user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO items(item_id,name,item_type,default_currency,default_unit_price,is_sold,is_purchased,active,is_hosting,hosting_domain,hosting_grace_days,hosting_suspension_enabled,hosting_status) VALUES ('10000000-0000-0000-0000-000000000001','Zebra','SERVICE','IDR',12.3400,true,false,true,true,'example.com',3,true,'ACTIVE'),('10000000-0000-0000-0000-000000000002','Alpha','USAGE','USD',NULL,true,false,false,false,NULL,NULL,NULL,NULL); INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,proxied) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','private-zone','rec','example.com','A','1.2.3.4','2.3.4.5',true)`)
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
	py := exec.Command("python", "-c", `
import os
from datetime import datetime
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.schemas.item import ItemCreate, ItemOut
from app.services.items import create_item
engine = create_engine(os.environ["ITEM_TEST_DATABASE_URL"].replace("postgres://", "postgresql+psycopg://", 1))
with Session(engine) as db:
    item = create_item(db, ItemCreate(name="Python hosting", item_type="SERVICE", default_currency="IDR", default_unit_price="123.4500", is_hosting=True, hosting_domain=" EXAMPLE.ORG ", hosting_grace_days=4, hosting_suspension_enabled=True, cloudflare_target={"zone_id":" zone ", "record_id":" record ", "record_name":" EXAMPLE.ORG ", "record_type":"a", "live_content":" 1.2.3.4 ", "maintenance_content":" 2.3.4.5 "}))
    item.created_at = datetime(2025, 1, 2, 3, 4, 5)
    item.hosting_last_paid_at = datetime(2025, 1, 2, 3, 4, 5, 100000)
    item.hosting_last_action_at = datetime(2025, 1, 2, 3, 4, 5, 123400)
    item.cloudflare_target.last_action_at = datetime(2025, 1, 2, 3, 4, 5, 1)
    item.cloudflare_target.created_at = datetime(2025, 1, 2, 3, 4, 5, 123456)
    item.cloudflare_target.updated_at = datetime(2025, 1, 2, 3, 4, 5, 370900)
    db.commit()
    db.refresh(item)
    print(ItemOut.model_validate(item).model_dump_json())
`)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "ITEM_TEST_DATABASE_URL="+db.URL())
	expected, err := py.CombinedOutput()
	if err != nil {
		t.Fatalf("Python fixture: %v %s", err, expected)
	}
	var pythonItem map[string]any
	if err := json.Unmarshal(expected, &pythonItem); err != nil {
		t.Fatal(err)
	}
	admin := token("00000000-0000-0000-0000-000000000001")
	user := token("00000000-0000-0000-0000-000000000002")
	call := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("no-store missing")
		}
		return w
	}
	pythonResponse := call("/api/v1/items/"+pythonItem["item_id"].(string), admin)
	var goItem map[string]any
	if pythonResponse.Code != 200 || json.Unmarshal(pythonResponse.Body.Bytes(), &goItem) != nil || !reflect.DeepEqual(pythonItem, goItem) {
		t.Fatalf("Python parity: python=%s go=%s", expected, pythonResponse.Body.String())
	}
	listResponse := call("/api/v1/items", admin)
	var listRows []map[string]any
	if listResponse.Code != 200 || json.Unmarshal(listResponse.Body.Bytes(), &listRows) != nil {
		t.Fatalf("item list: %d %s", listResponse.Code, listResponse.Body.String())
	}
	found := false
	for _, row := range listRows {
		if row["item_id"] == pythonItem["item_id"] {
			found = true
			if !reflect.DeepEqual(row, pythonItem) {
				t.Errorf("Python list parity: python=%s go=%v", expected, row)
			}
		}
	}
	if !found {
		t.Fatal("Python fixture missing from list")
	}
	// Keep the fixed filter expectations focused on the two hand-built fixtures.
	if _, err := pool.Exec(context.Background(), `DELETE FROM items WHERE item_id=$1`, pythonItem["item_id"]); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/items", "/api/v1/items/10000000-0000-0000-0000-000000000001"} {
		for _, tc := range []struct {
			c    *http.Cookie
			code int
		}{{nil, 401}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}, {user, 403}} {
			w := call(path, tc.c)
			if w.Code != tc.code || strings.Contains(w.Body.String(), "private-zone") {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
		}
	}
	// Compare repeated scalar query parameters against the actual FastAPI router.
	pyFilter := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.items import router
from app.api.deps import current_admin
from app.db.session import get_db
engine = create_engine(os.environ["ITEM_TEST_DATABASE_URL"].replace("postgres://", "postgresql+psycopg://", 1))
app = FastAPI()
app.include_router(router, prefix="/api/v1")
app.dependency_overrides[current_admin] = lambda: object()
def db_session():
    with Session(engine) as db:
        yield db
app.dependency_overrides[get_db] = db_session
with TestClient(app) as client:
    r = client.get("/api/v1/items?item_type=SERVICE&item_type=USAGE")
    assert r.status_code == 200, r.text
    print(json.dumps([item["name"] for item in r.json()]))
`)
	pyFilter.Dir = "../../../backend"
	pyFilter.Env = append(os.Environ(), "ITEM_TEST_DATABASE_URL="+db.URL())
	pythonNames, err := pyFilter.Output()
	if err != nil {
		t.Fatalf("Python duplicate query: %v", err)
	}
	var expectedNames []string
	if err := json.Unmarshal(pythonNames, &expectedNames); err != nil {
		t.Fatal(err)
	}
	wDuplicate := call("/api/v1/items?item_type=SERVICE&item_type=USAGE", admin)
	var duplicateRows []map[string]any
	if wDuplicate.Code != 200 || json.Unmarshal(wDuplicate.Body.Bytes(), &duplicateRows) != nil {
		t.Fatalf("duplicate query: %d %s", wDuplicate.Code, wDuplicate.Body.String())
	}
	actualNames := []string{}
	for _, row := range duplicateRows {
		actualNames = append(actualNames, row["name"].(string))
	}
	if !reflect.DeepEqual(actualNames, expectedNames) {
		t.Errorf("duplicate item_type: Python %v, Go %v", expectedNames, actualNames)
	}
	for _, tc := range []struct {
		path  string
		names string
	}{{"/api/v1/items", "Alpha,Zebra"}, {"/api/v1/items?active=true", "Zebra"}, {"/api/v1/items?active=false", "Alpha"}, {"/api/v1/items?item_type=USAGE", "Alpha"}, {"/api/v1/items?is_hosting=true", "Zebra"}, {"/api/v1/items?is_hosting=false", "Alpha"}, {"/api/v1/items?item_type=SERVICE&active=false", ""}} {
		w := call(tc.path, admin)
		var rows []map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		names := []string{}
		for _, row := range rows {
			names = append(names, row["name"].(string))
		}
		if strings.Join(names, ",") != tc.names {
			t.Errorf("%s got %v", tc.path, names)
		}
	}
	w := call("/api/v1/items/10000000-0000-0000-0000-000000000001", admin)
	var item map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if item["default_unit_price"] != "12.3400" || item["default_purchase_price"] != nil || item["hosting_domain"] != "example.com" || item["cloudflare_target"].(map[string]any)["zone_id"] != "private-zone" {
		t.Fatalf("unexpected item: %s", w.Body.String())
	}
	for _, tc := range []struct {
		path string
		code int
	}{{"/api/v1/items/10000000-0000-0000-0000-000000000099", 404}, {"/api/v1/items/invalid", 422}, {"/api/v1/items?active=maybe", 422}} {
		if w := call(tc.path, admin); w.Code != tc.code {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	w = call("/api/v1/items/invalid", admin)
	var validation map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &validation); err != nil {
		t.Fatal(err)
	}
	var expectedValidation map[string]any
	if err := json.Unmarshal([]byte(`{"detail":[{"type":"uuid_parsing","loc":["path","item_id"],"msg":"Input should be a valid UUID, invalid character: found `+"`"+`i`+"`"+` at 0","input":"invalid","ctx":{"error":"invalid character: found `+"`"+`i`+"`"+` at 0"}}]}`), &expectedValidation); err != nil {
		t.Fatal(err)
	}
	if w.Code != 422 || !reflect.DeepEqual(validation, expectedValidation) {
		t.Fatalf("invalid UUID contract: %d %s", w.Code, w.Body.String())
	}
	for _, field := range []string{"hosting_last_paid_at", "hosting_last_action_at"} {
		if _, err = pool.Exec(context.Background(), `UPDATE items SET `+field+`='infinity' WHERE item_id='10000000-0000-0000-0000-000000000001'`); err != nil {
			t.Fatal(err)
		}
		w := call("/api/v1/items/10000000-0000-0000-0000-000000000001", admin)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
			t.Fatalf("item timestamp %s: %d %s", field, w.Code, w.Body)
		}
		if _, err = pool.Exec(context.Background(), `UPDATE items SET `+field+`=NULL WHERE item_id='10000000-0000-0000-0000-000000000001'`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(context.Background(), `UPDATE cloudflare_targets SET last_action_at='-infinity' WHERE item_id='10000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	w = call("/api/v1/items/10000000-0000-0000-0000-000000000001", admin)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
		t.Fatalf("target timestamp: %d %s", w.Code, w.Body)
	}
	if _, err = pool.Exec(context.Background(), `UPDATE cloudflare_targets SET last_action_at=NULL WHERE item_id='10000000-0000-0000-0000-000000000001'; UPDATE items SET created_at='infinity' WHERE item_id='10000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		r := httptest.NewRequest(method, "/api/v1/items/10000000-0000-0000-0000-000000000001", strings.NewReader(`{"name":"Changed"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(admin)
		wr := httptest.NewRecorder()
		h.ServeHTTP(wr, r)
		if wr.Code != 500 || !strings.Contains(wr.Body.String(), "internal server error") {
			t.Fatalf("item %s timestamp: %d %s", method, wr.Code, wr.Body)
		}
		var name string
		var active bool
		if e := pool.QueryRow(context.Background(), `SELECT name,active FROM items WHERE item_id='10000000-0000-0000-0000-000000000001'`).Scan(&name, &active); e != nil || name != "Zebra" || !active {
			t.Fatalf("item %s committed: %s %v %v", method, name, active, e)
		}
	}
	if _, err = pool.Exec(context.Background(), `CREATE FUNCTION invalid_item_stamp() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.created_at := 'infinity'; RETURN NEW; END $$; CREATE TRIGGER invalid_item_stamp BEFORE INSERT ON items FOR EACH ROW EXECUTE FUNCTION invalid_item_stamp()`); err != nil {
		t.Fatal(err)
	}
	cr := httptest.NewRequest("POST", "/api/v1/items", strings.NewReader(`{"name":"New invalid item","item_type":"SERVICE","default_currency":"USD"}`))
	cr.Header.Set("Content-Type", "application/json")
	cr.AddCookie(admin)
	cw := httptest.NewRecorder()
	h.ServeHTTP(cw, cr)
	if cw.Code != 500 || !strings.Contains(cw.Body.String(), "internal server error") {
		t.Fatalf("item create timestamp: %d %s", cw.Code, cw.Body)
	}
	var count int
	if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM items WHERE name='New invalid item'`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("item create committed: %d %v", count, e)
	}
	_, err = pool.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	if w := call("/api/v1/items", admin); w.Code != 403 {
		t.Fatalf("demoted user: %d", w.Code)
	}
}

func TestItemReadOpenAPI(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &doc) != nil {
		t.Fatalf("openapi: %d %s", w.Code, w.Body.String())
	}
	paths := doc["paths"].(map[string]any)
	for _, path := range []string{"/api/v1/items", "/api/v1/items/{item_id}"} {
		if _, ok := paths[path].(map[string]any)["get"]; !ok {
			t.Errorf("GET missing: %s", path)
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	item := schemas["ItemOutputSchema"].(map[string]any)
	props := item["properties"].(map[string]any)
	for name, want := range map[string]string{"item_id": "string", "default_unit_price": "string", "sku": "string", "hosting_grace_days": "integer", "is_hosting": "boolean"} {
		if got := props[name].(map[string]any)["type"]; got != want {
			if types, ok := got.([]any); !ok || len(types) != 2 || types[0] != want || types[1] != "null" {
				t.Errorf("%s type: %v", name, got)
			}
		}
	}
}

func TestItemReadOpenAPIContract(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	checkTime := func(schema, field string, nullable bool) {
		t.Helper()
		obj := schemas[schema].(map[string]any)
		prop := obj["properties"].(map[string]any)[field].(map[string]any)
		expected := any("string")
		if nullable {
			expected = []any{"string", "null"}
		}
		if !reflect.DeepEqual(prop["type"], expected) || prop["format"] != "date-time" {
			t.Errorf("%s.%s: %v", schema, field, prop)
		}
		if !nullable {
			required := obj["required"].([]any)
			if !containsValue(required, field) {
				t.Errorf("%s.%s not required", schema, field)
			}
		}
	}
	checkTime("ItemOutputSchema", "created_at", false)
	checkTime("ItemOutputSchema", "hosting_last_paid_at", true)
	checkTime("ItemOutputSchema", "hosting_last_action_at", true)
	checkTime("ItemTargetSchema", "created_at", false)
	checkTime("ItemTargetSchema", "updated_at", false)
	checkTime("ItemTargetSchema", "last_action_at", true)
	item := schemas["ItemOutputSchema"].(map[string]any)["properties"].(map[string]any)
	for field, want := range map[string][]any{"item_type": {"SERVICE", "USAGE", "FIXED_FEE"}, "hosting_status": {"ACTIVE", "SUSPEND_PENDING", "SUSPENDED", "CANCELLED", nil}} {
		p := item[field].(map[string]any)
		if !reflect.DeepEqual(p["enum"], want) {
			t.Errorf("%s enum: %v", field, p["enum"])
		}
	}
	if got := item["hosting_status"].(map[string]any)["type"]; !reflect.DeepEqual(got, []any{"string", "null"}) {
		t.Errorf("hosting_status nullable: %v", got)
	}
}
func containsValue(values []any, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
