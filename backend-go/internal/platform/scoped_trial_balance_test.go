package platform

import (
	"context"
	"database/sql"
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
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestScopedTrialBalancePostgres(t *testing.T) {
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
	const seed = `
INSERT INTO users(user_id,email,password_hash,role) VALUES
 ('81000000-0000-0000-0000-000000000001','trial-admin@example.test','x','admin'),
 ('81000000-0000-0000-0000-000000000002','trial-user@example.test','x','user');
INSERT INTO chart_of_accounts(account_id,code,name,type,active) VALUES
 (81001,'TR-A','Cash','ASSET',false),(81002,'TR-B','Payable','LIABILITY',true),(81003,'TR-C','Future only','ASSET',true),
 (81004,'TR-D','Unused','ASSET',true),(81005,'TR-E','Capital','EQUITY',true),
 (81006,'TR-F','Sales','INCOME',true),(81007,'TR-G','Expenses','EXPENSE',true),(81008,'TR-H','COGS','COGS',true),
 (81009,'TR-I','Net zero','ASSET',true);
INSERT INTO journal_entries(entry_id,source_type,source_id,posted_at) VALUES
 ('82000000-0000-0000-0000-000000000001','TEST',gen_random_uuid(),'2026-01-02 23:59:59.999999'),
 ('82000000-0000-0000-0000-000000000002','TEST',gen_random_uuid(),'2026-01-03 00:00:00'),
 ('82000000-0000-0000-0000-000000000003','TEST',gen_random_uuid(),'2026-01-01 00:00:00');
INSERT INTO journal_lines(line_id,entry_id,account_id,currency,debit,credit,base_amount_debit,base_amount_credit) VALUES
 ('83000000-0000-0000-0000-000000000001','82000000-0000-0000-0000-000000000001',81001,'USD',100000000000000.1234,0,100000000000001.1234,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81002,'USD',0,100000000000000.1234,0,100000000000001.1234),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81001,'IDR',3.0000,0,0.0000,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81001,'IDR',0,3.0000,0,0.0000),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000002',81003,'SGD',999,0,999,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000003',81001,'USD',0.0001,0,0.0001,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000003',81002,'USD',0,0.0001,0,0.0001),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000003',81005,'USD',0,1.1234,0,1.1234),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000003',81006,'USD',0,2.1234,0,2.1234),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000003',81007,'USD',3.1234,0,3.1234,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000003',81008,'USD',0.1234,0,0.1234,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000003',81009,'USD',1,0,1,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000003',81009,'USD',0,1,0,1);`
	apply(pool, seed)
	apply(pythonPool, seed)
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
	const foreign = "84000000-0000-0000-0000-000000000001"
	const admin = "81000000-0000-0000-0000-000000000001"
	const user = "81000000-0000-0000-0000-000000000002"
	const foreignEntry = "82000000-0000-0000-0000-000000000091"
	const ownedEntry = "82000000-0000-0000-0000-000000000001"
	const ownedLine = "83000000-0000-0000-0000-000000000001"
	const foreignLine = "83000000-0000-0000-0000-000000000091"
	apply(pool, "INSERT INTO businesses (business_id) VALUES ($1)", foreign)
	apply(pool, "INSERT INTO users (business_id,user_id,email,password_hash,role) VALUES ($1,'81000000-0000-0000-0000-000000000091','trial-admin@example.test','x','admin')", foreign)
	apply(pool, "INSERT INTO chart_of_accounts (business_id,account_id,code,name,type) VALUES ($1,81091,'TR-A','Foreign cash','ASSET')", foreign)
	apply(pool, "INSERT INTO journal_entries (business_id,entry_id,source_type,source_id,posted_at) VALUES ($1,$2,'TEST',gen_random_uuid(),'2026-01-02')", foreign, foreignEntry)
	apply(pool, "INSERT INTO journal_lines (business_id,line_id,entry_id,account_id,currency,debit,base_amount_debit) VALUES ($1,$2,$3,81091,'SGD',900000,900000)", foreign, foreignLine, foreignEntry)
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode, BaseCurrency: "SGD"}
	router := func(company string, cfg SessionConfig) http.Handler {
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
	const path = "/api/v1/accounting/reports/trial-balance"
	call := func(handler http.Handler, reportPath, suffix, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, reportPath+suffix, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	handler, signed := router(company, cfg), token(admin, company)
	const cutoff = "?as_of=2026-01-02"
	for cookie, status := range map[string]int{"": 401, token(admin, foreign): 401, token(user, company): 403} {
		if w := call(handler, path, cutoff, cookie); w.Code != status {
			t.Fatalf("auth status %d, want %d: %s", w.Code, status, w.Body)
		}
	}
	snapshot := func() string {
		t.Helper()
		var result string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(e) ORDER BY entry_id) FROM journal_entries e),
 (SELECT jsonb_agg(to_jsonb(l) ORDER BY line_id) FROM journal_lines l),
 (SELECT jsonb_agg(to_jsonb(a) ORDER BY account_id) FROM chart_of_accounts a))::text`).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	paths := []string{cutoff, "?as_of=2026-01-03", "?as_of=0001-01-01", "?as_of=2026-01-02T00:00:00", "?as_of=&as_of=2026-01-02", "?as_of=1767312000", "?as_of=1767312000.0000001", "?as_of=1767312000.0000009", "?as_of=2026-01-02T00:00:00.0000009", "?as_of=2026-01-02T00:00:00.000001", "?as_of=2026-01-02T00:01:00", "?as_of=bad", "?as_of=", ""}
	want := pythonAccountingReports(t, reference.URL(), path, paths)
	for i, suffix := range paths {
		w := call(handler, path, suffix, signed)
		var body any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != want[i].Status || !reflect.DeepEqual(body, want[i].Body) {
			t.Fatalf("%q: got %d %s; want %d %+v", suffix, w.Code, w.Body, want[i].Status, want[i].Body)
		}
	}
	original := call(handler, path, cutoff, signed).Body.String()
	for _, fragment := range []string{`"base_currency":"SGD"`, `"total_debit_base":"100000000000005.3703"`, `"total_credit_base":"100000000000005.3703"`, `"net":"0.0000"`, `"net":"-100000000000000.1235"`} {
		if !strings.Contains(original, fragment) {
			t.Fatalf("missing %s in %s", fragment, original)
		}
	}
	if strings.Contains(original, "business_id") || strings.Contains(original, "Foreign cash") || before != snapshot() {
		t.Fatal("report leaked foreign data, exposed the business ID, or changed ledger records")
	}
	defaultCfg := cfg
	defaultCfg.BaseCurrency = ""
	w := call(router(foreign, defaultCfg), path, cutoff, token("81000000-0000-0000-0000-000000000091", foreign))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"base_currency":"IDR"`) || !strings.Contains(w.Body.String(), "Foreign cash") || strings.Contains(w.Body.String(), "Payable") {
		t.Fatalf("business B report: %d %s", w.Code, w.Body)
	}
	const balancePath = "/api/v1/accounting/reports/balance-sheet"
	const future = "?as_of=2026-01-03"
	for cookie, status := range map[string]int{"": 401, token(admin, foreign): 401, token(user, company): 403} {
		if w := call(handler, balancePath, cutoff, cookie); w.Code != status {
			t.Fatalf("balance sheet auth status %d, want %d: %s", w.Code, status, w.Body)
		}
	}
	balanceReference := pythonAccountingReports(t, reference.URL(), balancePath, []string{future, "?as_of=bad", cutoff})
	for i, suffix := range []string{future, "?as_of=bad"} {
		w := call(handler, balancePath, suffix, signed)
		var body any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != balanceReference[i].Status || !reflect.DeepEqual(body, balanceReference[i].Body) {
			t.Fatalf("balance sheet %q: got %d %s; want %d %+v", suffix, w.Code, w.Body, balanceReference[i].Status, balanceReference[i].Body)
		}
	}
	w = call(handler, balancePath, cutoff, signed)
	historical := w.Body.String()
	for _, fragment := range []string{
		`"base_currency":"SGD"`, `"assets_total_base":"100000000000001.1235"`, `"liabilities_total_base":"100000000000001.1235"`,
		`"equity_total_base":"1.1234"`, `"income_total_base":"2.1234"`, `"expense_total_base":"3.2468"`,
		`"name":"Net zero","base_balance":"0.0000"`,
	} {
		if w.Code != 200 || !strings.Contains(historical, fragment) {
			t.Fatalf("missing %s in balance sheet %d %s", fragment, w.Code, historical)
		}
	}
	legacy, err := json.Marshal(balanceReference[2].Body)
	if err != nil || balanceReference[2].Status != 200 || !strings.Contains(string(legacy), "Future only") || strings.Contains(historical, "Future only") {
		t.Fatalf("historical cutoff: staged %s, Python %s", historical, legacy)
	}
	for _, forbidden := range []string{"business_id", "Foreign cash", "Unused"} {
		if strings.Contains(historical, forbidden) {
			t.Fatalf("unexpected %q in historical balance sheet %s", forbidden, historical)
		}
	}
	w = call(handler, balancePath, "?as_of=0001-01-01", signed)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"groups":[],"assets_total_base":"0"`) {
		t.Fatalf("empty balance sheet: %d %s", w.Code, w.Body)
	}
	w = call(router(foreign, defaultCfg), balancePath, cutoff, token("81000000-0000-0000-0000-000000000091", foreign))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"base_currency":"IDR"`) || !strings.Contains(w.Body.String(), "Foreign cash") || strings.Contains(w.Body.String(), "Payable") {
		t.Fatalf("business B balance sheet: %d %s", w.Code, w.Body)
	}
	if before != snapshot() {
		t.Fatal("balance sheet changed ledger records")
	}
	const fxPath = "/api/v1/accounting/reports/fx-revaluation"
	const rateSeed = `
INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date,source) VALUES
 ('USD','SGD',0.5,'2026-01-01','MANUAL'),
 ('USD','SGD',2,'2026-01-02','MANUAL'),
 ('USD','SGD',3,'2026-01-03','MANUAL'),
 ('CAD','SGD',0.00000001,'2026-01-02','MANUAL'),
 ('CAD','SGD',0.00000010,'2026-01-03','MANUAL'),
 ('CHF','SGD',1,'2026-01-02','MANUAL'),
 ('GBP','SGD',2,'2026-01-02','MANUAL'),
 ('JPY','SGD',2,'2026-01-03','MANUAL'),
 ('SGD','IDR',4,'2026-01-02','MANUAL');
INSERT INTO journal_lines(line_id,entry_id,account_id,currency,debit,credit,base_amount_debit,base_amount_credit) VALUES
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81001,'EUR',0,5,0,6),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81001,'GBP',0,5,0,6),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81001,'JPY',0,1,0,1),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81001,'CAD',0,0.0001,0,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81002,'CAD',0.0001,0,0,0),
 (gen_random_uuid(),'82000000-0000-0000-0000-000000000001',81002,'CHF',0,1,0,1);`
	stagedRateSeed := strings.ReplaceAll(rateSeed, "fx_rates(", "fx_rates(business_id,")
	stagedRateSeed = strings.ReplaceAll(stagedRateSeed, "journal_lines(", "journal_lines(business_id,")
	stagedRateSeed = strings.ReplaceAll(stagedRateSeed, "\n (", "\n ('"+company+"',")
	apply(pool, stagedRateSeed)
	apply(pythonPool, rateSeed)
	apply(pool, "INSERT INTO fx_rates (business_id,from_currency,to_currency,rate,as_of_date,source) VALUES ($1,'USD','SGD',90,'2026-01-02','MANUAL'),($1,'EUR','SGD',50,'2026-01-02','MANUAL'),($1,'SGD','IDR',7,'2026-01-02','MANUAL')", foreign)
	rateSnapshot := func() string {
		t.Helper()
		var rates string
		if err := pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(r) ORDER BY rate_id)::text FROM fx_rates r").Scan(&rates); err != nil {
			t.Fatal(err)
		}
		return snapshot() + rates
	}
	fxBefore := rateSnapshot()
	for cookie, status := range map[string]int{"": 401, token(admin, foreign): 401, token(user, company): 403} {
		if w := call(handler, fxPath, cutoff, cookie); w.Code != status {
			t.Fatalf("FX report auth status %d, want %d: %s", w.Code, status, w.Body)
		}
	}
	fxDates := []string{"?as_of=2026-01-01", cutoff, future, "?as_of=0001-01-01", "?as_of=bad"}
	fxReference := pythonAccountingReports(t, reference.URL(), fxPath, fxDates)
	for i, suffix := range fxDates {
		w := call(handler, fxPath, suffix, signed)
		var body any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != fxReference[i].Status || !reflect.DeepEqual(body, fxReference[i].Body) {
			t.Fatalf("FX revaluation %q: got %d %s; want %d %+v", suffix, w.Code, w.Body, fxReference[i].Status, fxReference[i].Body)
		}
	}
	fxHistorical := call(handler, fxPath, cutoff, signed).Body.String()
	for _, fragment := range []string{
		`"missing_rates":["EUR","JPY"]`, `"face_balance":"-5.0000","original_base":"-6.0000","revalued_base":null,"rate":null,"rate_as_of":null,"unrealised_gain_loss":null,"rate_missing":true`,
		`"total_unrealised_gain":"99999999999999.1235"`, `"total_unrealised_loss":"100000000000003.1235"`,
		`"currency":"GBP","face_balance":"-5.0000","original_base":"-6.0000","revalued_base":"-10.0000","rate":"2.00000000","rate_as_of":"2026-01-02","unrealised_gain_loss":"-4.0000"`,
	} {
		if !strings.Contains(fxHistorical, fragment) {
			t.Fatalf("missing %s in FX report %s", fragment, fxHistorical)
		}
	}
	w = call(router(foreign, defaultCfg), fxPath, cutoff, token("81000000-0000-0000-0000-000000000091", foreign))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rate":"7.00000000"`) || !strings.Contains(w.Body.String(), "Foreign cash") || strings.Contains(w.Body.String(), "Payable") {
		t.Fatalf("business B FX revaluation: %d %s", w.Code, w.Body)
	}
	if strings.Contains(fxHistorical, "business_id") || strings.Contains(fxHistorical, "Foreign cash") || fxBefore != rateSnapshot() {
		t.Fatal("FX report leaked foreign data, exposed the business ID, or wrote records")
	}
	apply(pool, "UPDATE fx_rates SET rate=0 WHERE business_id=$1 AND from_currency='USD' AND to_currency='SGD' AND as_of_date='2026-01-02'", company)
	if w := call(handler, fxPath, cutoff, signed); w.Code != 500 || w.Body.String() != "{\"detail\":\"internal server error\"}\n" {
		t.Fatalf("invalid latest rate: %d %s", w.Code, w.Body)
	}
	apply(pool, "UPDATE fx_rates SET rate=2 WHERE business_id=$1 AND from_currency='USD' AND to_currency='SGD' AND as_of_date='2026-01-02'", company)
	original = call(handler, path, cutoff, signed).Body.String()
	historical = call(handler, balancePath, cutoff, signed).Body.String()
	for _, name := range []string{"journal_lines_entry_id_fkey", "journal_lines_account_id_fkey"} {
		apply(pool, "ALTER TABLE journal_lines DROP CONSTRAINT "+name)
	}
	apply(pool, "UPDATE journal_lines SET entry_id=$2,account_id=81001 WHERE business_id=$1 AND line_id=$3", foreign, ownedEntry, foreignLine)
	if w := call(handler, path, cutoff, signed); w.Code != 200 || w.Body.String() != original {
		t.Fatalf("foreign line attached to owned entry/account: %d %s", w.Code, w.Body)
	}
	if w := call(handler, balancePath, cutoff, signed); w.Code != 200 || w.Body.String() != historical {
		t.Fatalf("balance sheet foreign line attached to owned entry/account: %d %s", w.Code, w.Body)
	}
	if w := call(handler, fxPath, cutoff, signed); w.Code != 200 || w.Body.String() != fxHistorical {
		t.Fatalf("FX report foreign line attached to owned entry/account: %d %s", w.Code, w.Body)
	}
	assertDamaged := func() {
		t.Helper()
		before := rateSnapshot()
		for _, reportPath := range []string{path, balancePath, fxPath} {
			if w := call(handler, reportPath, cutoff, signed); w.Code != 500 || w.Body.String() != "{\"detail\":\"internal server error\"}\n" {
				t.Fatalf("damaged reference in %s: %d %s", reportPath, w.Code, w.Body)
			}
		}
		if before != rateSnapshot() {
			t.Fatal("failed report changed ledger or rate records")
		}
	}
	for _, id := range []int{81091, 81999} {
		apply(pool, "UPDATE journal_lines SET account_id=$3 WHERE business_id=$1 AND line_id=$2", company, ownedLine, id)
		assertDamaged()
	}
	apply(pool, "UPDATE journal_lines SET account_id=81001 WHERE business_id=$1 AND line_id=$2", company, ownedLine)
	for _, id := range []string{foreignEntry, "82000000-0000-0000-0000-000000000099"} {
		apply(pool, "UPDATE journal_lines SET entry_id=$3 WHERE business_id=$1 AND line_id=$2", company, ownedLine, id)
		assertDamaged()
	}
}

type accountingReferenceResponse struct {
	Status int
	Body   any
}

func pythonAccountingReports(t *testing.T, referenceURL, path string, paths []string) []accountingReferenceResponse {
	t.Helper()
	return pythonReportResponses(t, referenceURL, "accounting", path, paths)
}

func pythonReportResponses(t *testing.T, referenceURL, module, path string, paths []string) []accountingReferenceResponse {
	t.Helper()
	py := exec.Command("python", "-c", `import json, os, sys
from importlib import import_module
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
router = import_module("app.api.v1.routers." + sys.argv[1]).router
from app.api.deps import current_admin
from app.db.session import get_db
engine = create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
app = FastAPI()
app.include_router(router, prefix='/api/v1')
app.dependency_overrides[current_admin] = lambda: object()
def session():
    with Session(engine) as db: yield db
app.dependency_overrides[get_db] = session
with TestClient(app) as client:
    responses = [client.get(sys.argv[2]+q) for q in sys.argv[3:]]
    print(json.dumps([{'status': r.status_code, 'body': r.json()} for r in responses]))
`)
	py.Args = append(append(py.Args, module, path), paths...)
	py.Dir = "../../../backend"
	py.Env = append(os.Environ(), "DB="+referenceURL, "BASE_CURRENCY=SGD")
	out, err := py.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("Python reference: %v\n%s", err, exit.Stderr)
		}
		t.Fatal(err)
	}
	var want []accountingReferenceResponse
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("reference JSON: %v\n%s", err, out)
	}
	return want
}
