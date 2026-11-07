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

func TestQuotationStatusDifferential(t *testing.T) {
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

	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
from app.api.v1.routers.quotations import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
results=[]
with TestClient(app,raise_server_exceptions=False) as c:
 for action in ['mark-sent','accept','decline','void']:
  for status in ['DRAFT','SENT','ACCEPTED','DECLINED','EXPIRED','INVOICED','VOID']:
   with engine.begin() as conn: conn.execute(text('UPDATE quotations SET status=:s'),{'s':status})
   r=c.post('/api/v1/quotations/20000000-0000-0000-0000-000000000001/'+action,json={'accepted_by':'Ada'} if action=='accept' else None)
   data=r.json(); results.append([r.status_code, data.get('detail',data.get('status'))])
from fastapi import Request, HTTPException
def conditional_admin(request: Request):
 if request.headers.get('x-no-auth'): raise HTTPException(status_code=401, detail='Not authenticated')
 return object()
app.dependency_overrides[current_admin]=conditional_admin
with TestClient(app,raise_server_exceptions=False) as c:
 for body,headers in [('{',{'content-type':'application/json','x-no-auth':'1'}), ('{"accepted_by":"Ada"}',{}), ('null',{'content-type':'application/json'})]:
  with engine.begin() as conn: conn.execute(text("UPDATE quotations SET status='DRAFT', accepted_at=NULL, accepted_by=NULL, last_sent_at=NULL"))
  r=c.post('/api/v1/quotations/20000000-0000-0000-0000-000000000001/accept',content=body,headers=headers)
  data=r.json(); results.append([r.status_code, data.get('detail',data.get('status'))])
with TestClient(app,raise_server_exceptions=False) as c:
 for path, body in [('bad', '{"accepted_by":42}'), ('20000000-0000-0000-0000-000000000001', '{"accepted_by":"\\ud800"}'), ('20000000-0000-0000-0000-000000000001', '{"accepted_by":"\\ud83d\\ude00"}'),
                    ('20000000-0000-0000-0000-000000000001', json.dumps({'accepted_by':'é'*256})),
                    ('20000000-0000-0000-0000-000000000001', json.dumps({'accepted_by':'Ada\x00'})),
                    ('20000000-0000-0000-0000-000000000001', json.dumps({'accepted_by':'é'*255+' '}))]:
  with engine.begin() as conn: conn.execute(text("UPDATE quotations SET status='DRAFT', accepted_at=NULL, accepted_by=NULL, last_sent_at=NULL"))
  r=c.post('/api/v1/quotations/'+path+'/accept',content=body,headers={'content-type':'application/json'})
  if r.status_code==500:
   with engine.connect() as conn:
    assert tuple(conn.execute(text("SELECT status,accepted_at,accepted_by FROM quotations")).one()) == ('DRAFT',None,None)
   results.append([r.status_code, [r.headers['content-type'],r.text]])
  else:
   data=r.json()
   if r.status_code==200: results.append([r.status_code, {'status':data['status'],'accepted_by':data['accepted_by'],'accepted_at':bool(data['accepted_at']),'last_sent_at':data['last_sent_at'],'line_items':data['line_items']}])
   else: results.append([r.status_code,data['detail']])
print(json.dumps(results))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pydb.URL())
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("python: %v %s", e, out)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	index := 0
	for _, action := range []string{"mark-sent", "accept", "decline", "void"} {
		for _, status := range []string{"DRAFT", "SENT", "ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"} {
			if _, e = p.Exec(context.Background(), `UPDATE quotations SET status=$1`, status); e != nil {
				t.Fatal(e)
			}
			req := httptest.NewRequest("POST", "/api/v1/quotations/20000000-0000-0000-0000-000000000001/"+action, strings.NewReader(`{"accepted_by":"Ada"}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			var value map[string]any
			if e = json.Unmarshal(w.Body.Bytes(), &value); e != nil {
				t.Fatal(e)
			}
			result := value["detail"]
			if result == nil {
				result = value["status"]
			}
			if w.Code != int(expected[index][0].(float64)) || !reflect.DeepEqual(result, expected[index][1]) {
				t.Fatalf("%s %s: %d %v expected %v", action, status, w.Code, result, expected[index])
			}
			index++
		}
	}
	cases := []struct {
		body, contentType string
		auth              bool
	}{{"{", "application/json", false}, {`{"accepted_by":"Ada"}`, "", true}, {"null", "application/json", true}}
	for _, tc := range cases {
		if _, e = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT', accepted_at=NULL, accepted_by=NULL, last_sent_at=NULL`); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("POST", "/api/v1/quotations/20000000-0000-0000-0000-000000000001/accept", strings.NewReader(tc.body))
		if tc.contentType != "" {
			req.Header.Set("Content-Type", tc.contentType)
		}
		if tc.auth {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var value map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &value); e != nil {
			t.Fatal(e)
		}
		result := value["detail"]
		if result == nil {
			result = value["status"]
		}
		if w.Code != int(expected[index][0].(float64)) || (tc.contentType != "" && !reflect.DeepEqual(result, expected[index][1])) {
			t.Fatalf("body %q: %d %v expected %v", tc.body, w.Code, result, expected[index])
		}
		index++
		if w.Code != 200 {
			var status string
			var acceptedAt, acceptedBy any
			if e = p.QueryRow(context.Background(), `SELECT status, accepted_at, accepted_by FROM quotations`).Scan(&status, &acceptedAt, &acceptedBy); e != nil || status != "DRAFT" || acceptedAt != nil || acceptedBy != nil {
				t.Fatalf("rejected request wrote data: %s %v %v %v", status, acceptedAt, acceptedBy, e)
			}
		}
	}

	const quotationID = "20000000-0000-0000-0000-000000000001"
	for _, tc := range []struct{ path, body string }{
		{"bad", `{"accepted_by":42}`},
		{quotationID, `{"accepted_by":"\ud800"}`},
		{quotationID, `{"accepted_by":"\ud83d\ude00"}`},
		{quotationID, `{"accepted_by":"` + strings.Repeat("é", 256) + `"}`},
		{quotationID, `{"accepted_by":"Ada\u0000"}`},
		{quotationID, `{"accepted_by":"` + strings.Repeat("é", 255) + ` "}`},
	} {
		if _, e = p.Exec(context.Background(), `UPDATE quotations SET status='DRAFT', accepted_at=NULL, accepted_by=NULL, last_sent_at=NULL`); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("POST", "/api/v1/quotations/"+tc.path+"/accept", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var result any
		if w.Code == 500 {
			result = []any{w.Header().Get("Content-Type"), w.Body.String()}
		} else {
			var v map[string]any
			if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil {
				t.Fatal(e)
			}
			if w.Code == 200 {
				result = map[string]any{"status": v["status"], "accepted_by": v["accepted_by"], "accepted_at": v["accepted_at"] != nil, "last_sent_at": v["last_sent_at"], "line_items": v["line_items"]}
			} else {
				result = v["detail"]
			}
		}
		if w.Code != int(expected[index][0].(float64)) || !reflect.DeepEqual(result, expected[index][1]) {
			t.Fatalf("%s %s: %d %#v expected %#v", tc.path, tc.body, w.Code, result, expected[index])
		}
		index++
		if w.Code != 200 {
			var state string
			var acceptedAt, acceptedBy any
			if e = p.QueryRow(context.Background(), `SELECT status,accepted_at,accepted_by FROM quotations`).Scan(&state, &acceptedAt, &acceptedBy); e != nil || state != "DRAFT" || acceptedAt != nil || acceptedBy != nil {
				t.Fatalf("rejected request changed acceptance: %s %v %v %v", state, acceptedAt, acceptedBy, e)
			}
		}
	}

}
