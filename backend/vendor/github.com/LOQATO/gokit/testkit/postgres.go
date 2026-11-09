package testkit

import (
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" database/sql driver pgtestdb connects with
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
)

// PostgresURL gives the test a database of its own, with migrations applied,
// and returns its URL; open it with db.Open. pgtestdb clones a template
// database migrated once per set of migrations, so each test costs tens of
// milliseconds. A passing test's database is dropped; a failing test's is
// kept and its URL logged.
//
// The server comes from PGTESTDB_URL (an admin connection to a server used
// only for tests). Without it the test is skipped, saying so.
func PostgresURL(t testing.TB, migrations fs.FS) string {
	t.Helper()
	return pgtestdb.Custom(t, config(t), goosemigrator.New(".", goosemigrator.WithFS(migrations))).URL()
}

// EmptyPostgresURL is PostgresURL with no migrations.
func EmptyPostgresURL(t testing.TB) string {
	t.Helper()
	return pgtestdb.Custom(t, config(t), pgtestdb.NoopMigrator{}).URL()
}

func config(t testing.TB) pgtestdb.Config {
	t.Helper()
	raw := os.Getenv("PGTESTDB_URL")
	if raw == "" {
		t.Skip("PGTESTDB_URL is not set: no Postgres server for tests")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("PGTESTDB_URL: %v", err)
	}
	password, _ := u.User.Password()
	return pgtestdb.Config{
		DriverName: "pgx",
		Host:       u.Hostname(),
		Port:       u.Port(),
		User:       u.User.Username(),
		Password:   password,
		Database:   strings.TrimPrefix(u.Path, "/"),
		Options:    u.RawQuery,
	}
}
