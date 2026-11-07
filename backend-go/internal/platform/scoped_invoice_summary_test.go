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

type invoiceSummarySnapshotKey struct{}

type invoiceSummarySnapshotHook struct {
	afterBuckets func(*pgx.Conn)
}

func (h invoiceSummarySnapshotHook) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, invoiceSummarySnapshotKey{}, strings.Contains(data.SQL, "name: GetInvoiceSummaryBuckets"))
}

func (h invoiceSummarySnapshotHook) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if matched, _ := ctx.Value(invoiceSummarySnapshotKey{}).(bool); matched && data.Err == nil {
		h.afterBuckets(conn)
	}
}

func TestScopedInvoiceSummaryPostgres(t *testing.T) {
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
		paid       = "86000000-0000-0000-0000-000000000003"
		other      = "86000000-0000-0000-0000-000000000004"
		otherAdmin = "86000000-0000-0000-0000-000000000005"
		otherPaid  = "86000000-0000-0000-0000-000000000006"
		path       = "/api/v1/stats/invoices-summary"
	)
	now := time.Now()
	today := now.Format(time.DateOnly)
	for _, db := range []*pgxpool.Pool{pool, ref} {
		exec(db, `INSERT INTO users(user_id,email,password_hash,role) VALUES ($1,'summary-admin@example.test','x','admin'),($2,'summary-user@example.test','x','user')`, admin, user)
		exec(db, `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,issue_date,due_date,is_template)
			SELECT gen_random_uuid(),'MILESTONE',currency,amount,amount,status,$1::date,$1::date+due,template FROM (VALUES
			('USD',999999999999999.9999,'OPEN',-1,false),('USD',0.0001,'SENT',-3,false),('IDR',0.1234,'PARTIAL',-1,false),
			('USD',0.0001,'OPEN',0,false),('USD',0.0002,'PARTIAL',30,false),('USD',8,'OPEN',31,false),
			('USD',8,'DRAFT',0,false),('USD',8,'VOID',0,false),('USD',8,'PAID',0,false),
			('USD',8,'OPEN',0,true),('USD',8,'OPEN',NULL,false),('USD',0,'OPEN',0,false),('USD',-1,'OPEN',0,false)
			) AS x(currency,amount,status,due,template)`, today)
		exec(db, `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,issue_date)
			VALUES ($1,'MILESTONE','USD',1,0,'PAID',$2::date-70),
			('86000000-0000-0000-0000-000000000007','MILESTONE','USD',1,0,'PAID',NULL),
			('86000000-0000-0000-0000-000000000008','MILESTONE','USD',1,0,'OPEN',$2::date-80)`, paid, today)
		exec(db, `INSERT INTO payments(payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status)
			SELECT gen_random_uuid(),invoice_id,1,'USD','payer',$2::date+days,'MANUAL',status
			FROM (VALUES ($1::uuid,-90,'CLEARED'),($1,-70,'CLEARED'),($1,1,'CLEARED'),($1,-91,'CLEARED'),
			($1,-84,'CLEARED'),($1,-84,'CLEARED'),($1,-84,'CLEARED'),
			($1,-84,'CLEARED'),($1,-84,'CLEARED'),($1,-83,'CLEARED'),
			($1,0,'PENDING_MANUAL_REVIEW'),(NULL,0,'CLEARED'),
			('86000000-0000-0000-0000-000000000007',0,'CLEARED'),('86000000-0000-0000-0000-000000000008',0,'CLEARED')
			) AS x(invoice_id,days,status)`, paid, today)
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
	exec(pool, "INSERT INTO users(business_id,user_id,email,password_hash,role) VALUES ($1,$2,'summary-admin@example.test','x','admin')", other, otherAdmin)
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
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
	read := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("summary: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	a := func() map[string]any { return read(call(pool, company, admin, company)) }
	b := func() map[string]any { return read(call(pool, other, otherAdmin, other)) }
	expected := map[string]any{
		"overdue":         []any{map[string]any{"currency": "IDR", "amount": "0.1234"}, map[string]any{"currency": "USD", "amount": "1000000000000000.0000"}},
		"due_30d":         []any{map[string]any{"currency": "USD", "amount": "0.0003"}},
		"avg_days_to_pay": float64(-32.0 / 9.0),
	}
	if got := a(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("A summary: %v", got)
	}
	want := pythonReportResponses(t, reference.URL(), "stats", path, []string{""})[0]
	pythonBody := want.Body.(map[string]any)
	for _, key := range []string{"overdue", "due_30d"} {
		rows := pythonBody[key].([]any)
		if len(rows) == 2 && rows[0].(map[string]any)["currency"] == "USD" {
			rows[0], rows[1] = rows[1], rows[0]
		}
	}
	if want.Status != 200 || !reflect.DeepEqual(expected, pythonBody) {
		t.Fatalf("Python summary: %v", want)
	}
	if got := b(); !reflect.DeepEqual(got, map[string]any{"overdue": []any{}, "due_30d": []any{}, "avg_days_to_pay": nil}) {
		t.Fatalf("empty B summary: %v", got)
	}
	for _, auth := range []struct {
		id, claim string
		status    int
	}{{"", "", 401}, {otherAdmin, other, 401}, {otherAdmin, company, 403}, {user, company, 403}} {
		if w := call(pool, company, auth.id, auth.claim); w.Code != auth.status {
			t.Fatalf("authorization: %d %s", w.Code, w.Body.String())
		}
	}
	exec(pool, `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,issue_date,due_date)
		VALUES ($1,$2,'MILESTONE','USD',1,0,'PAID',$3::date-30,NULL),
		($1,gen_random_uuid(),'MILESTONE','USD',99,99,'SENT',NULL,$3::date-1)`, other, otherPaid, today)
	if _, err := pool.Exec(ctx, `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status)
		VALUES ($1,$2,'MILESTONE','USD',1,0,'PAID')`, other, paid); err == nil {
		t.Fatal("primary key accepted a shared invoice ID")
	}
	insert := `INSERT INTO payments(business_id,payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status)
		VALUES ($1,gen_random_uuid(),$2,1,'USD','payer',$3::date,'MANUAL','CLEARED')`
	if _, err := pool.Exec(ctx, insert, other, paid, today); err == nil {
		t.Fatal("composite FK accepted foreign invoice")
	}
	exec(pool, insert, other, otherPaid, today)
	exec(pool, "ALTER TABLE payments DROP CONSTRAINT payments_invoice_id_fkey")
	exec(pool, insert, other, paid, today)
	exec(pool, insert, company, otherPaid, today)
	exec(pool, insert, company, "86000000-0000-0000-0000-000000000099", today)
	if got := a(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("cross-business data changed A: %v", got)
	}
	if got := b(); got["avg_days_to_pay"] != float64(30) || !reflect.DeepEqual(got["overdue"], []any{map[string]any{"currency": "USD", "amount": "99.0000"}}) {
		t.Fatalf("B summary: %v", got)
	}

	traceCfg, err := pgxpool.ParseConfig(staged.URL())
	if err != nil {
		t.Fatal(err)
	}
	// UTC+14 and UTC-12 are always on different dates. Force the database
	// connection to use a different calendar date from the API process.
	dbZone := "Pacific/Kiritimati"
	if now.In(time.FixedZone("UTC+14", 14*60*60)).Format(time.DateOnly) == today {
		dbZone = "Etc/GMT+12"
	}
	traceCfg.ConnConfig.RuntimeParams["timezone"] = dbZone
	hits := 0
	traceCfg.ConnConfig.Tracer = invoiceSummarySnapshotHook{afterBuckets: func(conn *pgx.Conn) {
		hits++
		var isolation, readOnly, dbDate string
		if err := conn.QueryRow(ctx, "SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only'), current_date::text").Scan(&isolation, &readOnly, &dbDate); err != nil || isolation != "repeatable read" || readOnly != "on" || dbDate == today {
			t.Errorf("snapshot mode or test timezone: %q %q %q %v", isolation, readOnly, dbDate, err)
		}
		exec(pool, insert, company, paid, today)
	}}
	traced, err := pgxpool.NewWithConfig(ctx, traceCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	if got := read(call(traced, company, admin, company)); hits != 1 || !reflect.DeepEqual(got, expected) {
		t.Fatalf("response mixed snapshots: %v, hook calls %d", got, hits)
	}
	if got := a(); got["avg_days_to_pay"] != float64(3.8) {
		t.Fatalf("subsequent request missed committed payment: %v", got)
	}
	exec(pool, "UPDATE payments SET payment_date=$3::date-30 WHERE business_id=$1 AND invoice_id=$2", other, otherPaid, today)
	if got := b(); got["avg_days_to_pay"] != float64(0) {
		t.Fatalf("zero payment delta must remain a number: %v", got)
	}
}
