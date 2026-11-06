# Task 58 report

Implemented the admin-only recurring template rows endpoint using a generated sqlc query and the reviewed recurring schedule package. It includes all templates in creation order, customer names, exact decimal strings, live child counts and previous cycle date, schedule details and status precedence. Reads do not write data. Invalid stored dates/timestamps and numbers return a safe 500; malformed cycle-key values fall back to the issue date.

Test-first: added the endpoint to `TestInvoiceReadsDifferential` and observed Go return 422 while Python returned 200 with rows. After implementation the test passed using separate real Postgres databases and the actual Python FastAPI HTTP endpoint. Fixtures cover four frequencies, all three end modes, pause and ended precedence, invalid schedules, missing and linked customers, multiple currencies and amounts, legacy invalid cycle key, child issue ordering, and VOID, template and nonrecurring exclusions. The test also checks authentication, no-store, OpenAPI response/security, unchanged database snapshots, empty rows, invalid numeric, infinite issue date, and non-string cycle key.

Independent mutations of child status filter, template ordering, ended status, and removal of the admin check each caused the differential test to fail; each was restored. An initial auth mutation that continued after writing the denial survived, so it was replaced with a complete bypass mutation that failed as intended. `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` regenerated the bindings. The restored targeted test passed.

Limits: the Python differential overrides its admin dependency, so authorization behavior is independently checked against the Go handler rather than compared to Python HTTP. Non-string malformed cycle keys are tested for safe Go behavior, not Python parity. Out-of-range DB date behavior is tested on Go; Python raises on some such inputs. The original two lateral child lookups per template were replaced after review; see the query-plan evidence below. Mutation coverage is targeted, not exhaustive.

A compact ISO cycle-key fixture subsequently exposed a mismatch (Python accepts `20240229`); the added test failed before the fix and passed after accepting compact dates. An ISO week-date fixture then exposed another mismatch; it failed before support was added and passed afterward. An earlier full-suite run was interrupted because it preceded the last change; the final stable run is recorded below.

A mixed-separator ISO week key was then added to the Python differential: it failed before the fix and passes after rejecting inconsistent separators, while the valid ISO-week fixture also passes. The final full suite was restarted after this change.

Final stable verification passed: `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:64392/billing_task2_clean?sslmode=disable go test -count=1 ./...`. Platform took 230.291s; all packages passed.

## Query efficiency follow-up

Replaced the correlated child lookups with a materialized eligible-child scan and window count/rank, joined to the templates. The child order remains `issue_date DESC NULLS LAST`, with no additional tie breaker, matching Python's unspecified order among equal dates. The JSON template id comparison remains text equality.

Test-first: `TestRecurringRowsSingleChildScan` used 100 templates and 20,000 generated children in real Postgres. Before the change `EXPLAIN (ANALYZE, FORMAT JSON)` reported two invoice scans with 100 loops each and the test failed. After the change, the child scan returned 18,182 eligible rows in one loop, and the template scan returned 100 rows in one loop. The test checks every result against independent counts and maximum issue dates. The actual Python HTTP differential and this plan test passed together after regenerating sqlc v1.29.0. No timing performance claim is made.

After the query rewrite, the exact full-suite command above passed again; `internal/platform` took 319.337s and every package passed.
