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

func TestRecurringTemplateUpdateDifferential(t *testing.T) {
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
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name,default_wht_type,default_wht_rate,default_wht_gross_up) VALUES ('10000000-0000-0000-0000-000000000001','A','PPH23',2,true); INSERT INTO projects(project_id,customer_id,code,name,currency,status) VALUES ('30000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','P','Project','IDR','ACTIVE'); INSERT INTO items(item_id,name,item_type) VALUES ('40000000-0000-0000-0000-000000000001','Item','SERVICE'); INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000001','RECURRING','USD',1,1,'DRAFT',true,'{"frequency":"DAILY","interval":1,"start_date":"2026-01-01","end_mode":"NEVER"}')`
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
		`{"currency":"USD","payment_terms":"Net 30","line_items":[{"description":"Service","quantity":2,"unit_price":3}],"schedule":{"frequency":"MONTHLY","start_date":"2026-01-31"}}`,
		`{"currency":"IDR","payment_terms":"Cash","wht_type":"PPH23","wht_gross_up":true,"line_items":[{"description":"Service","quantity":1,"unit_price":100}],"schedule":{"frequency":"WEEKLY","interval":2,"start_date":"2026-01-31","end_mode":"AFTER_N","end_after_cycles":3}}`,
		`{"currency":"SGD","payment_terms":"Net 7","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":"2026-02-28","end_mode":"ON_DATE","end_date":"2027-01-01"}}`,
		`{"currency":"USD","payment_terms":"Net 7","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"YEARLY","start_date":"2024-02-29"}}`,
		`{"currency":"USD","payment_terms":"Net 7","line_items":[],"schedule":{"frequency":"YEARLY","start_date":"2024-02-29"}}`,
		`{"currency":"USD","payment_terms":"Net 7","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":"2024-02-29","end_mode":"ON_DATE"}}`,
		`{}`,
	}

	for _, sched := range []string{`{}`, `null`, `{"frequency":"BAD","start_date":"2026-01-01"}`, `{"frequency":"DAILY","start_date":"bad"}`, `{"frequency":"DAILY","start_date":"2026-01-01","interval":0}`, `{"frequency":"DAILY","start_date":"2026-01-01","end_mode":"AFTER_N"}`, `{"frequency":"DAILY","start_date":"2026-01-01","end_mode":"AFTER_N","end_after_cycles":0}`, `{"frequency":"DAILY","start_date":"2026-01-01","end_mode":"BAD"}`, `{"frequency":"DAILY","start_date":"2026-01-01","interval":"2"}`, `{"frequency":"DAILY","start_date":"2026-01-01","end_date":"bad"}`} {
		bodies = append(bodies, `{"currency":"USD","payment_terms":"Net 7","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":`+sched+`}`)
	}

	bodies = append(bodies,
		`{"customer_id":"10000000-0000-0000-0000-000000000001","project_id":"30000000-0000-0000-0000-000000000001","currency":"IDR","payment_terms":"Net 30","discount_type":"PERCENT","discount_value":"3.5","line_items":[{"item_id":"40000000-0000-0000-0000-000000000001","description":"A","quantity":"1.2345","unit_price":"7.6543"}],"schedule":{"frequency":"MONTHLY","start_date":"2026-01-31"}}`,
		`{"currency":"USD","payment_terms":"N","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":0}}`,
		`{"currency":"USD","payment_terms":"N","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":"2026-01-01","interval":true}}`,
		`{"currency":"USD","payment_terms":null,"schedule":null}`,
		`{"currency":"USD","payment_terms":"N","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":"2026-01-01","end_after_cycles":"2"}}`,
	)

	for _, freq := range []string{"DAILY", "WEEKLY", "MONTHLY", "YEARLY"} {
		for _, end := range []string{`"end_mode":"NEVER"`, `"end_mode":"ON_DATE","end_date":"2028-02-29"`, `"end_mode":"AFTER_N","end_after_cycles":5`} {
			bodies = append(bodies, `{"currency":"USD","payment_terms":"N","line_items":[{"description":"a","quantity":2,"unit_price":"1.25"}],"schedule":{"frequency":"`+freq+`","start_date":"2024-02-29",`+end+`}}`)
		}
	}

	for _, fields := range []string{`"currency":null,"payment_terms":"N"`, `"currency":"USD","payment_terms":12`, `"currency":"USD","payment_terms":"N","wht_type":"PPH23","wht_rate":100`, `"currency":"USD","payment_terms":"N","line_items":null`, `"currency":"USD","payment_terms":"N","customer_id":"bad"`} {
		bodies = append(bodies, `{`+fields+`,"schedule":{"frequency":"DAILY","start_date":"2026-01-01"}}`)
	}

	bodies = append(bodies, `{"currency":"US","payment_terms":null,"line_items":null,"schedule":{"frequency":"BAD","start_date":"bad"}}`, `{"customer_id":"bad","payment_terms":"N","schedule":null}`)

	bodies = append(bodies, `{`, `[]`, `null`, ``)

	for _, field := range []string{`"customer_id":"99999999-9999-9999-9999-999999999999"`, `"project_id":"99999999-9999-9999-9999-999999999999"`} {
		bodies = append(bodies, `{`+field+`,"currency":"USD","payment_terms":"N","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":"2026-01-01"}}`)
	}
	bodies = append(bodies, `{"currency":"USD","payment_terms":"N","line_items":[{"item_id":"99999999-9999-9999-9999-999999999999","description":"a","quantity":1,"unit_price":1}],"schedule":{"frequency":"DAILY","start_date":"2026-01-01"}}`)

	for _, sched := range []string{`{"frequency":"DAILY","start_date":null}`, `{"frequency":"DAILY","start_date":"2026-01-01","interval":null}`, `{"frequency":"DAILY","start_date":"2026-01-01","interval":"bad"}`, `{"frequency":"DAILY","start_date":"2026-01-01","interval":1.5}`} {
		bodies = append(bodies, `{"currency":"USD","payment_terms":"N","line_items":[{"description":"a","quantity":1,"unit_price":1}],"schedule":`+sched+`}`)
	}

	for _, overlay := range []string{`"wht_type":"PPH23","wht_rate":"3.25","wht_gross_up":true,`, ``, `"wht_type":null,"wht_rate":null,"wht_gross_up":null,`, `"wht_type":"PPH21",`, `"wht_rate":"4",`, `"wht_type":"NONE",`} {
		bodies = append(bodies, `{"currency":"IDR","payment_terms":"N",`+overlay+`"discount_type":"PERCENT","discount_value":"3.5","line_items":[{"description":"a","quantity":"1.2345","unit_price":"7.6543"},{"description":"b","quantity":"2.3333","unit_price":"4.4444"}],"schedule":{"frequency":"MONTHLY","start_date":"2026-01-31"}}`)
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
  before=counts(); r=c.put('/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001',content=b,headers={'content-type':'application/json'}); after=counts()
  results.append([r.status_code,(r.text if r.status_code == 500 else r.json()),before,after])
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
		req := httptest.NewRequest("PUT", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000001", strings.NewReader(body))
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
		if w.Code == 500 {
			if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || w.Body.String() != expected[i][1] {
				t.Errorf("case %d 500 response %s %q expected %#v", i, w.Header().Get("Content-Type"), w.Body.String(), expected[i][1])
			}
			continue
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
