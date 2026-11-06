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

func TestInvoicePaymentsPostgres(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user'); INSERT INTO customers(customer_id,name) VALUES ('40000000-0000-0000-0000-000000000001','Customer'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status) VALUES ('30000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001','MILESTONE','USD',123,0,'PAID'),('30000000-0000-0000-0000-000000000002','40000000-0000-0000-0000-000000000001','MILESTONE','IDR',100,0,'PAID'),('30000000-0000-0000-0000-000000000003','40000000-0000-0000-0000-000000000001','MILESTONE','USD',100,0,'PAID'); INSERT INTO payments(payment_id,invoice_id,customer_id,amount,wht_amount,currency,payer_name,payer_reference,payment_date,intake_source,external_ref,status,adjustment_type,confidence_score,created_at) VALUES ('20000000-0000-0000-0000-000000000001','30000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001',123.45,3,'USD','payer','ref','2024-01-01','MANUAL','r1','CLEARED','NONE',95.25,'2022-01-01'),('20000000-0000-0000-0000-000000000002','30000000-0000-0000-0000-000000000001',NULL,100,0,'IDR','other',NULL,'2024-01-02','WEBHOOK',NULL,'CLEARED','NONE',NULL,'2023-01-01'),('20000000-0000-0000-0000-000000000003','30000000-0000-0000-0000-000000000001',NULL,20,0,'USD','third',NULL,'2024-01-03','EMAIL',NULL,'PENDING_MANUAL_REVIEW','NONE',1,'2021-01-01'),('20000000-0000-0000-0000-000000000004','30000000-0000-0000-0000-000000000001',NULL,9,0,'SGD','flagged',NULL,'2024-01-04','WEBHOOK',NULL,'FLAGGED','BANK_FEE',NULL,'2024-01-01'),('20000000-0000-0000-0000-000000000005','30000000-0000-0000-0000-000000000002',NULL,9,0,'IDR','separate',NULL,'2024-01-04','WEBHOOK',NULL,'CLEARED','NONE',NULL,'2024-01-01'),('20000000-0000-0000-0000-000000000006','30000000-0000-0000-0000-000000000001',NULL,9,0,'USD','tie',NULL,'2024-01-02','WEBHOOK',NULL,'CLEARED','NONE',NULL,'2024-01-01')`
	for _, pool := range []*pgxpool.Pool{p, pp} {
		if _, e = pool.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	snapshot := func(pool *pgxpool.Pool) string {
		var s string
		if e := pool.QueryRow(context.Background(), `SELECT (SELECT jsonb_agg(to_jsonb(p) ORDER BY payment_id) FROM payments p)::text || (SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i)::text`).Scan(&s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	before, beforepy := snapshot(p), snapshot(pp)
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
	path := "/api/v1/invoices/30000000-0000-0000-0000-000000000001/payments"
	for _, tc := range []struct {
		c    *http.Cookie
		code int
	}{{nil, 401}, {user, 403}, {admin, 200}} {
		w := call(path, tc.c)
		if w.Code != tc.code {
			t.Fatalf("auth %d %s", w.Code, w.Body)
		}
	}
	paths := []string{path, "/api/v1/invoices/30000000-0000-0000-0000-000000000002/payments", "/api/v1/invoices/30000000-0000-0000-0000-000000000003/payments", "/api/v1/invoices/30000000-0000-0000-0000-000000000099/payments", "/api/v1/invoices/bad/payments"}
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
	for _, c := range []*http.Cookie{tok("00000000-0000-0000-0000-000000000099")} {
		if w := call(path, c); w.Code != 403 {
			t.Fatalf("deleted user: %d %s", w.Code, w.Body)
		}
	}
	if w := call(path, &http.Cookie{Name: "session", Value: admin.Value + "x"}); w.Code != 401 {
		t.Fatalf("tampered: %d %s", w.Code, w.Body)
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var openapi map[string]any
	if err := json.Unmarshal(spec.Body.Bytes(), &openapi); err != nil {
		t.Fatal(err)
	}
	pathsMap := openapi["paths"].(map[string]any)
	operation := pathsMap["/api/v1/invoices/{invoice_id}/payments"].(map[string]any)["get"].(map[string]any)
	if _, ok := operation["security"]; !ok {
		t.Error("missing security")
	}
	responses := operation["responses"].(map[string]any)
	for _, code := range []string{"200", "401", "403", "404", "422", "500"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("missing response %s", code)
		}
	}
	responseSchema := responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if responseSchema["type"] != "array" {
		t.Errorf("response schema: %#v", responseSchema)
	}
	if before != snapshot(p) || beforepy != snapshot(pp) {
		t.Error("database changed")
	}
}
