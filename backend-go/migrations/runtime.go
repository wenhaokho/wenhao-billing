package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"

	kitdb "github.com/LOQATO/gokit/db"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed 00001_baseline.sql 00002_recurring_outbox.sql 00003_hosting_restore_outbox.sql 00004_hosting_restore_lease.sql 00005_recurring_batch.sql 00006_hosting_operation_lease.sql 00007_hosting_claim_state.sql
var embedded embed.FS

// Files contains the baseline SQL at its root for goose-compatible migrators.
var Files fs.FS = embedded

// baselineFingerprint is the canonical PostgreSQL 16 catalog signature of the
// embedded migration, including goose schema but excluding row data.
const baselineFingerprint = "75237885708b1d85dd38588334ef35ce"
const outboxFingerprint = "1c571cfa71e0828bfba60b19b68184df"

// MigrateFresh migrates an empty database, or rechecks an already migrated Go
// database. It deliberately does not adopt databases managed by Alembic.
func MigrateFresh(ctx context.Context, urlString string) error {
	parsed, err := url.Parse(urlString)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return fmt.Errorf("fresh migration requires a PostgreSQL URL")
	}
	q := parsed.Query()
	q.Set("search_path", "public")
	parsed.RawQuery = q.Encode()
	urlString = parsed.String()
	db, err := sql.Open("pgx", urlString)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// This application-specific session lock covers both inspection and the kit's
	// migration, whose goose provider uses a separate connection and its own lock.
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(710287203441)`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(710287203441)`)
	if _, err := conn.ExecContext(ctx, `SET search_path TO pg_catalog, public`); err != nil {
		return err
	}
	var version int
	if err := conn.QueryRowContext(ctx, `SHOW server_version_num`).Scan(&version); err != nil {
		return err
	}
	if version < 160000 || version >= 170000 {
		return fmt.Errorf("fresh migration requires PostgreSQL 16 for schema verification")
	}
	var alembic, goose bool
	err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='alembic_version' AND n.nspname NOT IN ('pg_catalog','information_schema')), to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&alembic, &goose)
	if err != nil {
		return err
	}
	if alembic {
		return fmt.Errorf("fresh migration refused: alembic metadata present")
	}
	if goose {
		var valid bool
		err = conn.QueryRowContext(ctx, `SELECT count(*)=8 AND count(*) FILTER (WHERE id=1 AND version_id=0 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=2 AND version_id=1 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=3 AND version_id=2 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=4 AND version_id=3 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=5 AND version_id=4 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=6 AND version_id=5 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=7 AND version_id=6 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=8 AND version_id=7 AND is_applied AND tstamp IS NOT NULL)=1 FROM public.goose_db_version`).Scan(&valid)
		if err != nil || !valid {
			return fmt.Errorf("fresh migration refused: inconsistent goose metadata")
		}
		fingerprint, err := schemaFingerprint(ctx, conn)
		if err != nil {
			return fmt.Errorf("fresh migration refused: inspect schema: %w", err)
		}
		if fingerprint != outboxFingerprint {
			return fmt.Errorf("fresh migration refused: inconsistent goose metadata or schema")
		}

		return nil
	} else {
		var nonempty bool
		err = conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_depend d JOIN pg_namespace n ON n.oid=d.refobjid WHERE d.refclassid='pg_namespace'::regclass AND n.nspname='public' AND d.deptype='n' AND d.objsubid=0) OR EXISTS (SELECT 1 FROM pg_namespace WHERE nspname NOT IN ('public','pg_catalog','information_schema') AND nspname NOT LIKE 'pg_toast%' AND nspname NOT LIKE 'pg_temp_%') OR EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND c.relkind IN ('r','p','v','m','S','f')) OR EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%') OR EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND t.typtype IN ('e','d'))`).Scan(&nonempty)
		if err != nil {
			return err
		}
		if nonempty {
			return fmt.Errorf("fresh migration refused: nonempty schema")
		}
	}
	return kitdb.Migrate(ctx, urlString, Files)
}
