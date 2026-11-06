package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// Canonical signatures were generated from a fresh Alembic 0028 database on
// PostgreSQL 16, before and after creating goose metadata respectively.
const alembicFingerprint = "8a3544c12f60cd3f3d9e8fed7c73d617"
const adoptedFingerprint = "92a74cb0a43c4d08ff6a5b168be9505e"

// AdoptAlembic is unavailable until database-wide DDL quiescence can be
// established and verified throughout inspection and metadata commit.
// A cooperative advisory lock and serializable snapshot cannot provide it.
func AdoptAlembic(ctx context.Context, raw string) error {
	return fmt.Errorf("adoption unavailable: cannot establish database-wide DDL fencing")
}

func verifyAdoption(ctx context.Context, tx *sql.Tx, requireGoose bool) error {
	var version int
	if err := tx.QueryRowContext(ctx, `SHOW server_version_num`).Scan(&version); err != nil {
		return err
	}
	if !supportedAdoptionVersion(version) {
		return fmt.Errorf("adoption requires PostgreSQL 16")
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT count(*)=1 AND count(*) FILTER (WHERE version_num='0028_business_npwp')=1 FROM public.alembic_version`).Scan(&valid); err != nil || !valid {
		return fmt.Errorf("adoption refused: invalid Alembic revision")
	}
	var goose bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&goose); err != nil {
		return err
	}
	if requireGoose && !goose {
		return fmt.Errorf("adoption refused: missing goose metadata")
	}
	expected := alembicFingerprint
	if goose {
		if err := tx.QueryRowContext(ctx, `SELECT count(*)=2 AND count(*) FILTER (WHERE id=1 AND version_id=0 AND is_applied AND tstamp IS NOT NULL)=1 AND count(*) FILTER (WHERE id=2 AND version_id=1 AND is_applied AND tstamp IS NOT NULL)=1 FROM public.goose_db_version`).Scan(&valid); err != nil || !valid {
			return fmt.Errorf("adoption refused: inconsistent goose metadata")
		}
		expected = adoptedFingerprint
	}
	hash, err := schemaFingerprint(ctx, tx)
	if err != nil {
		return fmt.Errorf("adoption refused: inspect schema: %w", err)
	}
	if hash != expected {
		return fmt.Errorf("adoption refused: inconsistent schema")
	}
	return nil
}

func supportedAdoptionVersion(version int) bool { return version >= 160000 && version < 170000 }
