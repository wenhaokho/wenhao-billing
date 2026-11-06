# Task 28 — request Content-Type compatibility

Implemented a shared media-type check for `application/json` and `application/*+json` (case-insensitive, parameters ignored). In the installed FastAPI version, absent, empty, malformed and other media types leave nonempty input as unparsed bytes. Go substitutes a redacted JSON string before model validation, preserving syntax/auth/path ordering without returning or logging the original bytes. Empty bodies remain missing-body validation inputs. The existing one-megabyte bounded reads and no-store middleware remain in use. Account and item writes now parse JSON syntax before auth, and account PATCH combines invalid path and body errors. The change-password parser and item create also match FastAPI's empty/malformed JSON locations for the exercised inputs. OpenAPI request schemas remain application/json.

The deliberate security exception is validation `input`: FastAPI echoes non-JSON bytes, whereas Go returns `[REDACTED]`. Password-bearing parsed inputs retain existing redaction. For oversized requests Go's bounded-read responses are a deliberate operational limit; FastAPI without an equivalent configured limit has no equivalent default response. Full byte-for-byte parity is not claimed for arbitrary malformed JSON, every possible media parameter spelling, or arbitrary non-UTF-8 byte sequences; the tests compare the listed cases.

`TestVendorCreatePostgres` runs real HTTP requests against both FastAPI and Go, with separate disposable pgtestdb databases and real authenticated sessions on both sides. For all 14 implemented write routes, it compares 422 status, error type, location and message for absent, text/plain, malformed media, empty JSON and malformed JSON; compares successful status for JSON, mixed-case JSON with a parameter, and `application/vnd.billing+json`; checks protected-route auth versus malformed JSON ordering; and compares bad path versus body ordering on parameterized routes. It snapshots users, accounts, items, customers and vendors before/after rejected requests, checks raw and escaped-key secrets do not appear in Go responses or captured logs, and exercises no-store in the existing vendor test. The Python forgot-password success case uses a nonexistent address, avoiding delivery. All fixtures are disposable; no external email/provider or production service was used.

Mutation checks restored immediately after each run:

- Treating absent content type as JSON failed `TestRequestJSONMediaType`.
- Returning raw bytes instead of the redacted substitute failed `TestRequestJSONMediaType`.
- Disabling the login, shared password, and profile media guards individually failed `TestVendorCreatePostgres`.
- Bypassing pre-auth JSON syntax validation failed `TestVendorCreatePostgres` auth and path precedence comparisons.

Validation: targeted `TestVendorCreatePostgres` and `TestRequestJSONMediaType` passed after restoration. The final full suite passed (platform 147.607s; migrations 29.784s):

```sh
PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test -count=1 ./...
```

## Review follow-up: malformed `+json` types

The first guard accepted multiple slashes, such as `application/foo/bar+json`, and could parse and execute an otherwise valid request. Python's `email.message.Message.get_content_type`, used by FastAPI, treats a type with anything other than exactly one slash as `text/plain`. The guard now checks the slash count in the media type before recognizing `application/json` or an `application/*+json` subtype. It continues to use bounded body reads and retains Python's permissive treatment of parameters; strict RFC parsing would reject forms FastAPI accepts.

The real FastAPI/Go route differential now includes `application/foo/bar+json`, `application//foo+json`, `application/foo+json/`, and `application/foo+json, text/plain` on all 14 routes with real authenticated sessions, redaction checks, and database snapshots. Unauthenticated requests with a malformed media type and malformed body also compare auth ordering. Successful requests additionally cover `application/+json` and `application/foo+json; bad`, which FastAPI accepts. Reintroducing the missing slash guard failed both the focused unit test and the HTTP route differential; the mutation was restored. The original exception for redacting FastAPI's echoed raw input remains deliberate.

After restoring the reviewed guard, the focused test passed (15.818s), and the exact full-suite command above passed again (platform 131.163s; migrations 17.923s).
