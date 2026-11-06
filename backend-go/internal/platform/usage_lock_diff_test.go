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

func TestUsageLockDifferential(t *testing.T) {
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

	for _, typ := range []string{"USAGE", "MILESTONE", "RECURRING"} {
		for _, status := range []string{"DRAFT", "OPEN", "SENT", "PARTIAL", "PAID", "VOID"} {
			for _, currency := range []string{"USD", "IDR"} {
				for _, gross := range []bool{false, true} {
					n++
					id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
					for _, p := range pools {
						_, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,billing_cycle_ref,created_at) VALUES ($1,$2,$3,1,1,$4,'PPH23',2,$5,'{"nested":{"number":12345678901234567890}}','2020-01-01')`, id, typ, currency, status, gross)
						if e != nil {
							t.Fatal(e)
						}
					}
					cases = append(cases, testCase{"/api/v1/invoices/usage/" + id + "/lock", `{"accrued_amount":"100.0050","cutoff_date":"2026-10-31"}`})
				}
			}
		}
	}
	for _, cfg := range []struct {
		typ    string
		rate   any
		gross  bool
		amount string
	}{{"NONE", nil, false, "0"}, {"PPH21", "2.5", false, "0.005"}, {"PPH21", "2.5", true, "1.005"}, {"PPH23", nil, true, "100"}, {"PPH23", "99.9999", true, "1"}, {"PPH23", "2", false, "999999999999.9999"}} {
		for _, currency := range []string{"USD", "IDR"} {
			n++
			id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
			for _, p := range pools {
				_, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,created_at) VALUES ($1,'USAGE',$2,1,1,'DRAFT',$3,$4,$5,'2020-01-01')`, id, currency, cfg.typ, cfg.rate, cfg.gross)
				if e != nil {
					t.Fatal(e)
				}
			}
			cases = append(cases, testCase{"/api/v1/invoices/usage/" + id + "/lock", `{"accrued_amount":"` + cfg.amount + `","cutoff_date":"2026-01-01"}`})
		}
	}

	n++
	id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
	for _, p := range pools {
		_, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,billing_cycle_ref,created_at) VALUES ($1,'USAGE','USD',1,1,'DRAFT','null'::jsonb,'2020-01-01')`, id)
		if e != nil {
			t.Fatal(e)
		}
	}
	cases = append(cases, testCase{"/api/v1/invoices/usage/" + id + "/lock", `{"accrued_amount":1,"cutoff_date":"2026-01-01"}`})

	for _, ref := range []string{`[]`, `["ab"]`, `[{"a":1,"b":2}]`, `[["big",12345678901234567890],["nested",{"n":12345678901234567890}],["big",12345678901234567891]]`, `[["bad"]]`, `[1]`, `"bad"`, `false`, `0`, `""`} {
		n++
		id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
		for _, p := range pools {
			if _, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,billing_cycle_ref,created_at) VALUES ($1,'USAGE','USD',1,1,'DRAFT',$2::jsonb,'2020-01-01')`, id, ref); e != nil {
				t.Fatal(e)
			}
		}
		cases = append(cases, testCase{"/api/v1/invoices/usage/" + id + "/lock", `{"accrued_amount":1,"cutoff_date":"2026-01-01"}`})
	}
	for _, cfg := range []struct {
		typ, amount string
		gross       bool
	}{{"NONE", "1", true}, {"PPH23", "0", true}, {"PPH23", "1", true}, {"PPH23", "1", false}} {
		n++
		id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
		for _, p := range pools {
			if _, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,created_at) VALUES ($1,'USAGE','USD',1,1,'DRAFT',$2,100,$3,'2020-01-01')`, id, cfg.typ, cfg.gross); e != nil {
				t.Fatal(e)
			}
		}
		cases = append(cases, testCase{"/api/v1/invoices/usage/" + id + "/lock", `{"accrued_amount":` + cfg.amount + `,"cutoff_date":"2026-01-01"}`})
	}

	cases = append(cases, testCase{"/api/v1/invoices/usage/30000000-0000-0000-0000-999999999999/lock", `{"accrued_amount":1,"cutoff_date":"2026-01-01"}`})
	for _, body := range []string{"", "null", "{}", `{"accrued_amount":null,"cutoff_date":null}`, `{"accrued_amount":-1,"cutoff_date":"2026-01-01"}`, `{"accrued_amount":"1.00001","cutoff_date":"bad"}`, `[]`, `{`, `{"accrued_amount":1,"cutoff_date":"2026-01-01"}`} {
		n++
		id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
		for _, p := range pools {
			if _, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,created_at) VALUES ($1,'USAGE','USD',1,1,'DRAFT','2020-01-01')`, id); e != nil {
				t.Fatal(e)
			}
		}
		cases = append(cases, testCase{"/api/v1/invoices/usage/" + id + "/lock", body})
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
with TestClient(app,raise_server_exceptions=False) as c:
 print(json.dumps([[r.status_code,(r.json() if r.status_code != 500 else None)] for x in json.loads(os.environ['CASES']) for r in [c.post(x['path'],content=x['body'],headers={'content-type':'application/json'})]]))`)
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

		if float64(w.Code) != expected[i][0] || w.Code != 500 && !reflect.DeepEqual(got, expected[i][1]) {
			t.Errorf("%s: go %d %#v python %#v", tc.Path, w.Code, got, expected[i])
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
