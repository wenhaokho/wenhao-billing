# Replace the billing backend with Go

Status: draft for owner review. 2026-11-05.

## Intent and scope

Replace the FastAPI backend completely with a Go application based on the existing `github.com/LOQATO/go-api-template` and the pinned `github.com/LOQATO/gokit` module. This is a single-user internal accounting system. The finished deployment has no Python backend, Python sidecar, Alembic runtime, or WeasyPrint. Preserve the existing Postgres data and accounting behaviour, and keep the Vue application and the MCP connector usable.

The port covers all existing HTTP routes, including routes not currently used by Vue, incoming payment webhooks, all scheduled and background work, email, invoice/quotation/receipt PDFs, and the MCP server, its tools, OAuth and discovery endpoints. Inventory the actual checkout and record a method/path, authentication, request, response and test mapping for every endpoint and MCP tool in the implementation plan; do not treat an estimated count as an acceptance criterion. Preserve configuration-dependent behaviour and external integrations.

## Approach

The owner chose a complete replacement in preference to maintaining two production backends. Build the Go backend using the existing Python implementation and tests as behavioural references, then switch the application to Go. Retain the Python source as a local reference during development, and remove it from the delivered application after the replacement passes verification. Production continues to use its current backend until the owner authorizes deployment. No production data is deleted.

Use the default Go stack: chi and Huma, pgx and sqlc, goose migrations, shopspring decimal, Maroto v2, and the existing kit and template conventions. Add shared kit functionality only under its admission rule; app-specific compatibility belongs in this application. Pin dependencies and provide private-module access in CI and Coolify using read-only credentials passed as build secrets, never embedded in images. Coolify builds both application images on the shared VPS, as explicitly chosen by the owner; CI runs checks and does not publish images. Obtain approval before deployment or production configuration changes.

## Database and financial integrity

Adopt the existing Postgres schema without moving data. Create and verify a goose baseline corresponding to the actual Alembic head at implementation time, including constraints, indexes, defaults, types and extensions. Fresh databases apply the baseline; an existing database is marked as having the baseline only after verifying its Alembic revision and schema match. Baseline adoption must be explicit, transactional where possible, repeatable, and fail closed on mismatch; rehearse against a restored copy before production. Future changes use goose. Never automatically stamp an unknown schema.

Preserve exact decimal arithmetic, currency precision and rounding, balanced double-entry postings, FX gain/loss, and the reconciliation safe stop. Currency mismatches are rejected; ambiguous or mismatched payments require manual review and automated actions remain reversible. Preserve invoice lifecycle, recurring and usage rules, audit records and existing transaction boundaries. Follow the repository's business-id scoping instruction; resolve any discrepancy between the PRD and current schema explicitly in the implementation plan rather than inventing a tenant column or weakening isolation.

## API and authentication

Preserve paths, methods, status codes, JSON field names, list shapes, decimal strings, date formats, repeated query parameters and error shapes used by existing clients. Validate compatibility against the running Python API and existing tests; document any mechanical frontend edits before making them. Preserve `/api`, `/mcp`, `/oauth`, `/.well-known` and documentation routing through nginx.

The owner chose to reset the single user's password: Go stores Argon2id hashes; it need not verify legacy bcrypt hashes. Provide a secure one-off administrative password reset command that does not expose the password in command arguments or logs. Existing browser sessions are invalidated and the user signs in again. Preserve cookie security and remember-me behaviour. MCP OAuth clients may need to reconnect; specify and test token invalidation, registration and consent behaviour during implementation, without assuming Python and Go token formats are compatible. The connector must provide the full existing tool and confirmation behaviour at cutover.

## Documents, jobs and operations

Rebuild invoice, quotation and receipt PDFs in Maroto with the required business details, line items, totals and payment instructions; exact visual matching is not required. Review representative outputs, including long and multi-page documents, currency and withholding cases.

Port every scheduler job and its timing, timezone, failure handling and Postgres advisory-lock behaviour. Avoid duplicated jobs during verification; production jobs run from only the chosen active backend. Preserve webhook authentication, idempotency and safe error handling. Ensure externally sent email or payment effects cannot be triggered accidentally by parity tests.

Use a multi-stage Go image and a separate frontend image built by Coolify. Configure private module downloads using `GOPRIVATE` and scoped build secrets in CI and Coolify. Secrets and production configuration are supplied through existing deployment mechanisms. Measure actual idle RSS on the VPS after authorized deployment; approximately 30 MB is a goal, not a guaranteed result.

## Verification and cutover

Use real Postgres via pgtestdb for database tests; no database mocks. Port meaningful existing tests and add API contract and integration coverage for every route and MCP tool. For every guard, demonstrate that removing it makes an appropriate test fail for the expected reason, and record the evidence. Run race tests, lint, sqlc generation/verification, migration tests for fresh and existing databases, frontend build and browser flows. Verify accounting invariants and representative historical data on a sanitized restored database before considering production.

Before switching, prepare a reviewed backup and restore procedure, schema-adoption rehearsal, password-reset instructions, MCP reconnection instructions and rollback procedure. Ask the owner before deploying, changing production configuration or deleting data. After approval, deploy Go, validate browser and MCP workflows, scheduled jobs and integrations, and measure idle memory. Rollback must account for any schema or password changes; never assume Python can read Argon2id hashes. Remove Python runtime and source from the delivered application once acceptance is complete, preserving history in Git.

## Completion

The Go backend implements the inventoried behaviour; tests and CI are green for the application and any changed shared repositories; existing data is preserved; the Vue application and MCP work against Go; the owner has approved and performed or authorized the production switch; and idle memory has been measured. This design does not authorize deployment, production changes, data deletion or creation of repositories.
