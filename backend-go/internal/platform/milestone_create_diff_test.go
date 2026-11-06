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

func TestMilestoneCreateDifferential(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name,default_wht_type,default_wht_rate,default_wht_gross_up) VALUES ('10000000-0000-0000-0000-000000000001','A','PPH23',2,true),('10000000-0000-0000-0000-000000000002','B','PPH21',NULL,false),('10000000-0000-0000-0000-000000000003','C','NONE',NULL,false); INSERT INTO projects(project_id,customer_id,code,name,currency,status) VALUES ('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P','Project','IDR','ACTIVE'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000001','Item','SERVICE')`
	for _, p := range []*pgxpool.Pool{p, pp} {
		if _, e = p.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	bodies := []string{
		`{"customer_id":"10000000-0000-0000-0000-000000000001","project_id":"30000000-0000-0000-0000-000000000001","currency":"IDR","amount":"100.1234","milestone_ref":"first","issue_date":"2026-01-01","due_in_days":30}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","amount":"12.34","milestone_ref":"x"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000002","currency":"IDR","amount":"100.5000","milestone_ref":"round"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000002","currency":"USD","amount":"0.2000","milestone_ref":"round"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000003","currency":"USD","amount":"123.4567","milestone_ref":"none"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000003","currency":"USD","amount":1,"milestone_ref":"x","issue_date":0,"due_in_days":"30"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000003","currency":"USD","amount":1,"milestone_ref":"x","issue_date":"2026-02-01T00:00:00Z","due_in_days":1.0}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000003","currency":"USD","amount":1,"milestone_ref":"x","issue_date":true}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000003","currency":"USD","amount":1,"milestone_ref":"x","due_in_days":1.5}`,
		`{}`,
		`{"customer_id":null,"currency":"US","amount":0,"milestone_ref":null}`,
		`{"customer_id":"bad","currency":"USD","amount":"-1","milestone_ref":"x"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","amount":"0.00001","milestone_ref":"x"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"SGD","amount":"1","milestone_ref":"","issue_date":null,"due_in_days":-1}`,
		`{"customer_id":"99999999-9999-9999-9999-999999999999","currency":"USD","amount":"1","milestone_ref":"x"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000001","project_id":"99999999-9999-9999-9999-999999999999","currency":"USD","amount":"1","milestone_ref":"x"}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","amount":true,"milestone_ref":"x","due_in_days":null}`,
		`{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","amount":"1","milestone_ref":"x","issue_date":"2026-02-30"}`,
	}
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
def counts():
 with engine.connect() as conn: return list(conn.execute(text("SELECT (SELECT count(*) FROM invoices), (SELECT count(*) FROM invoice_line_items)")).one())
results=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for b in json.loads(os.environ['BODIES']):
  before=counts(); r=c.post('/api/v1/invoices/milestone',content=b,headers={'content-type':'application/json'}); after=counts()
  results.append([r.status_code,r.json() if r.status_code != 500 else {"detail":"internal server error"},before,after])
print(json.dumps(results))`)
	cmd.Dir = "../../../backend"
	b, _ := json.Marshal(bodies)
	cmd.Env = append(os.Environ(), "DB="+pydb.URL(), "BODIES="+string(b))
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, out)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatalf("python output: %v %s", e, out)
	}
	for i, body := range bodies {
		var beforeInvoices, beforeLines int
		if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM invoices),(SELECT count(*) FROM invoice_line_items)`).Scan(&beforeInvoices, &beforeLines); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("POST", "/api/v1/invoices/milestone", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code >= 400 {
			var afterInvoices, afterLines int
			if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM invoices),(SELECT count(*) FROM invoice_line_items)`).Scan(&afterInvoices, &afterLines); e != nil {
				t.Fatal(e)
			}
			if beforeInvoices != afterInvoices || beforeLines != afterLines || !reflect.DeepEqual(expected[i][2], expected[i][3]) {
				t.Errorf("case %d invalid input wrote data", i)
			}
		}
		var actual any
		if e = json.Unmarshal(w.Body.Bytes(), &actual); e != nil {
			t.Fatalf("go: %d %s", w.Code, w.Body)
		}
		for _, v := range []any{actual, expected[i][1]} {
			if m, ok := v.(map[string]any); ok {
				if details, ok := m["detail"].([]any); ok {
					for _, detail := range details {
						if d, ok := detail.(map[string]any); ok {
							d["input"] = "[REDACTED]"
						}
					}
				}
			}
			if m, ok := v.(map[string]any); ok {
				delete(m, "invoice_id")
				delete(m, "created_at")
				if lines, ok := m["line_items"].([]any); ok {
					for _, l := range lines {
						delete(l.(map[string]any), "line_item_id")
					}
				}
			}
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(actual, expected[i][1]) {
			t.Errorf("case %d: go=%d %#v python=%#v", i, w.Code, actual, expected[i])
		}
	}
}
