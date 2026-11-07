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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type monthlyRevenueSnapshotKey struct{}

type monthlyRevenueSnapshotHook struct {
	afterBuckets func(*pgx.Conn)
}

func (h monthlyRevenueSnapshotHook) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, monthlyRevenueSnapshotKey{}, strings.Contains(data.SQL, "name: GetMonthlyRevenue"))
}

func (h monthlyRevenueSnapshotHook) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if matched, _ := ctx.Value(monthlyRevenueSnapshotKey{}).(bool); matched && data.Err == nil {
		h.afterBuckets(conn)
	}
}

func TestScopedMonthlyRevenuePostgres(t *testing.T) {
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
	open := func(dsn string) *pgxpool.Pool {
		t.Helper()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	pool, ref := open(staged.URL()), open(reference.URL())
	exec := func(db *pgxpool.Pool, stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, stmt, args...); err != nil {
			t.Fatal(err)
		}
	}
	const (
		admin      = "86000000-0000-0000-0000-000000000001"
		user       = "86000000-0000-0000-0000-000000000002"
		other      = "86000000-0000-0000-0000-000000000004"
		otherAdmin = "86000000-0000-0000-0000-000000000005"
		path       = "/api/v1/stats/revenue-monthly"
	)
	now := time.Now()
	today := now.Format(time.DateOnly)
	for _, db := range []*pgxpool.Pool{pool, ref} {
		exec(db, `INSERT INTO users(user_id,email,password_hash,role) VALUES ($1,'revenue-admin@example.test','x','admin'),($2,'revenue-user@example.test','x','user')`, admin, user)
		exec(db, `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,issue_date,is_template)
			SELECT gen_random_uuid(),'MILESTONE',currency,amount,unpaid,status,date_trunc('month',$1::date)::date+day_offset,template FROM (VALUES
			('USD',0.0001,0.0001,'OPEN',0,false),('USD',0.0002,0.0002,'SENT',0,false),
			('SGD',999999999999999.9999,0,'PAID',0,false),('SGD',0.0001,0,'PAID',0,false),
			('EUR',2,2,'PARTIAL',0,false),('JPY',3,0,'PAID',0,false),
			('SGD',0,0,'OPEN',-1,false),('USD',2,2,'OPEN',-1,false),('USD',-2,-2,'OPEN',-1,false),
			('USD',99,99,'DRAFT',0,false),('USD',99,99,'VOID',0,false),
			('USD',99,99,'OPEN',0,true),('USD',99,99,'OPEN',NULL,false)
			) AS x(currency,amount,unpaid,status,day_offset,template)`, today)
		exec(db, `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,issue_date)
			SELECT gen_random_uuid(),'MILESTONE','USD',amount,amount,'OPEN',issue_date FROM (VALUES
			(-0.0001,(date_trunc('month',$1::date)-interval '11 months')::date),
			(99,(date_trunc('month',$1::date)-interval '11 months 1 day')::date),
			(99,(date_trunc('month',$1::date)+interval '1 month')::date),
			(1,(date_trunc('month',$1::date)+interval '1 month -1 day')::date)
			) AS x(amount,issue_date)`, today)
		exec(db, `INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date) VALUES
			('USD','SGD',0.5,$1::date),('USD','SGD',20,$1::date-1),('USD','SGD',30,$1::date+1),
			('JPY','SGD',20,$1::date+1),('SGD','EUR',20,$1::date),('EUR','IDR',20,$1::date)`, today)
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
	exec(pool, "INSERT INTO businesses(business_id) VALUES ($1)", other)
	exec(pool, "INSERT INTO users(business_id,user_id,email,password_hash,role) VALUES ($1,$2,'revenue-admin@example.test','x','admin')", other, otherAdmin)
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, BaseCurrency: "SGD"}
	call := func(pool *pgxpool.Pool, scope, id, claim string) *httptest.ResponseRecorder {
		t.Helper()
		company, err := business.Parse(scope)
		if err != nil {
			t.Fatal(err)
		}
		router, err := NewScopedProfileRouter(pool, cfg, company)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if id != "" {
			token, err := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id, "business_id": claim, "role": "admin"})
			if err != nil {
				t.Fatal(err)
			}
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	read := func(w *httptest.ResponseRecorder) monthlyRevenueOutput {
		t.Helper()
		var out monthlyRevenueOutput
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("monthly revenue: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	a := func() monthlyRevenueOutput { return read(call(pool, company, admin, company)) }
	b := func() monthlyRevenueOutput { return read(call(pool, other, otherAdmin, other)) }
	for _, authorization := range []struct {
		id, claim string
		status    int
	}{{"", "", 401}, {otherAdmin, other, 401}, {otherAdmin, company, 403}, {user, company, 403}} {
		if w := call(pool, company, authorization.id, authorization.claim); w.Code != authorization.status {
			t.Fatalf("authorization: %d %s", w.Code, w.Body.String())
		}
	}
	if got := b(); len(got.Months) != 12 || !reflect.DeepEqual(got.Currencies, []string{}) || !reflect.DeepEqual(got.Unconverted, []string{}) {
		t.Fatalf("empty B: %+v", got)
	}
	exec(pool, `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,issue_date)
		VALUES ($1,gen_random_uuid(),'MILESTONE','USD',2,1,'PARTIAL',$2::date)`, other, today)
	exec(pool, `INSERT INTO fx_rates(business_id,from_currency,to_currency,rate,as_of_date)
		VALUES ($1,'USD','SGD',5,$2::date),($1,'EUR','SGD',5,$2::date)`, other, today)

	got := a()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if len(got.Months) != 12 || got.BaseCurrency != "SGD" ||
		!reflect.DeepEqual(got.Currencies, []string{"SGD", "USD"}) || !reflect.DeepEqual(got.Unconverted, []string{"EUR", "JPY"}) {
		t.Fatalf("report metadata: %+v", got)
	}
	for i, bucket := range got.Months {
		if bucket.Month != month.AddDate(0, i-11, 0).Format("2006-01") {
			t.Fatalf("month %d: %s", i, bucket.Month)
		}
	}
	if first := got.Months[0]; first.NativeTotals["USD"] != "-0.0001" || first.Totals["USD"] != "-0.0000" || first.UnpaidTotals["USD"] != "-0.0000" {
		t.Fatalf("oldest month: %+v", first)
	}
	if previous := got.Months[10]; !reflect.DeepEqual(previous.NativeTotals, map[string]string{"SGD": "0", "USD": "0"}) ||
		!reflect.DeepEqual(previous.Totals, map[string]string{"SGD": "0.0000", "USD": "0.0000"}) ||
		len(previous.NativeUnpaidTotals) != 0 || len(previous.UnpaidTotals) != 0 {
		t.Fatalf("zero-priced and offsetting invoices: %+v", previous)
	}
	if last := got.Months[11]; !reflect.DeepEqual(last.Totals, map[string]string{"SGD": "1000000000000000.0000", "USD": "0.5002"}) ||
		!reflect.DeepEqual(last.NativeTotals, map[string]string{"SGD": "1000000000000000.0000", "USD": "1.0003", "EUR": "2.0000", "JPY": "3.0000"}) ||
		!reflect.DeepEqual(last.NativeUnpaidTotals, map[string]string{"USD": "1.0003", "EUR": "2.0000"}) ||
		!reflect.DeepEqual(last.UnpaidTotals, map[string]string{"USD": "0.5002"}) {
		t.Fatalf("current month: %+v", last)
	}
	if last := b().Months[11]; !reflect.DeepEqual(last.Totals, map[string]string{"USD": "10.0000"}) ||
		!reflect.DeepEqual(last.UnpaidTotals, map[string]string{"USD": "5.0000"}) {
		t.Fatalf("B current month: %+v", last)
	}
	want := pythonReportResponses(t, reference.URL(), "stats", path, []string{""})[0]
	var body map[string]any
	w := call(pool, company, admin, company)
	if want.Status != 200 || w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || !reflect.DeepEqual(body, want.Body) {
		t.Fatalf("Python differential: Go %s, Python %+v", w.Body.String(), want)
	}

	traceCfg, err := pgxpool.ParseConfig(staged.URL())
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	traceCfg.ConnConfig.Tracer = monthlyRevenueSnapshotHook{afterBuckets: func(conn *pgx.Conn) {
		hits++
		var isolation, readOnly string
		if err := conn.QueryRow(ctx, "SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')").Scan(&isolation, &readOnly); err != nil || isolation != "repeatable read" || readOnly != "on" {
			t.Errorf("snapshot mode: %q %q %v", isolation, readOnly, err)
		}
		exec(pool, `UPDATE fx_rates SET rate=2 WHERE business_id=$1 AND from_currency='USD' AND to_currency='SGD' AND as_of_date=$2::date`, company, today)
	}}
	traced, err := pgxpool.NewWithConfig(ctx, traceCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	if snapshot := read(call(traced, company, admin, company)); hits != 1 || !reflect.DeepEqual(snapshot, got) {
		t.Fatalf("response mixed snapshots: %+v, hook calls %d", snapshot, hits)
	}
	if updated := a().Months[11].Totals["USD"]; updated != "2.0006" {
		t.Fatalf("subsequent request missed rate update: %q", updated)
	}
	// A valid stored amount and rate can produce a value wider than Python's
	// 28-digit context can quantize to four places.
	exec(pool, `UPDATE fx_rates SET rate=1234567890.12345678 WHERE business_id=$1 AND from_currency='USD' AND to_currency='SGD' AND as_of_date=$2::date`, company, today)
	exec(pool, `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,issue_date)
		VALUES ($1,gen_random_uuid(),'MILESTONE','USD',999999999999998.1231,0,'PAID',$2::date)`, company, today)
	if exact := a().Months[11].Totals["USD"]; exact != "1234567890123455697777787.5178" {
		t.Fatalf("large exact conversion: %q", exact)
	}
	exec(pool, `UPDATE fx_rates SET rate=0 WHERE business_id=$1 AND from_currency='USD' AND to_currency='SGD' AND as_of_date=$2::date`, company, today)
	if w := call(pool, company, admin, company); w.Code != 500 {
		t.Fatalf("invalid current rate: %d %s", w.Code, w.Body.String())
	}
}
