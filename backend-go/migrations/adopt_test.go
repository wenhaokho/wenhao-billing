package migrations

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func freshAlembic(t *testing.T) (*sql.DB, string) {
	t.Helper()
	bin, err := exec.LookPath("alembic")
	if err != nil {
		t.Fatal("alembic executable required on PATH for disposable Alembic-head database")
	}
	db, u := freshDB(t)
	cmd := exec.Command(bin, "upgrade", "head")
	cmd.Dir = "../../backend"
	cmd.Env = append(os.Environ(), "DATABASE_URL="+u)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("alembic upgrade: %v\n%s", err, output)
	}
	return db, u
}

func TestAdoptionUnavailableWithoutFence(t *testing.T) {
	db, u := freshAlembic(t)
	if _, err := db.Exec(`INSERT INTO chart_of_accounts(code,name,type) VALUES ('X999','preserved','ASSET'); SELECT setval('chart_of_accounts_account_id_seq',10000)`); err != nil {
		t.Fatal(err)
	}
	before := fingerprintForTest(t, db)
	if err := AdoptAlembic(context.Background(), u); err == nil || !strings.Contains(err.Error(), "fencing") {
		t.Fatalf("expected fencing rejection: %v", err)
	}
	if before != fingerprintForTest(t, db) {
		t.Fatal("schema changed")
	}
	var n int
	var seq int64
	var goose bool
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM chart_of_accounts WHERE code='X999'),(SELECT last_value FROM chart_of_accounts_account_id_seq),to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&n, &seq, &goose); err != nil || n != 1 || seq != 10000 || goose {
		t.Fatalf("data changed: %d %d %v %v", n, seq, goose, err)
	}
}

// Demonstrates why the production API must fail closed: catalog inspection in
// serializable mode does not prevent a separate session from committing DDL.
func TestIndependentDDLRacesSerializableCatalogRead(t *testing.T) {
	db, _ := freshAlembic(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL search_path TO pg_catalog,public`); err != nil {
		t.Fatal(err)
	}
	before, err := schemaFingerprint(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE public.independent_ddl_race (id integer)`); err != nil {
		t.Fatal(err)
	}
	stale, err := schemaFingerprint(ctx, tx)
	if err != nil || stale != before {
		t.Fatalf("expected stale catalog snapshot: %v", err)
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE public.goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL, tstamp timestamp DEFAULT now()); INSERT INTO public.goose_db_version(version_id,is_applied) VALUES (0,true),(1,true)`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("unsafe transaction unexpectedly failed: %v", err)
	}
	if got := fingerprintForTest(t, db); got == adoptedFingerprint {
		t.Fatal("DDL drift not observed after commit")
	}
}

func TestFreshAlembicFingerprintMatchesReference(t *testing.T) {
	db, _ := freshAlembic(t)
	reference, err := sql.Open("pgx", os.Getenv("PGTESTDB_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Close()
	if got, want := fingerprintForTest(t, db), fingerprintForTest(t, reference); got != want || got != alembicFingerprint {
		t.Fatalf("fresh Alembic fingerprint %s, reference %s, expected %s", got, want, alembicFingerprint)
	}
}

func TestMigrateFreshRejectsAlembicEvenWithCanonicalGooseMetadata(t *testing.T) {
	db, u := freshAlembic(t)
	if _, err := db.Exec(`INSERT INTO public.chart_of_accounts(code,name,type) VALUES ('X999','preserved','ASSET'); SELECT setval('public.chart_of_accounts_account_id_seq',10000); CREATE TABLE public.goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL, tstamp timestamp DEFAULT now()); INSERT INTO public.goose_db_version(version_id,is_applied) VALUES (0,true),(1,true)`); err != nil {
		t.Fatal(err)
	}
	before := fingerprintForTest(t, db)
	if before != adoptedFingerprint {
		t.Fatalf("test setup is not canonical: %s", before)
	}
	var beforeHistory string
	if err := db.QueryRow(`SELECT string_agg(row_to_json(v)::text,',' ORDER BY id) FROM public.goose_db_version v`).Scan(&beforeHistory); err != nil {
		t.Fatal(err)
	}
	if err := MigrateFresh(context.Background(), u); err == nil || !strings.Contains(err.Error(), "alembic") {
		t.Fatalf("expected Alembic rejection, got %v", err)
	}
	var afterHistory string
	var n int
	var seq int64
	var revision string
	if err := db.QueryRow(`SELECT (SELECT string_agg(row_to_json(v)::text,',' ORDER BY id) FROM public.goose_db_version v),(SELECT count(*) FROM public.chart_of_accounts WHERE code='X999' AND name='preserved'),(SELECT last_value FROM public.chart_of_accounts_account_id_seq),(SELECT version_num FROM public.alembic_version)`).Scan(&afterHistory, &n, &seq, &revision); err != nil {
		t.Fatal(err)
	}
	if beforeHistory != afterHistory || n != 1 || seq != 10000 || revision != "0028_business_npwp" || before != fingerprintForTest(t, db) {
		t.Fatal("database changed on rejection")
	}
}
