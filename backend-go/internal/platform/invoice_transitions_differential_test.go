package platform

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestInvoiceTransitionDifferential(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	migrator := goosemigrator.New(".", goosemigrator.WithFS(migrations.Files))
	gd := pgtestdb.Custom(t, cfgdb, migrator)
	pd := pgtestdb.Custom(t, cfgdb, migrator)
	pools := make([]*pgxpool.Pool, 2)
	for i, d := range []string{gd.URL(), pd.URL()} {
		p, e := pgxpool.New(context.Background(), d)
		if e != nil {
			t.Fatal(e)
		}
		pools[i] = p
		defer p.Close()
		if _, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin')`); e != nil {
			t.Fatal(e)
		}
	}
	type testCase struct {
		Path string `json:"path"`
		Body string `json:"body"`
	}
	cases := []testCase{}
	n := 0
	for _, action := range []string{"finalize", "mark-paid", "void"} {
		for _, status := range []string{"DRAFT", "OPEN", "SENT", "PARTIAL", "PAID", "VOID"} {
			for _, balance := range []int{-1, 0, 1} {
				for _, template := range []bool{false, true} {
					n++
					id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
					for _, p := range pools {
						_, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template,created_at) VALUES ($1,'MILESTONE','USD',1,$2,$3,$4,'2020-01-01')`, id, balance, status, template)
						if e != nil {
							t.Fatal(e)
						}
					}
					body := ""
					if action == "void" {
						body = `{"reason":"test"}`
					}
					cases = append(cases, testCase{"/api/v1/invoices/" + id + "/" + action, body})
				}
			}
		}
	}
	cases = append(cases, testCase{"/api/v1/invoices/30000000-0000-0000-0000-999999999999/finalize", ""}, testCase{"/api/v1/invoices/bad/finalize", ""})
	for _, body := range []string{"", "null", "{}", `{"reason":null}`, `{"reason":123}`, `{"reason":true}`, `{"reason":[]}`, `{"reason":{}}`, `{"reason":""}`, `{"reason":"test","extra":1}`, `[]`, `123`, `"text"`, `{`, `{"reason":`, `{"reason":"secret"} trailing`, `{"reason":"é"} {}`, `{"reason": "unterminated`, `{"reason" 1}`, `{"reason": 1,}`, `{"reason": 1 "extra": 2}`, `{"reason":"\q"}`, `{"reason": tru}`, `{"reason": [1,]}`, " \n "} {
		n++
		id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
		for _, p := range pools {
			if _, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,created_at) VALUES ($1,'MILESTONE','USD',1,1,'OPEN','2020-01-01')`, id); e != nil {
				t.Fatal(e)
			}
		}
		cases = append(cases, testCase{"/api/v1/invoices/" + id + "/void", body})
	}

	cmd := exec.Command("python", "-c", `import json,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.invoices import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)); app=FastAPI(); app.include_router(router,prefix='/api/v1'); app.dependency_overrides[current_admin]=lambda: object()
def session():
 with Session(engine) as db: yield db
app.dependency_overrides[get_db]=session
with TestClient(app) as c:
 print(json.dumps([[r.status_code,r.json()] for x in json.loads(os.environ['CASES']) for r in [c.post(x['path'],content=x['body'],headers={'content-type':'application/json'})]]))`)
	cmd.Dir = "../../../backend"
	b, _ := json.Marshal(cases)
	cmd.Env = append(os.Environ(), "DB="+pd.URL(), "CASES="+string(b))
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var expected [][]any
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(pools[0], nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}
	for i, tc := range cases {
		r := httptest.NewRequest("POST", tc.Path, strings.NewReader(tc.Body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatal(e)
		}
		// Body values are intentionally redacted from validation errors.
		if w.Code == 422 && strings.HasSuffix(tc.Path, "/void") && tc.Body != "" {
			if obj, ok := expected[i][1].(map[string]any); ok {
				for _, item := range obj["detail"].([]any) {
					detail := item.(map[string]any)
					if detail["type"] == "string_type" || detail["type"] == "model_attributes_type" || (detail["type"] == "missing" && len(detail["loc"].([]any)) == 2) {
						detail["input"] = nil
					}
				}
			}
		}
		if float64(w.Code) != expected[i][0] || !reflect.DeepEqual(got, expected[i][1]) {
			t.Errorf("%s: go %d %#v python %#v", tc.Path, w.Code, got, expected[i])
		}
		if tc.Body != "" && !json.Valid([]byte(tc.Body)) {
			if strings.Contains(w.Body.String(), "secret") {
				t.Errorf("malformed input leaked in response: %s", w.Body.String())
			}
			var status, balance string
			id := strings.Split(tc.Path, "/")[4]
			for _, pool := range pools {
				if err := pool.QueryRow(context.Background(), `SELECT status,balance_due::text FROM invoices WHERE invoice_id=$1`, id).Scan(&status, &balance); err != nil || status != "OPEN" || balance != "1.0000" {
					t.Errorf("malformed request changed invoice: %s %s %v", status, balance, err)
				}
			}
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("missing no-store")
		}
	}
	for _, table := range []string{"invoices", "invoice_line_items", "payments", "journal_entries", "journal_lines"} {
		var a, b string
		q := fmt.Sprintf("SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM %s t", table)
		if e = pools[0].QueryRow(context.Background(), q).Scan(&a); e != nil {
			t.Fatal(e)
		}
		if e = pools[1].QueryRow(context.Background(), q).Scan(&b); e != nil {
			t.Fatal(e)
		}
		if a != b {
			t.Errorf("%s differs", table)
		}
	}
}
