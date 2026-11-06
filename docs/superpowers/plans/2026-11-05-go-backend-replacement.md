# Go Backend Replacement Implementation Plan

> Execute the stages in order, preserving the existing working tree. Each stage must pass its checks before the next. This is a migration roadmap; expand each feature stage into exact route-level tests and interfaces after completing the contract inventory.

**Goal:** Replace the Python backend with a Go backend that preserves billing data, API behaviour, scheduled work and MCP capabilities.

**Architecture:** Build a Go application from the LOQATO template in a separate temporary directory while retaining Python as the behavioural reference. Adopt the existing database with a verified goose baseline, then replace the deployed backend after approval.

**Tech Stack:** Go, chi, Huma, pgx, sqlc, goose, Postgres, gokit, Maroto v2, Vue, Coolify.

**Spec:** `docs/superpowers/specs/2026-11-05-go-backend-design.md`.

## Global constraints

- Preserve the working tree and all production data. Ask before deployment, production configuration changes, or data deletion.
- Use exact decimal arithmetic and preserve accounting, currency and reconciliation invariants.
- Use real Postgres for database tests; verify guards with mutation tests.
- Preserve existing HTTP and MCP contracts. Reset the sole user's password and invalidate old sessions at cutover.
- Coolify builds both images; never put credentials into image layers.

## Review focus

- Baseline adoption must reject a database at the wrong Alembic revision or with schema drift.
- Concurrent payment and scheduled operations must not double-post, double-send or skip safeguards.
- Decimal rounding, non-IDR currencies, withholding and partial payments must preserve ledger balance.
- Tests and local development must not send live emails or accept live webhook traffic.
- Password reset and schema changes must have an actionable rollback procedure.

## Stage 1: Inventory and baseline evidence

**Files:** `backend/app/`, `backend/alembic/versions/`, `backend/tests/`, `frontend/src/`; create `docs/superpowers/plans/2026-11-05-go-contract-inventory.md`.

- [ ] Record every route, method, auth requirement, request/response, error shape, side effect, source location and existing test, including `/mcp`, OAuth, webhooks and frontend consumers.
- [ ] Record every scheduled job, lock key, schedule, timezone, external integration and configuration variable.
- [ ] Check the actual Alembic head and capture a schema-only baseline from a fresh migrated Postgres database; compare it with the schema of a sanitized existing database.
- [ ] Classify every existing test by feature and map it to a Go test. Record differences between PRD, repository instructions and live schema for explicit resolution.
- [ ] Verify the existing gokit/template versions, tags and CI status; document the exact revision used.

## Stage 2: Application foundation and database adoption

**Files:** new Go application tree, `db/migrations/`, `sqlc.yaml`, Go tests, Compose and CI configuration.

- [ ] Expand this stage into exact file and interface tasks using the inventory. Scaffold from the template without modifying or deleting Python or user changes.
- [ ] Write real-Postgres tests for fresh migration, verified existing-schema adoption, wrong revision, drift, repeated execution and concurrent migration attempts. Confirm expected failures before implementing the baseline and adoption command.
- [ ] Configure typed SQL, decimal types, configuration, logging, health and FastAPI-compatible errors and JSON serialization. Test each against captured contracts.
- [ ] Configure Go lint, race tests, sqlc verification and private module access with read-only secrets; run the complete foundation checks.

## Stage 3: Authentication and business data

**Files:** Go feature packages for users/auth, business profile, customers, vendors, items and projects, and their tests.

- [ ] Expand routes and exact tests from Stage 1, including error cases and authorization checks; prove each guard with mutation tests.
- [ ] Implement Argon2id auth, secure cookie sessions, remember-me and a password reset command that reads secrets without exposing them in arguments or logs.
- [ ] Port each CRUD feature and contract test using real Postgres. Verify Vue response formats and frontend build.

## Stage 4: Accounting and billing

**Files:** Go feature packages and tests for ledger, FX, invoices, quotations, bills, payments and reconciliation.

- [ ] Expand the financial test matrix from Python tests and PRD: decimal boundaries, balance, currencies, FX, withholding, zero/partial payments, duplicate requests, ambiguity and reversals.
- [ ] Port ledger and FX first, then billing lifecycle, payment intake and matching. Demonstrate guard mutations fail for the expected reason.
- [ ] Run differential checks against Python using isolated databases and disabled external side effects. Verify historical-data read compatibility.

## Stage 5: Integrations, documents and scheduled work

**Files:** Go packages and tests for email, webhooks, PDF, scheduled tasks and integrations.

- [ ] Map each provider configuration and side effect to a test; retain idempotency and authentication behaviour.
- [ ] Port each scheduler job with its timing, timezone and advisory lock; verify simultaneous runners and failures against real Postgres.
- [ ] Implement Maroto invoice, quotation and receipt PDFs; inspect representative long, multi-page, multi-currency and withholding outputs.
- [ ] Verify email and webhook behaviour without sending or accepting production traffic in tests.

## Stage 6: MCP and OAuth

**Files:** Go MCP and OAuth packages and tests.

- [ ] Map and implement every tool, schema, permission, discovery document and OAuth endpoint from the inventory, including confirmation for destructive actions and sends.
- [ ] Test registration, authorization, token expiry/revocation, error formats, confirmations and reconnect with an MCP client. Establish explicit token invalidation and migration behaviour.

## Stage 7: Release readiness and cutover

**Files:** Dockerfiles, Compose, CI and operational documentation.

- [ ] Run all contract, integration, race, lint, mutation and frontend checks; compare schema and behaviour on a sanitized restore and run browser and MCP checks against Go.
- [ ] Prepare and rehearse a backup, restore, baseline adoption, password reset and rollback procedure; account for Argon2id passwords and subsequent migrations.
- [ ] Prepare Coolify configuration and private build secrets for review. Ask for approval before deployment or production changes.
- [ ] After approval, deploy, verify browser, MCP, integrations and scheduled work, then measure idle RSS. Remove Python from the delivered application after acceptance, retaining Git history.

## Baseline evidence (2026-11-05)

- Worktree base `d394da7d403002af8c6bc58dea09b0a6307501df`; original checkout's tracked differences are line endings only (`git diff --ignore-space-at-eol --quiet` succeeded).
- Go 1.27.3 installed; archive SHA-256 verified against go.dev. `GOPRIVATE=github.com/LOQATO/*` configured for this Ubuntu user.
- Existing template remote `main` is `2e7c8c06e6574d38500eee8ccce5079eb31927f3`; gokit remote tags include `v0.1.0` and `v0.2.0`. Template currently pins `v0.1.0`; assess newer version before pinning.
- Python baseline on an isolated Postgres 16: 527 passed, 38 warnings, with both `DATABASE_URL` and `TEST_DATABASE_URL` set to the isolated database. Setting only the latter initially failed `test_mcp_tool_db_composes_with_client_http_commits`, which also reads `DATABASE_URL`.
- Fresh Alembic head is `0028_business_npwp`; 23 public base tables include `alembic_version`. Fresh schema dump is temporary evidence only; no existing production schema was inspected or baselined.
- FastAPI OpenAPI exposes 114 operations across 85 paths. See `2026-11-05-go-openapi-inventory.md`; mounted MCP tools require separate verification.
