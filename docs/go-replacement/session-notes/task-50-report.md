# Task 50 report

Implemented replacement with bounded request parsing, Python-style currency normalization and validation order, transactional persisted-admin and singleton locks, sqlc queries, and pre-commit response serialization. OpenAPI documents the endpoint and API responses use no-store.

Verification against disposable real PostgreSQL databases:

- Red before implementation: `TestPaymentAccountReplacement` failed with HTTP 405.
- `/tmp/billing-tools/sqlc generate` passed.
- Focused `TestPaymentAccountReplacement` and `TestPaymentAccountsPythonDifferential` passed after restoration. They cover normalized replacement, duplicates, unsupported currency, default validation, clearing, malformed/null entries, DB failure rollback, preserved profile data, media/size/no-store, OpenAPI operation presence, concurrent distinct-admin replacements, demotion, and actual Python-vs-Go HTTP comparisons in separate databases, including Unicode uppercase expansion.
- Restored mutation checks: disabling duplicate detection caused the focused test to fail (500 vs 422); replacing the admin row lock with an unlocked read failed the demotion lock check; deleting outside the transaction failed the rollback assertion. All mutations were restored.
- Full suite passed: `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test -count=1 ./...` (platform 244.593s; migrations 37.321s).

Limits: differential coverage is representative, not exhaustive across all Unicode strings or every malformed input; concurrent replacement is stress-tested, not an exhaustive scheduling proof. OpenAPI test checks operation/security presence rather than a complete schema equivalence comparison. The body limit is 1 MiB, following existing Go write endpoints.

## Review follow-up

Unsupported-currency errors intentionally differ from Python: Go returns the fixed `Unsupported currency` detail, while Python appends normalized input. The fixed detail prevents disclosure of attacker-supplied values. Regression tests cover ASCII and Unicode secrets; they failed before the fix and passed after it.

Added unauthenticated and non-admin requests. A restored mutation bypassing the initial admin gate failed because the unauthenticated request returned 200. Added ten concurrent distinct-admin replacement rounds starting without a singleton; each response is checked against its own request and the persisted result must equal one complete replacement. Added actual Python HTTP comparisons for empty, null, wrong-type, malformed, and non-JSON media bodies, comparing status and validation type, location, and message while checking Go's intentional input redaction.

The follow-up focused tests passed after restoring the mutation. The final full suite passed with the same command above (platform 200.632s; migrations 27.679s).
