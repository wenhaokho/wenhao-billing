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
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInvoiceReadsDifferential(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'),('10000000-0000-0000-0000-000000000002','B'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,created_at,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'DRAFT',false,'2025-01-01','{"n":9007199254740993}'),('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000002','RECURRING','IDR',20,20,'OPEN',false,'2025-01-02',NULL),('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','USAGE','USD',30,0,'PAID',false,'2025-01-03',NULL),('20000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001','RECURRING','USD',40,40,'DRAFT',true,'2025-01-04',NULL),('20000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000001','MILESTONE','USD',40,40,'SENT',false,'2025-01-05',NULL),('20000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000001','USAGE','USD',40,20,'PARTIAL',false,'2025-01-06',NULL),('20000000-0000-0000-0000-000000000007','10000000-0000-0000-0000-000000000002','MILESTONE','USD',40,0,'VOID',false,'2025-01-07',NULL); INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001',0,'Test',2,5,10)`
	seed += `; INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,created_at,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000008','10000000-0000-0000-0000-000000000002','MILESTONE','USD',25,25,'OPEN',true,'2025-01-08','{"nested":{"n":9007199254740993}}'); INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000008','20000000-0000-0000-0000-000000000008',0,'Template line',5,5,25)`
	seed += `; INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,created_at,billing_cycle_ref,issue_date) VALUES
 ('20000000-0000-0000-0000-000000000010',NULL,'RECURRING','SGD',123.4567,0,'DRAFT',true,'2025-01-10','{"frequency":"MONTHLY","start_date":"2025-01-31","end_mode":"NEVER","paused":true}',NULL),
 ('20000000-0000-0000-0000-000000000011',NULL,'RECURRING','USD',12,0,'DRAFT',true,'2025-01-11','{"frequency":"DAILY","start_date":"2025-01-01","end_mode":"AFTER_N","end_after_cycles":1,"paused":true}',NULL),
 ('20000000-0000-0000-0000-000000000012',NULL,'RECURRING','USD',12,0,'DRAFT',true,'2025-01-12','{"frequency":"WEEKLY","start_date":"2025-01-01","end_mode":"ON_DATE","end_date":"2025-02-01"}',NULL),
 ('20000000-0000-0000-0000-000000000013',NULL,'RECURRING','USD',12,0,'DRAFT',true,'2025-01-13','{"frequency":"YEARLY","start_date":"2024-02-29"}',NULL),
 ('20000000-0000-0000-0000-000000000020',NULL,'RECURRING','USD',12,0,'OPEN',false,'2025-01-20','{"template_invoice_id":"20000000-0000-0000-0000-000000000010","cycle_key":"2025-01-31"}','2025-02-07'),
 ('20000000-0000-0000-0000-000000000021',NULL,'RECURRING','USD',12,0,'OPEN',false,'2025-01-21','{"template_invoice_id":"20000000-0000-0000-0000-000000000010","cycle_key":"invalid"}','2025-03-05'),
 ('20000000-0000-0000-0000-000000000022',NULL,'RECURRING','USD',12,0,'VOID',false,'2025-01-22','{"template_invoice_id":"20000000-0000-0000-0000-000000000010"}','2025-05-05'),
 ('20000000-0000-0000-0000-000000000023',NULL,'MILESTONE','USD',12,0,'OPEN',false,'2025-01-23','{"template_invoice_id":"20000000-0000-0000-0000-000000000010"}','2025-05-05'),
 ('20000000-0000-0000-0000-000000000024',NULL,'RECURRING','USD',12,0,'OPEN',true,'2025-01-24','{"template_invoice_id":"20000000-0000-0000-0000-000000000010"}','2025-05-05'),
 ('20000000-0000-0000-0000-000000000025',NULL,'RECURRING','USD',12,0,'OPEN',false,'2025-01-25','{"template_invoice_id":"20000000-0000-0000-0000-000000000011","cycle_key":"2025-01-01"}','2025-01-02'),
 ('20000000-0000-0000-0000-000000000026',NULL,'RECURRING','USD',12,0,'OPEN',false,'2025-01-26','{"template_invoice_id":"20000000-0000-0000-0000-000000000013","cycle_key":"2024-W09-4"}','2024-03-02'),
 ('20000000-0000-0000-0000-000000000027',NULL,'RECURRING','USD',12,0,'OPEN',false,'2025-01-27','{"template_invoice_id":"20000000-0000-0000-0000-000000000012","cycle_key":"2025-W013"}','2025-03-02')`
	for _, pool := range []*pgxpool.Pool{p, pp} {
		if _, e = pool.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	snap := func(pool *pgxpool.Pool) string {
		var s string
		if e := pool.QueryRow(context.Background(), `SELECT jsonb_agg(to_jsonb(p) ORDER BY invoice_id)::text FROM invoices p`).Scan(&s); e != nil {
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
		w := call("/api/v1/invoices/recurring-templates/rows", tc.c)
		if w.Code != tc.code {
			t.Fatalf("auth got %d %s", w.Code, w.Body)
		}
	}
	paths := []string{"/api/v1/invoices/recurring-templates/rows", "/api/v1/invoices/recurring-templates", "/api/v1/invoices/recurring-templates?status=BAD&customer_id=bad", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000004", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000008", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000099", "/api/v1/invoices/recurring-templates/bad", "/api/v1/invoices", "/api/v1/invoices?limit=1", "/api/v1/invoices?limit=0", "/api/v1/invoices?limit=501", "/api/v1/invoices?limit=bad", "/api/v1/invoices?status=DRAFT&status=OPEN", "/api/v1/invoices?status=SENT&status=PARTIAL&status=PAID&status=VOID", "/api/v1/invoices?status=", "/api/v1/invoices?invoice_type=RECURRING", "/api/v1/invoices?invoice_type=", "/api/v1/invoices?customer_id=10000000-0000-0000-0000-000000000001", "/api/v1/invoices?customer_id=bad&limit=bad", "/api/v1/invoices/awaiting-finalization", "/api/v1/invoices/20000000-0000-0000-0000-000000000001", "/api/v1/invoices/20000000-0000-0000-0000-000000000004", "/api/v1/invoices/20000000-0000-0000-0000-000000000099", "/api/v1/invoices/bad"}
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
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
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	if e = dec.Decode(&expected); e != nil {
		t.Fatal(e)
	}
	for i, path := range paths {
		w := call(path, admin)
		var body any
		dec := json.NewDecoder(strings.NewReader(w.Body.String()))
		dec.UseNumber()
		if e = dec.Decode(&body); e != nil {
			t.Fatalf("%s %d %s", path, w.Code, w.Body)
		}
		if json.Number(strconv.Itoa(w.Code)) != expected[i][0] || !reflect.DeepEqual(body, expected[i][1]) {
			t.Errorf("%s go=%d %#v python=%#v", path, w.Code, body, expected[i])
		}
	}
	negativePaths := []string{"/api/v1/invoices?limit=-1", "/api/v1/invoices?limit=-9223372036854775809", "/api/v1/invoices?limit=9223372036854775808"}
	neg := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app,raise_server_exceptions=False) as c:
 print(json.dumps([[r.status_code,r.headers.get('content-type'),r.text] for p in json.loads(os.environ['PATHS']) for r in [c.get(p)]]))`)
	neg.Dir = "../../../backend"
	negpaths, _ := json.Marshal(negativePaths)
	neg.Env = append(os.Environ(), "DB="+pydb.URL(), "PATHS="+string(negpaths))
	negout, e := neg.Output()
	if e != nil {
		t.Fatal(e)
	}
	var negexpected [][]any
	if e = json.Unmarshal(negout, &negexpected); e != nil {
		t.Fatal(e)
	}
	for i, path := range negativePaths {
		w := call(path, admin)
		if float64(w.Code) != negexpected[i][0] || w.Header().Get("Content-Type") != negexpected[i][1] || strings.TrimSpace(w.Body.String()) != strings.TrimSpace(negexpected[i][2].(string)) {
			t.Errorf("%s got %d %s %q want %#v", path, w.Code, w.Header().Get("Content-Type"), w.Body.String(), negexpected[i])
		}
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	ops := doc["paths"].(map[string]any)
	for _, path := range []string{"/api/v1/invoices", "/api/v1/invoices/{invoice_id}", "/api/v1/invoices/awaiting-finalization", "/api/v1/invoices/recurring-templates", "/api/v1/invoices/recurring-templates/{template_id}", "/api/v1/invoices/recurring-templates/rows"} {
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
	err500 := ops["/api/v1/invoices"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["500"].(map[string]any)["content"].(map[string]any)
	if err500["text/plain"] == nil || err500["application/json"] == nil {
		t.Fatalf("500 media types %#v", err500)
	}
	list := ops["/api/v1/invoices"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	templateList := ops["/api/v1/invoices/recurring-templates"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if templateList["type"] != "array" {
		t.Errorf("template schema %#v", templateList)
	}
	if list["type"] != "array" {
		t.Errorf("list schema %#v", list)
	}
	if before != snap(p) || beforepy != snap(pp) {
		t.Fatal("reads changed database")
	}
	if _, e = p.Exec(context.Background(), `UPDATE invoices SET issue_date='infinity' WHERE invoice_id='20000000-0000-0000-0000-000000000021'`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/invoices/recurring-templates/rows", admin); w.Code != 500 {
		t.Fatalf("out-of-range child date %d %s", w.Code, w.Body)
	}
	if _, e = p.Exec(context.Background(), `UPDATE invoices SET issue_date='2025-03-05', billing_cycle_ref='{"template_invoice_id":"20000000-0000-0000-0000-000000000010","cycle_key":123}' WHERE invoice_id='20000000-0000-0000-0000-000000000021'`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/invoices/recurring-templates/rows", admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"previous_issue_date":"2025-03-05"`) {
		t.Fatalf("malformed child cycle date %d %s", w.Code, w.Body)
	}
	if _, e = p.Exec(context.Background(), `UPDATE invoices SET amount='NaN' WHERE invoice_id='20000000-0000-0000-0000-000000000008'`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/invoices/recurring-templates/rows", admin); w.Code != 500 {
		t.Fatalf("invalid template numeric %d %s", w.Code, w.Body)
	}
	if _, e = p.Exec(context.Background(), `DELETE FROM invoice_line_items WHERE invoice_id IN (SELECT invoice_id FROM invoices WHERE is_template); DELETE FROM invoices WHERE is_template`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/invoices/recurring-templates/rows", admin); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty templates %d %s", w.Code, w.Body)
	}
	if _, e = p.Exec(context.Background(), `UPDATE invoices SET amount='NaN' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/invoices/20000000-0000-0000-0000-000000000001", admin); w.Code != 500 {
		t.Fatalf("invalid numeric %d %s", w.Code, w.Body)
	}
}
