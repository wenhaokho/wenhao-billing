# backend (Go)

The billing API. It replaced the FastAPI backend, which was removed on 2026-11-11 (the
last commit with `backend-python/` is e7499a1). Design and rationale:
[`docs/superpowers/specs/2026-11-08-go-backend-design.md`](../docs/superpowers/specs/2026-11-08-go-backend-design.md).
From the port, kept as history: the route and test inventories [`ROUTES.md`](ROUTES.md)
and [`TESTS.md`](TESTS.md), and [`PORT-NOTES.md`](PORT-NOTES.md), the Python behaviours
the port kept or changed and why (Go comments cite its entries).

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
why next to the test. `./dev test` runs `-p 8 -parallel 8 -timeout 180s` (plus `-race` with `GO_TEST_RACE=1`).
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
guard, the frontend build and its return_to check, then the Go checks: lint, generated
code current, and `go test` (`GO_TEST_RACE=1` adds `-race`). It **fails if any test is skipped**.

## The API binary

`cmd/api` is one binary with subcommands; configuration comes from the environment
(`DATABASE_URL`, `SESSION_SECRET` of at least 32 bytes, `ENVIRONMENT`, `CORS_ORIGINS`,
`APP_BASE_URL`, `PORT`).
The FX settings are optional: `BASE_CURRENCY` (default `IDR`; set but blank is refused),
`FX_PROVIDER_BASE_URL` (default `https://api.frankfurter.dev/v1`),
`FX_PROVIDER_TIMEOUT_SECONDS` (default `15`) and `FX_SYNC_CURRENCIES` (default `USD,SGD`;
3-letter codes, comma separated).

- `api` (no subcommand) serves. A database with `alembic_version` and no
  `goose_db_version` is adopted first (as `api adopt-baseline` does, with all its checks;
  on drift it stops with the differences and writes nothing), then the Go (goose)
  migrations are applied (PORT-NOTES 170).
- `api adopt-baseline` hands an Alembic-managed database to goose. It first checks that
  the Alembic head is the frozen one and that the schema matches the baseline exactly,
  and refuses otherwise. Serve does this itself; the command remains for checking a copy.
- `api set-password <email>` sets a user's password (read from stdin) and bumps their
  session epoch, signing out existing sessions.
- `api issue-session <email>` prints a session cookie value for an admin, for scripts
  against a running API.
- `api healthcheck` and `api openapi` are for the container healthcheck and `./dev openapi`.

The dev stack's `backend` service (root `docker-compose.yml`) is this binary, serving the
dev `billing` database on :8000. Run a subcommand with it, from the repo root:

```
docker compose run --rm -T backend issue-session admin@example.com
printf '%s
' 'new password' | docker compose run --rm -T backend set-password admin@example.com
```

Both need the user to exist: there is no command or seed that creates the first user.

## The baseline

`migrations/00001_baseline.sql` is the schema the Python backend left at Alembic head
`0028_business_npwp`, and `migrations/testdata/alembic-0028.*` describe it. They were
generated from the Alembic migrations by `port/make-baseline.sh`, removed with the Python
backend, and are now fixed: never edit them. `adopt` still checks any Alembic-built
database against them before handing it to goose. Schema changes are new goose
migrations after the baseline.
