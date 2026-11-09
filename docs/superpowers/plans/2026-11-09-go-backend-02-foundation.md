# Go backend, plan 2: foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go API in `backend/` that adopts the existing database, signs the owner in and out with Python's auth contract, and gives later plans the platform they port features onto: config, errors, JSON types, rounding, the session principal, the route and test inventories, and the Go checks.

**Architecture:** `backend/` starts as a copy of `LOQATO/go-api-template` (gokit v0.2.0, vendored), renamed to `github.com/wenhaokho/wenhao-billing/backend`, with the template's organizations, customers example and refresh-token principal removed. `migrations/00001_baseline.sql` is generated from the frozen Alembic head by a copy of Pintas's `make-baseline.sh`; `api adopt-baseline` stamps an existing database. `internal/platform` holds config, server wiring (RFC 9457 errors, CORS, no-store), JSON types, rounding and the cookie-session principal. `internal/auth` ports the six `/api/v1/auth/*` routes plus the CLI commands. Go runs inside the existing dev Compose project (no new Docker network).

**Tech Stack:** Go 1.27, chi v5, Huma v2.40, pgx v5, sqlc 1.31.1, goose v3, gokit v0.2.0 (`apierr`, `auth`, `db`, `observe`, `testkit`, `i18n`), shopspring/decimal, golangci-lint v2.15.0, Postgres 16, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` (sections "Where things live", "Behaviour that must be carried exactly", "Auth", "Database", "Tests", "Builds and deploys").

## Global Constraints

- Python in `backend-python/` is frozen: never edited. Its behaviour is the reference; anything odd goes to `backend/PORT-NOTES.md`, not into a fix.
- Errors: `apierr.ProblemJSON` (`application/problem+json`, `{"title","status","detail","code","errors"?}`). `detail` is **Python's exact sentence** where Python has one: `not authenticated`, `session expired`, `admin required`, `invalid credentials`, `current password is incorrect`, `new password must differ from current password`, `invalid or expired reset token`.
- English only: `platform.Catalog` has no Indonesian sentences (the kit falls back to the English template); do not run `i18ntest.Scan`.
- Unknown JSON request fields are accepted and ignored. No `$schema` member in responses.
- Every slice in a response encodes as `[]`, every optional field as `null` (never omitted).
- Money and other decimals: `decimal.Decimal` in Go; `float64` is refused by lint outside tests.
- Session cookie `billing_session`: HttpOnly, SameSite=Lax, path `/`, `Secure` when `ENVIRONMENT=production`. Lifetimes 8 h (`SESSION_MAX_AGE_SECONDS`, default 28800) or 30 days with remember (`SESSION_REMEMBER_MAX_AGE_SECONDS`, default 2592000).
- argon2id parameters: memory 19456 KiB, 2 iterations, parallelism 1, salt 16, key 32. Tests may use cheaper parameters through the same variable.
- Migrations after the baseline are **additive only** (Python runs beside them until the switch).
- Go runs only through Docker: `backend/dev` runs Go in the root Compose project's `go` service (profile `tools`), on the existing `wenhao-billing_default` network. Never create a new Compose project or prune other projects' networks.
- `gokit` comes from GitHub tag `v0.2.0`; never from `D:\Development\gokit`. Never edit `D:\Development\go-api-template` (someone else's uncommitted change); copy from a fresh `gh repo clone` into the scratchpad.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Branch `feat/go-foundation`; merging to `main` deploys nothing new (Coolify's compose does not reference `backend/` yet) but still waits for the owner's yes.

## Review Focus

- A Python bcrypt hash reaching the Go login must answer 401 `invalid credentials`, never 500 (the owner's hash is bcrypt until `set-password` runs): Task 7 `TestLoginWithBcryptHashIsInvalidCredentials`.
- Starting the Go API against the production database before `adopt-baseline` must refuse to start, not try to run the baseline over existing tables: Task 4 `TestCheckAdoptedRefusesAlembicDatabase`.
- A wrong-but-plausible `Accept-Language: id` must still get Python's English sentence, not an empty or template string: Task 5 `TestErrorDetailIgnoresIndonesian`.
- A login behind Cloudflare is throttled by the visitor's address, not by the proxy's: Task 7 `TestThrottleUsesCFConnectingIP` and `TestThrottleNeverLocksEmailAlone`.
- `CORS_ORIGINS` in Coolify is a JSON list (`'["https://billing.wenhao.id"]'`); a comma-split parser would produce `["https://billing.wenhao.id"]` with brackets and break CORS: Task 3 `TestConfigReadsJSONCorsOrigins`.

---

### Task 1: Scaffold `backend/` from the template

**Files:**
- Create: `backend/` (copied from the template at its `main` after PR #1 merged, i.e. `go.mod` naming `github.com/LOQATO/gokit v0.2.0`)
- Delete inside the copy: `internal/customers/`, `migrations/00001_organizations.sql`, `migrations/00002_customers.sql`, `compose.yaml`, `.github/`, `renovate.json`, `README.md` (rewritten in Task 9)
- Modify: `backend/go.mod` module line and every import; `backend/sqlc.yaml` (queries list emptied until Task 6); `backend/dev`
- Modify: `docker-compose.yml` (add service `go`); `scripts/check-compose-paths.sh` (see Step 4)

**Interfaces:**
- Produces: module `github.com/wenhaokho/wenhao-billing/backend`; `backend/dev` subcommands `test [pkgs]`, `lint`, `fmt`, `sqlc`, `openapi`, `generated`, `vendor [module@version]`, `go <args>`, used by every later task.
- Produces: Compose service `go` (image `golang:1.27-bookworm`, `profiles: ["tools"]`, `working_dir: /src`, volumes `./backend:/src`, named volumes `gomod:/go/pkg/mod` and `gobuild:/root/.cache/go-build`, env `CGO_ENABLED=1`, `GOFLAGS=-mod=vendor`, `PGTESTDB_URL=postgres://billing:billing@db:5432/postgres?sslmode=disable`, `depends_on: db: condition: service_healthy`).

- [ ] **Step 1: Precondition**

Run: `gh api repos/LOQATO/go-api-template/contents/go.mod -q .content | base64 -d | grep gokit`
Expected: `github.com/LOQATO/gokit v0.2.0`. If it still says v0.1.0, PR #1 is not merged: stop and ask the owner.

- [ ] **Step 2: Copy and rename**

Clone the template into the scratchpad (`gh repo clone LOQATO/go-api-template <scratch>/tpl`), copy everything except `.git` into `backend/`, delete the files listed above, and replace `github.com/LOQATO/go-api-template` with `github.com/wenhaokho/wenhao-billing/backend` in `go.mod` and all `.go` files. Remove `register`'s `customers.Register` call and the customers import from `cmd/api/main.go`.

- [ ] **Step 3: Point `backend/dev` at the root Compose project**

`run_go` becomes `docker compose -f ../docker-compose.yml --profile tools run --rm -T go "$@"`; `run` is removed (Task 9 adds an `api-go` service later plans use); `down` is removed (the root stack belongs to the dev environment). `lint`, `fmt`, `sqlc` keep their plain `docker run` (no network). `vendor` keeps its token handling.

- [ ] **Step 4: Make the compose path guard see profiled services**

`docker compose config` drops services whose profile is not active, so the guard would never check `./backend`. In `scripts/check-compose-paths.sh` pass `--profile '*'` to `docker compose config`.

Mutation: temporarily rename `backend/` to `backend-x/`; run `bash scripts/check-compose-paths.sh`.
Expected: exit 1 with `docker-compose.yml go: backend (missing)`. Restore the name; rerun: exit 0.

- [ ] **Step 5: It builds and its remaining tests pass**

Run: `cd backend && ./dev go build ./... && ./dev test`
Expected: build succeeds; `internal/platform` tests pass (the template's principal tests may fail once Task 6 replaces the principal; at this step they still pass unchanged).

- [ ] **Step 6: Commit**

```bash
git checkout -b feat/go-foundation main
git add backend docker-compose.yml scripts/check-compose-paths.sh
git commit -m "feat(go): backend/ from go-api-template (gokit v0.2.0), Go in the dev Compose project"
```

---

### Task 2: Baseline migration from the frozen Alembic head

**Files:**
- Create: `backend/port/make-baseline.sh`, `backend/port/clean-dump.pl` (copied from `D:\Development\pintas-go\backend\port\`, adapted), `backend/migrations/testdata/schema-snapshot.sql` (copied from Pintas), generated `backend/migrations/00001_baseline.sql`, `backend/migrations/testdata/alembic-0028.schema.txt`, `backend/migrations/testdata/alembic-0028.rows.txt`, `backend/migrations/testdata/rows-snapshot.sql`
- Test: `backend/migrations/baseline_test.go`

**Interfaces:**
- Produces: `migrations.FS` embedding `*.sql`; testdata files embedded through `migrations.Testdata` (`embed.FS` of `testdata/*`), used by Task 4.

- [ ] **Step 1: Adapt the script**

In the copy: `DB=wenhao-billing-db-1`, `BACKEND=wenhao-billing-backend-1`, `NETWORK=wenhao-billing_default`, `IMAGE=wenhao-billing-backend`, `HEAD=0028_business_npwp`, Postgres user `billing` (not `postgres`), scratch databases `wb_go_baseline` and `wb_go_baseline_goose`, Alembic run with `DATABASE_URL=postgresql+psycopg://billing:billing@db:5432/wb_go_baseline` and `-v "$root/backend-python:/app"` (no venv volume: the image installs into `/opt/venv`), file names `alembic-0028.*`. Comments name `backend-python/FROZEN.md`.

- [ ] **Step 2: Write the failing schema tests**

```go
// TestSchemaMatchesAlembic: a goose-migrated database (testkit.PostgresURL with
// only 00001 applied) runs testdata/schema-snapshot.sql and the output equals
// testdata/alembic-0028.schema.txt line for line.
// TestSeedRowsMatchAlembic: the same for rows-snapshot.sql vs alembic-0028.rows.txt
// (chart of accounts, FX gain/loss accounts, the business_profile singleton).
```
To apply only 00001, the test builds a sub-FS containing just `00001_baseline.sql`.

Run: `cd backend && ./dev test ./migrations/`
Expected: FAIL (`00001_baseline.sql` and the testdata do not exist yet).

- [ ] **Step 3: Generate**

Run: `bash backend/port/make-baseline.sh`
Expected: prints the seed tables, then `wrote migrations/00001_baseline.sql and migrations/testdata/alembic-0028.{schema,rows}.txt`; exits 0 (the script's own pg_dump and snapshot diffs passed).

- [ ] **Step 4: Tests pass**

Run: `cd backend && ./dev test ./migrations/`
Expected: PASS.

Mutation: delete one `CREATE INDEX` line from `00001_baseline.sql`; rerun. Expected: `TestSchemaMatchesAlembic` FAIL naming that index. Restore.

- [ ] **Step 5: Commit**

```bash
git add backend/port backend/migrations
git commit -m "feat(go): goose baseline equal to Alembic 0028_business_npwp, with schema and seed-row tests"
```

---

### Task 3: Config

**Files:**
- Modify: `backend/internal/platform/config.go`
- Test: `backend/internal/platform/config_test.go`

**Interfaces:**
- Produces:
```go
type Config struct {
    Environment            string   // ENVIRONMENT, default "development"
    DatabaseURL            string   // DATABASE_URL, required; "postgresql+psycopg://" and "postgres://" rewritten to "postgresql://"
    SessionSecret          string   // SESSION_SECRET, required, >= 32 bytes
    SessionMaxAge          time.Duration // SESSION_MAX_AGE_SECONDS, default 28800
    SessionRememberMaxAge  time.Duration // SESSION_REMEMBER_MAX_AGE_SECONDS, default 2592000
    CORSOrigins            []string // CORS_ORIGINS: a JSON list (Python's format) or comma-separated; default ["http://localhost:5173"]
    AppBaseURL             string   // APP_BASE_URL, default "http://localhost:5173"
    SentryDSN              string   // SENTRY_DSN
    SentryEnvironment      string   // SENTRY_ENVIRONMENT, default "development"
    SentryTracesSampleRate float64  // SENTRY_TRACES_SAMPLE_RATE, default 0 (not money; nolint justified)
    Port                   int      // PORT, default 8000
}
func LoadConfig() (Config, error)
func ParseConfig(vars map[string]string) (Config, error)
func (c Config) Production() bool // Environment == "production"
```
Later plans add their variables to this struct.

- [ ] **Step 1: Failing tests**

`TestConfigReadsJSONCorsOrigins` (`CORS_ORIGINS='["https://billing.wenhao.id"]'` → `[]string{"https://billing.wenhao.id"}`), `TestConfigReadsCommaCorsOrigins`, `TestConfigRewritesPsycopgURL` (`postgresql+psycopg://u:p@h:5432/d` → `postgresql://u:p@h:5432/d`), `TestConfigRefusesShortSessionSecret` (31 bytes → error naming `SESSION_SECRET`), `TestConfigRequiresDatabaseURL`, `TestConfigDefaults` (8h, 30d, port 8000).

Run: `./dev test ./internal/platform/ -run TestConfig` — Expected: FAIL.

- [ ] **Step 2: Implement, run, commit**

Run the same command — Expected: PASS. Commit `feat(go): config with Python's variable names`.

---

### Task 4: Adopting an existing database, and the first additive migrations

**Files:**
- Create: `backend/migrations/adopt.go`, `backend/migrations/00002_users_session_epoch.sql`, `backend/migrations/00003_login_attempts.sql`
- Modify: `backend/cmd/api/main.go` (subcommand `adopt-baseline`; `serve` calls `CheckAdopted` before `db.Migrate`)
- Test: `backend/migrations/adopt_test.go`

**Interfaces:**
- Produces:
```go
const AlembicHead = "0028_business_npwp"
var ErrNotAdopted = errors.New("database has the Alembic schema but no goose history: run `api adopt-baseline`")
func CheckAdopted(ctx context.Context, pool *pgxpool.Pool) error // nil when goose_db_version exists, or when neither alembic_version nor any baseline table exists (fresh database)
func Adopt(ctx context.Context, pool *pgxpool.Pool) error // one transaction under pg_advisory_xact_lock
```
- `00002`: `ALTER TABLE users ADD COLUMN session_epoch integer NOT NULL DEFAULT 0;`
- `00003`: `CREATE TABLE login_attempts (id bigserial PRIMARY KEY, email text NOT NULL, client_addr text NOT NULL, failed_at timestamptz NOT NULL DEFAULT now());` with indexes on `(email, client_addr, failed_at)` and `(client_addr, failed_at)`.
- Down sections drop what Up adds.

`Adopt` refuses unless: `goose_db_version` is absent or empty; `alembic_version.version_num = AlembicHead`; the output of `testdata/schema-snapshot.sql` equals `testdata/alembic-0028.schema.txt`. Then it creates `goose_db_version` exactly as goose v3 does and records versions 0 and 1 as applied. It never runs the baseline.

- [ ] **Step 1: Failing tests**

A helper `legacyDB(t)` builds an Alembic-shaped database: `testkit.EmptyPostgresURL`, execute the Up section of `00001_baseline.sql`, then `CREATE TABLE alembic_version (version_num varchar(32) PRIMARY KEY); INSERT … '0028_business_npwp'`.

- `TestAdoptThenMigrateAppliesOnlyNewMigrations`: Adopt, then `db.Migrate` → `users.session_epoch` exists, `login_attempts` exists, goose version is 3, and the baseline's seed rows are not duplicated (chart of accounts count unchanged).
- `TestAdoptRefusesWrongAlembicHead` (`0027_…` → error naming both versions).
- `TestAdoptRefusesSchemaDrift` (drop `customers.website` first → error, nothing written: `goose_db_version` absent afterwards).
- `TestAdoptRefusesAlreadyAdopted` (second call → error).
- `TestCheckAdoptedRefusesAlembicDatabase` (legacy DB → `ErrNotAdopted`), `TestCheckAdoptedAcceptsFreshDatabase`, `TestCheckAdoptedAcceptsGooseDatabase`.

Run: `./dev test ./migrations/` — Expected: FAIL (undefined `Adopt`).

- [ ] **Step 2: Implement, run**

Expected: PASS. Mutations, each reverted after watching it fail: remove the head check (wrong-head test red); remove the snapshot comparison (drift test red); remove the `CheckAdopted` call from `serve` and assert with `TestServeRefusesUnadoptedDatabase` in `cmd/api` (red).

- [ ] **Step 3: Commit** `feat(go): adopt-baseline for the existing database; session epoch and login attempts`

---

### Task 5: Platform: errors, server, JSON types, rounding

**Files:**
- Modify: `backend/internal/platform/server.go`, `backend/internal/platform/catalog.go`, `backend/internal/platform/types.go` (replace `Money`, `Page`)
- Create: `backend/internal/platform/round.go`
- Test: `backend/internal/platform/server_test.go`, `types_test.go`, `round_test.go`

**Interfaces:**
- Produces:
```go
func NewServer(cfg Config, deps Deps) (http.Handler, huma.API) // apierr.ProblemJSON; CORS from cfg.CORSOrigins with credentials, allowing sentry-trace and baggage headers; Cache-Control: no-store on /api/, /oauth/, /mcp, /.well-known/; unknown body fields allowed; no $schema; /docs and /openapi.json kept; recoverer answers a problem+json 500
type Deps struct { Pool *pgxpool.Pool; Logger *slog.Logger; Clock Clock } // Task 6 adds Sessions Sessions
type Clock interface { Now() time.Time }      // the only place time.Now is called
func Err(status int, code, detail string) error // apierr.New with i18n.T(detail): Python's sentence verbatim
// JSON types (each: MarshalJSON, UnmarshalJSON accepting a JSON number or string, huma Schema of a string with a pattern):
type Dec4 struct{ decimal.Decimal } // StringFixed(4): numeric(19,4) and computed money
type Dec8 struct{ decimal.Decimal } // StringFixed(8): numeric(19,8) FX rates
type Dec2 struct{ decimal.Decimal } // StringFixed(2): numeric(5,2) confidence
type Date struct{ time.Time }       // "YYYY-MM-DD"
type Time struct{ time.Time }       // UTC, "2006-01-02T15:04:05.999999Z"
type Optional[T any] struct { Set, Null bool; Value T } // PATCH: omitted → Set=false; null → Set, Null; value → Set, Value
func RoundHalfEven4(d decimal.Decimal) decimal.Decimal
func RoundHalfUp4(d decimal.Decimal) decimal.Decimal
func RoundWithholding(d decimal.Decimal, currency string) decimal.Decimal // HALF_UP, 0 places for IDR, 2 otherwise
```
Nullable fields are pointers (`*Dec4`, `*Date`, `*string`) with no `omitempty`.

- [ ] **Step 1: Failing tests** (all through a test Huma API with throwaway operations registered in the test)

- `TestErrorsAreProblemJSON`: a handler returning `Err(400, "invoice_voided", "cannot send a voided invoice")` → status 400, `Content-Type: application/problem+json`, body `{"title":"Bad Request","status":400,"detail":"cannot send a voided invoice","code":"invoice_voided"}`.
- `TestErrorDetailIgnoresIndonesian`: same with `Accept-Language: id` → same `detail`.
- `TestValidationIs422ProblemWithErrors`: missing required body field → 422, `detail` a sentence, `errors[0].location` names the field.
- `TestPanicIsProblem500`: a panicking handler → 500 problem body, `detail` contains no panic text.
- `TestUnknownBodyFieldsAreIgnored`: body `{"name":"x","receipt":null}` to an operation whose input has only `name` → 200.
- `TestNoSchemaMember`: no `$schema` in a 200 body.
- `TestNoStoreOnApiPaths`: `/api/v1/x` 200 and an unknown `/api/v1/nope` 404 both carry `Cache-Control: no-store` (ports `test_api_responses_are_no_store`, `test_api_404_is_no_store`).
- `TestCorsAllowsConfiguredOriginWithCredentials` and `TestCorsAllowsSentryHeaders` (preflight with `Access-Control-Request-Headers: sentry-trace, baggage`).
- `TestDec4MarshalsFourPlaces` (`1500` → `"1500.0000"`), `TestDec4AcceptsNumberAndString`, `TestDec8MarshalsEightPlaces`, `TestNullDecimalIsNull` (`*Dec4(nil)` → `null`, key present), `TestNilSliceField` (a response struct with a nil slice encodes `[]` — enforced by initialising slices in constructors; this test pins the convention on a sample type), `TestDateRoundTrip`, `TestTimeIsUTCWithZ` (`2026-01-01 10:00:00.123456 +07:00` → `"2026-01-01T03:00:00.123456Z"`), `TestOptionalThreeStates`.
- `TestRoundHalfEven4` (`1.00005`→`1.0000`, `1.00015`→`1.0002`), `TestRoundHalfUp4` (`1.00005`→`1.0001`, `-1.00005`→`-1.0001`), `TestRoundWithholding` (`IDR 1500.5`→`1501`, `USD 10.005`→`10.01`, `SGD 10.004`→`10.00`).

Run: `./dev test ./internal/platform/` — Expected: FAIL.

- [ ] **Step 2: Implement, run**

Expected: PASS. Mutations: switch `Install` to `apierr.Envelope` (problem test red); remove the no-store middleware (no-store test red); replace `RoundBank` with `Round` in `RoundHalfEven4` (half-even test red).

- [ ] **Step 3: Commit** `feat(go): platform — problem+json errors, CORS, no-store, JSON types, rounding`

---

### Task 6: The session principal

**Files:**
- Modify: `backend/internal/platform/principal.go` (replaces the template's access-token principal), `backend/internal/platform/server.go` (`Deps` gains `Sessions Sessions`; the middleware is installed from it)
- Test: `backend/internal/platform/principal_test.go`

**Interfaces:**
- Produces:
```go
const SessionCookie = "billing_session"
const PublicTag = "public"
type Principal struct { UserID uuid.UUID; Email string; Role string }
func PrincipalFrom(ctx context.Context) Principal
func Admin(op huma.Operation) huma.Operation // marks the operation admin-only (every non-public operation must use it)
type Sessions struct { Tokens auth.Tokens /* HS256, SESSION_SECRET */; Cookie auth.CookieSpec; MaxAge, RememberMaxAge time.Duration }
func (s Sessions) Issue(userID uuid.UUID, epoch int, remember bool) (token string, maxAge time.Duration, err error) // claims: sub, typ "session", epoch
func (s Sessions) SetCookie(w http.ResponseWriter, token string, maxAge time.Duration)
func (s Sessions) ClearCookie(w http.ResponseWriter)
```
The middleware reads `billing_session`, parses the token, loads the user (`user_id`, `email`, `role`, `session_epoch`) and answers, in this order: no cookie, a bad signature, or a non-session `typ` → 401 `not authenticated`; an expired token → 401 `session expired`; a token whose `epoch` is older than the user's `session_epoch` → 401 `session expired`; a missing user or `role != "admin"` → 403 `admin required`. An operation neither `Admin` nor `PublicTag` is refused to everyone (fail closed).

- [ ] **Step 1: Failing tests**

`TestNoCookieIs401NotAuthenticated`, `TestTamperedCookieIs401`, `TestExpiredCookieIs401SessionExpired`, `TestOlderEpochIs401SessionExpired`, `TestNonAdminIs403AdminRequired`, `TestDeletedUserIs403`, `TestAdminReachesHandlerWithPrincipal`, `TestUndeclaredOperationIsRefused`, `TestEveryOperationIsAdminOrPublic` (walks the registered operations of the real server, as the template's test does), `TestSessionCookieAttributes` (HttpOnly, SameSite=Lax, Path=/, Secure only when `Production()`).

Run: `./dev test ./internal/platform/ -run 'Cookie|Session|Admin|Operation|Epoch'` — Expected: FAIL.

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations, each reverted: drop the epoch comparison (epoch test red); drop the role check (403 test red); let undeclared operations through (fail-closed test red).

- [ ] **Step 3: Commit** `feat(go): billing_session principal — Python's 401/403 sentences, epoch sign-out`

---

### Task 7: Auth routes, throttle, password reset, CLI

**Files:**
- Create: `backend/internal/auth/auth.go` (API types), `service.go`, `routes.go`, `throttle.go`, `queries.sql`, generated `authdb/`; `backend/internal/email/email.go`
- Modify: `backend/sqlc.yaml` (add `internal/auth/queries.sql` → package `authdb`), `backend/cmd/api/main.go` (register auth; subcommands `set-password <email>` reading the password from stdin, `issue-session <email>` printing a token)
- Test: `backend/internal/auth/auth_test.go`, `throttle_test.go`, `backend/cmd/api/cli_test.go`

**Interfaces:**
- Consumes: `platform.Sessions`, `platform.Err`, `platform.Time`, `migrations` 00002/00003.
- Produces:
```go
// internal/email
type Message struct { To, Subject, Text, HTML string }
type Sender interface { Send(ctx context.Context, m Message) error }
type LogSender struct{ Logger *slog.Logger } // logs "[email disabled] would send to=… subject=…"
// internal/auth
var HashParams = auth.HashParams{Memory: 19456, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}
type UserOut struct { UserID uuid.UUID `json:"user_id"`; Email string `json:"email"`; Role string `json:"role"`; DisplayName *string `json:"display_name"`; CreatedAt platform.Time `json:"created_at"` }
func Register(api huma.API, pool *pgxpool.Pool, s platform.Sessions, mail email.Sender, cfg platform.Config, clock platform.Clock)
func SetPassword(ctx context.Context, pool *pgxpool.Pool, email, password string) error // argon2id, bumps session_epoch, clears reset token
func IssueSession(ctx context.Context, pool *pgxpool.Pool, s platform.Sessions, email string) (string, error) // admin only; 8 h; writes nothing
```
Routes (all under `/api/v1/auth`):

| Route | Auth | Success | Python behaviour kept |
|---|---|---|---|
| `POST /login` `{email, password, remember=false}` | public | 200 `UserOut`, sets cookie | unknown email or wrong password → 401 `invalid credentials`; no role check at login |
| `POST /logout` | public | 204, clears cookie | — |
| `GET /me` | admin | 200 `UserOut` | — |
| `POST /change-password` `{current_password, new_password}` (6–128) | admin | 200 `UserOut`; clears reset token; bumps epoch and re-issues this browser's cookie | wrong current → 400 `current password is incorrect`; same → 400 `new password must differ from current password` |
| `POST /forgot-password` `{email}` | public | 200 `{"message":"If an account exists for that email, a reset link has been sent.","reset_token":null,"reset_link":null}` always | stores **SHA-256 hex** of a `token_urlsafe(32)`-equivalent token, expiry now+1 h; email subject `Reset your wenhao-billing password`, link `{APP_BASE_URL}/reset-password?token=…`, Python's text and HTML bodies; send errors swallowed |
| `POST /reset-password` `{token, new_password}` (6–128) | public | 200 `UserOut`; clears token; bumps epoch | unknown, expired or used token → 400 `invalid or expired reset token` (an expired token is cleared) |

Throttle (in `login`, **before** verifying the password): client address = `CF-Connecting-IP`, else the left-most `X-Forwarded-For`, else the connection's host. If failures in the last 15 minutes for (email, address) ≥ 10 or for the address ≥ 30 → 429, `code` `too_many_attempts`, `detail` `too many failed sign-in attempts; try again later`, `Retry-After` = seconds until the oldest counted failure leaves the window. A failed login inserts a row and deletes rows older than one day; a successful login deletes the pair's rows.

- [ ] **Step 1: Failing tests**

`TestLoginSetsCookieAndReturnsUser`, `TestLoginRememberSetsThirtyDayCookie`, `TestLoginDefaultsToEightHours` (ports `test_login_defaults_to_no_remember`), `TestShortSessionExpiresAfterDefaultTTL`, `TestRememberSessionSurvivesShortTTL`, `TestRememberSessionExpiresAfterRememberTTL` (the three use `platform.Clock`; they port the Python tests of the same names), `TestLoginWrongPasswordIs401InvalidCredentials`, `TestLoginUnknownEmailIs401InvalidCredentials`, `TestLoginWithBcryptHashIsInvalidCredentials` (a `$2b$12$…` hash in the row → 401, not 500), `TestLogoutClearsCookieAnd204`, `TestMeReturnsUser`, `TestChangePasswordWrongCurrentIs400`, `TestChangePasswordSameIs400`, `TestChangePasswordSignsOutOtherSessions` (a second cookie issued before the change → 401 `session expired` after; the caller's new cookie → 200), `TestForgotPasswordIsGenericForUnknownEmail`, `TestForgotPasswordStoresHashNotToken` (the column holds `sha256(token)`, the emailed link holds the token), `TestResetPasswordConsumesToken` (a second use → 400), `TestResetPasswordExpiredTokenIs400AndCleared`, `TestThrottlePairLimit` (10 failures → 11th attempt with the **right** password is 429 with `Retry-After`), `TestThrottleAddressLimit` (30 failures across emails → 429), `TestThrottleNeverLocksEmailAlone` (10 failures from address A; a login from address B succeeds), `TestThrottleUsesCFConnectingIP`, `TestSuccessfulLoginClearsPairCount`, `TestPasswordLengthIs422`.
`cmd/api`: `TestSetPasswordThenLogin`, `TestSetPasswordBumpsEpoch`, `TestIssueSessionTokenOpensMe`, `TestIssueSessionRefusesNonAdmin`.

Run: `./dev test ./internal/auth/ ./cmd/api/` — Expected: FAIL.

- [ ] **Step 2: Queries and generate**

`queries.sql` names: `UserByEmail`, `UserByID`, `SetPassword` (hash, `session_epoch = session_epoch + 1`, reset token and expiry null), `SetResetToken`, `UserByResetTokenHash`, `ClearResetToken`, `CountPairFailures`, `CountAddressFailures`, `OldestPairFailure`, `OldestAddressFailure`, `InsertFailure`, `PruneFailures`, `ClearPairFailures`. Run `./dev sqlc`.

- [ ] **Step 3: Implement, run, mutate**

Expected: PASS. Mutations, each reverted: let a correct password bypass the throttle (`TestThrottlePairLimit` red: its 11th attempt uses the right password and must still be 429); key the throttle on email alone (never-locks test red); store the raw token (hash test red); skip the epoch bump in change-password (sign-out test red); treat a bcrypt hash as an error (bcrypt test red, 500).

- [ ] **Step 4: Commit** `feat(go): /auth routes with Python's contract; throttle; hashed reset tokens; set-password and issue-session`

---

### Task 8: Route and test inventories, port notes

**Files:**
- Create: `backend/port/inventory.py` (stdlib only, run with the host's Python 3.11+), generated `backend/ROUTES.md` and `backend/TESTS.md`, hand-written `backend/PORT-NOTES.md`

**Interfaces:**
- Produces: `python backend/port/inventory.py` rewrites both files from `backend-python/` (AST: `APIRouter(prefix=…)`, route decorators and their `status_code`, the auth dependency in the signature, `include_router` prefixes in `app/main.py`, the OAuth router, every `def test_` in `backend-python/tests/`) and keeps the hand-edited columns (`go`, `status`, `note`) of rows it finds again, keyed by method+path or file+test.

`ROUTES.md` columns: `method | path | auth | status | python | frontend | go | port status | note`. `frontend` is `yes` when the path template (params as wildcards) matches a string literal under `frontend/src`, else `no`. Totals line: 107 under `/api/v1`, 7 OAuth/discovery, `/mcp`.
`TESTS.md` columns: `python file | python test | go test | status | reason | mutation`; one row per `def test_` (520), plus a line giving the collected count 527 and why it differs (parametrisation).

- [ ] **Step 1: Run and check counts**

Run: `python backend/port/inventory.py && grep -E '^Totals' backend/ROUTES.md && grep -c '^| tests/' backend/TESTS.md`
Expected: the totals line reads `107 under /api/v1, 7 OAuth and discovery, /mcp`; `520` test rows. A different count means the AST walk missed a router or a test file: fix the script, not the expected number.

- [ ] **Step 2: Fill this plan's rows**

Mark the six `/auth` routes `ported` with `go` = `auth`; mark `test_login_defaults_to_no_remember`, the three session-TTL tests and the two no-store tests `ported` with their Go test names and the mutation each was checked by.

- [ ] **Step 3: Seed `PORT-NOTES.md`**

One numbered entry each (what Python does, where, what a person sees, the fix after the switch): usage-lock accrual hook raises `NotImplementedError`; email intake with attachments raises `NotImplementedError` (400); `payments/{id}/log` returns `[]` for an unknown payment; `GET /business-profile` creates the row without committing; `as_of` defaults use server-local today; the matching engine can auto-clear a tie between equal top candidates (PRD safe-stop); payment reversal restores face amount plus withholding (differs after cross-currency settlement); manual invoice and bill record-payment create no journal entries; MCP login redirect uses `return_to` while the login page reads `next`. Source of the last four: Codex's handoff (`codex/billing-go-replacement`, `docs/go-replacement/HANDOFF.md`), each re-checked against `backend-python/` with a file:line before writing it.

- [ ] **Step 4: Commit** `docs(go): route and test inventories, port notes`

---

### Task 9: Checks, dev API service, README

**Files:**
- Modify: `scripts/check.sh` (a `== go` section), `docker-compose.yml` (service `api-go`, profile `tools`)
- Create: `backend/README.md`

**Interfaces:**
- Produces: `scripts/check.sh` runs, after the existing three: `backend/dev lint`, `backend/dev generated` (sqlc code and `openapi.json` current), and `backend/dev test` with `-race`, failing if any test is skipped (`--- SKIP` in `go test -v` output, or `go test -json` `"Action":"skip"`), because `testkit` skips everything when `PGTESTDB_URL` is unset.
- Produces: Compose service `api-go` (profile `tools`, builds `./backend`, port `8001:8000`, `DATABASE_URL=postgresql://billing:billing@db:5432/billing`, `SESSION_SECRET` a 32+ byte dev value, `CORS_ORIGINS=["http://localhost:5173"]`, `ENVIRONMENT=development`) for later plans' local comparison.

- [ ] **Step 1: Make check.sh fail on Go skips, and watch it**

Run once with the skip guard but `PGTESTDB_URL` blanked for the run.
Expected: `check.sh` exits 1 naming skipped Go tests. Then the normal run: `bash scripts/check.sh` → `all checks passed`.

- [ ] **Step 2: The API image builds and serves**

Run: `docker compose --profile tools build api-go && docker compose --profile tools run --rm api-go /api adopt-baseline` against a **scratch copy** of the dev database only (`createdb -T billing billing_go_check`, `DATABASE_URL` pointed at it), then start `api-go` against that copy and request `GET http://localhost:8001/api/v1/auth/me`.
Expected: adopt prints success; `/auth/me` answers 401 problem+json `detail` `not authenticated`. Drop `billing_go_check` afterwards. The dev `billing` database is never adopted in this plan.

- [ ] **Step 3: README**

`backend/README.md`: what this is (spec link), the `./dev` commands, `scripts/check.sh`, `adopt-baseline` / `set-password` / `issue-session`, regenerating the baseline (only if the frozen head changed: never, while frozen), and the rule that Python is frozen.

- [ ] **Step 4: Commit, PR**

```bash
git add scripts/check.sh docker-compose.yml backend/README.md
git commit -m "chore(go): Go checks in check.sh; api-go dev service; README"
git push -u origin feat/go-foundation
gh pr create --base main --title "feat(go): backend foundation — baseline, platform, auth" --body-file -
```
The body lists what was ported, the inventories' counts, every mutation run, and that merging changes no production container. Merge waits for the owner.
