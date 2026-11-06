# Task 13c report

Implemented DELETE `/api/v1/items/{item_id}` with Huma security metadata and the shared persisted-user admin check. A sqlc query updates only the selected item's `active` field to false and returns the complete item including any existing Cloudflare target. Missing and malformed IDs use existing FastAPI-compatible error handling. Repeated deactivation succeeds; no schema changes, hard deletes, or Cloudflare calls were added.

Verification used `PATH=/tmp/billing-task6-venv/bin:$PATH PYTHONPATH=/tmp/task13a-fastapi PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable` from `backend-go`:

- `go test ./internal/platform -run 'TestItemDeactivate' -count=1`: passed against real Postgres. Tests cover active and inactive items, repeat calls, target output, another item's state, missing and malformed IDs, missing/tampered/nonadmin sessions, no-store, and OpenAPI DELETE/security.
- Before implementation the same tests failed with 405 and missing OpenAPI DELETE.
- Mutation: bypassing `requireAdmin` in the delete handler caused `TestItemDeactivatePostgres` to fail on all three unauthorized cases. Restored handler afterward.
- Mutation: changing the generated query to set `active=true` caused `TestItemDeactivatePostgres` to fail on the returned item. Restored generated query afterward.
- `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` followed by SHA-256 comparison of all generated files: identical.
- `go test ./... -count=1`: only `TestItemRoutesRequireSession` failed because PATCH returns 405; task 13d owns that unimplemented route. All other packages, including migrations, passed.

Limits: coverage checks the target and key response fields but does not compare every ItemOut field against Python. No production system, deployment, commit, or Python backend changes were made.

## Review follow-up

The update and target lookup now use one Postgres transaction; the handler commits only after both operations succeed and rolls back on errors. The real-Postgres handler test revokes SELECT on `cloudflare_targets` to force the lookup to fail after the update, restores the privilege, verifies HTTP 500, and checks the item remains active. Before the fix this test failed because the item was inactive; afterward it passed. The test also checks all ItemOut keys and compares the entire DELETE response against the preceding GET with only `active` changed, and compares a full `row_to_json` snapshot of the target before and after repeated deactivation.

Mutations: changing the query executor back to the connection pool reproduced the rollback test failure. Adding a target zone update before commit caused the complete response comparison to fail on repeated deactivation. Both mutations were restored. The test is an isolated Postgres database test; no external Cloudflare call is made.

After restoration, `go test ./internal/platform -run 'TestItemDeactivate' -count=1` passed. The follow-up `go test ./... -count=1` passed all other packages but failed `TestItemRoutesRequireSession` and `TestItemPatchPostgres`, both because the in-progress PATCH route returned 405; migrations passed.
