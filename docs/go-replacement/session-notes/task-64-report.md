# Task 64: Create bill

Implemented `POST /api/v1/bills` with sqlc and pgx. It locks and rechecks the persisted admin, locks referenced vendor, project, items and accounts, inserts the DRAFT bill and lines in one transaction, and serializes the response before commit. Line multiplication and discounts use decimal arithmetic with half-even rounding. Python's BillCreate schema has no tax or withholding fields; the service ignores those extra fields and does not create ledger postings. It allows an empty line list and uppercases currency.

Test-first: the first real PostgreSQL Python-vs-Go HTTP differential failed because Go returned 405 for the valid Python creation. The current differential exercises all three currencies, fields and linked entities, empty and multiple lines, amount and percentage discounts, half-even ties, ignored status and withholding fields, invalid fields, malformed, empty and null bodies, duplicate keys, invalid line integers, missing vendor/item references, and overlength database values. The latter three initially exposed JSON versus plain-text 500 responses; Go now returns Python's sanitized plain-text response. Focused real-PG tests also cover unauthenticated and demoted access, a demotion while waiting on the admin row, eight concurrent creations, forced line insertion failure, response serialization failure, no-store, body size limit, JSON media variants, OpenAPI, and seeded unrelated bill, line, payment and ledger snapshots. Existing bill-read regression passes after restoring read queries. The media-without-content-type case failed first and passed after the fix.

Independent temporary mutations were run and restored: changing line rounding to half-up failed the differential; disabling validation accepted invalid fields; bypassing the persisted role check let the demotion race create a bill; and committing on serialization failure left a bill behind. sqlc v1.29.0 generation succeeded. The earlier auth mutation failed to compile because it removed the only use of a variable; it was replaced with a compiling mutation that the demotion test detected.

Limits: the differential is representative, not exhaustive. It does not compare every missing foreign key, integer boundary, media-type error detail, or combination of tax/withholding extras; it does not prove every possible concurrent interleaving. No Python or production state was changed; Python was run against disposable test databases. No commit was made.

Final stable full suite passed: platform 274.294s, migrations 50.906s, all other packages passed. Earlier runs were interrupted when the media and database-error changes were added, and are not the final verification.

```sh
PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:64392/billing_task2_clean?sslmode=disable go test -count=1 ./...
```

## Review follow-up

The shared bill integer validator now trims surrounding whitespace before parsing. Real Python HTTP differentials cover whitespace around integer and account fields, invalid fraction/exponent strings with whitespace, and no-write behavior. Item references are now deduplicated and locked in UUID byte order, independent of canonical, URN, braced, or uppercase spelling. Two-admin POST/PATCH tests observe both requests waiting on an externally held item lock, then require both to complete; each request takes a turn entering the lock queue first. Before the fix, the cross-alias test produced a PostgreSQL deadlock and Go 500.

Deliberate safety ruling: Go preserves the exact JSON decimal token for validation rather than first rounding it through a binary floating-point number, as Python's JSON parser does. An actual Python HTTP regression shows `1.0000000000000000000001` accepted as `1.0`, while both Go create and update reject it with 422 and make no writes. This is an intentional precision-preserving difference.

Temporary mutations removing integer whitespace trimming, reversing create item lock order, reversing update item lock order, and discarding exact JSON number tokens were each detected by the focused tests, and restored. Final focused tests passed (5.712s). The final full suite passed: platform 244.690s, migrations 19.968s, all other packages passed. sqlc generation succeeded; the exact command is recorded in task-65-report.md.
