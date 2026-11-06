# Task 65: Update bill

Implemented PATCH `/api/v1/bills/{bill_id}` with sqlc and pgx. It rechecks and locks the persisted admin and bill, locks referenced vendor, project, items and accounts, applies supplied fields, replaces lines atomically and uses half-even decimal calculations. It computes the response before committing, and leaves payment and ledger records untouched. Generated sqlc v1.29.0 queries. Shared create/update line integer validation rejects fraction and exponent strings; out-of-range database integers return Python's sanitized 500.

Test first: the real PostgreSQL differential initially failed with Go 405 and Python 200. The passing differential uses separate disposable databases and actual Python HTTP requests, covering supplied, omitted and null fields, status transitions, currencies, multi-line and tie rounding, discounts, references and missing references, malformed/invalid bodies, integer edge cases, and a separate OPEN bill with a preexisting balance. Go checks cover anonymous requests, no-store, body limits and media types, OpenAPI, output and line-insert rollback, a concurrent disjoint update, admin demotion while blocked, and a bill status change while blocked. Snapshots protect another bill and line, payments and ledger rows. The final suite also includes a validation-error redaction check and explicit PARTIAL, PAID and VOID rejection checks.

Five independent temporary mutations were run and restored: removing the status guard, changing rounding to half-up, bypassing persisted admin authorization, removing the bill row lock, and committing on output serialization failure each caused the focused test to fail. The first auth mutation failed to compile and was replaced with a compiling mutation, which was detected.

Limits: coverage is representative rather than exhaustive; the existing-balance case seeds the post-payment state, and the bill lock race uses a database transaction because Go bill payment and void endpoints are not available. No claim is made about every concurrent interleaving or every Pydantic edge case. No production or Python source changes, commits, or external actions were made.

Final stable full suite passed: platform 238.296s, migrations 22.102s, and all other packages passed. Earlier incomplete runs were interrupted before this final run.

```sh
PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:64392/billing_task2_clean?sslmode=disable go test -count=1 ./...
```

## Review follow-up

The shared bill integer validator now trims surrounding whitespace before parsing. Real Python HTTP differentials cover whitespace around integer and account fields, invalid fraction/exponent strings with whitespace, and no-write behavior, with full target-bill and line snapshots for rejected updates. Item references are deduplicated and locked in UUID byte order independent of canonical, URN, braced, or uppercase spelling. Two-admin POST/PATCH tests observe both requests waiting on an externally held item lock, then require both to complete; each operation takes a turn entering the lock queue first. Before the fix, the cross-alias test produced a PostgreSQL deadlock and Go 500.

Deliberate safety ruling: Go preserves the exact JSON decimal token for validation rather than first rounding it through a binary floating-point number, as Python's JSON parser does. An actual Python HTTP regression shows `1.0000000000000000000001` accepted as `1.0`, while both Go create and update reject it with 422 and make no writes. This is an intentional precision-preserving difference.

Temporary mutations removing integer whitespace trimming, reversing create item lock order, reversing update item lock order, and discarding exact JSON number tokens were each detected by the focused tests, and restored. Final focused tests passed (5.712s). The final full suite passed: platform 244.690s, migrations 19.968s, all other packages passed. sqlc generation succeeded.
