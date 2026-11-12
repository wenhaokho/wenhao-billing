# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Stack

Single-tenant agency accounting system.

- **Backend:** Go API in `backend/` (chi + huma, pgx + sqlc, goose migrations, in-process scheduler), PostgreSQL 18 (production runs 18; the dev `db` and the Go tests' `db-test` use `postgres:18-alpine`). One binary, `backend/cmd/api`; each feature is a package under `backend/internal/<feature>/` (`auth`, `invoices`, `payments`, `ledger`, `matching`, `fx`, `jobs`, `mcp`, `oauth`, ...) with its SQL in `queries.sql` and the sqlc-generated code in `<feature>db/`; shared plumbing (config, sessions, server, clock) is in `internal/platform`. See `backend/README.md` and `docs/superpowers/specs/2026-11-08-go-backend-design.md`. The Python (FastAPI) backend it replaced was removed on 2026-11-11 (last commit with `backend-python/`: e7499a1).
- **Frontend:** Vue 3 + TypeScript + Vite, Pinia, vue-router, PrimeVue, @tanstack/vue-query, axios. Source under `frontend/src/` (`views`, `components`, `stores`, `composables`, `api`, `router`).
- **Infra:** `docker-compose.yml` at repo root runs `db`, `backend` (the Go API built from `backend/`; it migrates the dev `billing` database at start-up, scheduler off unless `SCHEDULER_ENABLED=true`) and `frontend` (vite dev server). The `tools` profile adds `go` (the toolchain `backend/dev` runs in) and `db-test` (throwaway Postgres for the Go tests). The `e2e` profile adds `backend-e2e` and `frontend-e2e` on the `billing_e2e` database, which `bash scripts/e2e.sh` (the Playwright suite in `frontend/e2e/`) recreates every run, so e2e data never lands in `billing`; specs seed what they need through the API. There is no separate worker or Redis.
- **Production (Coolify):** `docker-compose.coolify.yml` runs two containers: `wenhao-billing-api`, the Go API built from `backend/Dockerfile` (distroless, `ENTRYPOINT /api`; at start-up it adopts an Alembic-only database, refusing on schema drift, then applies its goose migrations; scheduler on only when `GO_SCHEDULER_ENABLED=true`), and `wenhao-billing-web` from `frontend/Dockerfile.prod` (nginx serving the SPA). Only the web container is public; its `frontend/nginx.conf.template` proxies `/api`, `/mcp`, `/oauth`, `/.well-known`, `/docs`, `/redoc` and `/openapi.json` to the API (resolved per request through Docker's DNS), so the browser sees one origin. Add new top-level backend paths there too. The cut-over from Python is recorded in `docs/runbooks/go-cutover.md`.

## Common commands

Run the full stack (from repo root):
```
docker compose up --build
```
Frontend: http://localhost:5173 · Backend: http://localhost:8000 · API prefix: `/api/v1`. The Go backend has no live reload: after a backend change, `docker compose up -d --build backend`.

Backend: only Docker is needed; run from `backend/` with `COMPOSE_PROJECT_NAME=wenhao-billing` set (worktrees share the one dev stack):
- Tests: `./dev test [packages]` (default `./...`; `-p 8 -parallel 8 -timeout 180s`, `-race` with `GO_TEST_RACE=1`). Single test: `./dev test ./internal/matching -run TestName`.
- Lint / format: `./dev lint`, `./dev fmt` (golangci-lint).
- After changing `internal/*/queries.sql` or a migration: `./dev sqlc`. After changing routes or schemas: `./dev openapi` (the frontend's types are generated from `backend/openapi.json`). `./dev generated` fails if either is stale.
- Any go command: `./dev go <args>`; vendored deps: `./dev vendor` (needs `gh auth login`; gokit is private).
- Migrations: goose SQL files in `backend/migrations/` (`000NN_<name>.sql`, `-- +goose Up` / `-- +goose Down`), embedded in the binary and applied by `api` (serve) at start-up under a lock. `00001_baseline.sql` is the schema the Python backend left (Alembic head `0028_business_npwp`); never edit it or the `testdata/alembic-0028.*` snapshots: `adopt` checks an Alembic-built database against them.
- Tests **require a real Postgres**: `PGTESTDB_URL` (set by the `go` service) points at `db-test`, and each test gets its own database cloned from a migrated template. No DB mocks. Without it every database test skips, and the gate fails on any skip. Every test calls `t.Parallel()` (lint enforces it) unless it changes package state other tests read, and says why.
- The full gate (GitHub Actions is off): `bash scripts/check.sh` from the repo root: compose paths, frontend build, the return_to check, Go lint, generated code current, `go test` with no skips.

Admin commands of the API binary (in the dev stack: `docker compose run --rm -T backend <command>`, or `-i` to type on stdin):
- `set-password <email>` sets an existing user's password (one line on stdin) and signs out their sessions.
- `issue-session <email>` prints a session cookie value for an existing admin, for scripts.
- `adopt-baseline`, `backpost-ledger [--apply]`, `healthcheck`, `openapi`: see `backend/README.md`.

There is no command or seed that creates the first user: both commands above need the user to exist already. A dev database built by the Python backend keeps its users; a fresh one has none (a known gap).

Frontend (inside `frontend/`):
- Dev: `npm run dev`
- Typecheck + build: `npm run build` (runs `vue-tsc -b && vite build`)
- Preview production build: `npm run preview`

## Architecture

**Multi-currency ledger is the center of gravity.** All financial amounts are `DECIMAL(19,4)` (`shopspring/decimal` in Go, never floats; lint forbids `float64` outside marked non-money uses). Every transaction posts to a double-entry ledger via `backend/internal/ledger` (chart-of-account codes in `ledger.go`); currency is fixed at invoice creation and webhooks/email-ingested payments whose currency doesn't match are **rejected** (never auto-converted). FX rates live in `backend/internal/fx` (provider sync in `sync.go`); cross-currency payment matching produces realized FX gain/loss postings (accounts 4900 FX gain and 5900 FX loss, `ledger.go`).

**Payment reconciliation pipeline:** ingestion via `backend/internal/payments` (`intake.go`, `intake_routes.go`: webhook + inbound email) → `backend/internal/matching` (`engine.go`) matches on (amount, currency, payer, date). Only ≥95% confidence auto-reconciles. **Any ambiguity or amount mismatch must set `PENDING_MANUAL_REVIEW`** — never guess or adjust amounts. Manual resolution lives in `ManualReviewView.vue` / `payments/recon_routes.go`. Every automated action must be manually reversible.

**Invoicing modes** (unified form — billing mode lives on the invoice, not the customer): milestone (manual), recurring (driven by `cycle_trigger_date`; the scheduler's recurring scan in `backend/internal/jobs/jobs.go` calls `invoices/scan.go`, cycles in `invoices/schedule/`), and usage-based (triggered by usage-lock at `cut_off_day`, see `invoices/usage.go` and `UsageLockScan` in `jobs/jobs.go`). Generated invoices land in an "Awaiting Finalization" queue before issue.

**Auth:** signed cookie sessions (`backend/internal/platform`, with a per-user session epoch so a password change signs every session out) + argon2id password hashing (a Python-era bcrypt hash never matches; that user gets back in through `set-password` or the reset flow). Routes in `backend/internal/auth` (`routes.go`: login/logout/forgot/reset; `users.go`: admin CRUD + invite). Frontend guards in `router/index.ts` via `useAuthStore`. The MCP connector (`internal/mcp`) authenticates through the OAuth server in `internal/oauth`.

**Business-id scoping:** the PRD mandates every SELECT/INSERT/UPDATE filter by `business_id`. When adding new queries, preserve this — don't introduce unscoped reads.

**Scheduled jobs:** `backend/internal/jobs` runs the jobs (recurring scan, usage lock, hosting enforcement, FX sync) on an in-process scheduler started by `api` serve when `SCHEDULER_ENABLED=true` (production: `GO_SCHEDULER_ENABLED`). Each job has a daily (or weekly) UTC slot, runs once per slot (`scheduled_job_runs`), and holds a Postgres advisory lock keyed on its id so several replicas never run a job twice concurrently.

## Conventions and gotchas

- **Migrations are mandatory when the schema changes.** Adding a column without a migration has hung list views in the past (e.g. the `active` column). Add the goose migration in the same change, run `./dev sqlc`, and rebuild `backend` (which applies it) before testing UI that depends on the new column.
- **UI disable vs. remove:** when asked to disable/readonly a field, set the disabled attribute — don't remove rows or restructure layout unless explicitly requested.
- **List views use TabView for status filtering** (see Bills/Projects/Invoices views); keep that pattern when adding new list screens.
- **API client** is centralized in `frontend/src/api/` with axios; use `@tanstack/vue-query` for data fetching rather than ad-hoc calls in components.
- Vite proxies `/api` to `VITE_API_PROXY_TARGET` (set to `http://backend:8000` in compose).
- Go code comments cite Python file:line and `PORT-NOTES.md` entries from the port; those paths refer to git history (before 2026-11-11).

## Reference

`prd.md` at repo root is authoritative for product rules (currency handling, safe-stop reconciliation, chart of accounts structure, manual-override requirement). `SQLSchema.sql` is an initial schema sketch — the live schema is the goose migrations in `backend/migrations/`.
