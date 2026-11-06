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

func TestBillReadsPostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	db := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pydb := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	pp, e := pgxpool.New(context.Background(), pydb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO vendors(vendor_id,name,default_currency) VALUES ('10000000-0000-0000-0000-000000000001','A','USD'),('10000000-0000-0000-0000-000000000002','B','IDR'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); INSERT INTO projects(project_id,customer_id,code,name,currency,status) VALUES ('50000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','P','Project','USD','ACTIVE'); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status,created_at) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','USD',123.45,20,'OPEN','2022-01-01'),('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000002','IDR',100,100,'DRAFT','2023-01-01'),('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','USD',0,0,'PAID','2021-01-01'),('20000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001','USD',12,5,'PARTIAL','2020-01-01'),('20000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001','USD',12,0,'VOID','2019-01-01'); UPDATE bills SET project_id='50000000-0000-0000-0000-000000000001', bill_number='B1',po_number='PO',subtotal=100,discount_type='AMOUNT',discount_value=3,issue_date='2024-01-02',due_date='2024-02-03',payment_terms='NET30',notes='notes' WHERE bill_id='20000000-0000-0000-0000-000000000001'; INSERT INTO bill_line_items(line_item_id,bill_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001',0,'line',2,10,20)`
	for _, pool := range []*pgxpool.Pool{p, pp} {
		if _, e = pool.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	snap := func(pool *pgxpool.Pool) string {
		var s string
		if e := pool.QueryRow(context.Background(), `SELECT jsonb_build_object('bills',(SELECT jsonb_agg(to_jsonb(b) ORDER BY bill_id) FROM bills b),'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM bill_line_items l))::text`).Scan(&s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	before, beforepy := snap(p), snap(pp)
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	tok := func(id string) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	admin := tok("00000000-0000-0000-0000-000000000001")
	user := tok("00000000-0000-0000-0000-000000000002")
	call := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("no-store")
		}
		return w
	}
	for _, tc := range []struct {
		c    *http.Cookie
		code int
	}{{nil, 401}, {user, 403}, {tok("00000000-0000-0000-0000-000000000003"), 403}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}} {
		w := call("/api/v1/bills", tc.c)
		if w.Code != tc.code {
			t.Fatalf("auth got %d %s", w.Code, w.Body)
		}
	}
	paths := []string{"/api/v1/bills", "/api/v1/bills?status=OPEN&status=DRAFT", "/api/v1/bills?status=BOGUS", "/api/v1/bills?status=PARTIAL&status=PAID&status=VOID", "/api/v1/bills?vendor_id=10000000-0000-0000-0000-000000000002&vendor_id=10000000-0000-0000-0000-000000000001", "/api/v1/bills?status=", "/api/v1/bills?vendor_id=10000000-0000-0000-0000-000000000001", "/api/v1/bills?vendor_id=bad", "/api/v1/bills/20000000-0000-0000-0000-000000000001", "/api/v1/bills/20000000-0000-0000-0000-000000000099", "/api/v1/bills/bad"}
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.bills import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
 print(json.dumps([[r.status_code,r.json()] for p in json.loads(os.environ['PATHS']) for r in [c.get(p)]]))`)
	cmd.Dir = "../../../backend"
	b, _ := json.Marshal(paths)
	cmd.Env = append(os.Environ(), "DB="+pydb.URL(), "PATHS="+string(b))
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	for i, path := range paths {
		w := call(path, admin)
		var body any
		if e = json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatalf("%s %d %s", path, w.Code, w.Body)
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(body, expected[i][1]) {
			t.Errorf("%s go=%d %#v python=%#v", path, w.Code, body, expected[i])
		}
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	ops := doc["paths"].(map[string]any)
	for _, path := range []string{"/api/v1/bills", "/api/v1/bills/{bill_id}"} {
		op := ops[path].(map[string]any)["get"].(map[string]any)
		if op["security"] == nil {
			t.Errorf("missing security %s", path)
		}
		responses := op["responses"].(map[string]any)
		for _, code := range []string{"200", "401", "403", "422", "500"} {
			if responses[code] == nil {
				t.Errorf("missing response %s %s", path, code)
			}
		}
	}
	list := ops["/api/v1/bills"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if list["type"] != "array" {
		t.Errorf("list schema %#v", list)
	}
	if before != snap(p) || beforepy != snap(pp) {
		t.Fatal("reads changed database")
	}
	for _, stamp := range []string{"infinity", "-infinity", "10000-01-01", "0001-01-01 BC"} {
		if _, e = p.Exec(context.Background(), `UPDATE bills SET created_at=$1::timestamp WHERE bill_id='20000000-0000-0000-0000-000000000001'`, stamp); e != nil {
			t.Fatal(e)
		}
		w := call("/api/v1/bills/20000000-0000-0000-0000-000000000001", admin)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
			t.Fatalf("timestamp %s: %d %s", stamp, w.Code, w.Body)
		}
	}
	if _, e = p.Exec(context.Background(), `UPDATE bills SET created_at='2024-01-01' WHERE bill_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(context.Background(), `UPDATE bills SET amount='NaN' WHERE bill_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/bills/20000000-0000-0000-0000-000000000001", admin); w.Code != 500 || strings.Contains(w.Body.String(), "NaN") {
		t.Fatalf("invalid numeric: %d %s", w.Code, w.Body)
	}
	if _, e = p.Exec(context.Background(), `DELETE FROM bills`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/bills", admin); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty: %d %s", w.Code, w.Body)
	}
}
