# Task 13 status: incomplete

No item HTTP operations or production authorization changes have been implemented. The implementation is not ready for use and the requested API contract, transaction guarantees, parity, mutation checks and full-suite verification remain outstanding.

Added `backend-go/queries/items.sql` with list/get/insert/update and target read/upsert SQL, and generated `backend-go/internal/dbquery/items.sql.go` using `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` from `backend-go`. These queries have not been exercised against Postgres and are not integrated with any handler. Their safety and behavior remain unverified.

Added `backend-go/internal/platform/items_test.go` as a first test-first HTTP check that all five routes reject missing sessions. `PATH=/tmp/billing-task6-venv/bin:$PATH go test ./internal/platform -run TestItemRoutesRequireSession -count=1` fails as expected: each route returns 404, not 401. The Go suite is therefore intentionally red with this test present. `PATH=/tmp/billing-task6-venv/bin:$PATH go test ./internal/dbquery` passes compilation (there are no tests in that package).

The Python source was inspected: admin is required for all routes; `DELETE` soft-deactivates; create requires hosting domain, grace days, suspension flag and target when `is_hosting` is true; hosting domain and target fields are normalized by the service; the service initializes hosting status and upserts a target. The Pydantic create/update schemas differ, particularly around optional fields and validation. This is source inspection only, not differential execution against Python.

No provider calls, mutations, production access, migrations, commits, or Python changes were performed. No claimed parity or security guarantees have been verified. Full real-Postgres tests, Python comparison, mutation tests, OpenAPI checks, exact decimal validation, and sqlc reproducibility comparison are still required.
