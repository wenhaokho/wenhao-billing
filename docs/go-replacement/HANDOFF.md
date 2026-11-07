# Go backend replacement handoff

The Go backend in `backend-go/` is a work in progress. The Python backend remains the application of record. Do not deploy the Go backend or remove Python yet.

The implementation plan and inventories are in `docs/superpowers/plans/2026-11-05-go-backend-replacement.md`, `2026-11-05-go-contract-inventory.md`, and `2026-11-05-go-openapi-inventory.md`. The approved design is `docs/superpowers/specs/2026-11-05-go-backend-design.md`. The historical task ledger and task reports in `session-notes/` record test evidence and review findings. They describe the state at each task, not necessarily the latest state of the application.

## Resume here

Development was paused at the user's request on 2026-11-06. The worktree is `/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing`, branch `codex/billing-go-replacement`. The last commit is `58c11f6`; all subsequent work is **uncommitted**. Preserve the working tree, including untracked files. Do not deploy or delete Python.

The user chose to add `business_id` to the schema and scope all Go queries. Tasks 1 and 2 of `docs/go-replacement/BUSINESS-SCOPE-PLAN.md` have passed independent review: identity and inventory, then the staged migration 8 and its PostgreSQL data-preservation and cross-business constraint tests. Task 3 remains in progress. Tasks 4–10 have partial staged work, but are not complete. The active API and scheduler still use unscoped v7; the v8 router, migration and scoped workers are **not connected to production startup**. Read `docs/go-replacement/session-notes/task-3-report.md` for the current staged routes, workers, tests, and limitations; it is tracked, unlike `.superpowers/sdd/` scratch files.

1. Resume automatic reconciliation in stages. The staged writer fence has passed scoped review: it locks the configured business row before candidate-affecting writers. Intake still creates only `PENDING_MANUAL_REVIEW` payments. Add candidate loading and scoring under the same fence in a READ COMMITTED transaction, still hold payments initially, and test a complete candidate set, ambiguity, strict amount equality, isolation and real concurrent writers. Then implement atomic auto-clear and exact invoice-relief provenance with reversal tests. Do not turn on auto-clear or v8 startup until every writer participates and the required writer fence is enforceable operationally.
2. Finish scoping and integrating all Go handlers, authentication and administrative commands, ledger/FX operations, workers and external effects. Review the SQL inventory and add the semantic coverage gate and complete two-business matrix. The named v8 SQL and many isolated routes exist, but the main runtime and worker startup still use v7.
3. Implement exact v8 admission and trusted `BUSINESS_ID` configuration only after the scoped runtime is complete. Provide and rehearse the fenced legacy-data migration, credential/session plan, backup and rollback including writes after cutover. Complete the remaining API, financial, integrations, MCP/OAuth and acceptance work before replacing Python.

The candidate fence is cooperative: Python, v7, direct SQL and maintenance tools do not participate. Scoped invoice send can hold it across the provider call, so other same-business writers may hit the five-second fence wait. Any future automatic selection must acquire the fence first, use READ COMMITTED, then read a complete candidate set in a separate statement. Never use a partial or `SKIP LOCKED` candidate set to justify automatic clearing.

## Current implementation

`backend-go/` contains the Go API, sqlc queries, PostgreSQL migrations and tests. It includes session authentication, item/customer/vendor/account/project APIs, invoice PDF, sending and payment receipt delivery, and portions of invoice and bill workflows, quotation creation, reads, update, draft deletion, status transitions, conversion to invoice, PDF rendering and synchronous email sending, payment reads and manual invoice payments, recurring generation, scheduled usage and FX scans, and hosting enforcement. The implementation has not received a final whole-branch review.

Quotation, invoice and receipt email are synchronous and have an unavoidable provider-acceptance/commit ambiguity; see `docs/go-replacement/quotation-send-decision.md`. The PDF renderer uses a safe subset of SVG and its embedded font does not cover all scripts. Remote logo URLs can cause server-side requests to internal destinations; restrict and test network access before cutover.

The trial balance, balance sheet, FX revaluation, and AR/AP aging reports are wired to the isolated v8 router. The staged invoice summary uses current invoice balances in their original currencies and preserves Python’s per-payment days-to-pay calculation, including its 90-day lower bound without a future-date cutoff. The staged monthly revenue report groups exact invoice amounts and balances into 12 calendar months and converts them using the business’s current direct rates. Missing rates leave native amounts visible and mark the affected currencies as unconverted. The staged dashboard reads counts, AR, quotations, reconciliation activity, recurring revenue and upcoming invoices in one read-only snapshot. It rejects broken references in visible activity and upcoming rows and uses stored invoice amounts for usage estimates. Aging accepts only the server’s current date. The schema cannot reconstruct historical document balances and statuses, and Python applies the requested date to due-date buckets while reading current balances. FX revaluation uses each business’s direct rates effective on the report date and flags currencies with no eligible rate. The balance sheet excludes future postings that Python incorrectly includes in historical totals. The main runtime remains on v7. The matching, intake parser, aging and FX revaluation packages remain pure. Matching safe-stops ambiguous and non-exact matches.

The recurring draft and hosting workers have durable queues and at-least-once delivery. Hosting actions can require manual review after an uncertain provider response or a target change. The isolated v8 usage cutoff scan requires an explicit business and an injected meter. It rechecks cutoff eligibility and linked records under transaction locks before reading the amount. Both Go usage scans reject meter amounts outside DECIMAL(19,4) to keep the stored base, withholding and total consistent. The application still has no configured meter, and the staged worker has no startup registration. See `backend-go/DELIVERY_WORKERS.md` and task reports 60–73 for the tested behavior and limits. Do not contact a live email or Cloudflare provider during tests.

The isolated v8 hosting scan and restore consumer take an explicit business ID and a fake provider. They share a durable per-item lease, recheck target and financial eligibility before and after delivery, and retain uncertain work for recovery or manual review. Production still needs a business-authorized physical DNS target check, a writer fence, and startup registration.

The isolated v8 router supports manual payment review and reversal. Approval posts the stored payment, changes the invoice, and records the audit and any hosting restore intent in one transaction. Reversal restores the original invoice-currency AR credit. Python restores the payment face amount plus withholding, which can produce a different balance after cross-currency settlement. Staged same-currency approvals mark their exact invoice relief on the original journal. Historical same-currency payments without that evidence require review because Python could clamp an overpayment or discard a rounding remainder. Claimed hosting restore intents and unresolved hosting operations on affected items require resolution before reversal.

Manual invoice payment currently follows Python's behavior of not creating ledger entries. This conflicts with the repository's stated double-entry architecture and requires a reviewed decision before cutover. The payment/hosting paths and scheduler behavior require a final integration review, including races and side effects.

Bill record-payment also only changes balance and status: it creates no payment or journal entries, ignores the extra wht_amount field, and cannot deduplicate retries of partial payments. This requires a reviewed accounting and idempotency design before cutover; the existing Go endpoint follows Python rather than resolving these discrepancies. The PRD requires business_id isolation. Python and the active Go v7 schema lack these columns, and Python's trial-balance query has no business filter. The user chose explicit ownership; migration 8 and an isolated scoped router are under development, but the main runtime still uses v7 and unscoped queries. The staged trial balance and other scoped reports are available through the isolated v8 router only. See `docs/go-replacement/BUSINESS-SCOPE-PLAN.md`. Python's matching engine picks the first highest-scoring candidate and can auto-clear a tie, contrary to the PRD safe-stop for ambiguity. A pure exact-match predicate is not an authorization to auto-clear; candidate uniqueness and the confidence gate must be enforced in any future reconciliation integration. The predicate accepts only three ASCII currency letters; legacy Unicode currency values can uppercase differently in Python and Go and are rejected for manual review.

## Database and cutover

Fresh Go databases use Goose migrations 1–7. Startup verifies the exact PostgreSQL 16 schema and migration metadata before starting the listener and workers. The API does not migrate at startup. `AdoptAlembic` is intentionally disabled, and existing Go schema versions 1–6 cannot be upgraded through the current entry point. A safe adoption and upgrade process with an enforceable DDL fence, backup, restore and rollback rehearsal is required before using existing data. See `backend-go/migrations/README.md`.

The admission check is a startup snapshot, not a fence against later DDL. Python and Go writers do not all share the same locks; do not run both against the same live data without a verified cutover fence. The approved work did not include production changes.

Go sessions and password hashes differ from Python. The Go implementation uses Argon2id; existing Python bcrypt hashes and sessions are not carried over. Plan and test credential and session migration before cutover.

## Verification

The Go integration suite needs Go 1.27, PostgreSQL 16, Python 3.12 with the backend's development dependencies, and an isolated Alembic-head reference database. `PGTESTDB_URL` must point to that Python Alembic reference, not to a Goose-migrated database. Tests create disposable databases and must not point at production.

From `backend-go/`, after preparing those prerequisites:

```sh
PATH=/path/to/python-venv/bin:$PATH \
PYTHONPATH=/absolute/path/to/wenhao-billing/backend \
PGTESTDB_URL='postgres://USER:PASSWORD@HOST:PORT/ALEMBIC_REFERENCE?sslmode=disable' \
go test -count=1 ./...
```

Regenerate SQL after changing queries with `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate`. The quotation update can be checked with `go test -count=1 ./internal/platform -run '^TestQuotationUpdate'` using the same environment.

The most recent full `go test -count=1 ./...` run was started before the final candidate-fence changes. It failed only `TestSQLCStagingNamesAndArguments` because six newly added v8 query names were not yet classified; all other packages, including platform and migrations, passed in that run. The six names were then explicitly classified, the focused gate passed, and independent review confirmed the checks were not weakened. The fence migration fixture and focused scoped worker tests subsequently passed on PostgreSQL with the race detector; the strengthened fence negative control also passed. **A clean full-suite rerun on the final worktree is still required.** See the tracked task-3 report for exact commands and the latest per-slice results.

## Workspace and disk usage

The work is in the worktree at `/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing`. The original checkout is separate. The ignored `.superpowers/sdd/` directory contains generated scratch reports and review artifacts. The durable task 3 report is `docs/go-replacement/session-notes/task-3-report.md`; do not rely on ignored files being present in a new checkout. This worktree contains uncommitted tracked and untracked changes, so the commit alone does not contain the latest work.

During the last session, the WSL root filesystem entered `emergency_ro` and returned I/O errors. A corrected copy of the interrupted test was saved on the Windows filesystem and restored after the workspace became writable. If I/O errors recur, stop writing and repair WSL before continuing.

Before enabling any scoped live Cloudflare worker, verify that the configured provider credentials are authorized for the configured business and that every physical DNS zone and record targeted by that business is actually owned or authorized for it. Database `business_id` isolation alone does not establish provider-side ownership. Keep live provider effects disabled until that verification and the scoped worker effect-boundary tests are complete.

The isolated v8 router now serves the administrator-only payment audit and per-payment reconciliation log. The audit returns a generic 500 if a selected event has an unresolved required payment or a damaged non-null actor, invoice, or customer reference. Foreign and missing payment history requests both return an empty array. Clean responses were compared with v7 Go and Python on PostgreSQL. See the task 3 report for the isolation cases.

The v8 recurring scanner and draft outbox worker are callable from development tests. They use a business-specific job lock and scope every lease operation and live recipient or invoice check. API startup still uses the v7 worker entry points. See the delivery worker guide and task 3 report for retry behavior and test evidence.

The isolated v8 router serves administrator FX sync. The staged weekly FX job and manual rate writers share a business-specific transaction lock. The weekly job also excludes duplicate runs within the same business and uses one connection through fetch and commit. PostgreSQL tests use fake providers and cover A/B isolation, cancellation, and batch rollback. Runtime scheduling remains on v7.

Go FX sync now requires a valid provider response base matching the requested source currency and rejects the whole batch if that check fails. This applies to v7 and v8. Python still takes the source currency from the request.

The isolated v8 router accepts signed webhook and token-authenticated email payment intake. It atomically queues complete inputs for administrator review and deduplicates by business, source and external reference. Attachment OCR requires an injected adapter. Automatic clearing remains a cutover blocker until the intake matcher uses the staged candidate fence during selection and exact journal relief can be proven for reversal. See the task 3 report for PostgreSQL and Python HTTP coverage.

The staged invoice lifecycle, recurring and usage workers, invoice payment, manual reconciliation, invoice send, quotation conversion, and customer writes now take a business row fence before downstream locks. PostgreSQL tests cover writer ordering, cancellation, tenant isolation and the unfenced insertion control. See the task 3 report for the writer inventory. Any future auto-clearing transaction must acquire the same fence before querying candidates and hold it through commit. Webhook and email intake still queue payments for manual review.

The disposable test database used during this session ran in container `codex-billing-go-test-db` with a bind-mounted temporary data directory, not the preserved billing volumes. Its host port and the `/tmp/codex-billing-py312` virtual environment are temporary; inspect availability before reusing them. The user explicitly said to keep all Docker volumes. Do not prune or delete any volume.
