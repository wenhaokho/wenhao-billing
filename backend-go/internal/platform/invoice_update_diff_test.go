package platform

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestInvoiceUpdateDifferential(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO projects(project_id,customer_id,code,name,currency,status) VALUES ('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P','Project','IDR','ACTIVE'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000001','Item','SERVICE'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,issue_date,due_date,created_at) VALUES ('50000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'DRAFT','2026-01-01','2026-01-15','2026-01-01'); INSERT INTO invoice_line_items(line_item_id,invoice_id,description,quantity,unit_price,amount,position) VALUES ('60000000-0000-0000-0000-000000000001','50000000-0000-0000-0000-000000000001','A',2,5,10,0)`
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
		`{"notes":"first","footer":"F","internal_note":"private","po_so_number":"PO","invoice_number":"NEW","customer_id":"10000000-0000-0000-0000-000000000001","project_id":"30000000-0000-0000-0000-000000000001","invoice_type":"RECURRING","currency":"IDR","issue_date":"2026-02-01","due_date":null,"payment_terms":"Net 30"}`,
		`{"notes":null,"customer_id":null,"project_id":null,"footer":null,"internal_note":null,"invoice_number":null,"po_so_number":null}`,
		`{"discount_type":"PERCENT","discount_value":"3.5","wht_type":"PPH23","wht_rate":"2","wht_gross_up":true,"line_items":[{"item_id":"40000000-0000-0000-0000-000000000001","description":"A","quantity":"1.2345","unit_price":"7.6543","position":1},{"description":"B","quantity":"0.0001","unit_price":"0.5","position":2}]}`,
		`{"currency":"USD","wht_type":null,"wht_rate":null,"wht_gross_up":null,"line_items":null}`,
		`{"wht_type":"PPH21","discount_type":"AMOUNT","discount_value":"0.1"}`,
		`{"wht_type":"NONE","discount_type":null,"discount_value":null}`,
		`{"line_items":[]}`,
		`{"currency":"IDR","wht_type":"PPH23","wht_rate":"2.3456","wht_gross_up":true,"line_items":[{"description":"round","quantity":"1.0001","unit_price":"50.0050"}]}`,
		`{"currency":"SGD","wht_type":"PPH23","wht_rate":"2.5","wht_gross_up":false,"discount_type":"PERCENT","discount_value":"12.3456","line_items":[{"description":"round","quantity":"0.0001","unit_price":"50.0050"},{"description":"round2","quantity":"1","unit_price":"100.0050"}]}`,
		`{"wht_rate":"-0.001"}`,
		`{"wht_rate":"0.00001"}`,
		`{"discount_value":"0.00001"}`,
		`{"currency":"US"}`,
		`{"line_items":[{"description":"negative","quantity":"-1","unit_price":"1"}]}`,

		`{"line_items":[{"description":"x","quantity":1,"unit_price":100}]}`,
		`{"wht_type":"PPH23","wht_rate":"100"}`,
		`{"currency":"US","notes":123}`,
		`{"line_items":[{"quantity":1}]}`,
		`{"wht_type":"BAD"}`,
		`{"issue_date":"bad"}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"invalid"}]}`,
		`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":1.5}]}`,
		`{}`,
	}

	for _, name := range []string{"customer_id", "project_id", "invoice_type", "currency", "invoice_number", "po_so_number", "issue_date", "due_date", "payment_terms", "notes", "footer", "internal_note", "discount_type", "discount_value", "wht_type", "wht_rate", "wht_gross_up", "line_items"} {
		bodies = append(bodies, `{"`+name+`":null}`)
	}
	bodies = append(bodies, `{"customer_id":"99999999-9999-9999-9999-999999999999"}`, `{"project_id":"99999999-9999-9999-9999-999999999999"}`, `{"line_items":[{"item_id":"99999999-9999-9999-9999-999999999999","description":"x","quantity":1,"unit_price":1}]}`)
	statusStart := len(bodies)
	for _, body := range []string{`{"line_items":[{"description":"overflow","quantity":1,"unit_price":1,"position":2147483648}]}`, `{"notes":"forbidden"}`, `{"notes":"forbidden"}`, `{"notes":"forbidden"}`} {
		bodies = append(bodies, body)
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
 for i,b in enumerate(json.loads(os.environ['BODIES'])):
  statuses=['OPEN','PARTIAL','PAID','VOID']; start=int(os.environ['STATUS_START'])
  if i>=start:
   with engine.begin() as conn: conn.execute(text("UPDATE invoices SET status=:s WHERE invoice_id='50000000-0000-0000-0000-000000000001'"),{'s':statuses[i-start]})
  before=counts(); r=c.patch('/api/v1/invoices/50000000-0000-0000-0000-000000000001',content=b,headers={'content-type':'application/json'}); after=counts()
  results.append([r.status_code,r.json() if r.status_code != 500 else None,before,after])
print(json.dumps(results))`)
	cmd.Dir = "../../../backend"
	b, _ := json.Marshal(bodies)
	cmd.Env = append(os.Environ(), "DB="+pydb.URL(), "BODIES="+string(b), fmt.Sprintf("STATUS_START=%d", statusStart))
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, out)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatalf("python output: %v %s", e, out)
	}
	for i, body := range bodies {
		if i >= statusStart {
			_, e = p.Exec(context.Background(), `UPDATE invoices SET status=$1 WHERE invoice_id='50000000-0000-0000-0000-000000000001'`, []string{"OPEN", "PARTIAL", "PAID", "VOID"}[i-statusStart])
			if e != nil {
				t.Fatal(e)
			}
		}
		var beforeInvoices, beforeLines int
		if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM invoices),(SELECT count(*) FROM invoice_line_items)`).Scan(&beforeInvoices, &beforeLines); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("PATCH", "/api/v1/invoices/50000000-0000-0000-0000-000000000001", strings.NewReader(body))
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
		if float64(w.Code) != expected[i][0] || (w.Code != 500 && !reflect.DeepEqual(actual, expected[i][1])) {
			t.Errorf("case %d: go=%d %#v python=%#v", i, w.Code, actual, expected[i])
		}
	}
}
