# Go backend replacement handoff

The Go backend in `backend-go/` is a work in progress. The Python backend remains the application of record. Do not deploy the Go backend or remove Python yet.

The implementation plan and inventories are in `docs/superpowers/plans/2026-11-05-go-backend-replacement.md`, `2026-11-05-go-contract-inventory.md`, and `2026-11-05-go-openapi-inventory.md`. The approved design is `docs/superpowers/specs/2026-11-05-go-backend-design.md`. The historical task ledger and task reports in `session-notes/` record test evidence and review findings. They describe the state at each task, not necessarily the latest state of the application.

## Resume here

1. Finish quotation update (task 75). The worktree entered emergency read-only mode during its final test edit. `backend-go/internal/platform/quotation_update_diff_test.go` was restored from the recovery copy and formatted. The full Go suite passed after recovery; task 75 still needs independent review. The task 75 report records the earlier checks; it predates the recovery and the final position-overflow case.
2. Fix the open task 74 review finding: `invoice_create.go` sorts item UUIDs by the submitted string. Equivalent UUID spellings can invert the order relative to quotation and bill operations and cause a database deadlock. Audit all item-lock paths, canonicalize and deduplicate UUID values before sorting, and test actual cross-endpoint races. See the end of `session-notes/progress.md`.
3. Continue the plan until all Python APIs, financial workflows, integrations, MCP/OAuth, jobs, documents, migrations, deployment configuration and acceptance checks have Go equivalents. Do not treat the presence of a route as proof of parity.

## Current implementation

`backend-go/` contains the Go API, sqlc queries, PostgreSQL migrations and tests. It includes session authentication, item/customer/vendor/account/project APIs, portions of invoice, bill and quotation workflows, payment reads and manual invoice payments, recurring generation, scheduled usage and FX scans, and hosting enforcement. The implementation has not received a final whole-branch review.

The recurring draft and hosting workers have durable queues and at-least-once delivery. Hosting actions can require manual review after an uncertain provider response or a target change. The usage cutoff job has no configured metering source and must not invent an amount. See `backend-go/DELIVERY_WORKERS.md` and task reports 60–73 for the tested behavior and limits. Do not contact a live email or Cloudflare provider during tests.

Manual invoice payment currently follows Python's behavior of not creating ledger entries. This conflicts with the repository's stated double-entry architecture and requires a reviewed decision before cutover. The payment/hosting paths and scheduler behavior require a final integration review, including races and side effects.

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

Regenerate SQL after changing queries with `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate`. Test the restored quotation update first with `go test -count=1 ./internal/platform -run '^TestQuotationUpdate'` using the same environment.

The full Go suite passed after filesystem recovery on the staged code: platform 378.133s, migrations 45.390s, and all other packages passed or had no tests. Task 75 has not yet received independent review. A worker integration test previously failed because it cancelled workers before their database writes completed; its test now waits for durable queue state and passed ten focused repetitions and the recovered full run.

## Workspace and disk usage

The work was performed in the detached worktree at `/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing`. The original checkout is separate. The ignored `.superpowers/sdd/` directory contains generated cumulative patch snapshots and historical reports; the reports and ledger have been copied into `session-notes/` for continuity. Do not rely on the ignored directory being present in a new checkout.

During the last session, the WSL root filesystem entered `emergency_ro` and returned I/O errors. A corrected copy of the interrupted test was saved on the Windows filesystem and restored after the workspace became writable. If I/O errors recur, stop writing and repair WSL before continuing.
