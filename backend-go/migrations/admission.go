package migrations

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
)

// VerifyCurrent admits only the exact current Go schema without modifying it.
// Schema changes after this check must be controlled by deployment procedures.
func VerifyCurrent(ctx context.Context, raw string) error {
	reject := errors.New("database schema is not the supported current Go schema")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return reject
	}
	q := u.Query()
	q.Set("search_path", "public")
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		return reject
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return reject
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL search_path TO pg_catalog, public`); err != nil {
		return reject
	}
	var version int
	if err = tx.QueryRowContext(ctx, `SHOW server_version_num`).Scan(&version); err != nil || version < 160000 || version >= 170000 {
		return reject
	}
	var alembic, goose bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='alembic_version' AND n.nspname NOT IN ('pg_catalog','information_schema')), to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&alembic, &goose)
	if err != nil || alembic || !goose {
		return reject
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT count(*)=8 AND count(*) FILTER (WHERE id=1 AND version_id=0 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=2 AND version_id=1 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=3 AND version_id=2 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=4 AND version_id=3 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=5 AND version_id=4 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=6 AND version_id=5 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=7 AND version_id=6 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=8 AND version_id=7 AND is_applied AND tstamp IS NOT NULL)=1 FROM public.goose_db_version`).Scan(&valid)
	if err != nil || !valid {
		return reject
	}
	fingerprint, err := schemaFingerprint(ctx, tx)
	if err != nil || fingerprint != outboxFingerprint {
		return reject
	}
	if err = tx.Commit(); err != nil {
		return reject
	}
	return nil
}
