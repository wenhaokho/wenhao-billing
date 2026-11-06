package migrations

import (
	"context"
	"database/sql"
	"fmt"
	kitdb "github.com/LOQATO/gokit/db"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/peterldowns/pgtestdb"
)

type emptyMigrator struct{}

func (emptyMigrator) Hash() (string, error)                                   { return "billing-empty-runtime-v1", nil }
func (emptyMigrator) Migrate(context.Context, *sql.DB, pgtestdb.Config) error { return nil }
func freshDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	p, _ := u.User.Password()
	c := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: p, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, emptyMigrator{})
	db, e := sql.Open("pgx", c.URL())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db, c.URL()
}
func TestMigrateFreshRepeatedConcurrent(t *testing.T) {
	db, u := freshDB(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- MigrateFresh(ctx, u) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var before, after string
	if e := db.QueryRow(`SELECT string_agg(row_to_json(c)::text, ',' ORDER BY account_id) FROM chart_of_accounts c`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if e := MigrateFresh(ctx, u); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT string_agg(row_to_json(c)::text, ',' ORDER BY account_id) FROM chart_of_accounts c`).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if before != after {
		t.Fatal("seeds changed")
	}
}
func TestRejectAlembic(t *testing.T) {
	db, u := freshDB(t)
	if _, e := db.Exec(`CREATE TABLE alembic_version(version_num text); INSERT INTO alembic_version VALUES ('0028_business_npwp'); CREATE TABLE important_data(value text); INSERT INTO important_data VALUES ('keep')`); e != nil {
		t.Fatal(e)
	}
	if e := MigrateFresh(context.Background(), u); e == nil || !strings.Contains(e.Error(), "alembic") {
		t.Fatalf("expected alembic rejection, got %v", e)
	}
	var v string
	if e := db.QueryRow(`SELECT value FROM important_data`).Scan(&v); e != nil || v != "keep" {
		t.Fatalf("data changed: %q %v", v, e)
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM alembic_version WHERE version_num='0028_business_npwp'`).Scan(&n); e != nil || n != 1 {
		t.Fatal("metadata changed")
	}
}
func TestRejectUnrelatedSchema(t *testing.T) {
	db, u := freshDB(t)
	if _, e := db.Exec(`CREATE TABLE unrelated(value text)`); e != nil {
		t.Fatal(e)
	}
	if e := MigrateFresh(context.Background(), u); e == nil || !strings.Contains(e.Error(), "nonempty") {
		t.Fatalf("expected nonempty rejection, got %v", e)
	}
}
func TestRejectInconsistentGoose(t *testing.T) {
	db, u := freshDB(t)
	if _, e := db.Exec(`CREATE TABLE goose_db_version(id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL, tstamp timestamp DEFAULT now()); INSERT INTO goose_db_version(version_id,is_applied) VALUES (0,true),(1,false)`); e != nil {
		t.Fatal(e)
	}
	if e := MigrateFresh(context.Background(), u); e == nil || !strings.Contains(e.Error(), "metadata") {
		t.Fatalf("expected metadata rejection, got %v", e)
	}
}

func TestRejectAlembicHeadReferenceUnchanged(t *testing.T) {
	raw := os.Getenv("PGTESTDB_URL")
	db, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshot := func() string {
		t.Helper()
		var s string
		err := db.QueryRow(`SELECT (SELECT string_agg(version_num, ',' ORDER BY version_num) FROM alembic_version) || '|' || (SELECT string_agg(row_to_json(c)::text, ',' ORDER BY account_id) FROM chart_of_accounts c)`).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	var gooseBefore, gooseAfter bool
	if err := db.QueryRow(`SELECT to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&gooseBefore); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(before, "0028_business_npwp|") {
		t.Fatalf("unexpected reference revision: %s", before)
	}
	if err := MigrateFresh(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "alembic") {
		t.Fatalf("expected alembic rejection, got %v", err)
	}
	if after := snapshot(); before != after {
		t.Fatal("reference data or metadata changed")
	}
	if err := db.QueryRow(`SELECT to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&gooseAfter); err != nil || gooseBefore != gooseAfter {
		t.Fatal("goose metadata changed")
	}
}

func TestRejectGooseWithMissingSchema(t *testing.T) {
	db, u := freshDB(t)
	if _, err := db.Exec(`CREATE TABLE goose_db_version(id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL, tstamp timestamp DEFAULT now()); INSERT INTO goose_db_version(version_id,is_applied) VALUES (0,true),(1,true)`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateFresh(context.Background(), u); err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("expected metadata rejection, got %v", err)
	}
}

// Inspection must wait behind the same lock as migration, not merely goose's
// migration lock. An object introduced while waiting must be noticed.
func TestInspectionWaitsForMigrationLock(t *testing.T) {
	db, u := freshDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(710287203441)`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `SELECT pg_advisory_unlock(710287203441)`)
	result := make(chan error, 1)
	go func() { result <- MigrateFresh(ctx, u) }()
	select {
	case err := <-result:
		t.Fatalf("inspection did not wait for lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err = conn.ExecContext(ctx, `CREATE TABLE unrelated(value text)`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_unlock(710287203441)`); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err == nil || !strings.Contains(err.Error(), "nonempty") {
		t.Fatalf("expected nonempty rejection after lock, got %v", err)
	}
}

func TestRejectModifiedMigratedSchema(t *testing.T) {
	for _, tc := range []struct{ name, change, verify string }{
		{"extra_schema", `CREATE SCHEMA extra`, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='extra')`},
		{"extra_relation", `CREATE SCHEMA extra; CREATE TABLE extra.foreign_data(value text); INSERT INTO extra.foreign_data VALUES ('keep')`, `SELECT EXISTS(SELECT 1 FROM extra.foreign_data WHERE value='keep')`},
		{"view", `CREATE VIEW public.extra_view AS SELECT 1 AS n`, `SELECT to_regclass('public.extra_view') IS NOT NULL`},
		{"function", `CREATE FUNCTION public.extra_function() RETURNS int LANGUAGE sql AS 'SELECT 1'`, `SELECT to_regprocedure('public.extra_function()') IS NOT NULL`},
		{"type", `CREATE TYPE public.extra_type AS ENUM ('x')`, `SELECT to_regtype('public.extra_type') IS NOT NULL`},
		{"column", `ALTER TABLE public.chart_of_accounts ADD COLUMN unrelated text`, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='chart_of_accounts' AND column_name='unrelated')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, u := freshDB(t)
			if err := MigrateFresh(context.Background(), u); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.change); err != nil {
				t.Fatal(err)
			}
			if err := MigrateFresh(context.Background(), u); err == nil {
				t.Fatal("expected modified schema rejection")
			}
			var kept bool
			if err := db.QueryRow(tc.verify).Scan(&kept); err != nil || !kept {
				t.Fatalf("schema changed: %v", err)
			}
		})
	}
}

func TestMigrateFreshSearchPath(t *testing.T) {
	db, u := freshDB(t)
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", "pg_catalog,public")
	parsed.RawQuery = q.Encode()
	u = parsed.String()
	if err := MigrateFresh(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := MigrateFresh(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	var ok bool
	if err := db.QueryRow(`SELECT to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&ok); err != nil || !ok {
		t.Fatalf("public metadata missing: %v", err)
	}
}

func TestRejectAlteredGooseHistoryOnBaseline(t *testing.T) {
	db, u := freshDB(t)
	if err := MigrateFresh(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE public.goose_db_version SET is_applied=false WHERE version_id=1`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateFresh(context.Background(), u); err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("expected metadata rejection, got %v", err)
	}
	var applied bool
	if err := db.QueryRow(`SELECT is_applied FROM public.goose_db_version WHERE version_id=1`).Scan(&applied); err != nil || applied {
		t.Fatalf("metadata changed: %v", err)
	}
}

func fingerprintForTest(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.ExecContext(ctx, `SET search_path TO pg_catalog,public`); err != nil {
		t.Fatal(err)
	}
	hash, err := schemaFingerprint(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestRejectCustomCatalogObjects(t *testing.T) {
	for _, migrated := range []bool{false, true} {
		for _, tc := range []struct{ name, setup, exists string }{
			{"collation", `CREATE COLLATION public.extra_collation (provider=libc, locale='C')`, `SELECT to_regcollation('public.extra_collation') IS NOT NULL`},
			{"operator", `CREATE OPERATOR public.=== (LEFTARG=int, RIGHTARG=int, FUNCTION=pg_catalog.int4pl)`, `SELECT to_regoperator('public.===(integer,integer)') IS NOT NULL`},
		} {
			t.Run(fmt.Sprintf("migrated_%t_%s", migrated, tc.name), func(t *testing.T) {
				db, u := freshDB(t)
				if migrated {
					if err := MigrateFresh(context.Background(), u); err != nil {
						t.Fatal(err)
					}
				}
				beforeFingerprint := fingerprintForTest(t, db)
				if _, err := db.Exec(tc.setup); err != nil {
					t.Fatal(err)
				}
				if fingerprintForTest(t, db) == beforeFingerprint {
					t.Fatal("custom object missing from fingerprint")
				}
				var before, after int
				if err := db.QueryRow(`SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='goose_db_version'`).Scan(&before); err != nil {
					t.Fatal(err)
				}
				if err := MigrateFresh(context.Background(), u); err == nil {
					t.Fatal("expected rejection of custom catalog object")
				}
				var exists bool
				if err := db.QueryRow(tc.exists).Scan(&exists); err != nil || !exists {
					t.Fatalf("custom object changed: %v", err)
				}
				if err := db.QueryRow(`SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='goose_db_version'`).Scan(&after); err != nil || before != after {
					t.Fatalf("metadata changed: %v", err)
				}
			})
		}
	}
}
func TestRejectModifiedSequenceOwnership(t *testing.T) {
	db, u := freshDB(t)
	if err := MigrateFresh(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	beforeFingerprint := fingerprintForTest(t, db)
	if _, err := db.Exec(`ALTER SEQUENCE public.chart_of_accounts_account_id_seq OWNED BY NONE`); err != nil {
		t.Fatal(err)
	}
	if fingerprintForTest(t, db) == beforeFingerprint {
		t.Fatal("sequence ownership missing from fingerprint")
	}
	if err := MigrateFresh(context.Background(), u); err == nil {
		t.Fatal("expected rejection of changed sequence ownership")
	}
	var owned bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_depend WHERE objid='public.chart_of_accounts_account_id_seq'::regclass AND deptype='a')`).Scan(&owned); err != nil || owned {
		t.Fatalf("sequence ownership changed: %v", err)
	}
}

func TestFingerprintCapturesIndexAndTriggerState(t *testing.T) {
	_, u := freshDB(t)
	if err := MigrateFresh(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	adminURL, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil {
		t.Fatal(err)
	}
	targetURL, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	adminURL.Path = targetURL.Path
	admin, err := sql.Open("pgx", adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	conn, err := admin.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), `SET search_path TO pg_catalog,public`); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		v, err := schemaFingerprint(context.Background(), conn)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := read()
	if _, err = conn.ExecContext(context.Background(), `UPDATE pg_index SET indisvalid=false WHERE indexrelid='public.chart_of_accounts_pkey'::regclass`); err != nil {
		t.Fatal(err)
	}
	if before == read() {
		t.Fatal("index validity missing from fingerprint")
	}
	if err := MigrateFresh(context.Background(), u); err == nil {
		t.Fatal("expected invalid index rejection")
	}
	if _, err = conn.ExecContext(context.Background(), `UPDATE pg_index SET indisvalid=true WHERE indexrelid='public.chart_of_accounts_pkey'::regclass; CREATE FUNCTION public.test_trigger() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'; CREATE TRIGGER test_trigger BEFORE INSERT ON public.chart_of_accounts FOR EACH ROW EXECUTE FUNCTION public.test_trigger()`); err != nil {
		t.Fatal(err)
	}
	before = read()
	if _, err = conn.ExecContext(context.Background(), `ALTER TABLE public.chart_of_accounts DISABLE TRIGGER test_trigger`); err != nil {
		t.Fatal(err)
	}
	if before == read() {
		t.Fatal("trigger enabled state missing from fingerprint")
	}
}

func TestRejectModifiedColumnCollation(t *testing.T) {
	db, u := freshDB(t)
	if err := MigrateFresh(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	before := fingerprintForTest(t, db)
	if _, err := db.Exec(`ALTER TABLE public.chart_of_accounts ALTER COLUMN name TYPE varchar(100) COLLATE "C"`); err != nil {
		t.Fatal(err)
	}
	if fingerprintForTest(t, db) == before {
		t.Fatal("column collation missing from fingerprint")
	}
	if err := MigrateFresh(context.Background(), u); err == nil {
		t.Fatal("expected altered collation rejection")
	}
}

func TestRejectRowSecurityDrift(t *testing.T) {
	for _, tc := range []struct{ name, setup, change string }{
		{"enabled", "", `ALTER TABLE public.chart_of_accounts ENABLE ROW LEVEL SECURITY`},
		{"forced", "", `ALTER TABLE public.chart_of_accounts FORCE ROW LEVEL SECURITY`},
		{"policy_created", "", `CREATE POLICY account_policy ON public.chart_of_accounts USING (account_id > 0)`},
		{"policy_definition", `CREATE POLICY account_policy ON public.chart_of_accounts FOR SELECT TO PUBLIC USING (account_id > 0)`, `ALTER POLICY account_policy ON public.chart_of_accounts USING (account_id > 1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, u := freshDB(t)
			if err := MigrateFresh(context.Background(), u); err != nil {
				t.Fatal(err)
			}
			if tc.setup != "" {
				if _, err := db.Exec(tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			before := fingerprintForTest(t, db)
			if _, err := db.Exec(tc.change); err != nil {
				t.Fatal(err)
			}
			if got := fingerprintForTest(t, db); got == before {
				t.Fatal("row security drift missing from fingerprint")
			}
			if err := MigrateFresh(context.Background(), u); err == nil {
				t.Fatal("row security drift accepted")
			}
		})
	}
}

func TestMigrateFreshRejectsExistingV1(t *testing.T) {
	db, u := freshDB(t)
	baseline, err := embedded.ReadFile("00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{"00001_baseline.sql": &fstest.MapFile{Data: baseline}}
	if err = kitdb.Migrate(context.Background(), u, fsys); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err = db.QueryRow(`SELECT string_agg(row_to_json(v)::text,',' ORDER BY id) FROM goose_db_version v`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = MigrateFresh(context.Background(), u); err == nil {
		t.Fatal("existing v1 must fail closed")
	}
	if err = db.QueryRow(`SELECT string_agg(row_to_json(v)::text,',' ORDER BY id) FROM goose_db_version v`).Scan(&after); err != nil || before != after {
		t.Fatalf("v1 history changed: %v", err)
	}
}
