package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

func TestScopedAgingPostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	config := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	migrator := goosemigrator.New(".", goosemigrator.WithFS(migrations.Files))
	staged := pgtestdb.Custom(t, config, migrator)
	reference := pgtestdb.Custom(t, config, migrator)
	ctx := context.Background()
	poolFor := func(dsn string) *pgxpool.Pool {
		t.Helper()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	pool, pythonPool := poolFor(staged.URL()), poolFor(reference.URL())
	apply := func(pool *pgxpool.Pool, stmt string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, stmt, args...); err != nil {
			t.Fatal(err)
		}
	}
	const (
		admin        = "85000000-0000-0000-0000-000000000001"
		user         = "85000000-0000-0000-0000-000000000002"
		party        = "85000000-0000-0000-0000-000000000003"
		foreign      = "85000000-0000-0000-0000-000000000004"
		foreignAdmin = "85000000-0000-0000-0000-000000000005"
		foreignParty = "85000000-0000-0000-0000-000000000006"
	)
	today := time.Now()
	date := today.Format(time.DateOnly)
	for _, db := range []*pgxpool.Pool{pool, pythonPool} {
		apply(db, `INSERT INTO users(user_id,email,password_hash,role) VALUES ($1,'aging-admin@example.test','x','admin'),($2,'aging-user@example.test','x','user')`, admin, user)
		apply(db, `INSERT INTO customers(customer_id,name) VALUES ($1,'   ')`, party)
		apply(db, `INSERT INTO vendors(vendor_id,name) VALUES ($1,'   ')`, party)
		for _, table := range []string{"invoices", "bills"} {
			insert := `INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,issue_date,due_date)
				SELECT gen_random_uuid(),$1,'MILESTONE',currency,amount,amount,status,$2::date,$2::date-due FROM `
			if table == "bills" {
				insert = `INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status,issue_date,due_date)
					SELECT gen_random_uuid(),$1,currency,amount,amount,status,$2::date,$2::date-due FROM `
			}
			apply(db, insert+`(VALUES
				('USD',999999999999999.9999,'OPEN',0), ('USD',0.0001,'OPEN',-1),
				('USD',0.0001,'OPEN',1), ('USD',0.0002,'PARTIAL',30),
				('USD',0.0003,'OPEN',31), ('USD',0.0004,'OPEN',60),
				('USD',0.0005,'OPEN',61), ('USD',0.0006,'OPEN',90),
				('USD',0.0007,'OPEN',91), ('IDR',1.2345,'OPEN',NULL),
				('USD',990,'VOID',3), ('USD',0,'OPEN',3), ('USD',990,'DRAFT',3)
				) AS data(currency,amount,status,due)`, party, date)
		}
		apply(db, `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template)
			VALUES(gen_random_uuid(),'MILESTONE','SGD',2.5,2.5,'SENT',false),
			(gen_random_uuid(),'RECURRING','USD',990,990,'OPEN',true)`)
	}
	db, err := sql.Open("pgx", staged.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var company string
	if err := pool.QueryRow(ctx, "SELECT business_id::text FROM businesses").Scan(&company); err != nil {
		t.Fatal(err)
	}
	apply(pool, "INSERT INTO businesses(business_id) VALUES ($1)", foreign)
	apply(pool, "INSERT INTO users(business_id,user_id,email,password_hash,role) VALUES ($1,$2,'aging-admin@example.test','x','admin')", foreign, foreignAdmin)
	apply(pool, "INSERT INTO customers(business_id,customer_id,name) VALUES ($1,$2,'Other party')", foreign, foreignParty)
	apply(pool, "INSERT INTO vendors(business_id,vendor_id,name) VALUES ($1,$2,'Other party')", foreign, foreignParty)
	apply(pool, `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status)
		VALUES($1,gen_random_uuid(),$2,'MILESTONE','USD',99,99,'OPEN')`, foreign, foreignParty)
	apply(pool, `INSERT INTO bills(business_id,bill_id,vendor_id,currency,amount,balance_due,status)
		VALUES($1,gen_random_uuid(),$2,'USD',88,88,'OPEN')`, foreign, foreignParty)
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	router := func(company string) http.Handler {
		t.Helper()
		scope, err := business.Parse(company)
		if err != nil {
			t.Fatal(err)
		}
		r, err := NewScopedProfileRouter(pool, cfg, scope)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	token := func(id, company string) string {
		t.Helper()
		signed, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id, "business_id": company})
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	call := func(handler http.Handler, path, cookie string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	snapshot := func() string {
		t.Helper()
		var result string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_array(
			(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i),
			(SELECT jsonb_agg(to_jsonb(b) ORDER BY bill_id) FROM bills b),
			(SELECT jsonb_agg(to_jsonb(c) ORDER BY customer_id) FROM customers c),
			(SELECT jsonb_agg(to_jsonb(v) ORDER BY vendor_id) FROM vendors v),
			(SELECT count(*) FROM payments), (SELECT count(*) FROM journal_entries))::text`).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	a, b := router(company), router(foreign)
	signed := token(admin, company)
	before := snapshot()
	for _, report := range []struct {
		kind, table, partyColumn, constraint, insert string
	}{
		{"ar", "invoices", "customer_id", "invoices_customer_id_fkey",
			`INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,issue_date)
			VALUES($1,$2,$3,'MILESTONE','EUR',99,99,'OPEN',$4::date)`},
		{"ap", "bills", "vendor_id", "bills_vendor_id_fkey",
			`INSERT INTO bills(business_id,bill_id,vendor_id,currency,amount,balance_due,status,issue_date)
			VALUES($1,$2,$3,'EUR',99,99,'OPEN',$4::date)`},
	} {
		path := "/api/v1/accounting/reports/" + report.kind + "-aging"
		for cookie, status := range map[string]int{"": 401, token(foreignAdmin, foreign): 401, token(user, company): 403} {
			if w := call(a, path, cookie); w.Code != status {
				t.Fatalf("auth status %d, want %d: %s", w.Code, status, w.Body)
			}
		}
		for _, fake := range []string{today.AddDate(0, 0, -1).Format(time.DateOnly), today.AddDate(0, 0, 1).Format(time.DateOnly), "2026-02-30", "0000-01-01", ""} {
			if w := call(a, path+"?as_of="+fake, signed); w.Code != 422 {
				t.Fatalf("fake date %q: %d %s", fake, w.Code, w.Body)
			}
		}
		want := pythonAccountingReports(t, reference.URL(), path, []string{"?as_of=" + date})
		w := call(a, path+"?as_of="+date+"&business_id="+foreign, signed)
		var body any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != want[0].Status || !reflect.DeepEqual(body, want[0].Body) {
			t.Fatalf("%s: got %d %s; want %d %+v", report.kind, w.Code, w.Body, want[0].Status, want[0].Body)
		}
		if got := call(a, path, signed); got.Code != 200 || got.Body.String() != w.Body.String() {
			t.Fatalf("default day: %d %s", got.Code, got.Body)
		}
		for _, fragment := range []string{`"current":"1000000000000000.0000"`, `"d_1_30":"0.0003"`, `"d_31_60":"0.0007"`, `"d_61_90":"0.0011"`, `"d_90_plus":"0.0007"`, `"total":"1000000000000000.0028"`} {
			if !strings.Contains(w.Body.String(), fragment) {
				t.Fatalf("missing %s: %s", fragment, w.Body)
			}
		}
		if got := call(b, path, token(foreignAdmin, foreign)); got.Code != 200 || !strings.Contains(got.Body.String(), "Other party") || strings.Contains(got.Body.String(), party) {
			t.Fatalf("other business: %d %s", got.Code, got.Body)
		}
		const added = "85000000-0000-0000-0000-000000000007"
		const missingParty = "85000000-0000-0000-0000-000000000008"
		idColumn := "bill_id"
		if report.kind == "ar" {
			idColumn = "invoice_id"
		}
		deleteAdded := "DELETE FROM " + report.table + " WHERE " + idColumn + "=$1"
		apply(pool, report.insert, company, added, party, today.AddDate(0, 0, 1).Format(time.DateOnly))
		if got := call(a, path, signed); got.Code != 200 || got.Body.String() != w.Body.String() {
			t.Fatalf("future document contributed: %d %s", got.Code, got.Body)
		}
		apply(pool, deleteAdded, added)

		apply(pool, "ALTER TABLE "+report.table+" DROP CONSTRAINT "+report.constraint)
		apply(pool, report.insert, foreign, added, party, date)
		if got := call(a, path, signed); got.Code != 200 || got.Body.String() != w.Body.String() {
			t.Fatalf("foreign document with owned party: %d %s", got.Code, got.Body)
		}
		apply(pool, deleteAdded, added)
		apply(pool, report.insert, company, added, foreignParty, today.AddDate(0, 0, 1).Format(time.DateOnly))
		if got := call(a, path, signed); got.Code != 200 || got.Body.String() != w.Body.String() {
			t.Fatalf("future document with damaged reference affected today's report: %d %s", got.Code, got.Body)
		}
		apply(pool, deleteAdded, added)
		for _, badParty := range []string{foreignParty, missingParty} {
			apply(pool, report.insert, company, added, badParty, date)
			if got := call(a, path, signed); got.Code != 500 || got.Body.String() != "{\"detail\":\"internal server error\"}\n" {
				t.Fatalf("damaged party reference: %d %s", got.Code, got.Body)
			}
			apply(pool, deleteAdded, added)
		}
	}

	if after := snapshot(); after != before {
		t.Fatal("reports changed database state")
	}
	for table, kind := range map[string]string{"invoices": "AR", "bills": "AP"} {
		apply(pool, "DELETE FROM "+table+" WHERE business_id=$1", foreign)
		path := "/api/v1/accounting/reports/" + strings.ToLower(kind) + "-aging"
		got := call(b, path, token(foreignAdmin, foreign))
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"rows":[],"totals_by_currency":{}`) {
			t.Fatalf("empty report: %d %s", got.Code, got.Body)
		}
	}
}
