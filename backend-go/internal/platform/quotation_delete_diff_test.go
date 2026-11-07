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

func TestQuotationDeleteDifferential(t *testing.T) {
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
 for status in ['SENT','ACCEPTED','DECLINED','EXPIRED','INVOICED','VOID','DRAFT']:
  with engine.begin() as conn: conn.execute(text('UPDATE quotations SET status=:s'),{'s':status})
  r=c.delete('/api/v1/quotations/20000000-0000-0000-0000-000000000001'); results.append([r.status_code,r.text])
 for path in ['20000000-0000-0000-0000-000000000001','bad']:
  r=c.delete('/api/v1/quotations/'+path); results.append([r.status_code,r.text])
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
	paths := []string{}
	for _, status := range []string{"SENT", "ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID", "DRAFT"} {
		if _, e = p.Exec(context.Background(), `UPDATE quotations SET status=$1`, status); e != nil {
			t.Fatal(e)
		}
		paths = append(paths, status)
		req := httptest.NewRequest("DELETE", "/api/v1/quotations/20000000-0000-0000-0000-000000000001", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		compareDelete(t, expected[len(paths)-1], w)
	}
	for i, path := range []string{"20000000-0000-0000-0000-000000000001", "bad"} {
		req := httptest.NewRequest("DELETE", "/api/v1/quotations/"+path, nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		compareDelete(t, expected[7+i], w)
	}
}
func compareDelete(t *testing.T, expected []any, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != int(expected[0].(float64)) {
		t.Fatalf("status: %d expected %v: %s", w.Code, expected, w.Body)
	}
	var a, b any
	es := expected[1].(string)
	if es == "" && w.Body.Len() == 0 {
		return
	}
	if json.Unmarshal([]byte(es), &a) != nil || json.Unmarshal(w.Body.Bytes(), &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("body: %s expected %s", w.Body, es)
	}
}
