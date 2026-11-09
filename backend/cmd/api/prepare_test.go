package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// serve's startup on a database that still has only Alembic's history must
// stop before goose touches it: running the baseline there would fail half way
// or, worse, write next to production data.
func TestServeRefusesUnadoptedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := testkit.EmptyPostgresURL(t)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE alembic_version (version_num varchar(32) PRIMARY KEY);
		INSERT INTO alembic_version (version_num) VALUES ('`+migrations.AlembicHead+`');`); err != nil {
		t.Fatal(err)
	}

	if err := prepareDatabase(ctx, url); !errors.Is(err, migrations.ErrNotAdopted) {
		t.Fatalf("prepareDatabase: %v, want ErrNotAdopted", err)
	}
	var goose bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&goose); err != nil {
		t.Fatal(err)
	}
	if goose {
		t.Error("goose_db_version was created: migrations ran on an unadopted database")
	}
}

// A fresh database is migrated by serve's startup as before.
func TestServeMigratesFreshDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := testkit.EmptyPostgresURL(t)
	if err := prepareDatabase(ctx, url); err != nil {
		t.Fatalf("prepareDatabase: %v", err)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var version int64
	if err := conn.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != lastMigrationVersion(t) {
		t.Errorf("goose version %d, want %d", version, lastMigrationVersion(t))
	}
	// The scheduler's table is there: the migration ran.
	var hasTable bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.scheduled_job_runs') IS NOT NULL`).Scan(&hasTable); err != nil {
		t.Fatal(err)
	}
	if !hasTable {
		t.Error("scheduled_job_runs is missing after the migration")
	}
}

// lastMigrationVersion is the number of the last migration, so a new
// migration does not break the version check.
func lastMigrationVersion(t *testing.T) int64 {
	t.Helper()
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	for _, e := range entries {
		var n int64
		if _, err := fmt.Sscanf(e.Name(), "%d_", &n); err != nil {
			continue
		}
		if n > last {
			last = n
		}
	}
	return last
}
