# Task 26: vendor reads

Implemented authenticated GET list and detail using sqlc/pgx, the shared persisted admin session check, existing Python-compatible UUID parsing and timestamp formatting. The list is name-sorted and accepts the last repeated `active` value with FastAPI boolean spellings. No vendor writes were added. Frontend callers use list (including `active=true`) and detail endpoints; existing write callers remain outside this task.

## Evidence

- Test-first: `TestVendorReadPostgres` failed with 404 before routes were registered.
- Focused `go test ./internal/platform -run '^TestVendorRead' -count=1` passed with the task's Python and Postgres environment. It covers absent, tampered, expired, nonadmin, missing-user and demoted-user sessions; ordering, true/false and repeated boolean filtering, invalid boolean; canonical, compact, braced, URN, malformed and missing UUIDs; no-store; populated and null vendor fields; whole-row snapshots before and after reads; OpenAPI operation security, response statuses and output properties.
- Actual Python FastAPI HTTP compared full decoded bodies and statuses on a separately created disposable Postgres database for list, true and repeated filters, invalid filter, populated detail, missing ID and malformed ID. Both zero and fractional timestamp forms and null/populated fields appear in the fixture.
- Pinned `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` generated the queries.
- Red mutations (restored after each): bypassing `requireAdmin` returned 200 instead of 401; replacing the filter predicate with true returned both rows for `active=true`; broadening the ID predicate to include all rows returned 200 instead of 404 for an absent ID. Each failed `TestVendorReadPostgres`.

## Parity and verification limits

The HTTP comparison covers representative inputs, not every boolean spelling, UUID error form, timestamp precision, or field value. Response validation now normalizes stored email and rejects invalid email, name, currency and payment terms. Email normalization uses the existing Go approximation of Pydantic's email-validator and remains subject to edge cases outside the tested corpus. The OpenAPI checks assert status/security/property presence rather than every schema attribute. Tests do not exercise an actual deleted user after deletion (a signed token for a nonexistent user is checked), concurrent demotion during a read, or database-created defaults. These remain gaps relative to exhaustive parity.

The requested full suite passed: `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test -count=1 ./...` (platform 138.701s). Regeneration after mutation restoration reproduced the generated vendor query byte-for-byte.

## Review follow-up

`TestVendorReadPostgres` first failed on actual Python HTTP differentials for legacy rows: whitespace and uppercase email, invalid email, empty name, short currency, and payment terms below zero or above 365. It also checks both detail and list responses, verifies the 500 body does not disclose the invalid value, and preserves the full-row snapshot. Both databases are disposable and separate. The response now normalizes email with the existing email helper and validates name and currency by Unicode character count and payment terms by range; invalid data returns a generic 500 before any list data is emitted. Creation uses the same response conversion before transaction commit. The list OpenAPI response is explicitly a non-null array.

Focused `go test ./internal/platform -run '^TestVendor(Read|Create)' -count=1` passed. Red mutations independently removing email normalization, email rejection, name validation, currency/payment-term validation, and the list schema non-null declaration each failed the relevant test and were restored. The invalid-email test detects both list and detail status changes. The Python 500 body is intentionally not compared: FastAPI's plain-text error and Go's sanitized JSON error both have status 500, with Go omitting row contents. Email parsing is an existing approximation, not an exhaustive reimplementation of Python email-validator. The test corpus does not cover all internationalized or unusual addresses.

After the review fix, the requested full `go test -count=1 ./...` command passed again with the specified Python and Postgres environment (platform 129.113s).

## Shared email parser follow-up

Python `EmailStr` rejects CR/LF anywhere and RFC comments that `net/mail` previously discarded. The shared parser now checks CR/LF before trimming and extracts a syntactically valid angle-bracket display name before validating the address. Direct Python differential covers comments in several positions, CR, LF, tabs, vertical tab, outer spaces, and display-name/angle-bracket forms. Actual Python HTTP comparisons against disposable Postgres rows cover both vendor list and detail for these cases. Valid values are normalized and compared in full; invalid values must return 500 with no stored email in the response. Login, customer, vendor, admin-user and profile call paths have regression checks. Focused `go test ./internal/platform -run '^TestEmailShared|^TestVendor(Read|Create)|^TestCustomer(Create|Update)' -count=1` passed (95.683s), and the follow-up `'^TestEmailShared|^TestVendorRead'` passed with the corrected CR/LF database fixtures.

Two independent red mutations removed the CR/LF guard and restored unrestricted `mail.ParseAddress`, respectively. Both failed the direct Python differential, shared callers, and vendor list/detail comparisons; both were restored before the full suite. The shared normalizer remains an approximation of Python email-validator, and the tested cases do not prove equivalence for every international or unusual address or every validation-error reason. Invalid vendor output uses a sanitized JSON 500 rather than Python's plain-text 500.

The requested full `go test -count=1 ./...` passed after the parser fix (platform 142.667s). While it ran, the vendor test's sanitized-error assertion was strengthened to require the exact generic JSON body; the focused EmailShared/VendorRead tests passed again with that assertion. The full run used the same production code and the previous, weaker version of that test assertion.


## Quoted display-name follow-up

The Pydantic differential now includes escaped and unescaped interior quotes, a trailing backslash, quoted parentheses and angle brackets, empty or unterminated quoted names, and trailing comments. Python accepts several strings that a strict RFC parser would reject, so the shared parser follows Pydantic's display-name extraction before validating the email itself. Python HTTP comparisons exercise escaped quotes and invalid forms on vendor list and detail. Additional tests exercise normalized values through login, customer and vendor input validation and profile HTTP; existing tests continue to cover CR/LF and unquoted comments. The focused `go test ./internal/platform -run '^TestEmailShared|^TestVendor(Read|Create)|^TestLoginValidationContract' -count=1` passed, as did a further focused run including angle brackets in quoted names. Restoring the interior-quote rejection produced red Python differential, vendor list/detail, login, customer, vendor, and profile tests; the mutation was restored.

The email normalizer remains an approximation of Python email-validator, and the differential corpus is not exhaustive for internationalized names or unusual malformed input. For invalid vendor output Go returns a sanitized JSON 500, while Python emits a plain-text 500.

The requested full Go suite passed after this follow-up with the specified Python and Postgres environment (platform 112.633s).
