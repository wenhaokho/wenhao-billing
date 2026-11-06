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

func TestInvoiceCreateDifferential(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name,default_wht_type,default_wht_rate,default_wht_gross_up) VALUES ('10000000-0000-0000-0000-000000000001','A','PPH23',2,true); INSERT INTO projects(project_id,customer_id,code,name,currency,status) VALUES ('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P','Project','IDR','ACTIVE'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000001','Item','SERVICE')`
	for _, p := range []*pgxpool.Pool{p, pp} {
		if _, e = p.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	for _, pool := range []*pgxpool.Pool{p, pp} {
		if _, e = pool.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_number,invoice_type,currency,amount,balance_due,status,is_template) VALUES ('90000000-0000-0000-0000-000000000001',$1,'MILESTONE','USD',0,0,'DRAFT',false)`, fmt.Sprintf("WH%d١٢", time.Now().Year())); e != nil {
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
		`{"customer_id":"10000000-0000-0000-0000-000000000001","project_id":"30000000-0000-0000-0000-000000000001","invoice_type":"MILESTONE","invoice_number":"CUSTOM-123","currency":"IDR","issue_date":"2026-01-01","due_date":"2026-02-01","payment_terms":"Net 30","po_so_number":"P","notes":"N","footer":"F","internal_note":"I","discount_type":"PERCENT","discount_value":"3.5","line_items":[{"item_id":"40000000-0000-0000-0000-000000000001","description":"A","quantity":"1.2345","unit_price":"7.6543","position":1},{"description":"B","quantity":"0.0001","unit_price":"0.5","position":2},{"description":"C","quantity":"0.0001","unit_price":"0.5","position":3}]}`,
		`{"invoice_type":"RECURRING","currency":"USD","wht_type":"PPH21","wht_gross_up":true,"discount_type":"AMOUNT","discount_value":"0.1","line_items":[{"description":"A","quantity":"1","unit_price":"100"}]}`,
		`{"invoice_type":"USAGE","currency":"SGD","wht_type":"NONE","line_items":[{"description":"A","quantity":"0","unit_price":"12"}]}`,
		`{}`,
		`{"currency":"US","line_items":[{"description":"x","quantity":"-1","unit_price":"0.00001"}]}`,
		`{"currency":"USD","line_items":[{"quantity":1}]}`,
		`{"currency":"USD","wht_type":"BAD","discount_type":"BAD","line_items":[]}`,
		`{"currency":"USD","wht_type":"PPH23","wht_gross_up":"yes","line_items":[{"description":"x","quantity":"2","unit_price":"1","position":"3"}]}`,
		`{"currency":"USD","wht_rate":"0.00001","line_items":[{"description":"x","quantity":1,"unit_price":1}]}`,
		`{"currency":"USD","wht_rate":"9999","line_items":[{"description":"x","quantity":1,"unit_price":1}]}`,
		`{"currency":"USD","line_items":[]}`,
		`{"currency":"USD","wht_type":"PPH23","wht_rate":"100","line_items":[{"description":"A","quantity":"1","unit_price":"1"}]}`,
	}
	for _, v := range []string{"1", "1.0", "1e0", "0", "0.0", "0e0"} {
		bodies = append(bodies, `{"currency":"USD","wht_type":"PPH23","wht_gross_up":`+v+`,"line_items":[{"description":"gross","quantity":1,"unit_price":100}]}`)
	}
	for _, fields := range []string{`"issue_date":0,"due_date":86400`, `"issue_date":0e0,"due_date":8.64e4`, `"issue_date":"1970-01-01T00:00:00Z","due_date":"2026-02-01T00:00:00+07:00"`, `"issue_date":-86400,"due_date":86400000`, `"issue_date":"2026-02-01T00:00:01Z"`, `"issue_date":1`, `"due_date":"2026-02-30"`} {
		bodies = append(bodies, `{"currency":"USD",`+fields+`,"line_items":[{"description":"date","quantity":1,"unit_price":1}]}`)
	}
	prefix := fmt.Sprintf("WH%d", time.Now().Year())
	base := `{"currency":"USD","line_items":[{"description":"x","quantity":1,"unit_price":1}]}`
	bodies = append(bodies, strings.Replace(base, `"currency"`, `"invoice_number":"`+prefix+`²x","currency"`, 1), base, strings.Replace(base, `"currency"`, `"invoice_number":"`+prefix+`²","currency"`, 1), base)

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
  before=counts(); r=c.post('/api/v1/invoices',content=b,headers={'content-type':'application/json'}); after=counts()
  results.append([r.status_code,r.json() if r.status_code != 500 else None,before,after])
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
		req := httptest.NewRequest("POST", "/api/v1/invoices", strings.NewReader(body))
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
