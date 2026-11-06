# Task 16 report

Implemented GET `/api/v1/users` and PATCH `/api/v1/users/me` with existing session/admin guards, Huma metadata, sqlc queries, and a transaction with row lock. PostgreSQL enforces email uniqueness, mapped to 409. Output selects public fields only.

Commands from `backend-go`:

- `/tmp/billing-tools/sqlc generate` (pinned sqlc v1.29.0) passed.
- `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./internal/platform -run '^TestUsers' -count=1` passed. Initial red test returned 404 for the missing routes.
- The same environment with `go test -count=1 ./...` passed after implementation and concurrency tests, before final test-only additions for Python parity, invalid session, and maximum name length.

HTTP tests cover authentication, sorted list without secrets, null/empty/omitted fields, email normalization and duplicate email, rollback injection, OpenAPI operation and response presence, concurrent unique email, and a held-lock lost-update case. Representative Python HTTP responses match for invalid email, invalid display name, and duplicate email. Removing `FOR UPDATE` from the generated `LockSessionUserByID` SQL made the held-lock test fail with `locked update lost email`; the generated file was restored. The first version of the lock test did not catch this mutation; it was strengthened before the successful mutation check.

Incomplete verification: per-guard security and validation mutation demonstrations, exhaustive OpenAPI schema assertions, and broader Python parity remain. Malformed JSON error positions/reasons use the existing change-password parser and may differ from Python. The task should not be considered complete until those checks and a final full suite are run.

## Follow-up verification

The focused HTTP test now compares raw malformed bodies (`{`, `{"email":`, `{"email": }`), empty input, null and array bodies with Python HTTP; all pass, together with the earlier validation/conflict cases. The profile parser was adjusted for the tested malformed JSON positions and reasons. Other malformed JSON forms are not exhaustively characterized and can still differ from Python. The OpenAPI test checks the request reference, field constraints, allowed extra fields, list and user response schemas, error references and absence of secret fields. The list response schema was corrected to be non-nullable.

Mutation commands, run from `backend-go` using the environment below: `python3 /tmp/task16_mutations.py`, `python3 /tmp/task16_auth_mutations.py`, and `python3 /tmp/task16_cookie_mutations.py`. These temporary scripts apply one mutation, run `go test ./internal/platform -run '^TestUsersHTTP$' -count=1`, and restore source in `finally`. Disabling email validation, name length validation, or unique-conflict mapping produced failures. Bypassing the missing-cookie and invalid-cookie checks, non-admin guard, deleted-user handling, and locked admin recheck each produced failures. The previous `FOR UPDATE` removal also failed as recorded above. Initial ineffective mutations that wrote an error before continuing, and an invalid-session mutation that still rejected the invalid token, survived; they were replaced with actual bypasses. The initial locked-admin mutation survived until a deterministic concurrent demotion test was added, after which it failed. All mutations were restored.

Final commands from `backend-go`:

```
/tmp/billing-tools/sqlc generate
PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./...
```

Both passed after all changes and restored mutations. The earlier incomplete verification note is superseded by this follow-up, except the stated limit on exhaustive malformed-JSON parity.

## Review fixes

The shared email validator enforces the email-validator 254-byte limit on the original, normalized, and IDNA-ASCII forms and formats the associated Python-compatible length reason. It also normalizes the library's case-insensitive mailbox names. A real HTTP differential test compares Python and Go across profile PATCH, user POST, and login POST for 254/255-byte ASCII, 254/256-byte UTF-8, IDNA expansion, and mailbox/domain normalization. Admin-create deliberately redacts the invalid email input in its validation response; the comparison accounts for that existing security difference. The test covers selected cases rather than all possible email-validator behavior.

User list now locks and rechecks the session user inside the same transaction as the list query. A Postgres test holds the user row lock, starts a list, demotes the user, and verifies the list returns 403 after the lock releases. The 1 MiB profile-body cap is a deliberate safety boundary: an oversized body returns 422 with the JSON validation error and `Cache-Control: no-store`, as tested; this is a Python parity difference.

The pre-fix HTTP regression showed overlong addresses accepted or returning 401/409 instead of 422, and the list race returned 200. Mutation command (same environment as above): `python3 /tmp/task16_review_mutations.py`. Replacing the 254-byte threshold with 10000 caused the HTTP length tests to fail; replacing the list's locked user read with an unlocked read caused the demotion test to fail. Both mutations were restored.

Final verification after restoration: `/tmp/billing-tools/sqlc generate` and `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./...` passed.

## Review round 2

The shared email validator now rejects original inputs longer than 2,048 Unicode code points before whitespace trimming or display-name extraction, using Pydantic's length error. Python and Go HTTP differential coverage exercises 2,048 and 2,049 code point ASCII and Unicode display names, as well as overlong leading whitespace, on profile PATCH, admin user creation and login. Tests also assert the supplied password never appears in the response; admin-create retains its existing input redaction. The 254-byte checks and list locking remain in place.

Before the fix, `go test ./internal/platform -run '^TestUsersHTTP$' -count=1` failed the new Python parity cases. Raising the guard from 2048 to 9999 and rerunning that test exited 1 with parity failures on each of the three endpoints; the original file was restored. Mutation output is `/tmp/task16-round2-mutation.log`.

After restoration, `/tmp/billing-tools/sqlc generate` and `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./...` passed from `backend-go`.
