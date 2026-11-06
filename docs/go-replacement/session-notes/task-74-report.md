# Task 74: Create quotation

Implemented POST `/api/v1/quotations` with the quotation input/output contract, Python-style date and numbering defaults, four-decimal line and aggregate rounding, discount calculation and nonnegative amount. It creates a DRAFT without invoice or ledger postings. A transaction locks the persisted admin, customer, project and sorted item references, and a transaction-scoped advisory lock serializes quotation number allocation. SQLC queries insert and reload the quote and lines; output conversion and JSON encoding precede commit. The request body is bounded and validation input is redacted. OpenAPI declares the endpoint, security and responses.

## Evidence

- The first real PostgreSQL endpoint test failed with 405 before implementation, then passed after implementation.
- `TestQuotationCreateDifferential` invokes the actual Python FastAPI router and Go endpoint against separate PostgreSQL databases. It compares response fields and errors after stripping nondeterministic identifiers/timestamps and redacting validation inputs, and checks failure counts. Cases include full fields and item/project references, omitted and null defaults, line-position coercion, multiple lines, IDR/USD/SGD, rounding and percentage/fixed discounts, zero clamping, missing/invalid fields, duplicate number, and missing customer/project/item references. Python's plain-text 500 and Go's redacted JSON 500 are compared by status and unchanged counts, not response body.
- PostgreSQL integration checks unique concurrent numbers, malformed and nonnumeric prefix suffixes, duplicate custom number, admin access, malformed/media errors, redaction, no-store and OpenAPI. Two distinct admins and customers demonstrably wait on a held allocator advisory lock before proceeding; an in-flight demotion waits on the user row and returns 403 after commit. Foreign key and injected line-insert failures and a trigger injecting an unserializable numeric roll back; snapshots compare all quotation, quotation-line, invoice and journal-entry rows.
- Restored-source mutation checks: disabling discount failed differential totals; incrementing number by two failed number scan; removing allocation lock failed the deterministic wait test; bypassing the persisted admin-role check failed the demotion race; committing after response conversion failure failed the snapshot. An attempted mutation committing after a SQL line error survived because PostgreSQL aborts the failed transaction; it is not counted as a killed mutation.
- `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` completed.

## Limits

The matrix does not exhaust all Pydantic coercions, string lengths, extreme numeric values or concurrent edits to every reference type. Related-row lock order is implemented and exercised by ordinary creation, but the tests do not deterministically race every referenced row. Python and Go use different error bodies for database-generated 500s. Mutations and tests do not establish behavior under Python and Go writers running simultaneously: Python does not take the Go allocation advisory lock. All databases and HTTP calls used by these tests are local and isolated.

## Final verification

From `backend-go` on the final source and tests:

```sh
PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:52677/billing_alembic_ref?sslmode=disable go test -count=1 ./...
```

All packages passed; `internal/platform` took 277.800s and `migrations` 34.564s. An earlier, subsequently interrupted suite run reported `TestWorkersDeliverDurableQueuesAcrossReplicas` failing with `queue state 1 1 <nil>` while multiple superseded suites were running concurrently. That test passed in the final full run, and a subsequent isolated `go test -count=10 ./cmd/api -run '^TestWorkersDeliverDurableQueuesAcrossReplicas$'` passed all ten repetitions in 4.992s. The test cancels workers 50ms after observing external call counts; the earlier concurrent load may have affected timing, but the cause was not established.

The expanded differential first exposed mismatches in empty-body and malformed-position 422 details; those cases failed before their fixes and pass on the final source. The snapshot includes journal lines as well as journal entries; an oversized-body test verifies the 413 response.

## Review follow-up

Quotation item references are now parsed, deduplicated by UUID bytes and locked in the same bytewise order as bill operations. A deterministic PostgreSQL test holds the lower UUID row, starts one request with lower-case low and upper-case high aliases (plus a duplicate), waits for its lock, starts another request with the opposite aliases, and probes the higher row with `FOR UPDATE NOWAIT` before releasing the lower row. Both requests then complete. Reverting to raw string sorting made the probe fail; the restored implementation passed three repetitions.

A position of 2147483648 now returns the Python endpoint's 500 status, `text/plain` body `Internal Server Error`, and leaves persisted rows unchanged. The handler detects the database integer overflow before beginning the write transaction, rather than relying on a database exception. This case first failed the actual Python HTTP differential with 422, and a restored-source mutation returning 422 again failed it. The response-body assertion was also red before the plain-text response was added.

The quotation and invoice allocators now recognize Unicode decimal digits in existing suffixes, including Arabic-Indic `١٢`, so the next suffix is `0013`. Non-decimal `isdigit` characters such as `²` cause the same 500 as Python's failed `int` conversion; a suffix with a non-digit such as `²x` is ignored. Separate real PostgreSQL databases and actual Python HTTP endpoints verify both quotation and invoice paths and failure rollback. The recurring invoice trigger also uses the shared parser because it allocates numbers under the same Python rule. Reinstating ASCII-only handling and ignoring the `isdigit`/`int` distinction separately failed both differentials and was restored.

The non-decimal digit table was checked against all code points in the test Python 3.12 Unicode 15 database; Go's Unicode decimal table and Python's Unicode version can differ for newly assigned characters. The body bound and existing rational-parser bounds remain in effect for extreme numeric inputs. Mixed Python and Go writers still do not share an allocation lock.

After the review fixes and OpenAPI addition, the full command in Final verification passed again on the final tree: all packages passed, `internal/platform` in 272.766s and `migrations` in 21.005s. The focused quotation and invoice differential tests also passed. The overflow-specific OpenAPI test failed before declaring `text/plain` for 500 responses and passed afterward.
