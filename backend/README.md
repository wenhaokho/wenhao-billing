# backend (Go)

The Go replacement for the frozen FastAPI backend in `backend-python/`. Design and rationale:
[`docs/superpowers/specs/2026-11-08-go-backend-design.md`](../docs/superpowers/specs/2026-11-08-go-backend-design.md).
Route and test inventories: [`ROUTES.md`](ROUTES.md), [`TESTS.md`](TESTS.md); behaviours
the port must keep: [`PORT-NOTES.md`](PORT-NOTES.md).

**The Python backend is frozen.** No feature, fix or migration lands in `backend-python/`
while the port is in progress; its Alembic head (`0028_business_npwp`) is the baseline
the Go migrations start from. A change that needs one waits for the port.

## Commands

Only Docker is needed. `./dev` runs Go in the root `docker-compose.yml` (service `go`,
profile `tools`), so it shares the dev stack's network and Postgres.

```
./dev test [packages]   go test -race (default ./...)
./dev lint | fmt        golangci-lint run | fmt
./dev sqlc              regenerate the sqlc code from internal/*/queries.sql
./dev openapi           write openapi.json (the frontend's generated types come from it)
./dev generated         fail if the sqlc code or openapi.json is stale
./dev vendor            update vendor/ (needs `gh auth login`; gokit is private)
./dev go <args>         any go command
```

Tests need Postgres: `PGTESTDB_URL` (set by the `go` service) points at the dev database
server, and each test gets its own database from a template. Without it every database
test skips.

## Checks

```
bash scripts/check.sh
```

From the repo root; GitHub Actions is off, so this is the gate. It runs the compose path
guard, the frozen Python tests, the frontend build, then the Go checks: lint, generated
code current, and `go test -race`. It **fails if any test is skipped**, Go or Python.

## The API binary

`cmd/api` is one binary with subcommands; configuration comes from the environment
(`DATABASE_URL`, `SESSION_SECRET` of at least 32 bytes, `ENVIRONMENT`, `CORS_ORIGINS`,
`APP_BASE_URL`, `PORT`).

- `api` (no subcommand) serves. It refuses a database Alembic still owns, then applies
  the Go (goose) migrations.
- `api adopt-baseline` hands an Alembic-managed database to goose. It first checks that
  the Alembic head is the frozen one and that the schema matches the baseline exactly,
  and refuses otherwise.
- `api set-password <email>` sets a user's password (read from stdin) and bumps their
  session epoch, signing out existing sessions.
- `api issue-session <email>` prints a session cookie value for an admin, for scripted
  comparison against the Python API.
- `api healthcheck` and `api openapi` are for the container healthcheck and `./dev openapi`.

For local comparison with the Python backend, `docker compose --profile tools up api-go`
serves the Go API on :8001. It migrates whatever database it points at, so point it at a
scratch copy (`GO_DATABASE_URL=postgresql://billing:billing@db:5432/<scratch>`), never at
the dev `billing` database. Build the scratch copy by running `alembic upgrade head`
against an empty database, then `docker compose --profile tools run --rm api-go adopt-baseline`.

## The baseline

`migrations/` holds the goose baseline, generated from the frozen Alembic head. It is
regenerated only if that head changes, which it does not while Python is frozen.
