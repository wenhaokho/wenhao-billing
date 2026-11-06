# Task 14 report

Implemented administrator-only chart-of-accounts list and detail reads with Huma registrations, persisted-user authentication, sqlc queries, and PostgreSQL. The list sorts by code and implements optional FastAPI-style boolean filtering (including last-value repeated parameters); detail returns numeric IDs and Python-compatible errors. The shared API middleware supplies `Cache-Control: no-store`. No business scope was introduced.

Verification:
- Wrote `TestAccountReads` and `TestAccountReadOpenAPI` before implementation; the initial run failed because both routes and schemas were missing.
- `TestAccountReads` uses a pgtestdb database, two distinct accounts, admin and non-admin persisted users, missing/tampered/deleted sessions, and no-store checks. It compares complete status and decoded response bodies against the Python FastAPI router on the same isolated database for unfiltered, true/false/YES, repeated and invalid filters, existing/missing/malformed/out-of-range IDs.
- OpenAPI test checks both GET paths, security and account properties.
- Mutation checks: bypassing the admin guard made auth assertions fail; negating the active SQL predicate made all filtered parity checks fail; changing the ID predicate to `<>` made detail and missing-ID parity fail. Restored all mutations and regenerated sqlc v1.29.0.
- Full suite: `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test -count=1 ./...` passed for all packages.

Parity limits: Python comparison overrides authentication to isolate the router response contract; Go authentication is exercised separately against persisted users. The comparison covers representative boolean spellings and integer errors, not every Unicode or extremely long numeric input. Out-of-range integer database IDs return Python's plain 500 response for the tested value.

## Review follow-up

ID parsing now accepts Pydantic's decimal-zero and digit-separated forms, while validating syntax before conversion and retaining the integer-range database guard. The real Python HTTP differential includes `101.0`, `101.00`, `1_01`, `1_01.0`, and invalid fractional, misplaced underscore, trailing decimal point, and exponent forms. The read operations now document JSON error bodies for 401, 403, 422 and 500, JSON 404 for detail, and the detail route's plain-text 500 for out-of-range IDs. Existing write schemas are not changed.

The focused `go test ./internal/platform -run TestAccountRead -count=1` passed after the fix. Disabling underscore normalization caused the Python differential for `1_01` and `1_01.0` to fail, and was restored. The prescribed `go test -count=1 ./...` command passed again after the review changes, including the existing write and OpenAPI tests. The earlier auth, filter and selection mutation evidence still applies. Input parity is verified for the listed representative cases, not exhaustively for every possible string.
