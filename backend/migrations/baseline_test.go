package migrations_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// baselineURL gives a database with only 00001_baseline.sql applied, so later
// Go-only migrations never change the comparison with the Python reference.
func baselineURL(t *testing.T) string {
	t.Helper()
	body, err := migrations.FS.ReadFile("00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	return testkit.PostgresURL(t, fstest.MapFS{"00001_baseline.sql": {Data: body}})
}

// run executes a one-column query against the baseline database and returns
// its lines.
func run(t *testing.T, queryFile string) []string {
	t.Helper()
	query, err := migrations.Testdata.ReadFile("testdata/" + queryFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, baselineURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	rows, err := conn.Query(ctx, string(query))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func want(t *testing.T, file string) []string {
	t.Helper()
	b, err := migrations.Testdata.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty: run port/make-baseline.sh", file)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// diff names a few lines present in from and absent from other.
func diff(t *testing.T, side string, from, other []string) {
	t.Helper()
	in := map[string]bool{}
	for _, l := range other {
		in[l] = true
	}
	shown := 0
	for _, l := range from {
		if !in[l] && shown < 5 {
			t.Errorf("only in %s: %s", side, l)
			shown++
		}
	}
}

// TestSchemaMatchesAlembic: a goose-migrated database (only 00001 applied)
// described by testdata/schema-snapshot.sql equals testdata/alembic-0028.schema.txt
// line for line.
func TestSchemaMatchesAlembic(t *testing.T) {
	t.Parallel()
	got := run(t, "schema-snapshot.sql")
	exp := want(t, "alembic-0028.schema.txt")
	if strings.Join(got, "\n") == strings.Join(exp, "\n") {
		return
	}
	diff(t, "the goose database", got, exp)
	diff(t, "Alembic's description", exp, got)
	if !t.Failed() {
		t.Error("schemas differ only in line order or count")
	}
}

// TestSeedRowsMatchAlembic: the rows a fresh `alembic upgrade head` leaves
// (chart of accounts, FX gain/loss accounts, the business_profile singleton)
// are the rows the baseline leaves.
func TestSeedRowsMatchAlembic(t *testing.T) {
	t.Parallel()
	got := run(t, "rows-snapshot.sql")
	exp := want(t, "alembic-0028.rows.txt")
	if len(got) != len(exp) {
		t.Errorf("%d seed rows, want %d", len(got), len(exp))
	}
	diff(t, "the goose database", got, exp)
	diff(t, "Alembic's database", exp, got)
}
