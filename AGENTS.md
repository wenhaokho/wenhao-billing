# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Stack

Single-tenant agency accounting system.

- **Backend:** FastAPI + SQLAlchemy 2.0 + Alembic, Celery (with beat) on Redis, Postgres 16. Package layout under `backend/app/` (`api/v1/routers`, `models`, `schemas`, `services`, `workers`, `db`).
- **Frontend:** Vue 3 + TypeScript + Vite, Pinia, vue-router, PrimeVue, @tanstack/vue-query, axios. Source under `frontend/src/` (`views`, `components`, `stores`, `composables`, `api`, `router`).
- **Infra:** `docker-compose.yml` at repo root runs db, redis, a one-shot `migrate` service (blocks `backend`/`worker` until `alembic upgrade head` succeeds), backend (uvicorn reload), worker (`celery -A app.workers.celery_app worker --beat`), and frontend (vite dev server).

## Common commands

Run the full stack (from repo root):
```
docker compose up --build
```
Frontend: http://localhost:5173 · Backend: http://localhost:8000 · API prefix: `/api/v1`.

Backend (inside `backend/`, or via `docker compose exec backend ...`):
- Install dev deps: `pip install -e .[dev]`
- Create migration: `alembic revision --autogenerate -m "<msg>"` — **revision id must be <32 chars** (Alembic `version_num` column limit).
- Apply migrations: `alembic upgrade head`
- Lint: `ruff check app`
- Run all tests: `TEST_DATABASE_URL=postgresql+psycopg://... pytest`
- Single test: `pytest tests/unit/test_matching_engine.py::test_name -x`
- Tests **require a real Postgres** (see `tests/conftest.py`) — they skip if `TEST_DATABASE_URL` is unset. No DB mocks; per-test transactional rollback; FX rates USD/SGD/EUR→IDR are auto-seeded.

Frontend (inside `frontend/`):
- Dev: `npm run dev`
- Typecheck + build: `npm run build` (runs `vue-tsc -b && vite build`)
- Preview production build: `npm run preview`

Seed scripts (in `backend/scripts/`, run via `docker compose exec backend python -m scripts.<name>`): `seed_admin`, `seed_dummy`, `seed_master_data`.

## Architecture

**Multi-currency ledger is the center of gravity.** All financial amounts are `DECIMAL(19,4)`. Every transaction posts to a double-entry ledger via `app/services/ledger.py`; currency is fixed at invoice creation and webhooks/email-ingested payments whose currency doesn't match are **rejected** (never auto-converted). FX rates live in `models/fx.py`; cross-currency payment matching produces realized FX gain/loss postings (see `0006_ledger_base_currency_and_fx` and `0008_fx_gain_loss_accounts`).

**Payment reconciliation pipeline:** ingestion via `services/intake/` (webhook + email OCR) → `services/matching_engine.py` matches on (amount, currency, payer, date). Only ≥95% confidence auto-reconciles. **Any ambiguity or amount mismatch must set `PENDING_MANUAL_REVIEW`** — never guess or adjust amounts. Manual resolution lives in `ManualReviewView.vue` / `recon.py` router. Every automated action must be manually reversible.

**Invoicing modes** (unified form — billing mode lives on the invoice, not the customer): milestone (manual), recurring (driven by `cycle_trigger_date`, Celery beat scans via `workers/tasks/recurring.py` and `services/recurring_schedule.py`), and usage-based (triggered by usage-lock at `cut_off_day`, see `workers/tasks/usage_lock.py`). Generated invoices land in an "Awaiting Finalization" queue before issue.

**Auth:** cookie session (Starlette `SessionMiddleware`) + password hashing via passlib/bcrypt. Routers: `auth.py` (login/logout/forgot/reset), `users.py` (admin CRUD + invite). Frontend guards in `router/index.ts` via `useAuthStore`.

**Business-id scoping:** the PRD mandates every SELECT/INSERT/UPDATE filter by `business_id`. When adding new queries, preserve this — don't introduce unscoped reads.

**Celery:** `app/workers/celery_app.py` with `beat_schedule.py`. The worker container runs worker+beat in one process (`--beat` flag). Tasks in `workers/tasks/`.

## Conventions and gotchas

- **Migrations are mandatory when models change.** Adding a column without a migration has hung list views in the past (e.g. the `active` column). Generate the migration in the same change and apply it before testing UI that depends on the new column.
- **Alembic revision IDs must be <32 chars** — longer IDs fail to insert into `alembic_version.version_num` and require manual `alembic stamp` recovery.
- **UI disable vs. remove:** when asked to disable/readonly a field, set the disabled attribute — don't remove rows or restructure layout unless explicitly requested.
- **List views use TabView for status filtering** (see Bills/Projects/Invoices views); keep that pattern when adding new list screens.
- **API client** is centralized in `frontend/src/api/` with axios; use `@tanstack/vue-query` for data fetching rather than ad-hoc calls in components.
- Vite proxies `/api` to `VITE_API_PROXY_TARGET` (set to `http://backend:8000` in compose).

## Reference

`prd.md` at repo root is authoritative for product rules (currency handling, safe-stop reconciliation, chart of accounts structure, manual-override requirement). `SQLSchema.sql` is an initial schema sketch — the live schema is Alembic-managed.
