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

func TestCustomerBalancesPostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	config := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	db := pgtestdb.Custom(t, config, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pydb := pgtestdb.Custom(t, config, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	pp, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user');
 INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'),('10000000-0000-0000-0000-000000000002','B');
 INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,due_date) VALUES
 ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'OPEN',false,$1::date-1),
 ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','MILESTONE','USD',20,20,'SENT',false,$1::date),
 ('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','MILESTONE','USD',30,30,'PARTIAL',false,$1::date+1),
 ('20000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001','MILESTONE','IDR',40,40,'OPEN',false,NULL),
 ('20000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000002','MILESTONE','USD',50,50,'SENT',false,$1::date-1),
 ('20000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000001','MILESTONE','USD',99,99,'DRAFT',false,$1::date-1),
 ('20000000-0000-0000-0000-000000000007','10000000-0000-0000-0000-000000000001','MILESTONE','USD',99,99,'PAID',false,$1::date-1),
 ('20000000-0000-0000-0000-000000000008','10000000-0000-0000-0000-000000000001','MILESTONE','USD',99,99,'VOID',false,$1::date-1),
 ('20000000-0000-0000-0000-000000000009','10000000-0000-0000-0000-000000000001','MILESTONE','USD',99,99,'OPEN',true,$1::date-1),
 ('20000000-0000-0000-0000-000000000010','10000000-0000-0000-0000-000000000001','MILESTONE','USD',0,0,'OPEN',false,$1::date-1),
 ('20000000-0000-0000-0000-000000000011','10000000-0000-0000-0000-000000000001','MILESTONE','USD',-2,-2,'OPEN',false,$1::date-1),
 ('20000000-0000-0000-0000-000000000012',NULL,'MILESTONE','USD',99,99,'OPEN',false,$1::date-1)`
	today := time.Now().Format("2006-01-02")
	for _, p := range []*pgxpool.Pool{pool, pp} {
		if _, e = p.Exec(context.Background(), strings.ReplaceAll(seed, "$1", "'"+today+"'")); e != nil {
			t.Fatal(e)
		}
	}
	snapshot := func(p *pgxpool.Pool) string {
		var s string
		e := p.QueryRow(context.Background(), `SELECT jsonb_build_object('invoices',(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i),'customers',(SELECT jsonb_agg(to_jsonb(c) ORDER BY customer_id) FROM customers c),'users',(SELECT jsonb_agg(to_jsonb(u) ORDER BY user_id) FROM users u))::text`).Scan(&s)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	before, beforePy := snapshot(pool), snapshot(pp)
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
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
	deleted := token("00000000-0000-0000-0000-000000000003")
	call := func(c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/customers/balances", nil)
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
	for _, tc := range []struct {
		c    *http.Cookie
		code int
	}{{nil, 401}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}, {user, 403}, {deleted, 403}} {
		w := call(tc.c)
		if w.Code != tc.code {
			t.Fatalf("auth: %d %s", w.Code, w.Body.String())
		}
	}
	w := call(admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var rows []map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &rows); e != nil {
		t.Fatal(e)
	}
	got := map[string]string{}
	for _, row := range rows {
		got[row["customer_id"].(string)+":"+row["currency"].(string)] = row["balance"].(string) + ":" + row["overdue"].(string)
	}
	want := map[string]string{"10000000-0000-0000-0000-000000000001:USD": "60.0000:10.0000", "10000000-0000-0000-0000-000000000001:IDR": "40.0000:0", "10000000-0000-0000-0000-000000000002:USD": "50.0000:50.0000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	py := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.customers import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
    r=c.get('/api/v1/customers/balances'); print(json.dumps([r.status_code,sorted(r.json(),key=lambda x:(x['customer_id'],x['currency']))]))`)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "DB="+pydb.URL())
	out, e := py.Output()
	if e != nil {
		t.Fatalf("python %v %s", e, out)
	}
	var expected []json.RawMessage
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	var pyRows []map[string]any
	json.Unmarshal(expected[1], &pyRows)
	pyGot := map[string]string{}
	for _, row := range pyRows {
		pyGot[row["customer_id"].(string)+":"+row["currency"].(string)] = row["balance"].(string) + ":" + row["overdue"].(string)
	}
	if !reflect.DeepEqual(got, pyGot) {
		t.Fatalf("Go %#v Python %#v", got, pyGot)
	}
	if snapshot(pool) != before || snapshot(pp) != beforePy {
		t.Fatal("database changed")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	op := spec["paths"].(map[string]any)["/api/v1/customers/balances"].(map[string]any)["get"].(map[string]any)
	if op["security"] == nil {
		t.Fatal("security schema missing")
	}
	responses := op["responses"].(map[string]any)
	for _, code := range []string{"200", "401", "403", "500"} {
		if responses[code] == nil {
			t.Errorf("missing response %s", code)
		}
	}
	schema := responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["type"] != "array" {
		t.Errorf("not an array schema: %#v", schema)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE invoices SET balance_due='NaN' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if w = call(admin); w.Code != 500 || strings.Contains(w.Body.String(), "NaN") {
		t.Fatalf("invalid numeric: %d %s", w.Code, w.Body.String())
	}
	if _, err := pool.Exec(context.Background(), `UPDATE invoices SET balance_due=10 WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM invoices`); err != nil {
		t.Fatal(err)
	}
	if w = call(admin); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty: %d %s", w.Code, w.Body.String())
	}
}
