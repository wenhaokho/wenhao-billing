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

func TestFXReadsDifferential(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pyDB := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pool, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	seedSQL := `DELETE FROM fx_rates; INSERT INTO fx_rates(rate_id,from_currency,to_currency,rate,as_of_date,source,created_at) VALUES (101,'USD','IDR',123.456,'2020-01-01','MANUAL','2020-01-01 12:00:00.123400'),(102,'USD','IDR',124,'2020-01-02','MANUAL','2020-01-02 12:00:00'),(103,'IDR','IDR',1,'2020-01-02','SEED','2020-01-02 12:00:00'); INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','u@example.com','x','user')`
	_, err = pool.Exec(context.Background(), seedSQL)
	if err != nil {
		t.Fatal(err)
	}
	pyPool, err := pgxpool.New(context.Background(), pyDB.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pyPool.Close()
	if _, err = pyPool.Exec(context.Background(), seedSQL); err != nil {
		t.Fatal(err)
	}
	snapshot := func(p *pgxpool.Pool) string {
		var value string
		err := p.QueryRow(context.Background(), `SELECT coalesce(json_agg(t ORDER BY rate_id)::text, '[]') FROM fx_rates t`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	beforeGo, beforePy := snapshot(pool), snapshot(pyPool)

	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
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
	deleted := token("00000000-0000-0000-0000-000000000003")
	call := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
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
	for _, path := range []string{"/api/v1/accounting/base-currency", "/api/v1/accounting/fx-rates", "/api/v1/accounting/fx-rates/latest"} {
		for _, tc := range []struct {
			c    *http.Cookie
			code int
		}{{nil, 401}, {&http.Cookie{Name: "session", Value: admin.Value + "x"}, 401}, {deleted, 403}, {user, 403}} {
			w := call(path, tc.c)
			if w.Code != tc.code {
				t.Errorf("%s auth: %d", path, w.Code)
			}
		}
	}
	paths := []string{"/api/v1/accounting/base-currency", "/api/v1/accounting/fx-rates", "/api/v1/accounting/fx-rates?from_currency=usd", "/api/v1/accounting/fx-rates?to_currency=idr", "/api/v1/accounting/fx-rates?from_currency=idr&from_currency=usd", "/api/v1/accounting/fx-rates?from_currency=a", "/api/v1/accounting/fx-rates/latest", "/api/v1/accounting/fx-rates/latest?from_currency=usd", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-01-01", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2019-12-31", "/api/v1/accounting/fx-rates/latest?from_currency=idr", "/api/v1/accounting/fx-rates/latest?from_currency=eur", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=bad", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=NaN", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-01-01T25:00:00", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-01-01T00:70:00", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=1577836800", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=1577836800000", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=1", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=abcd-01-01", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-aa-01", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-01-aa", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=0000-01-01", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-02-30", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-13-01", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020%2F01%2F01", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-01-01T00:00:00", "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date=2020-01-01T12:00:00"}

	for _, offset := range []string{"+0530", "-0530", "+05:30", "-05:30", "+2359", "-2359", "+23:59", "+2400", "-2400", "+24:00", "+05:60", "+0560", "+99:00", "+00", "+5:30", "+053", "+05:3", "+2460", "+99:99", "+05300", "+05:300", "+05:30x", "+05x30", "+ab30", "+05ab", "+05::30", "Z", "z", "Zx", "zX"} {
		paths = append(paths, "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date="+url.QueryEscape("2020-01-01T00:00:00"+offset))
	}

	for _, value := range []string{"253402300800000", "-62135683200000", "-62198755200000", "253402214400000", "-62135596800000", "2020-01-01T00:00+0530", "2020-01-01T00:00+9960", "2020-01-01T00:00+9900", "2020-01-01T00:00:00.000000+0530", "2020-01-01T01:00:00+0530", "2020-01-01T00:00:00.000001+05:30"} {
		paths = append(paths, "/api/v1/accounting/fx-rates/latest?from_currency=usd&on_date="+url.QueryEscape(value))
	}

	py := exec.Command("python", "-c", `import json, os, base64, time
from itsdangerous import TimestampSigner
from starlette.middleware.sessions import SessionMiddleware
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.accounting import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['FX_TEST_DATABASE_URL'].replace('postgres://','postgresql+psycopg://',1))
app=FastAPI(); app.add_middleware(SessionMiddleware, secret_key='fx-test-secret'); app.include_router(router,prefix='/api/v1')
def db_session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db]=db_session
def cookie(user):
    session=base64.b64encode(json.dumps({'user_id':user,'exp':int(time.time())+3600}).encode())
    return TimestampSigner('fx-test-secret').sign(session).decode()
with TestClient(app,raise_server_exceptions=False) as client:
    auth=[]
    for value in [None, cookie('00000000-0000-0000-0000-000000000001')+'x',cookie('00000000-0000-0000-0000-000000000003'),cookie('00000000-0000-0000-0000-000000000002')]:
        client.cookies.clear()
        if value: client.cookies.set('session',value)
        auth.append([[r.status_code,r.json()] for r in (client.get(p) for p in ['/api/v1/accounting/base-currency','/api/v1/accounting/fx-rates','/api/v1/accounting/fx-rates/latest'])])
    client.cookies.set('session',cookie('00000000-0000-0000-0000-000000000001'))
    print(json.dumps({'auth':auth,'responses':[[r.status_code, r.json() if r.headers.get('content-type','').startswith('application/json') else r.text] for r in (client.get(p) for p in json.loads(os.environ['FX_TEST_PATHS']))]}))`)
	pathJSON, _ := json.Marshal(paths)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "FX_TEST_DATABASE_URL="+pyDB.URL(), "FX_TEST_PATHS="+string(pathJSON))
	output, err := py.Output()
	if err != nil {
		t.Fatalf("python: %v %s", err, output)
	}
	var result struct {
		Auth      [][][]any `json:"auth"`
		Responses [][]any   `json:"responses"`
	}
	if err = json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	for i, c := range []*http.Cookie{nil, {Name: "session", Value: admin.Value + "x"}, deleted, user} {
		for j, path := range []string{"/api/v1/accounting/base-currency", "/api/v1/accounting/fx-rates", "/api/v1/accounting/fx-rates/latest"} {
			w := call(path, c)
			var body any
			json.Unmarshal(w.Body.Bytes(), &body)
			if float64(w.Code) != result.Auth[i][j][0] || !reflect.DeepEqual(body, result.Auth[i][j][1]) {
				t.Errorf("auth %s: Go %d %s Python %v", path, w.Code, w.Body.String(), result.Auth[i][j])
			}
		}
	}
	expected := result.Responses
	for i, path := range paths {
		w := call(path, admin)
		var body any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			body = w.Body.String()
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(body, expected[i][1]) {
			t.Errorf("%s Python=%v Go=%d %s", path, expected[i], w.Code, w.Body.String())
		}
	}
	if after := snapshot(pool); after != beforeGo {
		t.Errorf("Go reads changed database: %s", after)
	}
	if after := snapshot(pyPool); after != beforePy {
		t.Errorf("Python reads changed database: %s", after)
	}
	var count int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM fx_rates`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("read changed rates: count=%d err=%v", count, err)
	}

	_, err = pool.Exec(context.Background(), `INSERT INTO fx_rates(rate_id,from_currency,to_currency,rate,as_of_date,source) VALUES (104,'GBP','IDR','NaN','2020-01-01','MANUAL')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/accounting/fx-rates", "/api/v1/accounting/fx-rates/latest?from_currency=GBP"} {
		w := call(path, admin)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
			t.Errorf("nonfinite %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	cfg.BaseCurrency = "usd"
	configured, err := NewHandlerWithSessions(pool, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/v1/accounting/base-currency", nil)
	r.AddCookie(admin)
	w := httptest.NewRecorder()
	configured.ServeHTTP(w, r)
	if w.Body.String() != "{\"base_currency\":\"usd\"}\n" {
		t.Errorf("configured base: %s", w.Body.String())
	}

}
