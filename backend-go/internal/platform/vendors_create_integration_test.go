package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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

func TestVendorCreatePostgres(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	_, e = pool.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','user')`)
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pool, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token := func(id string) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	admin := token("00000000-0000-0000-0000-000000000001")
	user := token("00000000-0000-0000-0000-000000000002")
	call := func(body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/vendors", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("missing no-store")
		}
		return w
	}
	for _, tc := range []struct {
		body   string
		c      *http.Cookie
		status int
	}{{`{"name":"A"}`, nil, 401}, {`{"name":"A"}`, user, 403}, {`{"name":"A"}`, &http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}, {`{"name":"","active":null}`, admin, 422}, {`{`, admin, 422}, {`{"name":"A"}`, admin, 201}, {`{"name":"B","contact_email":"Test@EXAMPLE.COM","contact_name":"C","default_currency":"USD","payment_terms_days":"5","tax_id":"T","notes":"N","active":"false"}`, admin, 201}} {
		w := call(tc.body, tc.c)
		if w.Code != tc.status {
			t.Fatalf("%s got %d %s want %d", tc.body, w.Code, w.Body, tc.status)
		}
		if tc.status == 201 {
			var out map[string]any
			if json.Unmarshal(w.Body.Bytes(), &out) != nil || out["vendor_id"] == nil || out["created_at"] == nil {
				t.Fatalf("invalid response %s", w.Body)
			}
			if out["name"] == "A" && (out["default_currency"] != "IDR" || out["payment_terms_days"] != float64(30) || out["active"] != true || out["contact_email"] != nil) {
				t.Fatal(out)
			}
			if out["name"] == "B" && (out["contact_email"] != "Test@example.com" || out["active"] != false) {
				t.Fatal(out)
			}
		}
	}
	w := call(`{"password":"supersecret"}`, admin)
	if strings.Contains(w.Body.String(), "supersecret") {
		t.Fatal("password leaked")
	}
	var count int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM vendors`).Scan(&count); e != nil || count != 2 {
		t.Fatalf("count %d %v", count, e)
	}
	if w := call(strings.Repeat("x", (1<<20)+1), admin); w.Code != 413 {
		t.Fatalf("large body: %d %s", w.Code, w.Body)
	}
	_, e = pool.Exec(context.Background(), `CREATE FUNCTION reject_vendor() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private database detail'; END $$; CREATE TRIGGER reject_vendor BEFORE INSERT ON vendors FOR EACH ROW EXECUTE FUNCTION reject_vendor()`)
	if e != nil {
		t.Fatal(e)
	}
	if w := call(`{"name":"Failure"}`, admin); w.Code != 500 || strings.Contains(w.Body.String(), "private database detail") {
		t.Fatalf("database failure: %d %s", w.Code, w.Body)
	}
	_, e = pool.Exec(context.Background(), `DROP TRIGGER reject_vendor ON vendors; DROP FUNCTION reject_vendor()`)
	if e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM vendors`).Scan(&count); e != nil || count != 2 {
		t.Fatalf("failure changed rows: %d %v", count, e)
	}
	// A deferred failure happens only at commit, after INSERT has succeeded.
	_, e = pool.Exec(context.Background(), `CREATE FUNCTION reject_vendor_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private commit detail'; END $$; CREATE CONSTRAINT TRIGGER reject_vendor_commit AFTER INSERT ON vendors DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_vendor_commit()`)
	if e != nil {
		t.Fatal(e)
	}
	if w := call(`{"name":"CommitFailure"}`, admin); w.Code != 500 || strings.Contains(w.Body.String(), "private commit detail") {
		t.Fatalf("commit failure: %d %s", w.Code, w.Body)
	}
	_, e = pool.Exec(context.Background(), `DROP TRIGGER reject_vendor_commit ON vendors; DROP FUNCTION reject_vendor_commit()`)
	if e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM vendors`).Scan(&count); e != nil || count != 2 {
		t.Fatalf("commit failure changed rows: %d %v", count, e)
	}
	// The first auth check sees an admin; the transaction must recheck after waiting on this lock.
	lockTx, e := pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lockTx.Exec(context.Background(), `UPDATE users SET role='user' WHERE email='a@example.com'`); e != nil {
		t.Fatal(e)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- call(`{"name":"Race"}`, admin) }()
	time.Sleep(100 * time.Millisecond)
	if e = lockTx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if w := <-result; w.Code != 403 {
		t.Fatalf("demoted during request: %d %s", w.Code, w.Body)
	}
	if _, e = pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE email='a@example.com'`); e != nil {
		t.Fatal(e)
	}
	pydb := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	cases := []string{`{"name":"Parity"}`, `{"name":"Full","contact_email":"Test@EXAMPLE.COM","contact_name":"C","default_currency":"USD","payment_terms_days":"5","tax_id":"T","notes":"N","active":"false"}`, `{"name":"Nulls","contact_email":null,"contact_name":null,"notes":null,"tax_id":null}`, `{"name":"","default_currency":null,"payment_terms_days":366,"active":null}`, `{"name":"A","contact_email":"bad"}`, `{"name":"A","payment_terms_days":1.5}`, `{"name":"A","payment_terms_days":true}`, `[]`, `{}`}
	command := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.vendors import router
from app.api.deps import current_admin
from app.db.session import get_db
engine = create_engine(os.environ["VENDOR_DB"].replace("postgres://", "postgresql+psycopg://", 1))
app = FastAPI(); app.include_router(router, prefix="/api/v1")
app.dependency_overrides[current_admin] = lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db] = session
with TestClient(app) as client:
    print(json.dumps([[r.status_code, r.json()] for r in (client.post("/api/v1/vendors", content=p, headers={"content-type":"application/json"}) for p in json.loads(os.environ["VENDOR_CASES"]))]))
`)
	encoded, _ := json.Marshal(cases)
	command.Dir = "../../../backend"
	command.Env = append(os.Environ(), "VENDOR_DB="+pydb.URL(), "VENDOR_CASES="+string(encoded))
	output, e := command.Output()
	if e != nil {
		t.Fatalf("Python HTTP: %v %s", e, output)
	}
	var expected [][]any
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e)
	}
	for i, body := range cases {
		w := call(body, admin)
		var got any
		if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatal(e)
		}
		if w.Code == 201 {
			delete(got.(map[string]any), "vendor_id")
			delete(got.(map[string]any), "created_at")
			delete(expected[i][1].(map[string]any), "vendor_id")
			delete(expected[i][1].(map[string]any), "created_at")
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(got, expected[i][1]) {
			t.Errorf("parity %s: Go %d %v Python %v", body, w.Code, got, expected[i])
		}
	}
	mediaCases := []string{"application/json", "application/vnd.billing+json", "application/JSON", "", "text/plain", "bad", "application / json", "application/json; garbage"}
	mediaCmd := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.vendors import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ["VENDOR_DB"].replace("postgres://", "postgresql+psycopg://", 1))
app=FastAPI();app.include_router(router,prefix="/api/v1");app.dependency_overrides[current_admin]=lambda:object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as client:
    out=[]
    for ct in json.loads(os.environ["MEDIA_CASES"]):
        for body in ['{"name":"Media"}', '{']:
            r=client.post('/api/v1/vendors',content=body,headers={'content-type':ct} if ct else {})
            out.append([r.status_code,r.json()])
    print(json.dumps(out))
`)
	mediaJSON, _ := json.Marshal(mediaCases)
	mediaCmd.Dir = "../../../backend"
	mediaCmd.Env = append(os.Environ(), "VENDOR_DB="+pydb.URL(), "MEDIA_CASES="+string(mediaJSON))
	mediaOut, e := mediaCmd.Output()
	if e != nil {
		t.Fatalf("Python media: %v %s", e, mediaOut)
	}
	var mediaExpected [][]any
	if e = json.Unmarshal(mediaOut, &mediaExpected); e != nil {
		t.Fatal(e)
	}
	idx := 0
	for _, ct := range mediaCases {
		for _, body := range []string{`{"name":"Media"}`, `{`} {
			var before, after int
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM vendors`).Scan(&before); e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("POST", "/api/v1/vendors", strings.NewReader(body))
			if ct != "" {
				r.Header.Set("Content-Type", ct)
			}
			r.AddCookie(admin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			var got any
			if e = json.Unmarshal(rec.Body.Bytes(), &got); e != nil {
				t.Fatal(e)
			}
			if rec.Code == 201 {
				delete(got.(map[string]any), "vendor_id")
				delete(got.(map[string]any), "created_at")
				delete(mediaExpected[idx][1].(map[string]any), "vendor_id")
				delete(mediaExpected[idx][1].(map[string]any), "created_at")
			}
			// Raw input is deliberately redacted instead of mirroring FastAPI's echo.
			if ct == "" || ct == "text/plain" || ct == "bad" || ct == "application / json" {
				mediaExpected[idx][1].(map[string]any)["detail"].([]any)[0].(map[string]any)["input"] = "[REDACTED]"
			}
			if float64(rec.Code) != mediaExpected[idx][0] || !reflect.DeepEqual(got, mediaExpected[idx][1]) {
				t.Errorf("media %q body %q Go %d %v Python %v", ct, body, rec.Code, got, mediaExpected[idx])
			}
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM vendors`).Scan(&after); e != nil {
				t.Fatal(e)
			}
			if mediaExpected[idx][0] != float64(201) && after != before {
				t.Errorf("media %q wrote a row", ct)
			}
			idx++
		}
	}
	for _, ct := range []string{"text/plain", "", "bad"} {
		for _, body := range []string{`{"pass\u0077ord":"supersecret"}`, `arbitrary-secret`, `{"password":"raw-secret"}`, `{`} {
			var before, after int
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM vendors`).Scan(&before); e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("POST", "/api/v1/vendors", strings.NewReader(body))
			if ct != "" {
				r.Header.Set("Content-Type", ct)
			}
			r.AddCookie(admin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != 422 || strings.Contains(rec.Body.String(), "supersecret") || strings.Contains(rec.Body.String(), "arbitrary-secret") || strings.Contains(rec.Body.String(), "raw-secret") {
				t.Errorf("non-JSON leaked or wrong status %q %q: %d %s", ct, body, rec.Code, rec.Body)
			}
			var result map[string]any
			if e = json.Unmarshal(rec.Body.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
			if result["detail"].([]any)[0].(map[string]any)["input"] != "[REDACTED]" {
				t.Errorf("raw input not redacted: %s", rec.Body)
			}
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM vendors`).Scan(&after); e != nil || after != before {
				t.Fatalf("non-JSON wrote vendor: %d %d %v", before, after, e)
			}
		}
	}

	for _, tc := range []struct {
		body   string
		cookie *http.Cookie
		status int
	}{
		{`secret-unauth`, nil, 401}, {`secret-nonadmin`, user, 403}, {strings.Repeat("a", 1<<20) + "secret-too-large", admin, 413}, {"", admin, 422},
	} {
		req := httptest.NewRequest("POST", "/api/v1/vendors", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "text/plain")
		if tc.cookie != nil {
			req.AddCookie(tc.cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.status || strings.Contains(rec.Body.String(), "secret-") {
			t.Errorf("non-JSON error: %d %s", rec.Code, rec.Body)
		}
	}
	// Every implemented write route must reject unparsed input before changing data.
	var requestLogs bytes.Buffer
	oldLogWriter := log.Writer()
	log.SetOutput(&requestLogs)
	defer log.SetOutput(oldLogWriter)
	routeCases := []struct{ Method, Path, Body string }{
		{"POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"raw-secret"}`},
		{"POST", "/api/v1/auth/change-password", `{"current_password":"raw-secret","new_password":"other-secret"}`},
		{"POST", "/api/v1/auth/forgot-password", `{"email":"a@example.com","pass\u0077ord":"raw-secret"}`},
		{"POST", "/api/v1/auth/reset-password", `{"token":"raw-secret","new_password":"other-secret"}`},
		{"POST", "/api/v1/users", `{"email":"new@example.com","password":"raw-secret"}`},
		{"POST", "/api/v1/users/00000000-0000-0000-0000-000000000001/reset-password", `{"password":"raw-secret"}`},
		{"PATCH", "/api/v1/users/me", `{"display_name":"raw-secret"}`},
		{"POST", "/api/v1/accounting/chart-of-accounts", `{"code":"X","name":"raw-secret","type":"ASSET"}`},
		{"PATCH", "/api/v1/accounting/chart-of-accounts/1", `{"name":"raw-secret"}`},
		{"POST", "/api/v1/items", `{"name":"raw-secret","item_type":"SERVICE"}`},
		{"PATCH", "/api/v1/items/00000000-0000-0000-0000-000000000001", `{"name":"raw-secret"}`},
		{"POST", "/api/v1/customers", `{"name":"raw-secret"}`},
		{"PATCH", "/api/v1/customers/00000000-0000-0000-0000-000000000001", `{"name":"raw-secret"}`},
		{"POST", "/api/v1/vendors", `{"name":"raw-secret"}`},
	}
	routeJSON, _ := json.Marshal(routeCases)
	diffCmd := exec.Command("python", "-c", `
import json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from starlette.middleware.sessions import SessionMiddleware
from passlib.context import CryptContext
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
from app.api.deps import current_admin
from app.db.session import get_db
from app.api.v1.routers import auth, users, accounting, items, customers, vendors
app=FastAPI();app.add_middleware(SessionMiddleware,secret_key="test")
for module in (auth, users, accounting, items, customers, vendors): app.include_router(module.router, prefix='/api/v1')
engine=create_engine(os.environ['DIFF_DB'].replace('postgres://','postgresql+psycopg://',1))
with engine.begin() as conn:
 conn.execute(text("INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com',:h,'admin')"),{'h':CryptContext(schemes=['bcrypt']).hash('old-secret')})
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
out=[]
with TestClient(app) as c:
 assert c.post("/api/v1/auth/login",json={"email":"a@example.com","password":"old-secret"}).status_code==200
 for case in json.loads(os.environ['DIFF_CASES']):
  for media,body in (('',case['Body']),('text/plain',case['Body']),('bad',case['Body']),('application/foo/bar+json',case['Body']),('application//foo+json',case['Body']),('application/foo+json/',case['Body']),('application/foo+json, text/plain',case['Body']),('application/json',''),('application/json','{')):
   r=c.request(case['Method'],case['Path'],content=body,headers={'content-type':media} if media else {})
   detail=r.json()['detail']
   out.append([r.status_code,[[d['type'],d['loc'],d['msg']] for d in detail]])
print(json.dumps(out))
`)
	diffCmd.Dir = "../../../backend"
	diffCmd.Env = append(os.Environ(), "DIFF_DB="+pydb.URL(), "DIFF_CASES="+string(routeJSON))
	diffOut, err := diffCmd.Output()
	if err != nil {
		t.Fatalf("FastAPI route differential: %v %s", err, diffOut)
	}
	var routeExpected [][]any
	if err := json.Unmarshal(diffOut, &routeExpected); err != nil {
		t.Fatal(err)
	}
	routeIndex := 0
	for _, tc := range routeCases {
		for _, variant := range []struct{ media, body string }{{"", tc.Body}, {"text/plain", tc.Body}, {"bad", tc.Body}, {"application/foo/bar+json", tc.Body}, {"application//foo+json", tc.Body}, {"application/foo+json/", tc.Body}, {"application/foo+json, text/plain", tc.Body}, {"application/json", ""}, {"application/json", "{"}} {
			media := variant.media
			snapshot := func() string {
				var value string
				query := `SELECT md5((SELECT coalesce(string_agg(t::text, '' ORDER BY t::text),'') FROM users t) || (SELECT coalesce(string_agg(t::text, '' ORDER BY t::text),'') FROM chart_of_accounts t) || (SELECT coalesce(string_agg(t::text, '' ORDER BY t::text),'') FROM items t) || (SELECT coalesce(string_agg(t::text, '' ORDER BY t::text),'') FROM customers t) || (SELECT coalesce(string_agg(t::text, '' ORDER BY t::text),'') FROM vendors t))`
				if err := pool.QueryRow(context.Background(), query).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			before := snapshot()
			req := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(variant.body))
			if media != "" {
				req.Header.Set("Content-Type", media)
			}
			req.AddCookie(admin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 422 || strings.Contains(rec.Body.String(), "raw-secret") || strings.Contains(rec.Body.String(), "other-secret") {
				t.Errorf("%s %s %q: %d %s", tc.Method, tc.Path, media, rec.Code, rec.Body)
			}
			var parsed struct {
				Detail []struct {
					Type string `json:"type"`
					Loc  []any  `json:"loc"`
					Msg  string `json:"msg"`
				} `json:"detail"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
				t.Fatal(err)
			}
			gotErrors := make([]any, 0, len(parsed.Detail))
			for _, detail := range parsed.Detail {
				gotErrors = append(gotErrors, []any{detail.Type, detail.Loc, detail.Msg})
			}
			if float64(rec.Code) != routeExpected[routeIndex][0] || !reflect.DeepEqual(gotErrors, routeExpected[routeIndex][1]) {
				t.Errorf("FastAPI mismatch %s %s %q: %d %v vs %v", tc.Method, tc.Path, media, rec.Code, gotErrors, routeExpected[routeIndex])
			}
			routeIndex++
			if snapshot() != before {
				t.Errorf("%s %s %q changed data", tc.Method, tc.Path, media)
			}
		}
	}
	if strings.Contains(requestLogs.String(), "raw-secret") || strings.Contains(requestLogs.String(), "other-secret") {
		t.Fatal("request logs leaked credentials")
	}
	// Real success paths use separate disposable Python and Go databases.
	successCases := []struct{ Method, Path, Body string }{
		{"POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"old-secret"}`},
		{"POST", "/api/v1/auth/change-password", `{"current_password":"old-secret","new_password":"new-secret"}`},
		{"POST", "/api/v1/auth/forgot-password", `{"email":"absent@example.com"}`},
		{"POST", "/api/v1/auth/reset-password", `{"token":"test-token","new_password":"new-secret"}`},
		{"POST", "/api/v1/users", `{"email":"newVAR@example.com","password":"new-secret"}`},
		{"POST", "/api/v1/users/00000000-0000-0000-0000-000000000001/reset-password", `{"password":"new-secret"}`},
		{"PATCH", "/api/v1/users/me", `{"display_name":"Updated"}`},
		{"POST", "/api/v1/accounting/chart-of-accounts", `{"code":"T28VAR","name":"New","type":"ASSET"}`},
		{"PATCH", "/api/v1/accounting/chart-of-accounts/1", `{"name":"Updated"}`},
		{"POST", "/api/v1/items", `{"name":"New","item_type":"SERVICE"}`},
		{"PATCH", "/api/v1/items/10000000-0000-0000-0000-000000000001", `{"name":"Updated"}`},
		{"POST", "/api/v1/customers", `{"name":"New"}`},
		{"PATCH", "/api/v1/customers/10000000-0000-0000-0000-000000000001", `{"name":"Updated"}`},
		{"POST", "/api/v1/vendors", `{"name":"New"}`},
	}
	successJSON, _ := json.Marshal(successCases)
	successCmd := exec.Command("python", "-c", `
import json,os,uuid
from fastapi import FastAPI,Depends
from fastapi.testclient import TestClient
from starlette.middleware.sessions import SessionMiddleware
from sqlalchemy import create_engine,text
from sqlalchemy.orm import Session
from passlib.context import CryptContext
from app.api.deps import current_admin
from app.db.session import get_db
from app.models.user import User
from app.api.v1.routers import auth,users,accounting,items,customers,vendors
app=FastAPI();app.add_middleware(SessionMiddleware,secret_key='test')
for module in (auth,users,accounting,items,customers,vendors):app.include_router(module.router,prefix='/api/v1')
engine=create_engine(os.environ['DIFF_DB'].replace('postgres://','postgresql+psycopg://',1))
hash=CryptContext(schemes=['bcrypt']).hash('old-secret')
with engine.begin() as c:
 c.execute(text("INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com',:h,'admin') ON CONFLICT (user_id) DO UPDATE SET password_hash=:h"),{'h':hash})
 c.execute(text("INSERT INTO items(item_id,name,item_type,default_currency,is_sold,is_purchased,active,is_hosting) VALUES ('10000000-0000-0000-0000-000000000001','Before','SERVICE','IDR',true,false,true,false)"))
 c.execute(text("INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Before')"))
def session():
 with Session(engine) as db:yield db
def admin(db:Session=Depends(get_db)):return db.get(User,uuid.UUID('00000000-0000-0000-0000-000000000001'))
app.dependency_overrides[get_db]=session
out=[]
with TestClient(app) as client:
 assert client.post("/api/v1/auth/login",json={"email":"a@example.com","password":"old-secret"}).status_code==200
 for case in json.loads(os.environ['DIFF_CASES']):
  for i,media in enumerate(['application/json','Application/JSON; charset=utf-8','application/vnd.billing+json','application/+json','application/foo+json; bad']):
   with engine.begin() as c:c.execute(text("UPDATE users SET password_hash=:h,password_reset_token='test-token',password_reset_expires=now()+interval '1 hour' WHERE user_id='00000000-0000-0000-0000-000000000001'"),{'h':hash})
   r=client.request(case['Method'],case['Path'],content=case['Body'].replace('VAR',str(i)),headers={'content-type':media});out.append(r.status_code)
print(json.dumps(out))
`)
	successCmd.Dir = "../../../backend"
	successCmd.Env = append(os.Environ(), "DIFF_DB="+pydb.URL(), "DIFF_CASES="+string(successJSON))
	successOut, err := successCmd.Output()
	if err != nil {
		t.Fatalf("success differential %v %s", err, successOut)
	}
	var successExpected []int
	if err := json.Unmarshal(successOut, &successExpected); err != nil {
		t.Fatal(err)
	}
	fastHash, err := auth.Hash("old-secret", auth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO items(item_id,name,item_type,default_currency,is_sold,is_purchased,active,is_hosting) VALUES ('10000000-0000-0000-0000-000000000001','Before','SERVICE','IDR',true,false,true,false); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Before')`)
	if err != nil {
		t.Fatal(err)
	}
	successIndex := 0
	for _, tc := range successCases {
		for i, media := range []string{"application/json", "Application/JSON; charset=utf-8", "application/vnd.billing+json", "application/+json", "application/foo+json; bad"} {
			_, err = pool.Exec(context.Background(), `UPDATE users SET password_hash=$1,password_reset_token='test-token',password_reset_expires=now()+interval '1 hour' WHERE user_id='00000000-0000-0000-0000-000000000001'`, fastHash)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(strings.ReplaceAll(tc.Body, "VAR", fmt.Sprint(i))))
			req.Header.Set("Content-Type", media)
			req.AddCookie(admin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code < 200 || rec.Code >= 300 || rec.Code != successExpected[successIndex] {
				t.Errorf("success %s %s %s: Go %d %s Python %d", tc.Method, tc.Path, media, rec.Code, rec.Body, successExpected[successIndex])
			}
			successIndex++
		}
	}
	// Invalid paths still participate in validation after JSON decoding.
	pathCases := []struct{ Method, Path, Body string }{
		{"POST", "/api/v1/users/not-a-uuid/reset-password", `{"password":"raw-secret"}`},
		{"PATCH", "/api/v1/accounting/chart-of-accounts/not-an-int", `{"name":"raw-secret"}`},
		{"PATCH", "/api/v1/items/not-a-uuid", `{"name":"raw-secret"}`},
		{"PATCH", "/api/v1/customers/not-a-uuid", `{"name":"raw-secret"}`},
	}
	pathJSON, _ := json.Marshal(pathCases)
	pathCmd := exec.Command("python", "-c", `
import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from starlette.middleware.sessions import SessionMiddleware
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.deps import current_admin
from app.db.session import get_db
from app.api.v1.routers import auth,users,accounting,items,customers
app=FastAPI();app.add_middleware(SessionMiddleware,secret_key="test")
for module in (auth,users,accounting,items,customers): app.include_router(module.router,prefix='/api/v1')
engine=create_engine(os.environ['DIFF_DB'].replace('postgres://','postgresql+psycopg://',1))
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
out=[]
with TestClient(app) as c:
 assert c.post("/api/v1/auth/login",json={"email":"a@example.com","password":"old-secret"}).status_code==200
 for case in json.loads(os.environ['DIFF_CASES']):
  for media,body in [('application/json','{'),('text/plain',case['Body']),('application/json',case['Body'])]:
   r=c.request(case['Method'],case['Path'],content=body,headers={'content-type':media})
   out.append([r.status_code,[[d['type'],d['loc'],d['msg']] for d in r.json()['detail']]])
print(json.dumps(out))
`)
	pathCmd.Dir = "../../../backend"
	pathCmd.Env = append(os.Environ(), "DIFF_DB="+pydb.URL(), "DIFF_CASES="+string(pathJSON))
	pathOut, err := pathCmd.Output()
	if err != nil {
		t.Fatalf("path differential: %v %s", err, pathOut)
	}
	var pathExpected [][]any
	if err := json.Unmarshal(pathOut, &pathExpected); err != nil {
		t.Fatal(err)
	}
	pathIndex := 0
	for _, tc := range pathCases {
		for _, variant := range []struct{ media, body string }{{"application/json", "{"}, {"text/plain", tc.Body}, {"application/json", tc.Body}} {
			req := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(variant.body))
			req.Header.Set("Content-Type", variant.media)
			req.AddCookie(admin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			var parsed struct {
				Detail []struct {
					Type string `json:"type"`
					Loc  []any  `json:"loc"`
					Msg  string `json:"msg"`
				} `json:"detail"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
				t.Fatal(err)
			}
			got := make([]any, 0, len(parsed.Detail))
			for _, d := range parsed.Detail {
				got = append(got, []any{d.Type, d.Loc, d.Msg})
			}
			if float64(rec.Code) != pathExpected[pathIndex][0] || !reflect.DeepEqual(got, pathExpected[pathIndex][1]) {
				t.Errorf("path precedence %s %s %q: %d %v vs %v", tc.Method, tc.Path, variant.media, rec.Code, got, pathExpected[pathIndex])
			}
			pathIndex++
		}
	}
	// Compare syntax versus authentication precedence on every protected route.
	authCmd := exec.Command("python", "-c", `
import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from starlette.middleware.sessions import SessionMiddleware
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.deps import current_admin
from app.db.session import get_db
from app.api.v1.routers import auth,users,accounting,items,customers,vendors
app=FastAPI(); app.add_middleware(SessionMiddleware,secret_key='test')
for module in (auth,users,accounting,items,customers,vendors): app.include_router(module.router,prefix='/api/v1')
engine=create_engine(os.environ['DIFF_DB'].replace('postgres://','postgresql+psycopg://',1))
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
out=[]
with TestClient(app) as c:
 for case in json.loads(os.environ['DIFF_CASES']):
  if case['Path'] in ('/api/v1/auth/login','/api/v1/auth/forgot-password','/api/v1/auth/reset-password'): continue
  for media,body in [('application/json','{'),('text/plain','{'),('application/foo/bar+json','{'),('application/json',case['Body'])]:
   r=c.request(case['Method'],case['Path'],content=body,headers={'content-type':media}); out.append(r.status_code)
print(json.dumps(out))
`)
	authCmd.Dir = "../../../backend"
	authCmd.Env = append(os.Environ(), "DIFF_DB="+pydb.URL(), "DIFF_CASES="+string(routeJSON))
	authOut, err := authCmd.Output()
	if err != nil {
		t.Fatalf("auth differential: %v %s", err, authOut)
	}
	var authExpected []int
	if err := json.Unmarshal(authOut, &authExpected); err != nil {
		t.Fatal(err)
	}
	authIndex := 0
	for _, tc := range routeCases {
		if tc.Path == "/api/v1/auth/login" || tc.Path == "/api/v1/auth/forgot-password" || tc.Path == "/api/v1/auth/reset-password" {
			continue
		}
		for _, variant := range []struct{ media, body string }{{"application/json", "{"}, {"text/plain", "{"}, {"application/foo/bar+json", "{"}, {"application/json", tc.Body}} {
			req := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(variant.body))
			req.Header.Set("Content-Type", variant.media)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != authExpected[authIndex] {
				t.Errorf("auth precedence %s %s %s %q: Go %d Python %d", tc.Method, tc.Path, variant.media, variant.body, rec.Code, authExpected[authIndex])
			}
			authIndex++
		}
	}
	_, e = pool.Exec(context.Background(), `UPDATE users SET role='user' WHERE email='a@example.com'`)
	if e != nil {
		t.Fatal(e)
	}
	if w = call(`{"name":"C"}`, admin); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestVendorCreateOpenAPI(t *testing.T) {
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(nil, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &doc); e != nil {
		t.Fatal(e)
	}
	op := doc["paths"].(map[string]any)["/api/v1/vendors"].(map[string]any)["post"].(map[string]any)
	if op["security"] == nil || op["requestBody"] == nil {
		t.Fatal("missing security or request body")
	}
	responses := op["responses"].(map[string]any)
	for _, status := range []string{"201", "401", "403", "413", "422", "500"} {
		if responses[status] == nil {
			t.Errorf("missing %s", status)
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"VendorCreateSchema", "VendorOutputSchema"} {
		schema := schemas[name].(map[string]any)
		props := schema["properties"].(map[string]any)
		expectedRequired := []any{"name"}
		if name == "VendorOutputSchema" {
			expectedRequired = []any{"name", "vendor_id", "created_at"}
		}
		if !reflect.DeepEqual(schema["required"], expectedRequired) {
			t.Errorf("%s required: %v", name, schema["required"])
		}
		for _, tc := range []struct {
			field, typ string
			min, max   float64
		}{{"name", "string", 1, 255}, {"default_currency", "string", 3, 3}, {"payment_terms_days", "integer", 0, 365}} {
			p := props[tc.field].(map[string]any)
			if p["type"] != tc.typ {
				t.Errorf("%s.%s type: %v", name, tc.field, p)
			}
			low, high := "minLength", "maxLength"
			if tc.typ == "integer" {
				low, high = "minimum", "maximum"
			}
			if p[low] != tc.min || p[high] != tc.max {
				t.Errorf("%s.%s bounds: %v", name, tc.field, p)
			}
		}
		for _, field := range []string{"contact_email", "contact_name", "tax_id", "notes"} {
			p := props[field].(map[string]any)
			b, _ := json.Marshal(p)
			if !strings.Contains(string(b), "null") {
				t.Errorf("%s.%s not nullable: %s", name, field, b)
			}
		}
		if props["active"].(map[string]any)["type"] != "boolean" {
			t.Errorf("%s active type", name)
		}
	}
	if sec := op["security"].([]any); len(sec) != 1 || sec[0].(map[string]any)["sessionCookie"] == nil {
		t.Errorf("security: %v", sec)
	}
	for _, status := range []string{"401", "403", "413", "422", "500"} {
		r := responses[status].(map[string]any)
		if r["content"] == nil {
			t.Errorf("%s missing error schema", status)
		}
	}
	for _, schema := range []struct {
		name   string
		fields []string
	}{{"VendorCreateSchema", []string{"name", "contact_email", "contact_name", "default_currency", "payment_terms_days", "tax_id", "notes", "active"}}, {"VendorOutputSchema", []string{"vendor_id", "name", "contact_email", "contact_name", "default_currency", "payment_terms_days", "tax_id", "notes", "active", "created_at"}}} {
		props := schemas[schema.name].(map[string]any)["properties"].(map[string]any)
		for _, field := range schema.fields {
			if props[field] == nil {
				t.Errorf("%s missing %s", schema.name, field)
			}
		}
	}
}
