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
profile `tools`), so it shares the dev stack's network.

```
./dev test [packages]   go test -parallel 8, -race if GO_TEST_RACE=1 (default ./...)
./dev lint | fmt        golangci-lint run | fmt
./dev sqlc              regenerate the sqlc code from internal/*/queries.sql
./dev openapi           write openapi.json (the frontend's generated types come from it)
./dev generated         fail if the sqlc code or openapi.json is stale
./dev vendor            update vendor/ (needs `gh auth login`; gokit is private)
./dev go <args>         any go command
```

Tests need Postgres: `PGTESTDB_URL` (set by the `go` service) points at `db-test`, a
throwaway server for tests only (tmpfs, fsync off; not the dev `db`), and each test gets
its own database from a template. Without it every database test skips.

Tests run in parallel: every test calls `t.Parallel()` unless it changes package state
that other tests read (a swapped function, a filled semaphore, a test hook); those say
why next to the test. `./dev test` runs `-p 8 -parallel 8 -timeout 60s` (plus `-race` with `GO_TEST_RACE=1`).
`-p 8` caps how many packages run at once: each running package keeps its test databases
on db-test's tmpfs (1.5 GB, see the comment on `db-test` in `docker-compose.yml`), and at
go's default (`-p` = the number of CPUs, 32 on the dev machine) a full run fills it and
Postgres stops with "No space left on device". Failed tests keep their databases too.
If a full local `./dev test ./...` still fails that way (most often while other runs
share db-test), restart db-test to clear the kept databases and pass `-p 2` (a later
`-p` overrides the default).

## Checks

```
bash scripts/check.sh
```

From the repo root; GitHub Actions is off, so this is the gate. It runs the compose path
guard, the frozen Python tests, the frontend build, then the Go checks: lint, generated
code current, and `go test` (`GO_TEST_RACE=1` adds `-race`). It **fails if any test is skipped**, Go or Python.

## The API binary

`cmd/api` is one binary with subcommands; configuration comes from the environment
(`DATABASE_URL`, `SESSION_SECRET` of at least 32 bytes, `ENVIRONMENT`, `CORS_ORIGINS`,
`APP_BASE_URL`, `PORT`).
The FX settings are optional: `BASE_CURRENCY` (default `IDR`; set but blank is refused),
`FX_PROVIDER_BASE_URL` (default `https://api.frankfurter.dev/v1`),
`FX_PROVIDER_TIMEOUT_SECONDS` (default `15`) and `FX_SYNC_CURRENCIES` (default `USD,SGD`;
3-letter codes, comma separated).

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

For local comparison with the Python backend, the `api-go` service serves the Go API on
:8001. It migrates whatever database it points at, so give it a scratch database, never
the dev `billing` one. Every command below sets `GO_DATABASE_URL`; unset, it points at a
nonexistent database and fails to connect.

```
export GO_DATABASE_URL=postgresql://billing:billing@db:5432/billing_go_check
docker compose exec -T db createdb -U billing billing_go_check
docker compose exec -T -e DATABASE_URL=postgresql+psycopg://billing:billing@db:5432/billing_go_check backend alembic upgrade head
GO_DATABASE_URL=$GO_DATABASE_URL docker compose --profile tools run --rm api-go adopt-baseline
GO_DATABASE_URL=$GO_DATABASE_URL docker compose --profile tools up -d api-go
# ... try http://localhost:8001/api/v1/auth/me, then:
docker compose --profile tools rm -sf api-go
docker compose exec -T db dropdb -U billing billing_go_check
```

## The baseline

`migrations/` holds the goose baseline, generated from the frozen Alembic head. It is
regenerated only if that head changes, which it does not while Python is frozen.
