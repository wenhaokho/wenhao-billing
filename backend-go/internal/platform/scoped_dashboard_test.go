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
	"sort"
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

type dashboardSnapshotKey struct{}

type dashboardSnapshotHook struct {
	afterCounts func(*pgx.Conn)
}

func (h dashboardSnapshotHook) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, dashboardSnapshotKey{}, strings.Contains(data.SQL, "name: GetDashboardCounts"))
}

func (h dashboardSnapshotHook) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if matched, _ := ctx.Value(dashboardSnapshotKey{}).(bool); matched && data.Err == nil {
		h.afterCounts(conn)
	}
}

func TestScopedDashboardPostgres(t *testing.T) {
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
		admin         = "86000000-0000-0000-0000-000000000001"
		user          = "86000000-0000-0000-0000-000000000002"
		other         = "86000000-0000-0000-0000-000000000004"
		otherAdmin    = "86000000-0000-0000-0000-000000000005"
		customer      = "86000000-0000-0000-0000-000000000010"
		invoice       = "86000000-0000-0000-0000-000000000011"
		payment       = "86000000-0000-0000-0000-000000000012"
		logID         = "86000000-0000-0000-0000-000000000013"
		usage         = "86000000-0000-0000-0000-000000000014"
		otherCustomer = "86000000-0000-0000-0000-000000000020"
		otherInvoice  = "86000000-0000-0000-0000-000000000021"
		otherPayment  = "86000000-0000-0000-0000-000000000022"
		otherLog      = "86000000-0000-0000-0000-000000000023"
		missing       = "86000000-0000-0000-0000-000000000099"
		path          = "/api/v1/stats/dashboard"
	)
	now := time.Now()
	today := now.Format(time.DateOnly)
	for _, db := range []*pgxpool.Pool{pool, ref} {
		exec(db, `INSERT INTO users(user_id,email,password_hash,role) VALUES ($1,'dashboard-admin@example.test','x','admin'),($2,'dashboard-user@example.test','x','user')`, admin, user)
		exec(db, `INSERT INTO customers(customer_id,name,active) VALUES ($1,'A inactive customer',false)`, customer)
		exec(db, `INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,invoice_number)
			VALUES ($1,$2,'MILESTONE','USD',999999999999999.9999,999999999999999.9999,'OPEN','A-INV')`, invoice, customer)
		exec(db, `INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref)
			SELECT COALESCE(id,md5('dashboard-invoice-' || row_number() OVER ())::uuid),$1,kind,ccy,amount,balance,status,template,config FROM (VALUES
			(NULL::uuid,'MILESTONE','USD',1.0003,0.0004,'SENT',false,NULL::jsonb),
			(NULL,'MILESTONE','SGD',2.0000,2.0000,'PARTIAL',false,NULL),
			(NULL,'MILESTONE','JPY',3.0000,3.0000,'PARTIAL',false,NULL),
			(NULL,'MILESTONE','CAD',2.0000,0.0000,'OPEN',false,NULL),
			(NULL,'MILESTONE','USD',99,99,'PAID',false,NULL),
			(NULL,'MILESTONE','USD',99,99,'VOID',false,NULL),
			(NULL,'RECURRING','USD',12,0,'DRAFT',true,jsonb_build_object('frequency','YEARLY','start_date',($2::date+1)::text)),
			(NULL,'RECURRING','USD',1.0000,0,'DRAFT',true,jsonb_build_object('frequency','WEEKLY','start_date',$2::text)),
			(NULL,'RECURRING','SGD',6.0001,0,'DRAFT',true,jsonb_build_object('frequency','MONTHLY','interval',2,'start_date',($2::date+90)::text)),
			(NULL,'RECURRING','SGD',0.0001,0,'DRAFT',true,jsonb_build_object('frequency','MONTHLY','interval',2,'start_date',($2::date+91)::text)),
			(NULL,'RECURRING','USD',100,0,'DRAFT',true,jsonb_build_object('frequency','DAILY','paused','yes','start_date',$2::text)),
			(NULL,'RECURRING','USD',100,0,'DRAFT',true,jsonb_build_object('frequency','DAILY','end_mode','ON_DATE','end_date',($2::date-2)::text,'start_date',($2::date-3)::text)),
			(NULL,'RECURRING','USD',100,0,'DRAFT',true,'{"frequency":"bad"}'::jsonb),
			($3::uuid,'USAGE','SGD',8.1234,0,'DRAFT',false,jsonb_build_object('cut_off_day',extract(day from $2::date)::int,'locked',false)),
			(NULL,'USAGE','SGD',100,0,'DRAFT',false,'{"cut_off_day":31,"locked":"yes"}'::jsonb),
			(NULL,'USAGE','SGD',100,0,'DRAFT',false,'{"cut_off_day":31.0}'::jsonb),
			(NULL,'USAGE','SGD',100,0,'VOID',false,'{"cut_off_day":31}'::jsonb)
			) AS rows(id,kind,ccy,amount,balance,status,template,config)`, customer, today, usage)
		exec(db, `INSERT INTO quotations(quotation_id,customer_id,currency,amount,status)
			SELECT gen_random_uuid(),$1,currency,amount,status FROM (VALUES
			('USD',1000,'SENT'),('USD',0.1234,'ACCEPTED'),('SGD',900,'SENT'),('JPY',3,'DRAFT'),('JPY',3,'CONVERTED')) q(currency,amount,status)`, customer)
		exec(db, `INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date) VALUES
			('USD','SGD',0.5,$1::date),('USD','SGD',5,$1::date-1),('USD','SGD',6,$1::date+1),('SGD','JPY',1,$1::date)`, today)
		exec(db, `INSERT INTO payments(payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status,created_at,confidence_score,wht_amount)
			VALUES ($1,$2,12.3456,'USD','A payer',$3::date,'MANUAL','PENDING_MANUAL_REVIEW',$3::date,95.25,1.2345),
			(gen_random_uuid(),NULL,1,'USD','old',$3::date,'MANUAL','CLEARED',timezone('UTC',now())-interval '31 days',NULL,0),
			(gen_random_uuid(),NULL,1,'USD','recent',$3::date,'MANUAL','CLEARED',timezone('UTC',now())-interval '29 days',NULL,0)`, payment, invoice, today)
		exec(db, `INSERT INTO reconciliation_log(log_id,payment_id,action,reasons,actor_user_id,created_at)
			VALUES ($1,$2,'REVIEW',ARRAY['detail'],$3,$4::date)`, logID, payment, admin, today)
		exec(db, `INSERT INTO reconciliation_log(log_id,payment_id,action,created_at)
			SELECT md5('dashboard-log-' || g)::uuid,$1,'OLDER',$2::date-g FROM generate_series(1,12) g`, payment, today)

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
	exec(pool, "INSERT INTO users(business_id,user_id,email,password_hash,role) VALUES ($1,$2,'dashboard-admin@example.test','x','admin')", other, otherAdmin)
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
	read := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	a := func() map[string]any { return read(call(pool, company, admin, company)) }
	b := func() map[string]any { return read(call(pool, other, otherAdmin, other)) }
	for _, authCase := range []struct {
		id, claim string
		status    int
	}{{"", "", 401}, {otherAdmin, other, 401}, {otherAdmin, company, 403}, {user, company, 403}} {
		if w := call(pool, company, authCase.id, authCase.claim); w.Code != authCase.status {
			t.Fatalf("authorization: %d %s", w.Code, w.Body.String())
		}
	}
	exec(pool, `INSERT INTO customers(business_id,customer_id,name) VALUES ($1,$2,'B customer')`, other, otherCustomer)
	exec(pool, `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,invoice_number,is_template,billing_cycle_ref)
		VALUES ($1,$2,$3,'MILESTONE','JPY',50,40,'SENT','B-INV',false,NULL),
		($1,gen_random_uuid(),$3,'USAGE','SGD',2,0,'DRAFT',NULL,false,'{"cut_off_day":31}'),
		($1,gen_random_uuid(),$3,'RECURRING','JPY',5,0,'DRAFT',NULL,true,jsonb_build_object('frequency','MONTHLY','start_date',($4::date+1)::text))`, other, otherInvoice, otherCustomer, today)
	exec(pool, `INSERT INTO quotations(business_id,quotation_id,customer_id,currency,amount,status) VALUES ($1,gen_random_uuid(),$2,'JPY',6,'SENT')`, other, otherCustomer)
	exec(pool, `INSERT INTO fx_rates(business_id,from_currency,to_currency,rate,as_of_date) VALUES ($1,'JPY','SGD',2,$2::date),($1,'CAD','SGD',9,$2::date)`, other, today)
	exec(pool, `INSERT INTO payments(business_id,payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status)
		VALUES ($1,$2,$3,10,'JPY','B payer',$4::date,'MANUAL','CLEARED'),
		($1,gen_random_uuid(),NULL,1,'JPY','B review',$4::date,'MANUAL','PENDING_MANUAL_REVIEW')`, other, otherPayment, otherInvoice, today)
	exec(pool, `INSERT INTO reconciliation_log(business_id,log_id,payment_id,action,actor_user_id,created_at)
		VALUES ($1,$2,$3,'B ACTION',$4,$5::date+1)`, other, otherLog, otherPayment, otherAdmin, today)
	exec(pool, `UPDATE users SET display_name='B administrator' WHERE business_id=$1 AND user_id=$2`, other, otherAdmin)

	got := a()
	want := pythonReportResponses(t, reference.URL(), "stats", path, []string{""})[0]
	// Native AR is an unordered SQL GROUP BY in Python.
	native := want.Body.(map[string]any)["open_ar_by_currency"].([]any)
	sort.Slice(native, func(i, j int) bool {
		return native[i].(map[string]any)["currency"].(string) < native[j].(map[string]any)["currency"].(string)
	})
	if want.Status != 200 || !reflect.DeepEqual(got, want.Body) {
		t.Fatalf("Python differential: Go %+v, Python %+v", got, want)
	}
	if got["awaiting_review_count"] != float64(1) || got["draft_count"] != float64(3) ||
		got["sent_count"] != float64(3) || got["customer_count"] != float64(1) ||
		got["auto_cleared_last_30d"] != float64(1) || got["open_quotation_count"] != float64(3) ||
		!reflect.DeepEqual(got["open_ar_unconverted"], []any{"CAD", "JPY"}) ||
		len(got["recent_activity"].([]any)) != 10 || len(got["upcoming_invoices"].([]any)) != 4 {
		t.Fatalf("A dashboard: %+v", got)
	}
	if recent := got["recent_activity"].([]any)[0].(map[string]any); recent["log_id"] != logID ||
		recent["actor_name"] != "dashboard-admin@example.test" || recent["invoice_number"] != "A-INV" ||
		recent["customer_name"] != "A inactive customer" {
		t.Fatalf("A recent activity: %+v", recent)
	}
	if upcoming := got["upcoming_invoices"].([]any)[0].(map[string]any); upcoming["invoice_id"] != usage ||
		upcoming["next_date"] != today || upcoming["amount"] != "8.1234" || upcoming["amount_is_estimate"] != true {
		t.Fatalf("A upcoming usage: %+v", upcoming)
	}
	otherBody := b()
	if otherBody["awaiting_review_count"] != float64(1) || otherBody["draft_count"] != float64(1) ||
		otherBody["sent_count"] != float64(1) || otherBody["customer_count"] != float64(1) ||
		otherBody["auto_cleared_last_30d"] != float64(1) || otherBody["open_quotation_count"] != float64(1) ||
		!reflect.DeepEqual(otherBody["open_ar_by_currency"], []any{map[string]any{"currency": "JPY", "amount": "40.0000"}}) ||
		!reflect.DeepEqual(otherBody["open_ar_base_by_currency"], []any{map[string]any{"currency": "JPY", "base_amount": "80.0000"}}) ||
		!reflect.DeepEqual(otherBody["open_quotation_pipeline"], []any{map[string]any{"currency": "JPY", "amount": "6.0000"}}) ||
		!reflect.DeepEqual(otherBody["mrr_by_currency"], []any{map[string]any{"currency": "JPY", "amount": "5.0000"}}) ||
		!reflect.DeepEqual(otherBody["open_ar_unconverted"], []any{}) ||
		len(otherBody["upcoming_invoices"].([]any)) != 2 || len(otherBody["recent_activity"].([]any)) != 1 {
		t.Fatalf("B dashboard: %+v", otherBody)
	}
	if recent := otherBody["recent_activity"].([]any)[0].(map[string]any); recent["log_id"] != otherLog ||
		recent["actor_name"] != "B administrator" || recent["payment_id"] != otherPayment ||
		recent["invoice_number"] != "B-INV" || recent["customer_name"] != "B customer" {
		t.Fatalf("B recent activity: %+v", recent)
	}

	lastOfMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
	for _, item := range otherBody["upcoming_invoices"].([]any) {
		upcoming := item.(map[string]any)
		if upcoming["customer_id"] != otherCustomer || upcoming["customer_name"] != "B customer" {
			t.Fatalf("B upcoming customer: %+v", upcoming)
		}
		if upcoming["mode"] == "USAGE" && (upcoming["next_date"] != lastOfMonth || upcoming["amount_is_estimate"] != true) {
			t.Fatalf("B usage month clamp: %+v", upcoming)
		}
	}

	traceCfg, err := pgxpool.ParseConfig(staged.URL())
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	traceCfg.ConnConfig.Tracer = dashboardSnapshotHook{afterCounts: func(conn *pgx.Conn) {
		hits++
		var isolation, readOnly string
		if err := conn.QueryRow(ctx, "SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')").Scan(&isolation, &readOnly); err != nil || isolation != "repeatable read" || readOnly != "on" {
			t.Errorf("snapshot mode: %q %q %v", isolation, readOnly, err)
		}
		exec(pool, `UPDATE invoices SET balance_due=0,status='PAID' WHERE business_id=$1 AND invoice_id=$2`, company, invoice)
	}}
	traced, err := pgxpool.NewWithConfig(ctx, traceCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	if snapshot := read(call(traced, company, admin, company)); hits != 1 || !reflect.DeepEqual(snapshot, got) {
		t.Fatalf("response mixed snapshots: %+v, hook calls %d", snapshot, hits)
	}
	if later := a(); later["sent_count"] != float64(2) || reflect.DeepEqual(later["open_ar_by_currency"], got["open_ar_by_currency"]) {
		t.Fatalf("next request did not observe write: %+v", later)
	}

	corruptions := []struct{ table, key, constraint, column, id, old, foreign string }{
		{"reconciliation_log", "log_id", "reconciliation_log_payment_id_fkey", "payment_id", logID, payment, otherPayment},
		{"reconciliation_log", "log_id", "reconciliation_log_actor_user_id_fkey", "actor_user_id", logID, admin, otherAdmin},
		{"payments", "payment_id", "payments_invoice_id_fkey", "invoice_id", payment, invoice, otherInvoice},
		{"payments", "payment_id", "payments_customer_id_fkey", "customer_id", payment, "", otherCustomer},
		{"invoices", "invoice_id", "invoices_customer_id_fkey", "customer_id", usage, customer, otherCustomer},
	}
	for _, c := range corruptions {
		exec(pool, "ALTER TABLE "+c.table+" DROP CONSTRAINT "+c.constraint)
		stmt := "UPDATE " + c.table + " SET " + c.column + "=NULLIF($3,'')::uuid WHERE business_id=$1 AND " + c.key + "=$2"
		for _, value := range []string{c.foreign, missing} {
			exec(pool, stmt, company, c.id, value)
			if w := call(pool, company, admin, company); w.Code != 500 ||
				strings.Contains(w.Body.String(), value) || strings.Contains(w.Body.String(), "B customer") {
				t.Fatalf("damaged %s.%s: %d %s", c.table, c.column, w.Code, w.Body.String())
			}
			if result := b(); !reflect.DeepEqual(result, otherBody) {
				t.Fatalf("A corruption changed B dashboard: %+v", result)
			}
		}
		exec(pool, stmt, company, c.id, c.old)
	}
	// A half-even MRR tie and an FX product near the 28-digit Python
	// context boundary prove the intentional use of exact Go intermediates.
	for _, fixture := range []struct {
		db         *pgxpool.Pool
		businessID string
	}{{pool, company}, {ref, ""}} {
		args := []any{today}
		businessColumn, businessValue := "", ""
		if fixture.businessID != "" {
			args = append(args, fixture.businessID)
			businessColumn, businessValue = "business_id,", "$2::uuid,"
		}
		exec(fixture.db, `INSERT INTO invoices(`+businessColumn+`invoice_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref)
			SELECT `+businessValue+`gen_random_uuid(), 'RECURRING','CHF',amount,0,'DRAFT',TRUE,
			       jsonb_build_object('frequency','MONTHLY','interval',cycles,'start_date',$1::date::text)
			FROM (VALUES (0.0004,3),(0.0004,3),(0.0004,3),(0.0003,2)) t(amount,cycles)`, args...)
		exec(fixture.db, `INSERT INTO invoices(`+businessColumn+`invoice_id,invoice_type,currency,amount,balance_due,status,issue_date)
			VALUES (`+businessValue+`gen_random_uuid(),'MILESTONE','CHF',999999999994999.9999,999999999994999.9999,'OPEN',$1::date)`, args...)
		exec(fixture.db, `INSERT INTO fx_rates(`+businessColumn+`from_currency,to_currency,rate,as_of_date)
			VALUES (`+businessValue+`'CHF','SGD',100000000.00000001,$1::date)`, args...)
	}
	goPrecision := a()
	pyPrecision := pythonReportResponses(t, reference.URL(), "stats", path, []string{""})[0]
	if pyPrecision.Status != 200 {
		t.Fatalf("Python precision fixture: %+v", pyPrecision)
	}
	for _, check := range []struct {
		field, key, goValue, pyValue string
	}{
		{"mrr_by_currency", "amount", "0.0006", "0.0005"},
		{"open_ar_base_by_currency", "base_amount", "99999999999500009989999.9999", "99999999999500009990000.0000"},
	} {
		amount := func(body map[string]any) any {
			for _, value := range body[check.field].([]any) {
				row := value.(map[string]any)
				if row["currency"] == "CHF" {
					return row[check.key]
				}
			}
			return nil
		}
		if goAmount, pyAmount := amount(goPrecision), amount(pyPrecision.Body.(map[string]any)); goAmount != check.goValue || pyAmount != check.pyValue {
			t.Fatalf("CHF %s: Go %v, Python %v", check.field, goAmount, pyAmount)
		}
	}

	exec(pool, `UPDATE fx_rates SET rate=0 WHERE business_id=$1 AND from_currency='USD' AND to_currency='SGD' AND as_of_date=$2::date`, company, today)
	if w := call(pool, company, admin, company); w.Code != 500 {
		t.Fatalf("invalid current rate: %d %s", w.Code, w.Body.String())
	}
	if result := b(); !reflect.DeepEqual(result, otherBody) {
		t.Fatalf("A invalid rate changed B dashboard: %+v", result)
	}

}
