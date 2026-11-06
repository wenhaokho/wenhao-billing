# Task 3 report

Implemented `migrations.Files` (embedded baseline) and `migrations.MigrateFresh(ctx, url)`. It acquires a PostgreSQL session advisory lock before examining the database, rejects Alembic metadata, nonempty unrecognized databases, inconsistent goose history and missing/unexpected baseline tables, and calls gokit v0.2.0's goose migrator while retaining the admission lock. No startup integration or adoption path was added. The baseline SQL was not changed. Tests use pgtestdb-created databases; the explicitly supplied isolated Alembic reference is inspected and passed to the rejection path only.

Commands run from `backend-go` (the URL is the supplied isolated reference; pgtestdb creates separate databases):

- `go list -m -versions github.com/LOQATO/gokit` → `v0.1.0 v0.2.0`.
- `go get github.com/LOQATO/gokit/db@v0.2.0` and `go mod tidy` → success.
- `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./migrations -run TestMigrateFreshRepeatedConcurrent -count=1` with an initial not-implemented stub → FAIL, `not implemented` (expected red).
- `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./... -count=1` after restoring all guards → `ok github.com/wenhaokho/wenhao-billing/backend/migrations 1.867s`.

Guard mutations were temporary changes to `migrations/runtime.go`, restored before the final suite. Each command used the same `PGTESTDB_URL` as above and `go test ./migrations -run '<test>' -count=1`:

- Bypassed Alembic check; `^TestRejectAlembic$` failed: `expected alembic rejection, got fresh migration refused: nonempty schema`.
- Bypassed nonempty check; `^TestRejectUnrelatedSchema$` failed: `expected nonempty rejection, got <nil>`.
- Bypassed goose metadata and schema checks; `^TestRejectInconsistentGoose$` failed: `expected metadata rejection, got <nil>`.
- Bypassed missing-schema check; `^TestRejectGooseWithMissingSchema$` failed: `expected schema rejection, got <nil>`.
- Replaced the admission lock and unlock SQL with `SELECT 1`; `^TestInspectionWaitsForMigrationLock$` failed: `expected nonempty rejection after lock, got <nil>`. The ordinary concurrent test alone passed with this mutation because gokit's separate migration lock still serialized the actual migration; the admission-lock test adds the needed check-before-act coverage.

Known limitations: Recognition of an already migrated database checks exact goose history and the baseline table names, but does not fingerprint every column, index, seed row, function, or non-public object. PostgreSQL advisory locks are cooperative: unrelated schema writers that do not take the admission lock are not serialized. Reference preservation assertions cover the Alembic version, goose-table existence, and chart-of-accounts seed rows; they do not snapshot every data table. The rejection test with a separate pgtestdb database also verifies a sentinel data row remains intact.

## Review round 1

Replaced the table-name-only recognition with a canonical PostgreSQL catalog fingerprint covering namespaces, relations and views, columns and defaults, constraints, indexes, sequences, functions, enum/domain/composite types, and user triggers, including the goose table schema. It checks the complete expected fingerprint before invoking goose; data values remain outside this schema check. The fingerprint is specific to PostgreSQL 16, so other major versions are refused before any migration. Changing the baseline or goose schema requires updating the fingerprint from a verified fresh migration. This is deliberately fail-closed and may also refuse a compatible database if PostgreSQL changes its catalog rendering within that major version. The check is not a universal fingerprint of every possible PostgreSQL object class (for example custom operators and collations are not included).

The connection URL now overrides `search_path` to `public` for gokit's connection; inspection fixes its rendering path independently. Only PostgreSQL URL-form connection strings are accepted. An empty extra schema is also rejected on the fresh path. Goose history validation now requires the expected row IDs and non-null timestamps as well as versions and applied flags.

Red run: `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./migrations -run 'TestRejectModifiedMigratedSchema|TestMigrateFreshSearchPath' -count=1` failed all six schema cases with `expected modified schema rejection`, and failed the search-path case with `permission denied for schema pg_catalog`.

Guard mutation commands used the same URL and `go test ./migrations -run '<test>' -count=1`; the implementation was restored after each:

- Bypassing only the goose history condition after creating a fully migrated database and changing version 1 to unapplied: `^TestRejectAlteredGooseHistoryOnBaseline$` failed `expected metadata rejection, got <nil>`.
- Bypassing only the fingerprint comparison: `^TestRejectModifiedMigratedSchema$` failed all six cases (`extra_schema`, `extra_relation`, `view`, `function`, `type`, `column`) with `expected modified schema rejection`.
- Removing only the search-path override: `^TestMigrateFreshSearchPath$` failed with `permission denied for schema pg_catalog`.

The explicit Alembic guard is intentionally redundant with the nonempty/fingerprint checks: the earlier isolated mutation fails on the specific Alembic error assertion, but its failure does not establish that bypassing this guard alone allows migration. It does establish the diagnostic distinction. The reference test also verifies rejection without changing the sampled rows or metadata.

After restoring all mutations: `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./... -count=1` → `ok github.com/wenhaokho/wenhao-billing/backend/migrations 6.298s`.

## Review round 2

Fresh-schema inspection now rejects objects with normal dependencies on the `public` namespace, including collations and operators. The schema fingerprint includes a general namespace dependency inventory, covering additional named PostgreSQL catalog object classes instead of only the previous enumeration. The fingerprint also includes sequence ownership, index validity/readiness/liveness, and trigger enabled state. The earlier statement that custom operators and collations were omitted is superseded. This remains a catalog-based check, not a claim to verify every database property or data value; extensions and database-level objects without namespace dependencies are not exhaustively classified. Advisory locks remain cooperative.

Real-Postgres tests exercise collations and operators on both empty and migrated databases (with a built-in operator function, so no extra user function masks the test), verify the object and goose-table presence are unchanged, and compare fingerprints before/after. They also change sequence ownership and index validity, check migration rejects the modified schema, and verify trigger enabled state changes the fingerprint. The index-state test uses the superuser credentials in `PGTESTDB_URL` only against the separate pgtestdb-generated test database, because PostgreSQL requires superuser privileges to alter `pg_index` directly.

Initial red command: `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./migrations -run 'TestRejectCustomCatalogObjects|TestRejectModifiedSequenceOwnership' -count=1` failed fresh and migrated collation cases (`expected rejection of custom catalog object`) and sequence case (`expected rejection of changed sequence ownership`).

The following mutations were made separately and restored after each; commands used the same URL and `go test ./migrations -run '<test>' -count=1`:

- Removing the fresh namespace-dependency check: `^TestRejectCustomCatalogObjects/migrated_false` failed both collation and operator cases, `expected rejection of custom catalog object`.
- Removing the fingerprint namespace inventory: `^TestRejectCustomCatalogObjects` failed all four cases, `custom object missing from fingerprint`.
- Removing sequence ownership from the fingerprint: `^TestRejectModifiedSequenceOwnership$` failed `sequence ownership missing from fingerprint`.
- Removing index state from the fingerprint: `^TestFingerprintCapturesIndexAndTriggerState$` failed `index validity missing from fingerprint`.
- Removing trigger enabled state alone: the same test failed `trigger enabled state missing from fingerprint`.

Final restored suite: `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./... -count=1` → `ok github.com/wenhaokho/wenhao-billing/backend/migrations 7.426s`.
