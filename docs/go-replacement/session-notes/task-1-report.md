# Task 1 report: fresh database baseline

Status: complete for fresh databases. No adoption or production changes were made.

- Added `backend-go/migrations/00001_baseline.sql`, derived from the supplied PostgreSQL 16 schema-only dump, omitting the Alembic version table, psql commands, and dump session settings. It has no destructive down migration.
- Corrected the existing pgtestdb/goose integration test and Go module dependencies. The test requires `PGTESTDB_URL`, checks the reference revision before creating the test database, applies the goose baseline, and compares PostgreSQL catalog definitions for columns and defaults, constraints, indexes, enums, sequence attributes and ownership, extensions, and public functions. It reports individual differences.
- The test normalizes one specific equivalent rendering of the `items.ck_items_type` array cast: PostgreSQL reparses the dumped CHECK expression into a different but equivalent internal expression on restore.

Verification (from `backend-go`):

- `docker exec codex-billing-go-test-db psql -U billing -d billing_test -Atc 'select version_num from alembic_version'` returned `0028_business_npwp` before using the supplied dump.
- Before adding the migration, `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_test?sslmode=disable' go test ./...` failed with `no migrations found`.
- Temporarily changed the baseline's `bills.currency` from `varchar(3)` to `varchar(4)` and ran the full Go suite with `-count=1`: the test failed with both differing column definitions. Restored the baseline immediately afterward.
- `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_test?sslmode=disable' go test -count=1 ./...` passed after restoration.
- An accidental `go test -count=1 ./...` from the repository root failed because the Go module is in `backend-go`; rerunning in that directory passed as above.
- `env -u PGTESTDB_URL go test -count=1 ./...` failed as intended with a clear configuration message.

Concerns and limits: the test requires a separately prepared isolated Alembic-head reference database and a PostgreSQL role able to create databases and roles; CI must provision both and set `PGTESTDB_URL`. The comparison does not exhaustively compare every PostgreSQL catalog property (such as privileges, triggers, views, domains, collations, or non-public schemas); it covers the listed objects in the current public schema. The dump includes no application-defined enum, function, or extension creation; the comparison will detect such definitions if present in the reference. Data, sequence current values, and existing-database adoption are outside this task. pgtestdb creates disposable template and instance databases and logs its generated test connection details.

## Review follow-up: catalog properties

Extended the comparison to include column collation (schema-qualified name) and identity mode, constraint validation state, and index validity and readiness. These are catalog properties in addition to the previously compared definitions.

From `backend-go`, temporarily added `COLLATE "C"` to `bills.currency` in the baseline. With `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_test?sslmode=disable' go test -count=1 ./...`, the original comparison passed despite this drift. After adding the catalog comparisons, an initial run exposed a PostgreSQL `text || "char"` ambiguity; casting `attidentity` to text fixed the query. The same test then failed as intended, reporting `pg_catalog."default"` versus `pg_catalog."C"` for `bills.currency`. Restored the baseline and reran the same full Go test command: passed. The broader comparison limits noted above still apply.
