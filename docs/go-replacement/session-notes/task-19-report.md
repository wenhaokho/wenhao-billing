# Task 19 report

Implemented customer list and detail reads with persisted-user admin authorization, sqlc pgx queries, ordered and optional active-filtered results, Python-compatible JSON fields, and Huma operation/security/error schemas. CustomerOut has no timestamps, so timestamp formatting is not applicable. No business scope was invented. The common no-store middleware applies.

## Verification

- `TestCustomerReadRequiresSession`: initial red (both routes returned 404), then green.
- `TestCustomerReadPostgres`: isolated pgtestdb HTTP tests cover absent, tampered, expired, non-admin, and deleted sessions; sorting, active values and repeated filter; detail, nulls, aliases, CC arrays, decimals, invalid UUID/filter, missing ID, no-store, no secret/customer leakage and unchanged fixture data. Python FastAPI TestClient responses were compared structurally against Go for list, repeated filtering, detail, invalid filter/UUID and 404 using the same Postgres fixtures.
- `TestCustomerReadOpenAPI`: operations, security, error statuses and representative fields.
- Independent red mutations: bypassing the admin guard exposed the fixture and failed the auth test; changing the filter predicate to `active = active` failed filter assertions; changing detail selection to `customer_id != $1` failed detail and Python differential assertions. Restored all mutations and regenerated with sqlc v1.29.0.
- Full Go suite: `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test -count=1 ./...` passed, exit 0 (all packages).

## Limits

Differential fixtures are representative, not exhaustive for all PostgreSQL collations, UUID spellings, boolean spellings, or database-corrupt JSON values. GET requests do not consume bodies; transport/server request bounds are inherited rather than newly introduced. Other customer endpoints are outside this task. The deleted-user response follows the existing shared Go guard (403). No production or live provider was accessed.

## UUID and schema review follow-up

Added strict shared path UUID parsing to customer GET, item GET/PATCH/DELETE, and administrator password reset, retaining the existing route-specific auth and validation order. Canonical, compact, braced and `urn:uuid:` forms are accepted; malformed separators are rejected. The error formatter bounds scanning at 4096 bytes; for longer path segments it reports the length rather than locating an invalid character, a deliberate limit to exact Python error parity. The customer array schemas now declare arrays without null. HTTP differential tests cover all five routes for malformed separators and each alternate valid form against the Python routers, using absent IDs to avoid writes. The reset validation input remains redacted as in the existing Go security behavior. Full JSON snapshots of both customers and users are compared before and after all these requests.

Independent mutations replacing the shared parser with pgx Scan caused HTTP differential failures on every route and the UUID unit test; removing the non-null array tags failed all three array schema assertions. Mutations were restored before final verification.

After restoring the mutations, the full specified `go test -count=1 ./...` command passed again (exit 0, all packages).

## Review follow-up: UUID offsets and PATCH precedence

The shared UUID error formatter now reports invalid-character offsets relative to the original braced or URN path. Python HTTP differential tests cover `{xyz}` and `urn:uuid:xyz` across customer GET, item GET/PATCH/DELETE, and admin password reset. Item PATCH now reads the bounded body after the existing admin guard, returns JSON decoding errors before path errors, and combines path and body validation errors in FastAPI order for valid JSON. The tests compare malformed UUID plus malformed JSON and malformed UUID plus multiple invalid fields to the Python HTTP responses. Additional checks verify missing/non-admin sessions still take precedence and oversized bodies are rejected, including with an invalid path; existing item integration tests cover transaction behavior.

Independent mutations removing the UUID offset and restoring early UUID rejection both failed the differential test, then were restored. Focused `go test -count=1 ./internal/platform -run 'TestCustomerReadPostgres|TestItemUpdate'` passed.

Final specified full `go test -count=1 ./...` passed after restoring mutations (exit 0, all packages).
