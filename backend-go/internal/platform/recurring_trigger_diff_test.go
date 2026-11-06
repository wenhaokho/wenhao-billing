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

func TestRecurringTriggerPythonDifferential(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	config := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	migrator := goosemigrator.New(".", goosemigrator.WithFS(migrations.Files))
	gd := pgtestdb.Custom(t, config, migrator)
	pd := pgtestdb.Custom(t, config, migrator)
	p, e := pgxpool.New(context.Background(), gd.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	py, e := pgxpool.New(context.Background(), pd.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer py.Close()
	seed := `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO items(item_id,name,item_type,is_hosting) VALUES ('40000000-0000-0000-0000-000000000001','Host','SERVICE',true); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,discount_type,discount_value,wht_type,wht_rate,wht_gross_up,payment_terms,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','RECURRING','USD',10,10,'DRAFT',true,'PERCENT',10,'PPH23',2,false,'NET 30','{"frequency":"DAILY","start_date":"2025-01-01"}'); INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','40000000-0000-0000-0000-000000000001',0,'Service',2,12.3456,24.6912)`
	for _, pool := range []*pgxpool.Pool{p, py} {
		if _, e = pool.Exec(context.Background(), seed); e != nil {
			t.Fatal(e)
		}
	}
	cases := [][2]string{{`{"frequency":"DAILY","start_date":"2025-01-01"}`, "2025-01-02"}, {`{"frequency":"WEEKLY","start_date":"2025-01-01"}`, "2025-01-08"}, {`{"frequency":"MONTHLY","start_date":"2025-01-31"}`, "2025-02-28"}, {`{"frequency":"YEARLY","start_date":"2024-02-29"}`, "2025-02-28"}, {`{"frequency":"DAILY","start_date":"2025-01-01"}`, "bad"}, {`{"frequency":"MONTHLY","start_date":"2025-01-31"}`, "2025-02-27"}, {`{"frequency":"DAILY","start_date":"20250101"}`, "2025-W01-3"}, {`{"frequency":"BAD","start_date":"2025-01-01"}`, "2025-01-01"}, {`{"frequency":"DAILY","start_date":"bad"}`, "2025-01-01"}, {`{"frequency":"DAILY","start_date":"2025-01-01"}`, "2025-01-01"}, {`{"frequency":"DAILY","start_date":"2025-01-01"}`, "2025-01-01"}, {`{"frequency":"DAILY","start_date":"2025-01-01"}`, "2025-01-01"}, {`{"frequency":"DAILY","start_date":"2025-01-01"}`, "2025-01-01"}}
	raw, _ := json.Marshal(cases)
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
from app.services import draft_notifications
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object(); draft_notifications.notify_users_of_new_drafts=lambda *a: None
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
result=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for i,(schedule,key) in enumerate(json.loads(os.environ['CASES'])):
  with engine.begin() as db:
   db.execute(text("DELETE FROM invoice_line_items WHERE invoice_id <> '20000000-0000-0000-0000-000000000001'"));db.execute(text("DELETE FROM invoices WHERE NOT is_template"));db.execute(text("UPDATE invoices SET billing_cycle_ref=cast(:s as jsonb) WHERE is_template"),{'s':schedule})
  if i >= 9:
   with engine.begin() as db: db.execute(text("UPDATE invoices SET currency=:c,wht_gross_up=:g WHERE is_template"),{'c':'IDR' if i<11 else 'SGD','g':i>9})
  if i == 12:
   with engine.begin() as db: db.execute(text("UPDATE invoices SET wht_rate=NULL WHERE is_template"))
  r=c.post('/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger',json={'cycle_key':key});d=r.json()
  result.append([r.status_code, {k:(d.get(k) if k != 'line_items' else [line['description'] for line in d[k]]) for k in ['subtotal','amount','wht_base','wht_amount','payment_terms','billing_cycle_ref','coverage_start','coverage_end','line_items']} if r.status_code==200 else d.get('detail')])
print(json.dumps(result))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pd.URL(), "CASES="+string(raw))
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, output)
	}
	var expected []any
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("python output: %s: %v", output, e)
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

	itemTx, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = itemTx.Exec(context.Background(), `UPDATE items SET is_hosting=false WHERE item_id='40000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	itemResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(`{"cycle_key":"2025-01-01"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		itemResult <- w
	}()
	waited := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e = p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')`).Scan(&waited); e != nil {
			t.Fatal(e)
		}
		if waited {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = itemTx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	response := <-itemResult
	if !waited || response.Code != 200 || !strings.Contains(response.Body.String(), `"coverage_start":null`) {
		t.Fatalf("hosting item lock waited %v, status %d: %s", waited, response.Code, response.Body.String())
	}
	if _, e = p.Exec(context.Background(), `UPDATE items SET is_hosting=true WHERE item_id='40000000-0000-0000-0000-000000000001'`); e != nil {
		t.Fatal(e)
	}
	for i, tc := range cases {
		_, e = p.Exec(context.Background(), `DELETE FROM invoice_line_items WHERE invoice_id <> '20000000-0000-0000-0000-000000000001'; DELETE FROM invoices WHERE NOT is_template`)
		if e != nil {
			t.Fatal(e)
		}
		_, e = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref=$1 WHERE is_template`, tc[0])
		if e != nil {
			t.Fatal(e)
		}
		if i >= 9 {
			currency := "IDR"
			if i >= 11 {
				currency = "SGD"
			}
			_, e = p.Exec(context.Background(), `UPDATE invoices SET currency=$1,wht_gross_up=$2 WHERE is_template`, currency, i > 9)
			if e != nil {
				t.Fatal(e)
			}
		}
		if i == 12 {
			if _, e = p.Exec(context.Background(), `UPDATE invoices SET wht_rate=NULL WHERE is_template`); e != nil {
				t.Fatal(e)
			}
		}
		b, _ := json.Marshal(map[string]string{"cycle_key": tc[1]})
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var d map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &d); e != nil {
			t.Fatal(e)
		}
		var result any = d["detail"]
		if w.Code == 200 {
			m := map[string]any{}
			for _, k := range []string{"subtotal", "amount", "wht_base", "wht_amount", "payment_terms", "billing_cycle_ref", "coverage_start", "coverage_end", "line_items"} {
				if k == "line_items" {
					var descriptions []any
					for _, line := range d[k].([]any) {
						descriptions = append(descriptions, line.(map[string]any)["description"])
					}
					m[k] = descriptions
				} else {
					m[k] = d[k]
				}
			}
			result = m
		}
		got := []any{float64(w.Code), result}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Fatalf("case %d: go %#v python %#v", i, got, expected[i])
		}
	}
	bodies := []string{"", `null`, `[]`, `{}`, `{"cycle_key":null}`, `{"cycle_key":123}`, `{"cycle_key":""}`, `{"cycle_key":"2024-12-31"}`, `{"cycle_key":"2025-01-01","extra":true}`}
	bjson, _ := json.Marshal(bodies)
	bcmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
from app.services import draft_notifications
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1));app=FastAPI();app.include_router(router,prefix='/api/v1');app.dependency_overrides[current_admin]=lambda:object();draft_notifications.notify_users_of_new_drafts=lambda *a: None
def session():
 with Session(engine) as db:yield db
app.dependency_overrides[get_db]=session
with engine.begin() as db: db.execute(text("UPDATE invoices SET billing_cycle_ref='{\"frequency\":\"DAILY\",\"start_date\":\"2025-01-01\"}' WHERE is_template"))
result=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for body in json.loads(os.environ['BODIES']):
  r=c.post('/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger',content=body,headers={'content-type':'application/json'});d=r.json();detail=d.get('detail');result.append([r.status_code,detail[0]['type'] if isinstance(detail,list) else detail if r.status_code!=200 else None])
print(json.dumps(result))`)
	bcmd.Dir = "../../../backend"
	bcmd.Env = append(os.Environ(), "DB="+pd.URL(), "BODIES="+string(bjson))
	output, e = bcmd.Output()
	if e != nil {
		t.Fatalf("body python %v %s", e, output)
	}
	var bodyExpected [][]any
	if e = json.Unmarshal(output, &bodyExpected); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","start_date":"2025-01-01"}' WHERE is_template`); e != nil {
		t.Fatal(e)
	}
	for i, body := range bodies {
		req := httptest.NewRequest("POST", "/api/v1/invoices/recurring/20000000-0000-0000-0000-000000000001/trigger", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var d map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &d); e != nil {
			t.Fatal(e)
		}
		detail := d["detail"]
		if list, ok := detail.([]any); ok {
			detail = list[0].(map[string]any)["type"]
		}
		if w.Code == 200 {
			detail = nil
		}
		if !reflect.DeepEqual([]any{float64(w.Code), detail}, bodyExpected[i]) {
			t.Fatalf("body %q: Go %d %#v Python %#v", body, w.Code, detail, bodyExpected[i])
		}
	}

}
