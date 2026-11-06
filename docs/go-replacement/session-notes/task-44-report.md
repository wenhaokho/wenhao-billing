# Task 44: Quotation list and detail reads

Implemented GET /api/v1/quotations and /{quotation_id}, registered Huma security and response schemas, sqlc queries and batched line item serialization. Supports status, customer, project and limit query filtering, exact 4-place PostgreSQL decimal output, and safe nonfinite-number rejection. Negative limit is returned as Python's plain text 500, with both 500 media types documented.

Test-first: `TestQuotationReadsPostgres` failed on missing 404 routes before implementation. The test seeds separate real PostgreSQL databases, runs a real FastAPI TestClient against one and Go HTTP against the other; it compares all seven statuses, repeated status/customer, project filtering, invalid UUIDs and combined validation errors, zero/one/over-limit/invalid limit, optional fields, quotation lines, ordering, missing IDs, empty list, invalid numeric safety, auth, no-store, list schema/security, and full quotation/line snapshots. `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=$PWD/../backend PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./internal/platform -run '^TestQuotationReadsPostgres$'` passed (7.129s).

Red mutations in generated SQL, restored after each: customer predicate, project predicate, status predicate, and reverse ordering each failed the focused test. An initial project mutation (`project_id=project_id`) survived because NULL values were excluded; the replacement mutation adding OR TRUE failed as expected. `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` passed.

Final full suite passed: `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=$PWD/../backend PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./...`.

Limits: Quotation records do not expose JSON metadata; large JSON numeric cases are not applicable. Exhaustive Pydantic coercion/date/time cases, every optional field (particularly populated converted_invoice_id), tied ordering, negative limit HTTP differential, project auth variants, and full field-by-field schema assertions remain untested. No external systems accessed and no Python code edited.

Final full suite passed after adding populated sent/accepted timestamps and signer to Python differential: platform 184.281s, migrations 38.916s. Focused comparison passed 4.374s.
