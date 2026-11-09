package main

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// serve's startup on a database that still has only Alembic's history must
// stop before goose touches it: running the baseline there would fail half way
// or, worse, write next to production data.
func TestServeRefusesUnadoptedDatabase(t *testing.T) {
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
	if version != 3 {
		t.Errorf("goose version %d, want 3", version)
	}
}
