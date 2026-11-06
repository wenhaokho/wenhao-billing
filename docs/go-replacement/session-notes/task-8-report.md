# Task 8 report

Implemented `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, and `GET /api/v1/auth/me` in the Go HTTP application. Login verifies Argon2id with gokit, issues a gokit HS256 token as an HttpOnly `session` cookie, and returns the existing user fields. `/me` validates the token and reads the current user and role from PostgreSQL. Logout clears the cookie. Legacy Starlette cookies and bcrypt hashes are rejected. The token carries 8-hour or 30-day expiry depending on `remember`; the cookie has the same Max-Age. API responses use no-store.

Runtime configuration: `SESSION_SIGNING_SECRET` must contain at least 32 bytes and differ from configured MCP secrets; use a unique cryptographically random secret. `SESSION_COOKIE_PATH` defaults to `/`; `SESSION_COOKIE_SECURE` defaults to `true` (set `false` for local HTTP); `SESSION_COOKIE_SAMESITE` defaults to `lax`, and accepts `strict` or `none` (`none` requires secure). Configuration is validated before serving. `NewHandlerWithSessions` accepts an explicit `SessionConfig`; `NewHandler` remains available for foundation tests. The two user reads are sqlc queries, generated with `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate`.

Test-first: `TestSessionHTTP` initially failed to compile because the new handler and configuration did not exist. The test runs the real HTTP handler against a pgtestdb database, covers successful login/me/logout, unknown email, wrong password, bcrypt, required fields and invalid input, missing/malformed/tampered/expired cookies, both cookie and token TTLs, non-admin and deleted users, no-store, output secret leakage, and secure/HttpOnly/SameSite/path cookie attributes. Expiry is tested using an independently signed expired token. No DB mocks were used.

Independent mutations applied one at a time and restored: bypass email validation (test observed 401 instead of 422), bypass wrong-password check (200 instead of 401), accept a token with appended tamper byte (200 instead of 401), return success for expiry (200 instead of 401), bypass role check (non-admin accepted). Each mutation made `go test ./internal/platform -run TestSessionHTTP -count=1` fail; original source was restored.

Full suite: `PATH=/tmp/billing-task6-venv/bin:$PATH PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test -count=1 ./...` passed before final mutation testing; rerun after restoration is recorded in the task handoff.

Limitations: malformed login payloads return a generic 422 `detail` string rather than FastAPI/Pydantic's structured validation-error array; email normalization and the exact Pydantic email validation rules are not replicated. Logout clears the client cookie but, as with the existing stateless session, cannot revoke a copied token before expiry. The application enforces secret length and separation from configured MCP values but cannot prove entropy; operators must generate a random value. Password change and recovery routes remain out of scope.

## Contract follow-up

Captured actual FastAPI TestClient responses using `app.schemas.auth.LoginRequest` in `/tmp/codex-billing-py312`. `TestLoginValidationContract` reproduces the captured 422 JSON for an empty body, `{`, truncated property value, empty object, null and array bodies, missing fields, invalid email, incorrect field types and invalid/null `remember`, including multiple ordered errors. Test-first run failed against the prior generic detail and passed after implementation. Pydantic-compatible accepted boolean strings/numbers are supported. End-to-end database tests verify normalized whitespace, display name syntax, domain case, and IDNA punycode to Unicode.

Remaining compatibility boundaries: this is not a full reimplementation of Python `email-validator`; for example some invalid local-part forms may be accepted, and special-use or globally reserved domains are not checked. JSON decode `ctx.error` and offset mapping covers the captured malformed examples but is not guaranteed byte-for-byte identical for every syntax error (for example malformed object separators). The previous generic-422 limitation is superseded for the captured contract cases. Password values are redacted from validation error input, a deliberate security exception to FastAPI response compatibility.

## Validation security follow-up

`TestValidationRedactsPasswords` exercises the actual HTTP handler with password values in missing-field input objects, type errors, and nested objects/arrays. Password fields are replaced recursively and direct password-type error input is replaced with `[REDACTED]`; 422 and the `detail` field remain available to the frontend. `TestEmailDomainLabels` rejects labels with leading or trailing hyphens, invalid characters, and labels longer than 63 bytes, using IDNA ASCII validation. These tests failed before the changes and passed afterwards. The requested full Go suite passed again after the changes.

## Review round 1: email differential

Using the actual Python `LoginRequest` and installed `email-validator`, captured and added `TestEmailPythonDifferential` for leading, trailing, and consecutive local-part periods; `.test`, `.invalid`, `.local`, `.onion` and `.arpa` names; numeric TLDs; and accepted `+` local parts and `.internal`/`.example` domains. Invalid examples now yield 422 with the observed reasons. Added guards for quoted local parts. Security redaction tests remain passing. Each of five independent mutations (remove leading, trailing, consecutive period guard, reserved-name guard, and numeric-TLD guard) made the differential test fail, then source was restored. The required full Go suite passed afterwards.

Remaining precise compatibility gap: `a(b)@example.com` and `a,bc@example.com` are accepted by the Go validator but rejected by Python's email-validator due to invalid local-part characters. Domain-label and malformed JSON reason strings are not exhaustively identical to Python; uncommon internationalized or syntactically invalid local parts have not been fully compared. Prior statements that special-use domains are not checked are superseded by this section.

## Local-part punctuation follow-up

Added Python-derived differential cases for invalid parentheses, comma, and colon and for accepted complete ASCII atext punctuation, Chinese, and emoji local parts. The new invalid cases failed before the fix and pass after rejecting disallowed ASCII local-part characters, preserving normalized valid Unicode. The earlier documented parentheses/comma gap is resolved. The full Go suite passed after this change.

A remaining observed gap is the unsafe Unicode example `a\u200bb@example.com`: Python rejects its zero-width space as unsafe, while the current Go validation accepts it. Error messages for uncommon control characters can also differ; for example Python names U+0001 while Go's message contains the character. The validators are not otherwise proven identical for all Unicode or malformed JSON inputs.

## Unicode safety follow-up

The previously reported zero-width-space gap is fixed. Python-derived differential cases now cover U+200B, U+200D, U+202E, U+FEFF, U+00A0, U+0085, and U+0001 with the observed validation reasons, as well as valid composed/combining Unicode, Chinese, and emoji. The new cases failed before the fix and `go test ./internal/platform -count=1` passed after it, including HTTP status and password-redaction checks. The Go implementation rejects Unicode control, format, private-use, separator and leading combining characters and uses Unicode character names in errors. Unassigned Unicode code points and Unicode-version differences are not exhaustively compared with Python; Go and Python's Unicode databases may differ. Malformed JSON and uncommon email error wording remain non-exhaustive compatibility boundaries.

## Review round 2: TLD acceptance

Pydantic differential checks confirm `a@foo.1a` is accepted, while `a@foo.123` and `a@foo.a1` are rejected. Python's actual email-validator requires the ASCII IDNA domain to end in a letter, so the guard now follows that rule rather than rejecting every TLD starting with a digit (or only all-numeric TLDs). The new test failed before the correction. Independent mutations restoring the old first-digit check and skipping the TLD check were detected by the differential test; both were restored. The real-Postgres platform suite passed after restoration.

## Generated OpenAPI integration

Auth routes are now registered as typed Huma operations, generating request and user/error response schemas, session-cookie API-key security on `GET /me`, and Set-Cookie response headers for login and logout. Huma operation middleware dispatches the same underlying HTTP handlers before Huma's body decoding; this preserves existing validation, redaction, cookies, and wire responses while registration and documentation share the same runtime operations. Login input schema permits extra properties as the handler does. `TestAuthOpenAPI` checks all methods, success/error statuses, schemas, cookie headers, and security. It failed before registration. Independent mutations removing `/me` security and changing the cookie name each caused the test to fail and were restored. The full required Go suite passed after registration; the platform suite passed again after a final adjustment preserving other security schemes.

The request schema describes `remember` as boolean, matching FastAPI's generated schema; as in Python, runtime also accepts Pydantic-style boolean strings and 0/1 coercions. Huma automatically adds a generic default error entry to the logout specification, although the current logout handler returns 204.
