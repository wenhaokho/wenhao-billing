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
	"sync"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestPaymentAccountReplacement(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	d := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), d.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(context.Background(), `DELETE FROM business_profile; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile/payment-accounts", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	for _, secret := range []string{"secret-token", "秘密ß"} {
		b, _ := json.Marshal([]map[string]string{{"currency": secret, "instructions": "x"}})
		w := call(string(b))
		if w.Code != 422 || w.Body.String() != `{"detail":"Unsupported currency"}`+"\n" || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), strings.ToUpper(secret)) {
			t.Fatalf("unsanitized unsupported currency: %d %s", w.Code, w.Body)
		}
	}
	unauth := httptest.NewRecorder()
	ur := httptest.NewRequest("PUT", "/api/v1/business-profile/payment-accounts", strings.NewReader(`[]`))
	ur.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(unauth, ur)
	if unauth.Code != 401 {
		t.Fatalf("unauthenticated: %d", unauth.Code)
	}
	if _, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000003','u@example.com','x','user')`); err != nil {
		t.Fatal(err)
	}
	userToken, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000003"})
	if err != nil {
		t.Fatal(err)
	}
	nonadmin := httptest.NewRecorder()
	nr := httptest.NewRequest("PUT", "/api/v1/business-profile/payment-accounts", strings.NewReader(`[]`))
	nr.Header.Set("Content-Type", "application/json")
	nr.AddCookie(&http.Cookie{Name: "session", Value: userToken})
	h.ServeHTTP(nonadmin, nr)
	if nonadmin.Code != 403 {
		t.Fatalf("non-admin: %d", nonadmin.Code)
	}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`[{"currency":"usd","instructions":"wire"}]`, 200},
		{`[{"currency":"usd","instructions":"a"},{"currency":"USD","instructions":"b"}]`, 422},
		{`[{"currency":"EUR","instructions":"a"}]`, 422},
		{`[]`, 200},
	} {
		if w := call(tc.body); w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body)
		}
	}
	spec := httptest.NewRecorder()
	h.ServeHTTP(spec, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(map[string]any)["/api/v1/business-profile/payment-accounts"].(map[string]any)["put"].(map[string]any)
	if op["requestBody"] == nil || op["security"] == nil {
		t.Fatalf("OpenAPI contract: %#v", op)
	}
	for _, tc := range []struct {
		body, media string
		code        int
	}{{"private-secret", "text/plain", 422}, {`[{"currency":`, "application/json", 422}} {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile/payment-accounts", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.media)
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "private-secret") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("boundary: %d %s", w.Code, w.Body)
		}
	}

	if _, err = p.Exec(context.Background(), `UPDATE business_profile SET name='keep profile' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	// A failed insert must roll back both deletion and singleton changes.
	if w := call(`[{"currency":"USD","instructions":"preserve"}]`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if _, err = p.Exec(context.Background(), `ALTER TABLE payment_account ADD CONSTRAINT reject_test CHECK (instructions <> 'reject')`); err != nil {
		t.Fatal(err)
	}
	if w := call(`[{"currency":"SGD","instructions":"reject"}]`); w.Code != 500 {
		t.Fatalf("database error: %d %s", w.Code, w.Body)
	}
	var currency, instructions string
	if err = p.QueryRow(context.Background(), `SELECT currency,instructions FROM payment_account`).Scan(&currency, &instructions); err != nil || currency != "USD" || instructions != "preserve" {
		t.Fatalf("rollback: %q %q %v", currency, instructions, err)
	}
	var profileName string
	if err = p.QueryRow(context.Background(), `SELECT name FROM business_profile WHERE id=1`).Scan(&profileName); err != nil || profileName != "keep profile" {
		t.Fatalf("unrelated profile changed: %q %v", profileName, err)
	}
	for _, body := range []string{`null`, `{}`, `[null]`, `[{"currency":null,"instructions":"x"}]`, `[{"currency":"USD","instructions":"x","is_default":null}]`} {
		if w := call(body); w.Code != 422 {
			t.Fatalf("invalid %s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := call(strings.Repeat("x", (1<<20)+1)); w.Code != 413 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("limit: %d", w.Code)
	}

	if _, err = p.Exec(context.Background(), `ALTER TABLE payment_account DROP CONSTRAINT reject_test; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin')`); err != nil {
		t.Fatal(err)
	}
	token2, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		if _, err = p.Exec(context.Background(), `DELETE FROM business_profile WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		type result struct {
			code                   int
			body                   string
			currency, instructions string
		}
		results := make(chan result, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, pair := range [][3]string{{token, "USD", "first"}, {token2, "SGD", "second"}} {
			wg.Add(1)
			go func(pair [3]string) {
				defer wg.Done()
				body, _ := json.Marshal([]map[string]string{{"currency": pair[1], "instructions": pair[2]}})
				r := httptest.NewRequest("PUT", "/api/v1/business-profile/payment-accounts", strings.NewReader(string(body)))
				r.Header.Set("Content-Type", "application/json")
				r.AddCookie(&http.Cookie{Name: "session", Value: pair[0]})
				w := httptest.NewRecorder()
				<-start
				h.ServeHTTP(w, r)
				results <- result{w.Code, w.Body.String(), pair[1], pair[2]}
			}(pair)
		}
		close(start)
		wg.Wait()
		close(results)
		for v := range results {
			var accounts []struct {
				ID           int    `json:"id"`
				Currency     string `json:"currency"`
				Instructions string `json:"instructions"`
				IsDefault    bool   `json:"is_default"`
				UpdatedAt    string `json:"updated_at"`
			}
			if v.code != 200 || json.Unmarshal([]byte(v.body), &accounts) != nil || len(accounts) != 1 || accounts[0].Currency != v.currency || accounts[0].Instructions != v.instructions || accounts[0].ID <= 0 || accounts[0].IsDefault || accounts[0].UpdatedAt == "" {
				t.Fatalf("concurrent response: %+v", v)
			}
		}
		var count int
		var c, ins string
		var defaults int
		if err = p.QueryRow(context.Background(), `SELECT count(*),min(currency),min(instructions),count(*) FILTER (WHERE is_default) FROM payment_account WHERE business_profile_id=1`).Scan(&count, &c, &ins, &defaults); err != nil || defaults != 0 || count != 1 || !((c == "USD" && ins == "first") || (c == "SGD" && ins == "second")) {
			t.Fatalf("concurrent final state: %d %s %s %v", count, c, ins, err)
		}
	}

	trace := &customerLockTracer{reached: make(chan uint32, 1), release: make(chan struct{})}
	defer trace.unblock()
	pc, err := pgxpool.ParseConfig(d.URL())
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.Tracer = trace
	traced, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	th, err := NewHandlerWithSessions(traced, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile/payment-accounts", strings.NewReader(`[]`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		th.ServeHTTP(w, r)
		done <- w.Code
	}()
	select {
	case <-trace.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("admin lock not reached")
	}
	if _, err = p.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	trace.unblock()
	select {
	case code := <-done:
		if code != 403 {
			t.Fatalf("demotion: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("demotion stuck")
	}

}

func TestPaymentAccountsPythonDifferential(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	d := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	pd := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), d.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pp, err := pgxpool.New(context.Background(), pd.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer pp.Close()
	for _, pool := range []*pgxpool.Pool{p, pp} {
		_, err = pool.Exec(context.Background(), `DELETE FROM business_profile; INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`)
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{`[]`, `[{"currency":"usd","instructions":"wire"},{"currency":"sgd","instructions":"x","is_default":true},{"currency":"idr","instructions":"y"}]`, `[{"currency":"USD","instructions":"x"},{"currency":"usd","instructions":"y"}]`, `[{"currency":"EUR","instructions":"x"}]`, `[{"currency":"ß","instructions":"x"}]`, `[{"currency":"ß","instructions":"x"},{"currency":"ss","instructions":"y"}]`, `[{"currency":"USD","instructions":"x","is_default":true},{"currency":"IDR","instructions":"y","is_default":true}]`, `[]`}
	b, _ := json.Marshal(bodies)
	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.business_profile import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
 print(json.dumps([[r.status_code,r.json()] for body in json.loads(os.environ['BODIES']) for r in [c.put('/api/v1/business-profile/payment-accounts',content=body,headers={'content-type':'application/json'})]]))`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pd.URL(), "BODIES="+string(b))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want [][]any
	if err = json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, body := range bodies {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile/payment-accounts", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if i == 3 || i == 4 {
			if w.Code != 422 || got.(map[string]any)["detail"] != "Unsupported currency" || !strings.HasPrefix(want[i][1].(map[string]any)["detail"].(string), "Unsupported currency: ") {
				t.Fatalf("unsupported redaction: got %#v python %#v", got, want[i])
			}
			continue
		}
		if w.Code == 200 {
			for _, row := range got.([]any) {
				delete(row.(map[string]any), "updated_at")
			}
			for _, row := range want[i][1].([]any) {
				delete(row.(map[string]any), "updated_at")
			}
		}
		if w.Code != int(want[i][0].(float64)) || !reflect.DeepEqual(got, want[i][1]) {
			t.Fatalf("%s: got %d %#v want %#v", body, w.Code, got, want[i])
		}
	}
	// Python returns the original invalid input; compare error semantics while requiring Go to redact it.
	cases := []struct {
		Body  string `json:"body"`
		Media string `json:"media"`
	}{{"", "application/json"}, {"null", "application/json"}, {"{}", "application/json"}, {"[null]", "application/json"}, {`[{"currency":null,"instructions":"x"}]`, "application/json"}, {`[{"currency":"USD","instructions":"x","is_default":null}]`, "application/json"}, {`[{"currency":"private-secret",`, "application/json"}, {"private-secret", "text/plain"}, {`[{"currency":["private-secret"],"instructions":"x"}]`, "application/json"}}
	payload, _ := json.Marshal(cases)
	boundaryCmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.business_profile import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
 print(json.dumps([[r.status_code,r.json()] for v in json.loads(os.environ['CASES']) for r in [c.put('/api/v1/business-profile/payment-accounts',content=v['body'],headers={'content-type':v['media']})]]))`)
	boundaryCmd.Dir = "../../../backend"
	boundaryCmd.Env = append(os.Environ(), "DB="+pd.URL(), "CASES="+string(payload))
	raw, err := boundaryCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var expected [][]any
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for i, v := range cases {
		r := httptest.NewRequest("PUT", "/api/v1/business-profile/payment-accounts", strings.NewReader(v.Body))
		r.Header.Set("Content-Type", v.Media)
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != int(expected[i][0].(float64)) || strings.Contains(w.Body.String(), "private-secret") {
			t.Fatalf("boundary %d: status=%d response=%s python=%v", i, w.Code, w.Body, expected[i])
		}
		var actual map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &actual); err != nil {
			t.Fatal(err)
		}
		ga := actual["detail"].([]any)
		pa := expected[i][1].(map[string]any)["detail"].([]any)
		if len(ga) != len(pa) {
			t.Fatalf("boundary %d error count: %#v %#v", i, ga, pa)
		}
		for j := range ga {
			g := ga[j].(map[string]any)
			p := pa[j].(map[string]any)
			for _, key := range []string{"type", "loc", "msg"} {
				if !reflect.DeepEqual(g[key], p[key]) {
					t.Fatalf("boundary %d %s: %#v %#v", i, key, g, p)
				}
			}
		}
	}

}
