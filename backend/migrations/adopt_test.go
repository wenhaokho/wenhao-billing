package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// legacyDB builds a database shaped like the production one: the Alembic
// schema at the frozen head with its seed rows and alembic_version, and no
// goose history. The schema comes from the Up section of 00001_baseline.sql,
// which TestSchemaMatchesAlembic proves equal to Alembic's.
func legacyDB(t *testing.T) string {
	t.Helper()
	body, err := migrations.FS.ReadFile("00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, found := strings.Cut(string(body), "-- +goose Down")
	if !found {
		t.Fatal("00001_baseline.sql has no Down section")
	}
	up = strings.Replace(up, "-- +goose Up", "", 1)
	url := testkit.EmptyPostgresURL(t)
	exec(t, url, up)
	exec(t, url, `CREATE TABLE alembic_version (version_num varchar(32) PRIMARY KEY);
		INSERT INTO alembic_version (version_num) VALUES ('`+migrations.AlembicHead+`');`)
	return url
}

// exec runs sql (several statements allowed) on its own connection.
func exec(t *testing.T, url, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}

func openPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func scalar[T any](t *testing.T, pool *pgxpool.Pool, sql string) T {
	t.Helper()
	var v T
	if err := pool.QueryRow(context.Background(), sql).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func gooseTableExists(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	return scalar[bool](t, pool, `SELECT to_regclass('public.goose_db_version') IS NOT NULL`)
}

func TestAdoptThenMigrateAppliesOnlyNewMigrations(t *testing.T) {
	ctx := context.Background()
	url := legacyDB(t)
	pool := openPool(t, url)
	accounts := scalar[int64](t, pool, `SELECT count(*) FROM chart_of_accounts`)
	if accounts == 0 {
		t.Fatal("the baseline seeded no chart of accounts")
	}

	if err := migrations.Adopt(ctx, pool); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if err := migrations.CheckAdopted(ctx, pool); err != nil {
		t.Fatalf("CheckAdopted after Adopt: %v", err)
	}
	if err := db.Migrate(ctx, url, migrations.FS); err != nil {
		t.Fatalf("Migrate after Adopt: %v", err)
	}

	if got := scalar[int64](t, pool, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`); got != 3 {
		t.Errorf("goose version %d, want 3", got)
	}
	if got := scalar[int64](t, pool, `SELECT count(*) FROM goose_db_version WHERE version_id = 1`); got != 1 {
		t.Errorf("version 1 recorded %d times, want once (the baseline must not run)", got)
	}
	if !scalar[bool](t, pool, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'session_epoch')`) {
		t.Error("users.session_epoch is missing")
	}
	if !scalar[bool](t, pool, `SELECT to_regclass('public.login_attempts') IS NOT NULL`) {
		t.Error("login_attempts is missing")
	}
	if got := scalar[int64](t, pool, `SELECT count(*) FROM chart_of_accounts`); got != accounts {
		t.Errorf("%d chart of accounts rows after migrating, want %d (seed rows duplicated)", got, accounts)
	}
}

func TestAdoptRefusesWrongAlembicHead(t *testing.T) {
	url := legacyDB(t)
	exec(t, url, `UPDATE alembic_version SET version_num = '0027_previous_head'`)
	pool := openPool(t, url)
	err := migrations.Adopt(context.Background(), pool)
	if err == nil {
		t.Fatal("Adopt accepted Alembic head 0027_previous_head")
	}
	for _, v := range []string{"0027_previous_head", migrations.AlembicHead} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("error %q does not name %s", err, v)
		}
	}
	if gooseTableExists(t, pool) {
		t.Error("goose_db_version was written although Adopt refused")
	}
}

func TestAdoptRefusesSchemaDrift(t *testing.T) {
	url := legacyDB(t)
	exec(t, url, `ALTER TABLE customers DROP COLUMN website`)
	pool := openPool(t, url)
	err := migrations.Adopt(context.Background(), pool)
	if err == nil {
		t.Fatal("Adopt accepted a schema without customers.website")
	}
	if !strings.Contains(err.Error(), "customers.website") {
		t.Errorf("error %q does not name the drifted column", err)
	}
	if gooseTableExists(t, pool) {
		t.Error("goose_db_version was written although Adopt refused")
	}
}

func TestAdoptRefusesAlreadyAdopted(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, legacyDB(t))
	if err := migrations.Adopt(ctx, pool); err != nil {
		t.Fatalf("first Adopt: %v", err)
	}
	if err := migrations.Adopt(ctx, pool); err == nil {
		t.Fatal("second Adopt succeeded")
	}
	if got := scalar[int64](t, pool, `SELECT count(*) FROM goose_db_version`); got != 2 {
		t.Errorf("%d goose_db_version rows, want 2 (versions 0 and 1)", got)
	}
}

func TestCheckAdoptedRefusesAlembicDatabase(t *testing.T) {
	err := migrations.CheckAdopted(context.Background(), openPool(t, legacyDB(t)))
	if !errors.Is(err, migrations.ErrNotAdopted) {
		t.Fatalf("CheckAdopted on an Alembic database: %v, want ErrNotAdopted", err)
	}
}

func TestCheckAdoptedAcceptsFreshDatabase(t *testing.T) {
	if err := migrations.CheckAdopted(context.Background(), openPool(t, testkit.EmptyPostgresURL(t))); err != nil {
		t.Fatalf("CheckAdopted on an empty database: %v", err)
	}
}

func TestCheckAdoptedAcceptsGooseDatabase(t *testing.T) {
	if err := migrations.CheckAdopted(context.Background(), openPool(t, testkit.PostgresURL(t, migrations.FS))); err != nil {
		t.Fatalf("CheckAdopted on a goose-migrated database: %v", err)
	}
}
