package migrations

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
)

func TestBaselineMatchesAlembicSchema(t *testing.T) {
	raw := os.Getenv("PGTESTDB_URL")
	if raw == "" {
		t.Fatal("PGTESTDB_URL must identify the isolated Alembic test database")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil || u.Hostname() == "" || u.Path == "" {
		t.Fatal("PGTESTDB_URL must be a valid PostgreSQL URL with user, host and database")
	}
	password, _ := u.User.Password()
	config := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: password, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	sourceDB, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	ctx := context.Background()
	var rev string
	if err := sourceDB.QueryRowContext(ctx, "SELECT version_num FROM alembic_version").Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if rev != "0028_business_npwp" {
		t.Fatalf("reference database revision = %q", rev)
	}
	baseline, err := embedded.ReadFile("00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	baselineFS := fstest.MapFS{"00001_baseline.sql": &fstest.MapFile{Data: baseline}}
	target := pgtestdb.Custom(t, config, goosemigrator.New(".", goosemigrator.WithFS(baselineFS)))
	targetDB, err := sql.Open("pgx", target.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer targetDB.Close()
	// Compare Postgres' canonical definitions, not ORM models, so defaults,
	// column types, constraints, indexes and enum values are part of the contract.
	query := `SELECT kind, name, definition FROM (
 SELECT 'column' AS kind, c.relname||'.'||a.attname AS name, pg_catalog.format_type(a.atttypid,a.atttypmod)||'|'||a.attnotnull||'|'||coalesce(pg_get_expr(d.adbin,d.adrelid),'')||'|'||a.attidentity::text||'|'||coalesce(quote_ident(cn.nspname)||'.'||quote_ident(coll.collname),'') AS definition
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum LEFT JOIN pg_collation coll ON coll.oid=a.attcollation LEFT JOIN pg_namespace cn ON cn.oid=coll.collnamespace
 WHERE n.nspname='public' AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped AND c.relname NOT IN ('alembic_version','goose_db_version')
 UNION ALL SELECT 'constraint', c.relname||'.'||co.conname, pg_get_constraintdef(co.oid)||'|'||co.convalidated FROM pg_constraint co JOIN pg_class c ON c.oid=co.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname NOT IN ('alembic_version','goose_db_version')
 UNION ALL SELECT 'index', p.tablename||'.'||p.indexname, p.indexdef||'|'||i.indisvalid||'|'||i.indisready FROM pg_indexes p JOIN pg_namespace n ON n.nspname=p.schemaname JOIN pg_class c ON c.relnamespace=n.oid AND c.relname=p.indexname JOIN pg_index i ON i.indexrelid=c.oid WHERE p.schemaname='public' AND p.tablename NOT IN ('alembic_version','goose_db_version')
 UNION ALL SELECT 'sequence', c.relname, format('%s|%s|%s|%s|%s|%s|%s', format_type(s.seqtypid, NULL), s.seqstart, s.seqincrement, s.seqmin, s.seqmax, s.seqcache, s.seqcycle) FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname <> 'goose_db_version_id_seq'
 UNION ALL SELECT 'sequence ownership', seq.relname, tbl.relname||'.'||att.attname FROM pg_class seq JOIN pg_namespace n ON n.oid=seq.relnamespace JOIN pg_depend dep ON dep.objid=seq.oid AND dep.deptype IN ('a','i') JOIN pg_class tbl ON tbl.oid=dep.refobjid JOIN pg_attribute att ON att.attrelid=tbl.oid AND att.attnum=dep.refobjsubid WHERE n.nspname='public' AND seq.relkind='S' AND seq.relname <> 'goose_db_version_id_seq'
 UNION ALL SELECT 'extension', extname, extversion FROM pg_extension
 UNION ALL SELECT 'function', p.proname||'('||pg_get_function_identity_arguments(p.oid)||')', pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.prokind='f'
 UNION ALL SELECT 'enum', t.typname||'.'||e.enumsortorder::text, e.enumlabel FROM pg_enum e JOIN pg_type t ON t.oid=e.enumtypid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='public'
 ) items ORDER BY kind,name`
	read := func(db *sql.DB) []string {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var kind, name, def string
			if err := rows.Scan(&kind, &name, &def); err != nil {
				t.Fatal(err)
			}
			// PostgreSQL reparses this equivalent array cast differently on restore.
			if kind == "constraint" && name == "items.ck_items_type" {
				def = strings.ReplaceAll(def, "ARRAY[('SERVICE'::character varying)::text, ('USAGE'::character varying)::text, ('FIXED_FEE'::character varying)::text]", "(ARRAY['SERVICE'::character varying, 'USAGE'::character varying, 'FIXED_FEE'::character varying])::text[]")
			}
			out = append(out, kind+" "+name+" "+def)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	a, b := read(sourceDB), read(targetDB)
	if len(a) == 0 {
		t.Fatal("reference schema is empty")
	}
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		source := make(map[string]bool, len(a))
		target := make(map[string]bool, len(b))
		for _, item := range a {
			source[item] = true
		}
		for _, item := range b {
			target[item] = true
		}
		for _, item := range a {
			if !target[item] {
				t.Errorf("only Alembic: %s", item)
			}
		}
		for _, item := range b {
			if !source[item] {
				t.Errorf("only goose: %s", item)
			}
		}
		t.Fatal("schema differs")
	}
}
