package businessscope

import (
	"context"
	"database/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestOwnedTablesMatchMigrationCatalog(t *testing.T) {
	raw := os.Getenv("PGTESTDB_URL")
	if raw == "" {
		t.Fatal("PGTESTDB_URL must identify an isolated PostgreSQL test database")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil || u.Hostname() == "" || u.Path == "" {
		t.Fatal("invalid PGTESTDB_URL")
	}
	pw, _ := u.User.Password()
	info := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	db, err := sql.Open("pgx", info.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v8 bool
	if err := db.QueryRowContext(context.Background(), `SELECT EXISTS (SELECT 1 FROM goose_db_version WHERE version_id = 8 AND is_applied)`).Scan(&v8); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(context.Background(), `SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]bool{}
	for _, table := range OwnedTables {
		if want[table.Name] {
			t.Fatalf("duplicate %s", table.Name)
		}
		want[table.Name] = true
	}
	if len(want) != 26 {
		t.Fatalf("expected 26 tables, got %d", len(want))
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "goose_db_version" || (name == "businesses" && v8) {
			continue
		}
		if !want[name] {
			t.Errorf("unclassified catalog table %s", name)
		}
		delete(want, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for name := range want {
		t.Errorf("manifest table absent from catalog: %s", name)
	}
}
