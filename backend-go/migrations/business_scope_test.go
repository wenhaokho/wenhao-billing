package migrations

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/businessscope"
)

//go:embed 00008_business_scope.sql
var scopeMigration []byte

func TestBusinessScopeMigration(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil || u.Hostname() == "" {
		t.Fatal("PGTESTDB_URL must identify the isolated reference database")
	}
	password, _ := u.User.Password()
	cfg := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: password, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	target := pgtestdb.Custom(t, cfg, goosemigrator.New(".", goosemigrator.WithFS(Files)))
	db, err := sql.Open("pgx", target.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fsys := fstest.MapFS{}
	for i := 1; i <= 7; i++ {
		entries, e := embedded.ReadDir(".")
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), fmt.Sprintf("%05d_", i)) {
				data, e := embedded.ReadFile(entry.Name())
				if e != nil {
					t.Fatal(e)
				}
				fsys[entry.Name()] = &fstest.MapFile{Data: data}
			}
		}
	}
	fsys["00008_business_scope.sql"] = &fstest.MapFile{Data: scopeMigration}
	scopeFixture(t, db)
	originalEdges := scopeEdges(t, db)
	snapshots := map[string]string{}
	for _, table := range businessscope.OwnedTables {
		var rows string
		if err := db.QueryRow(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text), '[]'::jsonb)::text FROM ` + table.Name + ` x`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		snapshots[table.Name] = rows
	}
	var before string
	if err := db.QueryRow(`SELECT jsonb_agg(to_jsonb(c) ORDER BY account_id)::text FROM chart_of_accounts c`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Inject a failure late in the same transaction and verify that no part of
	// the migration, including its generated identity, survives.
	fsys["00008_business_scope.sql"] = &fstest.MapFile{Data: []byte(strings.Replace(string(scopeMigration), "-- +goose Down", "SELECT 1/0;\n-- +goose Down", 1))}
	failing, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = failing.Up(context.Background()); err == nil {
		t.Fatal("injected failure unexpectedly succeeded")
	}
	var exists bool
	if err = db.QueryRow(`SELECT to_regclass('public.businesses') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("failed migration left identity table: %v", err)
	}
	var version int
	if err = db.QueryRow(`SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 7 {
		t.Fatalf("failed migration left version %d: %v", version, err)
	}
	for _, table := range businessscope.OwnedTables {
		var rows string
		if err = db.QueryRow(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text), '[]'::jsonb)::text FROM ` + table.Name + ` x`).Scan(&rows); err != nil || rows != snapshots[table.Name] {
			t.Fatalf("failed migration changed %s: %v", table.Name, err)
		}
	}
	// Simulate a legacy database with an unvalidated orphan. Migration must fail
	// validation and preserve the orphan rather than deleting or remapping it.
	if _, err = db.Exec(`ALTER TABLE invoices DROP CONSTRAINT invoices_customer_id_fkey;
 UPDATE invoices SET customer_id='00000000-0000-0000-0000-000000000099';
 ALTER TABLE invoices ADD CONSTRAINT invoices_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES customers(customer_id) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	fsys["00008_business_scope.sql"] = &fstest.MapFile{Data: scopeMigration}
	invalid, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = invalid.Up(context.Background()); err == nil {
		t.Fatal("orphan migration unexpectedly succeeded")
	}
	var orphan string
	if err = db.QueryRow(`SELECT customer_id::text FROM invoices LIMIT 1`).Scan(&orphan); err != nil || orphan != "00000000-0000-0000-0000-000000000099" {
		t.Fatalf("orphan changed: %s %v", orphan, err)
	}
	if err = db.QueryRow(`SELECT to_regclass('public.businesses') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("orphan failure left identity: %v", err)
	}
	if _, err = db.Exec(`UPDATE invoices SET customer_id='00000000-0000-0000-0000-000000000001'; ALTER TABLE invoices VALIDATE CONSTRAINT invoices_customer_id_fkey`); err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The sqlc overlay only models the dynamic ownership-column additions.
	// Compare it both to its generator and to the actual migrated catalog.
	overlay, err := os.ReadFile("../sqlc_business_scope.sql")
	if err != nil || string(overlay) != businessscope.SQLCOwnershipOverlay() {
		t.Fatalf("sqlc ownership overlay is stale; run go run ./cmd/sqlcscope: %v", err)
	}
	for _, table := range businessscope.OwnedTables {
		var typ string
		var notNull, hasDefault bool
		err = db.QueryRow(`SELECT format_type(a.atttypid, a.atttypmod), a.attnotnull, a.atthasdef
FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid
JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND c.relname=$1 AND a.attname='business_id' AND NOT a.attisdropped`, table.Name).Scan(&typ, &notNull, &hasDefault)
		if err != nil || typ != "uuid" || !notNull || hasDefault {
			t.Fatalf("%s ownership column differs from sqlc overlay: type=%q not-null=%v default=%v: %v", table.Name, typ, notNull, hasDefault, err)
		}
	}
	var after string
	if err := db.QueryRow(`SELECT jsonb_agg(to_jsonb(c) - 'business_id' ORDER BY account_id)::text FROM chart_of_accounts c`).Scan(&after); err != nil || before != after {
		t.Fatalf("seed accounts changed: %v", err)
	}
	var id string
	if err = db.QueryRow(`SELECT business_id::text FROM businesses`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, table := range businessscope.OwnedTables {
		var n int
		err = db.QueryRow(`SELECT count(*) FROM `+table.Name+` WHERE business_id IS DISTINCT FROM $1::uuid`, id).Scan(&n)
		if err != nil || n != 0 {
			t.Fatalf("%s: unowned=%d: %v", table.Name, n, err)
		}
	}
	for _, table := range businessscope.OwnedTables {
		var rows string
		if err := db.QueryRow(`SELECT coalesce(jsonb_agg(to_jsonb(x)-'business_id' ORDER BY (to_jsonb(x)-'business_id')::text), '[]'::jsonb)::text FROM ` + table.Name + ` x`).Scan(&rows); err != nil || rows != snapshots[table.Name] {
			t.Fatalf("%s changed: %v", table.Name, err)
		}
		var invalid int
		if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='business_id' AND (is_nullable <> 'NO' OR column_default IS NOT NULL)`, table.Name).Scan(&invalid); err != nil || invalid != 0 {
			t.Fatalf("%s ownership definition: %v", table.Name, err)
		}
	}
	if got := scopeEdges(t, db); got != originalEdges {
		t.Fatalf("foreign-key inventory changed: before %s after %s", originalEdges, got)
	}
	var bad int
	if err := db.QueryRow(`SELECT count(*) FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='public' AND c.contype='f' AND c.confrelid <> 'businesses'::regclass AND (array_length(c.conkey,1) <> 2 OR NOT c.convalidated)`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("unscoped or invalid foreign keys: %d %v", bad, err)
	}
	if _, err := p.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	var again string
	if err := db.QueryRow(`SELECT business_id::text FROM businesses`).Scan(&again); err != nil || again != id {
		t.Fatalf("identity changed: %s %v", again, err)
	}
	var second string
	if err := db.QueryRow(`INSERT INTO businesses(business_id) VALUES(gen_random_uuid()) RETURNING business_id::text`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	cloneScope(t, db, second)
	checkForeignKeyIsolation(t, db, id, second)
	var profileID int
	if err := db.QueryRow(`SELECT nextval('business_profile_id_seq')`).Scan(&profileID); err != nil || profileID != 2 {
		t.Fatalf("profile sequence: %d %v", profileID, err)
	}
	checkNaturalKeyConflicts(t, db, id)
	testScopedProfileQueries(t, db)
	testScopedProfileAdapter(t, target.URL())
	testScopedProfileHTTP(t, target.URL())
	var fingerprint string
	if _, err = db.Exec(`SET search_path TO pg_catalog, public`); err != nil {
		t.Fatal(err)
	}
	fingerprint, err = schemaFingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PostgreSQL 16 v8 fingerprint: %s", fingerprint)
}

func TestBusinessScopeFreshFingerprint(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil || u.Hostname() == "" {
		t.Fatal("PGTESTDB_URL required")
	}
	password, _ := u.User.Password()
	cfg := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: password, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	target := pgtestdb.Custom(t, cfg, goosemigrator.New(".", goosemigrator.WithFS(Files)))
	db, err := sql.Open("pgx", target.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var server int
	if err = db.QueryRow(`SHOW server_version_num`).Scan(&server); err != nil || server < 160000 || server >= 170000 {
		t.Fatalf("PostgreSQL 16 required: %d %v", server, err)
	}
	fsys := fstest.MapFS{}
	entries, err := embedded.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, e := embedded.ReadFile(entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		fsys[entry.Name()] = &fstest.MapFile{Data: data}
	}
	fsys["00008_business_scope.sql"] = &fstest.MapFile{Data: scopeMigration}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`SET search_path TO pg_catalog, public`); err != nil {
		t.Fatal(err)
	}
	got, err := schemaFingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "d9657f14b43a8c6fb1d7c3d8757d1fb9"
	if got != expected {
		t.Fatalf("fresh v8 fingerprint: got %s want %s", got, expected)
	}
	t.Logf("fresh PostgreSQL 16 v8 fingerprint: %s", got)
}

func scopeEdges(t *testing.T, db *sql.DB) string {
	t.Helper()
	var result string
	err := db.QueryRow(`SELECT string_agg(child.relname||'.'||ca.attname||'->'||parent.relname||'.'||pa.attname||':'||c.confdeltype::text,',' ORDER BY child.relname,ca.attname,parent.relname,pa.attname) FROM pg_constraint c JOIN pg_class child ON child.oid=c.conrelid JOIN pg_class parent ON parent.oid=c.confrelid JOIN pg_attribute ca ON ca.attrelid=c.conrelid AND ca.attnum=c.conkey[array_length(c.conkey,1)] JOIN pg_attribute pa ON pa.attrelid=c.confrelid AND pa.attnum=c.confkey[array_length(c.confkey,1)] JOIN pg_namespace n ON n.oid=child.relnamespace WHERE n.nspname='public' AND c.contype='f' AND parent.relname <> 'businesses'`).Scan(&result)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
