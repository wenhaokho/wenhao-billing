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
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestItemCreatePostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`)
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/api/v1/items", strings.NewReader(`{"name":"Ordinary","item_type":"SERVICE","default_unit_price":"12.3400"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: v})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"default_unit_price":"12.3400"`) {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	call := func(body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/items", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Header().Get("Cache-Control") != "no-store" {
			t.Error("no-store missing")
		}
		return out
	}
	admin := &http.Cookie{Name: "session", Value: v}
	for _, body := range []string{
		`{"name":"x","item_type":"SERVICE","revenue_account_id":"true"}`,
		`{"name":"x","item_type":"SERVICE","revenue_account_id":1e1000000000}`,
		`{"name":"x","item_type":"SERVICE","expense_account_id":1e-1000000000}`,
		`{"name":"x","item_type":"SERVICE","hosting_grace_days":1e1000000000}`,
		`{"name":"x","item_type":"SERVICE","default_unit_price":1e-1000000000}`,
	} {
		if out := call(body, admin); out.Code == 201 {
			t.Errorf("extreme exponent accepted: %s", body)
		}
	}
	if out := call(`{}`, nil); out.Code != 401 {
		t.Errorf("missing session: %d", out.Code)
	}
	if out := call(`{}`, &http.Cookie{Name: "session", Value: v + "x"}); out.Code != 401 {
		t.Errorf("tampered session: %d", out.Code)
	}
	_, e = pool.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if e != nil {
		t.Fatal(e)
	}
	if out := call(`{}`, admin); out.Code != 403 {
		t.Errorf("non-admin: %d", out.Code)
	}
	_, e = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE user_id='00000000-0000-0000-0000-000000000001'`)
	if e != nil {
		t.Fatal(e)
	}
	if out := call(`{"name":"x","item_type":"SERVICE"} {}`, admin); out.Code != 422 {
		t.Errorf("trailing JSON: %d %s", out.Code, out.Body)
	}
	if out := call(`{"name":"x","item_type":"SERVICE","revenue_account_id":2147483648}`, admin); out.Code != 500 {
		t.Errorf("integer database boundary: %d %s", out.Code, out.Body)
	}
	for _, body := range []string{`[]`, `123`, `null`} {
		out := call(body, admin)
		want := `{"detail":[{"type":"model_attributes_type","loc":["body"],"msg":"Input should be a valid dictionary or object to extract fields from","input":` + body + `}]}`
		var got, w any
		json.Unmarshal(out.Body.Bytes(), &got)
		json.Unmarshal([]byte(want), &w)
		if out.Code != 422 || !reflect.DeepEqual(got, w) {
			t.Errorf("non-object: %s: %d %s", body, out.Code, out.Body)
		}
	}
	if out := call(`{"name":"Huge unknown","item_type":"SERVICE","unknown":1e400}`, admin); out.Code != 201 {
		t.Errorf("large ignored number: %d %s", out.Code, out.Body)
	}
	if out := call(`{"name":9007199254740993,"item_type":"SERVICE"}`, admin); out.Code != 422 || !strings.Contains(out.Body.String(), `9007199254740993`) {
		t.Errorf("integer error preserves input: %d %s", out.Code, out.Body)
	}
	for _, body := range []string{
		`{}`, `{"name":"","item_type":"SERVICE"}`, `{"name":"x","item_type":"BAD"}`,
		`{"name":"x","item_type":"SERVICE","default_currency":"US"}`,
		`{"name":"x","item_type":"SERVICE","is_hosting":true}`,
		`{"name":"x","item_type":"SERVICE","default_unit_price":"0.00001"}`,
		`{"name":"x","item_type":"SERVICE","default_purchase_price":"0.00001"}`,
		`{"name":"x","item_type":"SERVICE","default_unit_price":"1000000000000000"}`,
		`{"name":"x","item_type":"SERVICE","hosting_grace_days":366}`,
		`{"name":"x","item_type":"SERVICE","cloudflare_target":{"zone_id":"","record_id":"r","record_name":"r","record_type":"A","live_content":"1","maintenance_content":"2"}}`,
	} {
		if out := call(body, admin); out.Code != 422 {
			t.Errorf("validation %s: %d %s", body, out.Code, out.Body)
		}
	}

	for _, tc := range []struct{ body, want string }{
		{`{"name":"","item_type":"SERVICE"}`, `{"detail":[{"type":"string_too_short","loc":["body","name"],"msg":"String should have at least 1 character","input":"","ctx":{"min_length":1}}]}`},
		{`{"name":"x","item_type":"SERVICE","default_unit_price":"0.00001"}`, `{"detail":[{"type":"decimal_max_places","loc":["body","default_unit_price"],"msg":"Decimal input should have no more than 4 decimal places","input":"0.00001","ctx":{"decimal_places":4}}]}`},
		{`{"name":"x","item_type":"SERVICE","default_unit_price":"1000000000000000"}`, `{"detail":[{"type":"decimal_whole_digits","loc":["body","default_unit_price"],"msg":"Decimal input should have no more than 15 digits before the decimal point","input":"1000000000000000","ctx":{"whole_digits":15}}]}`},
		{`{"name":"x","item_type":"SERVICE","hosting_grace_days":366}`, `{"detail":[{"type":"less_than_equal","loc":["body","hosting_grace_days"],"msg":"Input should be less than or equal to 365","input":366,"ctx":{"le":365}}]}`},
		{`{"name":"x","item_type":"BAD"}`, `{"detail":[{"type":"literal_error","loc":["body","item_type"],"msg":"Input should be 'SERVICE', 'USAGE' or 'FIXED_FEE'","input":"BAD","ctx":{"expected":"'SERVICE', 'USAGE' or 'FIXED_FEE'"}}]}`},
		{`{"name":"x","item_type":"SERVICE","is_sold":null}`, `{"detail":[{"type":"bool_type","loc":["body","is_sold"],"msg":"Input should be a valid boolean","input":null}]}`},
		{`{"name":"x","item_type":"SERVICE","cloudflare_target":{}}`, `{"detail":[{"type":"missing","loc":["body","cloudflare_target","zone_id"],"msg":"Field required","input":{}},{"type":"missing","loc":["body","cloudflare_target","record_id"],"msg":"Field required","input":{}},{"type":"missing","loc":["body","cloudflare_target","record_name"],"msg":"Field required","input":{}},{"type":"missing","loc":["body","cloudflare_target","record_type"],"msg":"Field required","input":{}},{"type":"missing","loc":["body","cloudflare_target","live_content"],"msg":"Field required","input":{}},{"type":"missing","loc":["body","cloudflare_target","maintenance_content"],"msg":"Field required","input":{}}]}`},
		{`{"name":12,"item_type":"SERVICE"}`, `{"detail":[{"type":"string_type","loc":["body","name"],"msg":"Input should be a valid string","input":12}]}`},
		{`{"name":"","item_type":"BAD"}`, `{"detail":[{"type":"string_too_short","loc":["body","name"],"msg":"String should have at least 1 character","input":"","ctx":{"min_length":1}},{"type":"literal_error","loc":["body","item_type"],"msg":"Input should be 'SERVICE', 'USAGE' or 'FIXED_FEE'","input":"BAD","ctx":{"expected":"'SERVICE', 'USAGE' or 'FIXED_FEE'"}}]}`},
		{`{"name":"x","item_type":"SERVICE","cloudflare_target":{"zone_id":"","record_id":"x","record_name":"x","record_type":"A","live_content":"x","maintenance_content":"x"}}`, `{"detail":[{"type":"string_too_short","loc":["body","cloudflare_target","zone_id"],"msg":"String should have at least 1 character","input":"","ctx":{"min_length":1}}]}`},
		{`{}`, `{"detail":[{"type":"missing","loc":["body","name"],"msg":"Field required","input":{}},{"type":"missing","loc":["body","item_type"],"msg":"Field required","input":{}}]}`},
		{`{"name":"x","item_type":"SERVICE","is_hosting":true}`, `{"detail":[{"type":"value_error","loc":["body"],"msg":"Value error, hosting item requires: hosting_domain, hosting_grace_days, hosting_suspension_enabled, cloudflare_target","input":{"name":"x","item_type":"SERVICE","is_hosting":true},"ctx":{"error":"hosting item requires: hosting_domain, hosting_grace_days, hosting_suspension_enabled, cloudflare_target"}}]}`},
	} {
		out := call(tc.body, admin)
		var got, want any
		json.Unmarshal(out.Body.Bytes(), &got)
		json.Unmarshal([]byte(tc.want), &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("validation detail: got %s want %s", out.Body, tc.want)
		}
	}

	differential := []string{`{"name":"x","item_type":"SERVICE","hosting_grace_days":2147483648}`, `{"name":9007199254740993,"item_type":"SERVICE"}`, `{"name":"x","item_type":"SERVICE","default_unit_price":true}`, `{"name":"x","item_type":"SERVICE","default_unit_price":"-abc"}`, `{"name":"x","item_type":"SERVICE","hosting_grace_days":"1/1"}`, `{"name":"x","item_type":"SERVICE","hosting_grace_days":1.5}`, `{"name":"","item_type":"SERVICE","default_currency":"US","is_sold":null,"hosting_grace_days":366}`, `{"name":12,"item_type":"BAD","description":42,"hosting_domain":false}`, `{"name":"x","item_type":"SERVICE","is_sold":"wat","active":null,"hosting_grace_days":"bad"}`, `{"name":"","item_type":"SERVICE","is_sold":null}`, `{"name":"","item_type":"SERVICE","default_currency":"US","cloudflare_target":{"zone_id":"z","record_id":"r","record_name":"x","record_type":"A","live_content":"x","maintenance_content":"x","proxied":"bad"}}`, `{"name":"","item_type":"SERVICE","default_currency":"US"}`, `{"name":"x","item_type":"SERVICE","cloudflare_target":{"zone_id":"z","record_id":"r","record_name":"x","record_type":"A","live_content":"x","maintenance_content":"x","proxied":"bad"}}`, `{"name":"x","item_type":"SERVICE","cloudflare_target":{"zone_id":"z","record_id":"r","record_name":"x","record_type":"A","live_content":"x","maintenance_content":"x","proxied":null}}`, `{"name":"","item_type":"SERVICE","default_unit_price":"-1"}`, `{"name":12,"item_type":"SERVICE"}`, `{"name":"","item_type":"BAD"}`, `{"name":"x","item_type":"SERVICE","cloudflare_target":{"zone_id":"","record_id":"x","record_name":"x","record_type":"A","live_content":"x","maintenance_content":"x"}}`}
	for _, body := range differential {
		py := exec.Command("python", "-c", `import json,sys
from app.schemas.item import ItemCreate
from pydantic import ValidationError
try: ItemCreate.model_validate_json(sys.argv[1])
except ValidationError as e:
 errors=e.errors(include_url=False)
 for err in errors: err["loc"]=["body",*err["loc"]]
 print(json.dumps({"detail":errors},default=str))`, body)
		py.Dir = "../../../backend"
		output, err := py.CombinedOutput()
		if err != nil {
			t.Fatalf("Python validation fixture: %v %s", err, output)
		}
		var expected, actual any
		json.Unmarshal(output, &expected)
		out := call(body, admin)
		json.Unmarshal(out.Body.Bytes(), &actual)
		if out.Code != 422 || !reflect.DeepEqual(actual, expected) {
			t.Errorf("validation Python=%s Go=%s", output, out.Body)
		}
	}
	for _, field := range []string{"default_currency", "is_sold", "is_purchased", "active", "is_hosting"} {
		body := `{"name":"Null","item_type":"SERVICE","` + field + `":null}`
		if out := call(body, admin); out.Code != 422 {
			t.Errorf("null %s: %d %s", field, out.Code, out.Body)
		}
	}
	floatingBool := call(`{"name":"Float bool","item_type":"SERVICE","is_sold":1.0}`, admin)
	if floatingBool.Code != 201 {
		t.Errorf("float bool: %d %s", floatingBool.Code, floatingBool.Body)
	}
	coercion := call(`{"name":"Coercion","item_type":"SERVICE","is_sold":"yes","is_purchased":1,"hosting_grace_days":"3","default_unit_price":"1e2"}`, admin)
	var coerced map[string]any
	if coercion.Code != 201 || json.Unmarshal(coercion.Body.Bytes(), &coerced) != nil || coerced["is_sold"] != true || coerced["is_purchased"] != true || coerced["hosting_grace_days"] != float64(3) || coerced["default_unit_price"] != "100.0000" {
		t.Errorf("coercion: %d %s", coercion.Code, coercion.Body)
	}
	hosting := `{"name":"Hosting","item_type":"SERVICE","is_hosting":true,"hosting_domain":" EXAMPLE.COM ","hosting_grace_days":0,"hosting_suspension_enabled":false,"cloudflare_target":{"zone_id":" zone ","record_id":" id ","record_name":" EXAMPLE.COM ","record_type":"a","live_content":" 1 ","maintenance_content":" 2 "}}`
	out := call(hosting, admin)
	var obj map[string]any
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &obj) != nil {
		t.Fatalf("hosting: %d %s", out.Code, out.Body)
	}
	target := obj["cloudflare_target"].(map[string]any)
	if obj["hosting_domain"] != "example.com" || obj["hosting_status"] != "ACTIVE" || target["record_name"] != "example.com" || target["record_type"] != "A" || target["zone_id"] != "zone" || target["proxied"] != true {
		t.Errorf("normalization: %s", out.Body)
	}

	py := exec.Command("python", "-c", `import os
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.schemas.item import ItemCreate, ItemOut
from app.services.items import create_item
engine=create_engine(os.environ["ITEM_TEST_DATABASE_URL"].replace("postgres://", "postgresql+psycopg://", 1))
with Session(engine) as db:
 item=create_item(db,ItemCreate(name="Hosting",item_type="SERVICE",is_hosting=True,hosting_domain=" EXAMPLE.COM ",hosting_grace_days=0,hosting_suspension_enabled=False,cloudflare_target={"zone_id":" zone ","record_id":" id ","record_name":" EXAMPLE.COM ","record_type":"a","live_content":" 1 ","maintenance_content":" 2 "}))
 db.commit(); db.refresh(item); print(ItemOut.model_validate(item).model_dump_json())`)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "ITEM_TEST_DATABASE_URL="+db.URL())
	raw, e := py.CombinedOutput()
	if e != nil {
		t.Fatalf("Python fixture: %v %s", e, raw)
	}
	var expected map[string]any
	if e = json.Unmarshal(raw, &expected); e != nil {
		t.Fatal(e)
	}
	for _, v := range []map[string]any{obj, expected} {
		delete(v, "item_id")
		delete(v, "created_at")
		target := v["cloudflare_target"].(map[string]any)
		for _, key := range []string{"target_id", "item_id", "created_at", "updated_at"} {
			delete(target, key)
		}
	}
	if !reflect.DeepEqual(obj, expected) {
		t.Errorf("Python parity: Go %v Python %v", obj, expected)
	}
	_, e = pool.Exec(context.Background(), `ALTER TABLE cloudflare_targets ADD CONSTRAINT fail_insert CHECK (zone_id <> 'fail')`)
	if e != nil {
		t.Fatal(e)
	}
	out = call(strings.Replace(hosting, ` zone `, `fail`, 1), admin)
	if out.Code != 500 || strings.Contains(out.Body.String(), "fail_insert") {
		t.Errorf("target failure: %d %s", out.Code, out.Body)
	}
	var count int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM items WHERE name='Hosting'`).Scan(&count); e != nil || count != 2 {
		t.Errorf("rollback: count=%d err=%v", count, e)
	}
	doc := httptest.NewRecorder()
	h.ServeHTTP(doc, httptest.NewRequest("GET", "/openapi.json", nil))
	var spec map[string]any
	if json.Unmarshal(doc.Body.Bytes(), &spec) != nil {
		t.Fatal("invalid openapi")
	}

	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	create := schemas["ItemCreateSchema"].(map[string]any)
	props := create["properties"].(map[string]any)
	required := create["required"].([]any)
	for _, key := range []string{"name", "item_type"} {
		if !containsValue(required, key) {
			t.Errorf("create missing required %s", key)
		}
	}
	if containsValue(required, "is_sold") {
		t.Error("is_sold incorrectly required")
	}
	if got := props["item_type"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []any{"SERVICE", "USAGE", "FIXED_FEE"}) {
		t.Errorf("item_type enum: %v", got)
	}
	if got := props["default_unit_price"].(map[string]any)["anyOf"]; got == nil {
		t.Error("decimal schema must allow numeric and string input")
	}
	if got := props["name"].(map[string]any)["maxLength"]; got != float64(255) {
		t.Errorf("name maxLength: %v", got)
	}
	if _, ok := spec["paths"].(map[string]any)["/api/v1/items"].(map[string]any)["post"]; !ok {
		t.Error("POST absent from openapi")
	}

}
