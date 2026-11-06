# Task 6 review follow-up: adoption unavailable

**Database adoption remains incomplete and is deliberately disabled.** `AdoptAlembic` returns a fencing error without connecting or modifying data. A serializable catalog snapshot plus a cooperative advisory lock does not prevent independent DDL. The deterministic `TestIndependentDDLRacesSerializableCatalogRead` runs on a disposable pgtestdb database: it reads the fingerprint in a serializable transaction, commits `CREATE TABLE` on another connection, observes the original transaction still reading its stale fingerprint, and demonstrates that the old metadata creation transaction can commit. This is evidence of the unsafe historical approach, not a production operation. An enforceable database-wide DDL fence, covering independent and privileged sessions from before inspection through commit, must be established and verified before adoption can be implemented. No such mechanism has been established here. `TestAdoptionUnavailableWithoutFence` checks that rejection preserves a representative row, sequence value, schema and absence of goose metadata.

The fingerprint now includes `relrowsecurity`, `relforcerowsecurity`, and each policy's table, name, command, permissiveness, role names, USING and WITH CHECK expressions. `TestRejectRowSecurityDrift` uses disposable PostgreSQL databases and tests enable RLS, force RLS, policy creation, and policy expression alteration. All four pass with the guard present and `MigrateFresh` rejects the drift. Fresh disposable database fingerprints are Go `75237885708b1d85dd38588334ef35ce`, Alembic `8a3544c12f60cd3f3d9e8fed7c73d617`, and Alembic with canonical goose metadata `92a74cb0a43c4d08ff6a5b168be9505e`. The Alembic fingerprint is independently checked against the task-only reference.

Mutation checks, using the environment below, restored after each run:

- Removing both row-security relation fields made `go test ./migrations -run '^TestRejectRowSecurityDrift/(enabled|forced)$' -count=1 -v` fail for both cases with `row security drift missing from fingerprint`.
- Removing the policy signature query made `go test ./migrations -run '^TestRejectRowSecurityDrift/(policy_created|policy_definition)$' -count=1 -v` fail for both cases with `row security drift missing from fingerprint`.

Final full-suite command and output:

```
PATH="/tmp/billing-task6-venv/bin:$PATH" PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./... -count=1
ok   github.com/wenhaokho/wenhao-billing/backend/cmd/api 3.932s
?    github.com/wenhaokho/wenhao-billing/backend/internal/dbquery [no test files]
ok   github.com/wenhaokho/wenhao-billing/backend/internal/platform 3.728s
ok   github.com/wenhaokho/wenhao-billing/backend/internal/withholding 0.004s
ok   github.com/wenhaokho/wenhao-billing/backend/migrations 27.001s
```

A safe versioned migration entry point for known future goose migrations is not implemented. `MigrateFresh` remains fail-closed for unknown future history and schema. The earlier direct-goose test did not exercise a safe application entry point and was removed. No production, deployment, app startup, or commits were touched. Tests invoke Python Alembic only on newly created disposable pgtestdb databases; the task-only reference is read only.

## Round 2: close the remaining Alembic admission route

`MigrateFresh` now rejects any Alembic metadata even when canonical goose rows are already present. `TestMigrateFreshRejectsAlembicEvenWithCanonicalGooseMetadata` generates a fresh Alembic database on pgtestdb, creates canonical goose metadata, verifies the expected adopted fingerprint, and checks rejection preserves the schema, goose history, Alembic revision, persisted row and sequence value.

Restoring the former conditional Alembic acceptance as a temporary mutation made the new test fail with `expected Alembic rejection, got <nil>`. The mutation was restored; the test passed (`1.98s`). The same test also reproduced the failure before the fix. All commands used the task-only `PGTESTDB_URL` and temporary Alembic PATH above.

Final verification after restoring the mutation:

```
PATH="/tmp/billing-task6-venv/bin:$PATH" PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./... -count=1
ok   github.com/wenhaokho/wenhao-billing/backend/cmd/api 2.826s
?    github.com/wenhaokho/wenhao-billing/backend/internal/dbquery [no test files]
ok   github.com/wenhaokho/wenhao-billing/backend/internal/platform 0.373s
ok   github.com/wenhaokho/wenhao-billing/backend/internal/withholding 0.004s
ok   github.com/wenhaokho/wenhao-billing/backend/migrations 16.286s
```

Adoption remains disabled and a safe future-version migration entry point remains incomplete.
