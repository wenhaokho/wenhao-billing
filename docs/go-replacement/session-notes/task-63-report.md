# Task 63 — recurring template update

Implemented `PUT /api/v1/invoices/recurring-templates/{template_id}` using the template creation validator, recurring schedule domain, and existing invoice update calculations. The update locks and rechecks the admin, locks the template, and takes deterministic locks on referenced customer, project, and items before replacing template lines. It preserves the paused flag and overlays only supplied withholding fields; schedule, fields, totals and balance are updated in one transaction. The response is serialized before commit. Existing issue and due dates are preserved. The endpoint does not create invoices, ledger entries, or outbox events.

Verification uses Python's actual HTTP endpoint and separate real PostgreSQL databases. The differential covers 58 request cases including all four frequencies and end modes, references, validation, empty lines, multi-line rounding, and supplied, omitted, and null withholding overlays. Real-Postgres tests cover paused, ended, misconfigured, missing and non-template records, body bounds, redaction, no-store and OpenAPI; serialization and forced line-insert failure roll back, with invoice, line, journal, and outbox snapshots. Actual concurrent requests contend on the template row for update versus pause, trigger, and finalize; a disjoint template proceeds while another is locked. A demotion race verifies persisted authorization after waiting.

Test-first evidence: the initial valid update test returned 405 before implementation. Five temporary mutations were each detected and restored: disabling the ended guard returned 200 instead of 409; bypassing the persisted admin role check returned 200 after demotion; changing the computed amount failed the amount assertion; committing on response serialization failure changed the stored schedule; replacing the row lock with an unlocked read left only one request waiting. The restored focused tests passed. `sqlc` v1.29.0 generation completed from the locally cached module.

The full suite passed after the last code and test changes (platform 226.196s; migrations 18.518s):

```sh
PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:64392/billing_task2_clean?sslmode=disable go test -count=1 ./...
```

An earlier full suite passed, but preceded the final date-preservation change and tests. Several superseded runs were deliberately interrupted when those changes were added; their interrupt failures are not the final verification. The schedule/error combination matrix and concurrency tests are representative, not an exhaustive proof of every interleaving or malformed legacy record.
