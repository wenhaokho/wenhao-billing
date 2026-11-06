package platform

import (
	"bytes"
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

func TestProjectSummaryPostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	db := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pydb := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
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
 INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Customer');
 INSERT INTO projects(project_id,customer_id,code,name,currency,contract_value,active) VALUES ('11000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P1','Project','USD',100,true),('11000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','P2','Other','IDR',NULL,true),('11000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','P3','Empty','IDR',NULL,true);
 INSERT INTO invoices(invoice_id,customer_id,project_id,invoice_type,currency,amount,balance_due,status,is_template,created_at,billing_cycle_ref) VALUES
 ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','MILESTONE','USD',10,5,'OPEN',false,'2025-01-01','{"nested":{"n":9007199254740993}}'),
 ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','RECURRING','IDR',20,10,'SENT',false,'2025-01-02',NULL),
 ('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','USAGE','USD',30,4,'PARTIAL',false,'2025-01-03',NULL),
 ('20000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','MILESTONE','USD',40,0,'PAID',false,'2025-01-04',NULL),
 ('20000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','MILESTONE','USD',50,50,'DRAFT',true,'2025-01-05',NULL),
 ('20000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','MILESTONE','USD',60,60,'VOID',false,'2025-01-06',NULL),
 ('20000000-0000-0000-0000-000000000007','10000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000002','MILESTONE','USD',999,999,'OPEN',false,'2025-01-07',NULL);
 INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001',0,'Line',2,5,10);
 INSERT INTO vendors(vendor_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Vendor');
 INSERT INTO bills(bill_id,vendor_id,project_id,currency,amount,balance_due,status) VALUES
 ('50000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','USD',2,2,'OPEN'),
 ('50000000-0000-0000-0000-000000000002','40000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','IDR',3,2,'PARTIAL'),
 ('50000000-0000-0000-0000-000000000003','40000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','USD',4,0,'PAID'),
 ('50000000-0000-0000-0000-000000000004','40000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','USD',500,500,'DRAFT'),
 ('50000000-0000-0000-0000-000000000005','40000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000001','USD',600,600,'VOID'),
 ('50000000-0000-0000-0000-000000000006','40000000-0000-0000-0000-000000000001','11000000-0000-0000-0000-000000000002','USD',999,999,'OPEN');`
	for _, p := range []*pgxpool.Pool{pool, pp} {
		if _, e = p.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	tok, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	user, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	call := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	snap := func() string {
		var v string
		e := pool.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(p) ORDER BY project_id) FROM projects p),(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i),(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM invoice_line_items l),(SELECT jsonb_agg(to_jsonb(b) ORDER BY bill_id) FROM bills b))::text`).Scan(&v)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	before := snap()
	py := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.projects import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
    print(json.dumps([c.get('/api/v1/projects/11000000-0000-0000-0000-000000000001/'+s).json() for s in ['summary','invoices']]))`)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "DB="+pydb.URL())
	python, e := py.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, python)
	}
	var expected []any
	dec := json.NewDecoder(bytes.NewReader(python))
	dec.UseNumber()
	if e = dec.Decode(&expected); e != nil {
		t.Fatal(e)
	}
	base := "/api/v1/projects/11000000-0000-0000-0000-000000000001/"
	for n, suffix := range []string{"summary", "invoices"} {
		w := call(base+suffix, tok)
		if w.Code != 200 {
			t.Fatalf("%s %d %s", suffix, w.Code, w.Body.String())
		}
		var got any
		d := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
		d.UseNumber()
		if e = d.Decode(&got); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, expected[n]) {
			t.Fatalf("%s mismatch Go %s Python %s", suffix, w.Body.String(), python)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("no-store missing")
		}
	}
	if snap() != before {
		t.Fatal("reads changed database")
	}
	for _, s := range []string{"summary", "invoices"} {
		if w := call(base+s, ""); w.Code != 401 {
			t.Fatalf("missing auth %d", w.Code)
		}
		if w := call(base+s, user); w.Code != 403 {
			t.Fatalf("user auth %d", w.Code)
		}
		if w := call(base+s, tok+"bad"); w.Code != 401 {
			t.Fatalf("bad auth %d", w.Code)
		}
		if w := call("/api/v1/projects/not-a-uuid/"+s, tok); w.Code != 422 {
			t.Fatalf("invalid id %d", w.Code)
		}
		if w := call("/api/v1/projects/11000000-0000-0000-0000-000000000099/"+s, tok); w.Code != 404 {
			t.Fatalf("missing project %d", w.Code)
		}
	}
	spec := call("/openapi.json", "")
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"summary", "invoices"} {
		p := doc["paths"].(map[string]any)["/api/v1/projects/{project_id}/"+s].(map[string]any)["get"].(map[string]any)
		if p["security"] == nil {
			t.Fatal("security missing")
		}
		for _, code := range []string{"200", "401", "403", "404", "422", "500"} {
			if p["responses"].(map[string]any)[code] == nil {
				t.Fatalf("missing response %s", code)
			}
		}
	}
	emptyBase := "/api/v1/projects/11000000-0000-0000-0000-000000000003/"
	if w := call(emptyBase+"invoices", tok); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty invoices %d %s", w.Code, w.Body.String())
	}
	if w := call(emptyBase+"summary", tok); w.Code != 200 || !strings.Contains(w.Body.String(), `"invoiced_total":"0"`) || !strings.Contains(w.Body.String(), `"contract_remaining":null`) {
		t.Fatalf("empty summary %d %s", w.Code, w.Body.String())
	}
	if _, e = pool.Exec(context.Background(), `UPDATE invoices SET amount='NaN' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"summary", "invoices"} {
		if w := call(base+s, tok); w.Code != 500 || strings.Contains(w.Body.String(), "NaN") {
			t.Fatalf("invalid numeric %s %d %s", s, w.Code, w.Body.String())
		}
	}

}
