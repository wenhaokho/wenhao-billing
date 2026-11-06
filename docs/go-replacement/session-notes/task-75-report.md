# Task 75: Update quotation

Implemented PATCH `/api/v1/quotations/{quotation_id}`. The handler validates bounded, redacted input, locks the persisted administrator and quotation, locks supplied related rows with canonical UUID-byte ordering and replaces lines transactionally. It calculates subtotal and discounts using the existing quotation creation arithmetic, reloads and serializes the response before committing, and declares its OpenAPI contract. No invoice or journal writes are performed.

## Evidence

- The initial real-PostgreSQL test failed with 405 before implementation and passes on the restored source. Focused tests pass with a real PostgreSQL database.
- An actual Python FastAPI HTTP differential uses a separate PostgreSQL database. It compares 19 sequential update bodies covering full text/date fields, project/item references, invalid references, omission and explicit null, line replacement and empty lines, percentage and fixed discounts, IDR/USD/SGD, rounding, invalid UUID/date/decimal/body and null required database fields. It compares the status for all seven documented quotation statuses. Validation inputs are redacted for comparison and database 500 response bodies are not compared.
- Deterministic database tests hold a quotation lock while two distinct administrators update separate fields, check both wait and neither change is lost; hold a lower item lock while distinct administrators update different quotations using opposite UUID case aliases and duplicates, then probe the higher item; hold an in-flight demotion and verify a 403; and commit a status transition while an update waits and verify the update rejects the new status.
- Injected line-insert failure leaves the target quotation and lines unchanged. An injected NaN response conversion failure leaves snapshots of all quotations, quotation lines, invoices, journal entries and journal lines unchanged. Tests cover bounded bodies, media errors, redaction, no-store and schema/security presence.
- Restored-source mutations of the status gate, discount calculation, persisted-role check, quotation lock, response-conversion rollback and item-lock ordering were each caught by the tests. An initial role mutation did not compile and was discarded; the subsequently tested compiling mutation bypassed the role check and returned 200 after demotion, failing the test.
- `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` completed.

## Limits

The differential and validation matrix is representative rather than exhaustive of all Pydantic coercions, UUID spellings, numeric extremes, malformed bodies, and combinations of writable fields. The status race models another transaction changing status; Go quotation send, conversion and status endpoints are not present yet, so races against those endpoints cannot be exercised. The tests do not prove coordination with Python writers, which do not take these locks. No external, production, or live services were used.

## Full verification

The final full-suite command was started after restoring mutation changes and regenerating sqlc:

```sh
PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:52677/billing_alembic_ref?sslmode=disable go test -count=1 ./...
```

Result pending. Earlier runs were interrupted to run mutations and add coverage. One subsequent run reported `TestWorkersDeliverDurableQueuesAcrossReplicas` failing with `queue state 1 1`, after which it was stopped and investigated.

That worker test counted external calls and then slept 50 ms before canceling the workers, even though the call counter increments before the database completion. Adding a 200 ms delay after the provider call counter reproduced the identical failure. The test now waits for the durable queue state before canceling. With the injected delay, the corrected test passed three repetitions; after removing the delay it passed ten repetitions. The full suite was restarted on the corrected final source.

## Recovery verification (2026-11-06)

The interrupted differential test was restored from the recovery copy and formatted after the WSL filesystem became writable. The full `go test -count=1 ./...` suite passed against an isolated Alembic-head PostgreSQL 16 reference: platform 378.133s, migrations 45.390s, and all other packages passed or had no tests. The final task 75 implementation still requires independent review.
