package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// prepLogger is a logger writing to a buffer, for asserting serve's startup
// lines.
func prepLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// execSQL runs sql (several statements allowed) on its own connection.
func execSQL(t *testing.T, url, sql string) {
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

// queryOne reads one value on its own connection.
func queryOne[T any](t *testing.T, url, sql string) T {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var v T
	if err := conn.QueryRow(ctx, sql).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// alembicDB builds a database shaped like production before the switch: the
// Alembic schema at the frozen head (the Up section of 00001_baseline.sql,
// which TestSchemaMatchesAlembic proves equal to Alembic's), alembic_version,
// and no goose history.
func alembicDB(t *testing.T) string {
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
	execSQL(t, url, up)
	execSQL(t, url, `CREATE TABLE alembic_version (version_num varchar(32) PRIMARY KEY);
		INSERT INTO alembic_version (version_num) VALUES ('`+migrations.AlembicHead+`');`)
	return url
}

func gooseExists(t *testing.T, url string) bool {
	t.Helper()
	return queryOne[bool](t, url, `SELECT to_regclass('public.goose_db_version') IS NOT NULL`)
}

// The production database at the switch: serve's startup adopts it (the
// baseline is recorded, never run) and then applies the later migrations.
func TestServeAdoptsAlembicDatabaseThenMigrates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := alembicDB(t)
	accounts := queryOne[int64](t, url, `SELECT count(*) FROM chart_of_accounts`)
	logger, logs := prepLogger()

	if err := prepareDatabase(ctx, url, logger); err != nil {
		t.Fatalf("prepareDatabase: %v", err)
	}
	if got, want := queryOne[int64](t, url, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`), lastMigrationVersion(t); got != want {
		t.Errorf("goose version %d, want %d", got, want)
	}
	if got := queryOne[int64](t, url, `SELECT count(*) FROM goose_db_version WHERE version_id IN (0, 1)`); got != 2 {
		t.Errorf("%d rows for versions 0 and 1, want 2 (recorded once by the adopt)", got)
	}
	if !queryOne[bool](t, url, `SELECT to_regclass('public.scheduled_job_runs') IS NOT NULL`) {
		t.Error("scheduled_job_runs is missing: the later migrations did not run")
	}
	// The baseline did not run again over the data: 00008 adds only 6000.
	if got := queryOne[int64](t, url, `SELECT count(*) FROM chart_of_accounts`); got != accounts+1 {
		t.Errorf("%d chart of accounts rows, want %d", got, accounts+1)
	}
	out := logs.String()
	if !strings.Contains(out, "adopt: the Alembic database is now owned by goose") {
		t.Errorf("no adopt line in the startup log:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("from_version=1 to_version=%d", lastMigrationVersion(t))) {
		t.Errorf("no migrations line from 1 to the last version in the startup log:\n%s", out)
	}
}

// A drifted Alembic database: serve refuses to start, names the drift, and
// writes nothing.
func TestServeRefusesDriftedAlembicDatabase(t *testing.T) {
	t.Parallel()
	url := alembicDB(t)
	execSQL(t, url, `ALTER TABLE customers DROP COLUMN website`)
	logger, logs := prepLogger()

	err := prepareDatabase(context.Background(), url, logger)
	if err == nil {
		t.Fatal("prepareDatabase accepted a schema without customers.website")
	}
	if !strings.Contains(err.Error(), "auto-adopt refused") || !strings.Contains(err.Error(), "customers.website") {
		t.Errorf("error %q does not say the adopt was refused and name the drifted column", err)
	}
	if gooseExists(t, url) {
		t.Error("goose_db_version was written although the adopt was refused")
	}
	if strings.Contains(logs.String(), "now owned by goose") || strings.Contains(logs.String(), "migrations:") {
		t.Errorf("the startup log claims an adopt or a migration:\n%s", logs.String())
	}
}

// An Alembic database at another head is refused the same way.
func TestServeRefusesWrongAlembicHead(t *testing.T) {
	t.Parallel()
	url := alembicDB(t)
	execSQL(t, url, `UPDATE alembic_version SET version_num = '0027_older'`)
	logger, _ := prepLogger()
	err := prepareDatabase(context.Background(), url, logger)
	if err == nil || !strings.Contains(err.Error(), "0027_older") {
		t.Fatalf("prepareDatabase: %v, want the wrong-head refusal", err)
	}
	if gooseExists(t, url) {
		t.Error("goose_db_version was written although the adopt was refused")
	}
}

// Already goose-owned (the second deploy, which still has alembic_version):
// no adopt, migrations as before.
func TestServeDoesNotAdoptGooseDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := alembicDB(t)
	logger, _ := prepLogger()
	if err := prepareDatabase(ctx, url, logger); err != nil {
		t.Fatalf("first prepareDatabase: %v", err)
	}
	rows := queryOne[int64](t, url, `SELECT count(*) FROM goose_db_version`)

	logger, logs := prepLogger()
	if err := prepareDatabase(ctx, url, logger); err != nil {
		t.Fatalf("second prepareDatabase: %v", err)
	}
	if got := queryOne[int64](t, url, `SELECT count(*) FROM goose_db_version`); got != rows {
		t.Errorf("goose_db_version has %d rows after the second start, want %d", got, rows)
	}
	if strings.Contains(logs.String(), "adopt:") {
		t.Errorf("the second start adopted again:\n%s", logs.String())
	}
}

// Two replicas starting together on the Alembic database: one adopts, the
// other waits on the lock, finds the goose table and only migrates.
func TestServeAdoptsOnceUnderConcurrentStarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := alembicDB(t)
	const replicas = 3
	var wg sync.WaitGroup
	errs := make([]error, replicas)
	logs := make([]*bytes.Buffer, replicas)
	for i := range replicas {
		logger, buf := prepLogger()
		logs[i] = buf
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = prepareDatabase(ctx, url, logger)
		}()
	}
	wg.Wait()
	adopts := 0
	for i := range replicas {
		if errs[i] != nil {
			t.Errorf("replica %d: %v", i, errs[i])
		}
		if strings.Contains(logs[i].String(), "now owned by goose") {
			adopts++
		}
	}
	if adopts != 1 {
		t.Errorf("%d replicas adopted, want exactly 1", adopts)
	}
	if got := queryOne[int64](t, url, `SELECT count(*) FROM goose_db_version WHERE version_id = 1`); got != 1 {
		t.Errorf("version 1 recorded %d times, want once", got)
	}
	if got, want := queryOne[int64](t, url, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`), lastMigrationVersion(t); got != want {
		t.Errorf("goose version %d, want %d", got, want)
	}
}

// A goose table without the baseline next to Alembic's history is not
// adopted automatically (only a missing goose table is): serve still stops
// before goose would run the baseline over the Alembic schema.
func TestServeRefusesGooseTableWithoutBaseline(t *testing.T) {
	t.Parallel()
	url := alembicDB(t)
	execSQL(t, url, `CREATE TABLE goose_db_version (
		id integer PRIMARY KEY GENERATED BY DEFAULT AS IDENTITY,
		version_id bigint NOT NULL,
		is_applied boolean NOT NULL,
		tstamp timestamp NOT NULL DEFAULT now());`)
	logger, _ := prepLogger()
	if err := prepareDatabase(context.Background(), url, logger); !errors.Is(err, migrations.ErrNotAdopted) {
		t.Fatalf("prepareDatabase: %v, want ErrNotAdopted", err)
	}
	if got := queryOne[int64](t, url, `SELECT count(*) FROM goose_db_version`); got != 0 {
		t.Errorf("goose_db_version has %d rows, want 0: migrations ran on an unadopted database", got)
	}
}

// A fresh database is migrated by serve's startup as before.
func TestServeMigratesFreshDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := testkit.EmptyPostgresURL(t)
	logger, logs := prepLogger()
	if err := prepareDatabase(ctx, url, logger); err != nil {
		t.Fatalf("prepareDatabase: %v", err)
	}
	if version := queryOne[int64](t, url, `SELECT max(version_id) FROM goose_db_version`); version != lastMigrationVersion(t) {
		t.Errorf("goose version %d, want %d", version, lastMigrationVersion(t))
	}
	// The scheduler's table is there: the migration ran.
	if !queryOne[bool](t, url, `SELECT to_regclass('public.scheduled_job_runs') IS NOT NULL`) {
		t.Error("scheduled_job_runs is missing after the migration")
	}
	if strings.Contains(logs.String(), "adopt:") {
		t.Errorf("a fresh database was adopted:\n%s", logs.String())
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
