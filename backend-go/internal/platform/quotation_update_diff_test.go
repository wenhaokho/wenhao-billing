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

func TestQuotationUpdateDifferential(t *testing.T) {
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
		if _, e = pool.Exec(context.Background(), `INSERT INTO quotations(quotation_id,customer_id,quotation_number,currency,subtotal,amount,status) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','Q1','USD',6,6,'DRAFT'); INSERT INTO quotation_line_items(line_item_id,quotation_id,description,quantity,unit_price,amount,position) VALUES ('50000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','initial',2,3,6,0)`); e != nil {
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
	bodies := []string{`{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":2147483648}]}`, `{}`, `{"customer_id":"10000000-0000-0000-0000-000000000001","business_id":"ignored"}`, `{"notes":"updated","payment_terms":"Net 30","currency":"IDR","project_id":"30000000-0000-0000-0000-000000000001","quotation_number":"NEW","po_so_number":"PO","issue_date":"2026-01-01","valid_until":"2026-02-01","footer":"end"}`, `{"discount_type":"PERCENT","discount_value":"3.5","line_items":[{"item_id":"40000000-0000-0000-0000-000000000001","description":"A","quantity":"1.2345","unit_price":"7.6543","position":1},{"description":"B","quantity":"0.0001","unit_price":"0.5","position":2}]}`, `{"line_items":null,"notes":null,"project_id":null,"discount_type":null,"discount_value":null}`, `{"line_items":[]}`, `{"currency":"US"}`, `{"valid_until":"bad","quotation_number":123}`, `{"customer_id":"bad"}`, `{"line_items":[{"description":"x","quantity":1,"unit_price":1,"position":"bad"}]}`, `{"currency":`, `{"customer_id":null}`, `{"currency":null}`, `{"line_items":[{"description":"bad","quantity":"0.00001","unit_price":1}]}`, `{"customer_id":"99999999-9999-9999-9999-999999999999"}`, `{"project_id":"99999999-9999-9999-9999-999999999999"}`, `{"line_items":[{"item_id":"99999999-9999-9999-9999-999999999999","description":"bad","quantity":1,"unit_price":1}]}`, `{"discount_type":"AMOUNT","discount_value":10000,"line_items":[{"description":"zero","quantity":1,"unit_price":1}]}`, `{"currency":"SGD","line_items":[{"description":"a","quantity":"0.0001","unit_price":"0.5"},{"description":"b","quantity":"0.0001","unit_price":"0.5"}]}`}

	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.quotations import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
def counts():
 with engine.connect() as conn: return list(conn.execute(text("SELECT (SELECT count(*) FROM quotations), (SELECT count(*) FROM quotation_line_items)")).one())
results=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for b in json.loads(os.environ['BODIES']):
  before=counts(); r=c.patch('/api/v1/quotations/20000000-0000-0000-0000-000000000001',content=b,headers={'content-type':'application/json'}); after=counts()
  results.append([r.status_code,r.json() if r.status_code != 500 else r.text,before,after])
 for status in ['DRAFT','SENT','ACCEPTED','DECLINED','EXPIRED','INVOICED','VOID']:
  with engine.begin() as conn: conn.execute(text("UPDATE quotations SET status=:status WHERE quotation_id='20000000-0000-0000-0000-000000000001'"), {'status':status})
  r=c.patch('/api/v1/quotations/20000000-0000-0000-0000-000000000001',json={'notes':'status'})
  results.append([r.status_code,r.json()])
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
		if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM quotations),(SELECT count(*) FROM quotation_line_items)`).Scan(&beforeInvoices, &beforeLines); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code >= 400 {
			var afterInvoices, afterLines int
			if e = p.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM quotations),(SELECT count(*) FROM quotation_line_items)`).Scan(&afterInvoices, &afterLines); e != nil {
				t.Fatal(e)
			}
			if beforeInvoices != afterInvoices || beforeLines != afterLines || !reflect.DeepEqual(expected[i][2], expected[i][3]) {
				t.Errorf("case %d invalid input wrote data", i)
			}
		}
		if i == 0 {
			if float64(w.Code) != expected[i][0] || w.Body.String() != expected[i][1] {
				t.Errorf("overflow response: %d %q python %v", w.Code, w.Body.String(), expected[i])
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
				delete(m, "quotation_id")
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
	for i, status := range []string{"DRAFT", "SENT", "ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
		if _, e = p.Exec(context.Background(), `UPDATE quotations SET status=$1 WHERE quotation_id='20000000-0000-0000-0000-000000000001'`, status); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("PATCH", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", strings.NewReader(`{"notes":"status"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		want := expected[len(bodies)+i]
		if float64(w.Code) != want[0] {
			t.Errorf("status %s: %d python %v", status, w.Code, want)
		}
	}

}
