package platform

import (
	"context"
	"database/sql"
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
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
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
	sd := pgtestdb.Custom(t, cfgdb, migrator)
	pools := make([]*pgxpool.Pool, 3)
	for i, d := range []string{gd.URL(), pd.URL(), sd.URL()} {
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
		Path   string `json:"path"`
		Body   string `json:"body"`
		Method string `json:"method"`
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
					cases = append(cases, testCase{"/api/v1/invoices/" + id + "/" + action, body, "POST"})
				}
			}
		}
	}
	cases = append(cases, testCase{"/api/v1/invoices/30000000-0000-0000-0000-999999999999/finalize", "", "POST"}, testCase{"/api/v1/invoices/bad/finalize", "", "POST"})
	for _, body := range []string{"", "null", "{}", `{"reason":null}`, `{"reason":123}`, `{"reason":true}`, `{"reason":[]}`, `{"reason":{}}`, `{"reason":""}`, `{"reason":"test","extra":1}`, `[]`, `123`, `"text"`, `{`, `{"reason":`, `{"reason":"secret"} trailing`, `{"reason":"é"} {}`, `{"reason": "unterminated`, `{"reason" 1}`, `{"reason": 1,}`, `{"reason": 1 "extra": 2}`, `{"reason":"\q"}`, `{"reason": tru}`, `{"reason": [1,]}`, " \n "} {
		n++
		id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
		for _, p := range pools {
			if _, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,created_at) VALUES ($1,'MILESTONE','USD',1,1,'OPEN','2020-01-01')`, id); e != nil {
				t.Fatal(e)
			}
		}
		cases = append(cases, testCase{"/api/v1/invoices/" + id + "/void", body, "POST"})
	}

	for _, status := range []string{"DRAFT", "OPEN", "SENT", "PARTIAL", "PAID", "VOID"} {
		n++
		id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
		for _, p := range pools {
			if _, err := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,created_at) VALUES ($1,'MILESTONE','USD',1,1,$2,'2020-01-01')`, id, status); err != nil {
				t.Fatal(err)
			}
		}
		cases = append(cases, testCase{"/api/v1/invoices/" + id, "", "DELETE"})
	}

	// Insert position 20 before 10, with line IDs in the same reverse order.
	// All three implementations must return position order for a saved invoice.
	for _, p := range pools {
		if _, err := p.Exec(context.Background(), `
			INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount,created_at)
			SELECT (l.id_prefix || right(i.invoice_id::text, 12))::uuid,
				i.invoice_id,l.position,l.description,l.quantity,l.unit_price,l.amount,'2020-01-01'
			FROM invoices i CROSS JOIN (VALUES
				('40000000-0000-0000-0000-',20,'Second line',1.2500,4.0001,5.0001),
				('41000000-0000-0000-0000-',10,'First line',2.0000,3.2500,6.5000)
			) AS l(id_prefix,position,description,quantity,unit_price,amount)
			ORDER BY i.invoice_id,l.position DESC`); err != nil {
			t.Fatal(err)
		}
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
 print(json.dumps([[r.status_code,r.json() if r.content else None] for x in json.loads(os.environ['CASES']) for r in [c.request(x['method'],x['path'],content=x['body'],headers={'content-type':'application/json'})]]))`)
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

	scopedDB, e := sql.Open("pgx", sd.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer scopedDB.Close()
	provider, e := goose.NewProvider(goose.DialectPostgres, scopedDB, os.DirFS("../../migrations"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = provider.Up(context.Background()); e != nil {
		t.Fatal(e)
	}
	var owner string
	if e = scopedDB.QueryRow("SELECT business_id::text FROM businesses").Scan(&owner); e != nil {
		t.Fatal(e)
	}
	scope, e := business.Parse(owner)
	if e != nil {
		t.Fatal(e)
	}
	scoped, e := NewScopedProfileRouter(pools[2], cfg, scope)
	if e != nil {
		t.Fatal(e)
	}
	scopedToken, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{
		"sub": "00000000-0000-0000-0000-000000000001", "business_id": owner,
	})
	if e != nil {
		t.Fatal(e)
	}
	type lineContent struct {
		Position    int    `json:"position"`
		Description string `json:"description"`
		Quantity    string `json:"quantity"`
		UnitPrice   string `json:"unit_price"`
		Amount      string `json:"amount"`
	}
	wantLines := []lineContent{
		{10, "First line", "2.0000", "3.2500", "6.5000"},
		{20, "Second line", "1.2500", "4.0001", "5.0001"},
	}
	verifiedActions := map[string]bool{}
	for i, tc := range cases {
		r := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(tc.Body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got any
		if w.Body.Len() != 0 {
			if e = json.Unmarshal(w.Body.Bytes(), &got); e != nil {
				t.Fatal(e)
			}
		}
		scopedRequest := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(tc.Body))
		scopedRequest.Header.Set("Content-Type", "application/json")
		scopedRequest.AddCookie(&http.Cookie{Name: "session", Value: scopedToken})
		scopedResponse := httptest.NewRecorder()
		scoped.ServeHTTP(scopedResponse, scopedRequest)
		if scopedResponse.Code != w.Code || scopedResponse.Body.String() != w.Body.String() {
			t.Errorf("%s: v8 %d %s; v7 %d %s", tc.Path, scopedResponse.Code, scopedResponse.Body, w.Code, w.Body)
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
		if w.Code == http.StatusOK {
			var response struct {
				Lines []lineContent `json:"line_items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || !reflect.DeepEqual(response.Lines, wantLines) {
				t.Fatalf("%s: ordered invoice lines = %+v, want %+v (%v)", tc.Path, response.Lines, wantLines, err)
			}
			verifiedActions[tc.Path[strings.LastIndex(tc.Path, "/")+1:]] = true
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
		if w.Header().Get("Cache-Control") != "no-store" || scopedResponse.Header().Get("Cache-Control") != "no-store" {
			t.Error("missing no-store")
		}
	}
	for _, action := range []string{"finalize", "mark-paid", "void"} {
		if !verifiedActions[action] {
			t.Errorf("no successful %s response checked for line order", action)
		}
	}
	for _, table := range []string{"invoices", "invoice_line_items", "payments", "journal_entries", "journal_lines"} {
		var a, b, c string
		q := fmt.Sprintf("SELECT coalesce(jsonb_agg(to_jsonb(t) - 'business_id' ORDER BY (to_jsonb(t) - 'business_id')::text),'[]'::jsonb)::text FROM %s t", table)
		if e = pools[0].QueryRow(context.Background(), q).Scan(&a); e != nil {
			t.Fatal(e)
		}
		if e = pools[1].QueryRow(context.Background(), q).Scan(&b); e != nil {
			t.Fatal(e)
		}
		if a != b {
			t.Errorf("%s differs between v7 and Python", table)
		}
		if e = pools[2].QueryRow(context.Background(), q+" WHERE business_id=$1", owner).Scan(&c); e != nil {
			t.Fatal(e)
		}
		if a != c {
			t.Errorf("%s differs between v7 and v8", table)
		}
	}
}
