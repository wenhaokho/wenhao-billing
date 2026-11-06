# Task 35: Project list and detail

Implemented GET `/api/v1/projects` and `/api/v1/projects/{project_id}` using sqlc queries, persisted-admin authentication, Huma operation/security schemas and fixed-scale NUMERIC(19,4) output. Filters support optional customer UUID, repeated statuses and boolean active, with descending created_at ordering. Invalid stored nonfinite contract values fail with a sanitized 500. No Python, provider, production data or email changed.

Verification:
- Test-first `TestProjectReadsPostgres` failed before route registration: expected 401, got 404.
- `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` succeeded.
- `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=$PWD/../backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test ./internal/platform -run '^TestProjectReadsPostgres$' -count=1` passed. Actual Python FastAPI TestClient versus Go HTTP on separate disposable PG databases covers all project fields, three projects, inactive related customer, both active states, customer filter, repeated/unknown/empty statuses, invalid UUID/bool individually and combined, repeated boolean, detail/missing/invalid ID, ordering, nulls and zero decimal formatting. Tests check missing/tampered/deleted/non-admin authentication, no-store, empty array, unchanged project snapshots, nonfinite stored numeric safety and operation response/security/list schema presence.
- Four independent generated SQL mutations were restored after each and each made the focused test fail: bypass customer filter, active filter, status filter and reverse created_at ordering.
- Full suite command: `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test -count=1 ./...` (running).

Limits: differential does not exhaust UUID spellings, status strings, Unicode edge cases or tied created_at order (Python specifies no tie-breaker). Snapshot includes projects but not related tables; no writes are made by handlers. OpenAPI checks operation response/security/list shape, not every property constraint. Full suite result is appended below.

Final full suite passed all packages: platform 152.227s, migrations 19.057s.
