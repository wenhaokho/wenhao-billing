package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
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

func TestBillJSONDecimalPrecisionPolicy(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	goDB := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pyDB := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), goDB.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	pp, e := pgxpool.New(context.Background(), pyDB.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pp.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin');INSERT INTO vendors(vendor_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Vendor');INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','USD',0,0,'DRAFT')`
	for _, p := range []*pgxpool.Pool{pool, pp} {
		if _, e = p.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	cmd := exec.Command("python", "-c", `import os,json
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.bills import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1));app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
body='{"line_items":[{"description":"x","quantity":1.0000000000000000000001,"unit_price":1}]}'
with TestClient(app,raise_server_exceptions=False) as c:
 patch=c.patch('/api/v1/bills/50000000-0000-0000-0000-000000000001',content=body,headers={'content-type':'application/json'})
 create=c.post('/api/v1/bills',content=body[:-1]+',"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD"}',headers={'content-type':'application/json'})
 print(json.dumps([patch.status_code,create.status_code]))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pyDB.URL())
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var py []int
	if e = json.Unmarshal(output, &py); e != nil {
		t.Fatal(e)
	}
	if len(py) != 2 || py[0] != 200 || py[1] != 201 {
		t.Fatalf("Python float behavior: %s", output)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ method, path, body string }{{"PATCH", "/api/v1/bills/50000000-0000-0000-0000-000000000001", `{"line_items":[{"description":"x","quantity":1.0000000000000000000001,"unit_price":1}]}`}, {"POST", "/api/v1/bills", `{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":[{"description":"x","quantity":1.0000000000000000000001,"unit_price":1}]}`}} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 422 {
			t.Errorf("%s exact decimal: %d %s", tc.method, w.Code, w.Body)
		}
	}
	var bills, lines int
	if e = pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bills),(SELECT count(*) FROM bill_line_items)`).Scan(&bills, &lines); e != nil || bills != 1 || lines != 0 {
		t.Errorf("precision rejection wrote data: %d %d %v", bills, lines, e)
	}
}
