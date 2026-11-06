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

func TestPaymentReadsPostgres(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); INSERT INTO payments(payment_id,customer_id,amount,wht_amount,currency,payer_name,payer_reference,payment_date,intake_source,external_ref,status,adjustment_type,confidence_score,created_at) VALUES ('20000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001',123.45,3,'USD','payer','ref','2024-01-01','MANUAL','r1','PENDING_MANUAL_REVIEW','NONE',95.25,'2022-01-01'),('20000000-0000-0000-0000-000000000002',NULL,100,0,'IDR','other',NULL,'2024-01-02','WEBHOOK',NULL,'CLEARED','NONE',NULL,'2023-01-01'),('20000000-0000-0000-0000-000000000003',NULL,20,0,'USD','third',NULL,'2024-01-03','EMAIL',NULL,'PENDING_MANUAL_REVIEW','NONE',1,'2021-01-01'),('20000000-0000-0000-0000-000000000004',NULL,9,0,'SGD','flagged',NULL,'2024-01-04','WEBHOOK',NULL,'FLAGGED','BANK_FEE',NULL,'2024-01-01'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','MILESTONE','USD',123,0,'PAID'); UPDATE payments SET invoice_id='30000000-0000-0000-0000-000000000001' WHERE payment_id='20000000-0000-0000-0000-000000000001'`
	for _, pool := range []*pgxpool.Pool{p, pp} {
		if _, e = pool.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	snap := func(pool *pgxpool.Pool) string {
		var s string
		if e := pool.QueryRow(context.Background(), `SELECT (SELECT coalesce(jsonb_agg(to_jsonb(p) ORDER BY payment_id),'[]') FROM payments p)::text || (SELECT coalesce(jsonb_agg(to_jsonb(l) ORDER BY log_id),'[]') FROM reconciliation_log l)::text`).Scan(&s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	for _, pool := range []*pgxpool.Pool{p, pp} {
		_, e = pool.Exec(context.Background(), `INSERT INTO reconciliation_log(log_id,payment_id,action,reasons,actor_user_id,created_at) VALUES ('50000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','MATCH',ARRAY['one','two'],'00000000-0000-0000-0000-000000000001','2024-01-01 12:00:00'),('50000000-0000-0000-0000-000000000002','20000000-0000-0000-0000-000000000002','REVIEW',ARRAY[]::text[],NULL,'2024-01-02'),('50000000-0000-0000-0000-000000000003','20000000-0000-0000-0000-000000000001','FINAL',ARRAY['later'],NULL,'2024-01-03')`)
		if e != nil {
			t.Fatal(e)
		}
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
		for _, path := range []string{"/api/v1/payments/awaiting-review", "/api/v1/payments/audit-log", "/api/v1/payments/20000000-0000-0000-0000-000000000001/log"} {
			w := call(path, tc.c)
			if w.Code != tc.code {
				t.Fatalf("auth %s got %d %s", path, w.Code, w.Body)
			}
		}
	}
	paths := []string{"/api/v1/payments/audit-log", "/api/v1/payments/audit-log?action=MATCH", "/api/v1/payments/audit-log?action=WRONG&action=MATCH", "/api/v1/payments/audit-log?from_date=2024-01-02&from_date=2024-01-01", "/api/v1/payments/audit-log?limit=0&limit=1", "/api/v1/payments/audit-log?payer=PAY", "/api/v1/payments/audit-log?from_date=2024-01-02", "/api/v1/payments/audit-log?to_date=2024-01-01", "/api/v1/payments/audit-log?payer=%25", "/api/v1/payments/audit-log?limit=0", "/api/v1/payments/audit-log?limit=501", "/api/v1/payments/audit-log?from_date=bad", "/api/v1/payments/bad/log", "/api/v1/payments/20000000-0000-0000-0000-000000000001/log", "/api/v1/payments/20000000-0000-0000-0000-000000000099/log", "/api/v1/payments/awaiting-review", "/api/v1/payments/20000000-0000-0000-0000-000000000001", "/api/v1/payments/20000000-0000-0000-0000-000000000002", "/api/v1/payments/20000000-0000-0000-0000-000000000004", "/api/v1/payments/20000000-0000-0000-0000-000000000099", "/api/v1/payments/bad"}
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.recon import router
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
	negativePaths := []string{"/api/v1/payments/audit-log?limit=-1", "/api/v1/payments/audit-log?limit=-9223372036854775809"}
	negativeCmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.recon import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app,raise_server_exceptions=False) as c:
 print(json.dumps([[r.status_code,r.text,r.headers.get('content-type')] for p in json.loads(os.environ['PATHS']) for r in [c.get(p)]]))`)
	negativeCmd.Dir = "../../../backend"
	negJSON, _ := json.Marshal(negativePaths)
	negativeCmd.Env = append(os.Environ(), "DB="+pydb.URL(), "PATHS="+string(negJSON))
	negativeRaw, err := negativeCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var negativeExpected [][]any
	if err = json.Unmarshal(negativeRaw, &negativeExpected); err != nil {
		t.Fatal(err)
	}
	for i, path := range negativePaths {
		w := call(path, admin)
		if float64(w.Code) != negativeExpected[i][0] || w.Body.String() != negativeExpected[i][1] || w.Header().Get("Content-Type") != negativeExpected[i][2] {
			t.Errorf("negative %s got %d %q %q expected %#v", path, w.Code, w.Body.String(), w.Header().Get("Content-Type"), negativeExpected[i])
		}
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(spec.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	ops := doc["paths"].(map[string]any)
	for _, path := range []string{"/api/v1/payments/awaiting-review", "/api/v1/payments/{payment_id}", "/api/v1/payments/audit-log", "/api/v1/payments/{payment_id}/log"} {
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
	list := ops["/api/v1/payments/awaiting-review"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if list["type"] != "array" {
		t.Errorf("list schema %#v", list)
	}
	if before != snap(p) || beforepy != snap(pp) {
		t.Fatal("reads changed database")
	}
	for _, stamp := range []string{"infinity", "-infinity", "10000-01-01", "0001-01-01 BC"} {
		if _, e = p.Exec(context.Background(), `UPDATE payments SET created_at=$1::timestamp WHERE payment_id='20000000-0000-0000-0000-000000000001'`, stamp); e != nil {
			t.Fatal(e)
		}
		w := call("/api/v1/payments/20000000-0000-0000-0000-000000000001", admin)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
			t.Fatalf("timestamp %s: %d %s", stamp, w.Code, w.Body)
		}
	}
	if _, e = p.Exec(context.Background(), `UPDATE payments SET created_at='2024-01-01' WHERE payment_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(context.Background(), `UPDATE payments SET created_at='infinity',status='CLEARED' WHERE payment_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/invoices/30000000-0000-0000-0000-000000000001/payments", admin); w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
		t.Fatalf("invoice payments timestamp: %d %s", w.Code, w.Body)
	}
	if _, e = p.Exec(context.Background(), `UPDATE payments SET created_at='2022-01-01',status='PENDING_MANUAL_REVIEW' WHERE payment_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(context.Background(), `UPDATE reconciliation_log SET created_at='infinity' WHERE log_id='50000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/v1/payments/audit-log", "/api/v1/payments/20000000-0000-0000-0000-000000000001/log"} {
		w := call(path, admin)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
			t.Fatalf("log timestamp %s: %d %s", path, w.Code, w.Body)
		}
	}
	if _, e = p.Exec(context.Background(), `UPDATE payments SET amount='NaN' WHERE payment_id='20000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/payments/20000000-0000-0000-0000-000000000001", admin); w.Code != 500 || strings.Contains(w.Body.String(), "NaN") {
		t.Fatalf("invalid numeric: %d %s", w.Code, w.Body)
	}
	if _, e = p.Exec(context.Background(), `DELETE FROM reconciliation_log; DELETE FROM payments`); e != nil {
		t.Fatal(e)
	}
	if w := call("/api/v1/payments/awaiting-review", admin); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty: %d %s", w.Code, w.Body)
	}
}
