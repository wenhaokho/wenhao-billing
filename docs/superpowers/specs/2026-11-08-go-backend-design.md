# wenhao-billing backend in Go

Date: 2026-11-08. Status: draft for the owner's review.

Builds on `D:\Development\tagara\docs\superpowers\specs\2026-11-03-go-default-stack-design.md`
(approved): its library choices, the `gokit` + template split, the
conventions, lint, tests and the kit admission rule apply here. The
TAGARA-specific parts (two kinds of user, the `{"error": …}` envelope,
baseline 0042) do not. Where this document departs from that design it says
so and why.

## Intent

Replace wenhao-billing's Python backend (FastAPI, SQLAlchemy, Alembic,
APScheduler, WeasyPrint, fastmcp; ~17,600 lines of app code, ~9,300 lines of
tests, 525 tests) with a Go backend on the default stack, then delete the
Python backend.

Why: memory on the shared VPS. The Python API idles at ~390 MB (measured
locally); the template's Go API idles at 6 MB. The goal is under ~30 MB.

wenhao-billing is a single-tenant accounting system with exactly one user,
the owner. That makes some compatibility work unnecessary (see Auth) and
makes a short, rehearsed switch-over acceptable.

## Goals

1. Every route the Vue app calls (96 method + path pairs, inventory below)
   is answered by Go with what the frontend relies on: paths, methods,
   status codes, JSON field names, value formats, and an error body whose
   `detail` is the same sentence Python returns today. The Vue app works
   with one source change, which fixes the MCP login redirect (see "MCP
   connector").
2. The rest of `/api/v1` (107 routes in all: payment intake webhooks, usage
   lock, recurring trigger, deletes) and the MCP connector (`/mcp` with all
   42 tools, its OAuth 2.1 server and discovery documents) work in Go from
   the switch-over on.
3. Everything that runs without a request does the same: the four scheduled
   jobs, emails, Cloudflare DNS suspend/restore, the FX rate sync.
4. The production database is adopted as it is. No data moves.
5. Invoice, quotation and receipt PDFs are produced with Maroto v2.
6. Idle memory of the Go API on the shared VPS under ~30 MB, measured there.

## Non-goals

- New product features, or fixing Python's odd behaviour during the port
  (see "Port notes"). Exceptions are named under "Best-practice changes".
- Keeping the existing password hash, login session or MCP tokens valid
  across the switch (there is one user, who signs in again).
- Changing the frontend, its build or its nginx container, beyond the one
  login-redirect fix.
- A Redis instance. Nothing here needs one.
- The PRD's `business_id` scoping. There is no `business_id` column
  anywhere today; the app is single-tenant and stays so. The template's
  organization tenancy is removed.

## Anti-goals

- A port that "mostly works" and is switched on before the comparison is
  clean: one wrong ledger posting or FX rounding is a money defect.
- `gokit` growing a wenhao-only feature. Anything this app alone needs lives
  in this repository.
- Anything on the VPS that idles at hundreds of MB (no Chromium, no Python
  sidecar for PDFs).
- Mocked databases in tests.

## Constraints

- Postgres 16, the existing shared Coolify Postgres. Alembic head
  `0028_business_npwp` (28 migrations; note revision ids `business_pay_instr`
  and `mcp_oauth` differ from their file names).
- Coolify builds both images on the shared VPS (`100-app-part-1`), as today.
  This **replaces** the stack design's "built in GitHub Actions, pushed to
  GHCR, Coolify pulls" for this project, at the owner's decision. A Go build
  peaks at a few hundred MB for under a minute; the Vue build is unchanged.
  `gokit` is vendored (as the template does), so the build needs no GitHub
  credential.
- The owner approves, in chat, anything that deploys, deletes data or
  changes production configuration.
- Tests run on a real Postgres (pgtestdb), never a mocked database. Every
  guard has a test that turns red, for the expected reason, when the guard
  is removed.

## Approaches considered

**Error shape** (the owner chose 1, as best practice):

1. **RFC 9457 problem+json, `gokit`'s `apierr.ProblemJSON`.** The IETF
   standard for HTTP API errors and the stack's default. Its `detail`
   member is a string, which is exactly what the frontend reads in 38 places.
   No frontend change, no kit change.
2. `gokit`'s `{"error": …}` envelope, with 38 mechanical frontend edits.
3. A FastAPI-style `{"detail": …}` format added to `gokit`: grows the kit
   for one app.

**Proving the port** (my call): port the Python tests to Go (tracked one by
one in `TESTS.md`) and run a comparison of every frontend call against both
backends on the same data. Tests alone miss what the tests never asserted;
a comparison alone misses the write paths' rules.

**Switch-over** (the owner's brief): Python and Go side by side against the
same database until the comparison is clean, then the web container points
at Go. Not route-by-route: with one user and one nginx upstream, a single
switch with a rehearsed way back is simpler and safer than splitting
sessions across two backends.

## Where things live

- `LOQATO/gokit` **exists** (v0.2.0, CI green) and `LOQATO/go-api-template`
  **exists** (CI green), created on 2026-11-03. `pintas-booking` is already
  being ported from them; its porting kit (`backend/port/`, `ROUTES.md`,
  `TESTS.md`, `PORT-NOTES.md`, `make-baseline.sh`) is the pattern this port
  copies, in a lighter form.
- No repository is created. wenhao-billing stays at
  `github.com/wenhaokho/wenhao-billing`.
- Python moves to `backend-python/` (frozen at a commit named in
  `backend-python/FROZEN.md`). Go is built in `backend/`, module
  `github.com/wenhaokho/wenhao-billing/backend`, started from a copy of the
  template, depending on `github.com/LOQATO/gokit v0.2.0`, vendored.
- `GOPRIVATE=github.com/LOQATO/*` in `backend/dev` and the README. Only
  `./dev vendor` (upgrading the kit) needs GitHub access, through the
  owner's `gh` login. Local checks and the Coolify build read `vendor/` and
  need no token.
- **GitHub Actions is off** on `gokit`, `go-api-template` and wenhao-billing,
  and the owner keeps it off (decided 2026-11-09). The checks run locally
  through Docker instead: `scripts/check.sh` at the repository root (compose
  paths, frozen Python tests, frontend build), which plan 2 extends with
  golangci-lint, `sqlc vet`/`diff`, `./dev generated` and `go test -race`
  against Postgres. No workflow file is added.

## How it is built

### Order of work

1. **Kit check.** wenhao-billing uses `apierr`, `db`, `sched`, `auth`
   (argon2id hashing, HS256 tokens, cookies), `observe`, `testkit` and
   `i18n` from v0.2.0. It does **not** use `money.Quantize` (2 places,
   half-up; see "Rounding"), `auth.Throttle` (needs Redis) or
   `auth.RefreshStore`. The signed-in principal middleware comes from the
   template's `internal/platform`, not the kit. No kit change is planned; a
   gap found later goes through the admission rule, ships as a kit release,
   and the template is bumped to it. Modules come from the `v0.2.0` tag on
   GitHub, never from a local checkout of `gokit`.
2. **Template check.** The template pins `gokit v0.1.0`; bump it to v0.2.0
   in the template repository (its checks green locally) so it matches what this project and
   Pintas use. Nothing else changes in the template.
3. **Freeze and rename**: `backend/` → `backend-python/` with every path that
   names it (`docker-compose.yml`, `docker-compose.coolify.yml`, CLAUDE.md,
   AGENTS.md). This is merged on its own and deployed (owner approval), so
   production keeps running Python from the new path before any Go exists.
4. **Go foundation** in `backend/`: config, baseline migration, platform
   (principal, permissions, errors, JSON types), auth, `ROUTES.md`,
   `TESTS.md`, `PORT-NOTES.md`, the Go checks in `scripts/check.sh`.
5. **Features**, each with its ported tests: master data (customers,
   vendors, items, chart of accounts, business profile, users); FX rates and
   the ledger; invoices, recurring templates, quotations, projects, bills;
   payments, intake, the matching engine and reconciliation; reports and
   stats.
6. **Documents and integrations**: Maroto PDFs, Resend email, Cloudflare,
   Frankfurter, the scheduler.
7. **MCP**: OAuth server, `/mcp`, the 42 tools.
8. **Comparison clean** locally on a copy of production data.
9. **Side by side on Coolify**, then **switch-over**, then Python deleted.

Each step after 2 is its own implementation plan, written when the
previous one is merged.

### Route inventory

`ROUTES.md` is generated from the frozen Python app (method, path, auth,
status, Python file:line, frontend caller, Go package, status of the port).
Totals today: auth 6, users 4, customers 8, invoices 23, bills 6,
quotations 12, projects 7, vendors 5, items 5, accounting 16,
business-profile 4, payments 2 (intake), recon 6, stats 3 (107 under
`/api/v1`); OAuth and discovery 7; `/mcp` redirect and mount. All are ported.
Huma serves `/openapi.json` and `/docs`, which nginx already proxies.

### Behaviour that must be carried exactly

These come from reading every frontend call site; each gets a test.

- **Errors.** `apierr.ProblemJSON` (`application/problem+json`):
  `{"title","status","detail","code","errors"?}`. `detail` is the **same
  English sentence** Python's `HTTPException` gives today, for every one of
  them (listed in `ROUTES.md`), because the login page matches
  `invalid|incorrect|unauthor|credentials` against it. `code` is a new
  stable snake_case code per sentence. Validation failures are 422 with a
  sentence in `detail` and the fields in `errors`. Python's 422s that carry
  a string (`business_profile.py:79-84`, `customers.py:86,116`) keep their
  sentences. An unexpected error is a 500 problem body with a generic
  sentence, logged and sent to Sentry (Python sends plain text today).
- **401 only means signed out.** The frontend logs the user out on any 401
  except `/auth/me`. Wrong current password on change-password and admin
  password reset stay 400.
- **Lists are bare JSON arrays**, never `null`, with Python's `limit`
  parameters (default 200, max 500, over the max is 422). This departs from
  the template's `Page` envelope because the frontend reads arrays.
  `ORDER BY` matches Python's and ends with the primary key.
- **Every slice in a response is `[]`, never `null`**, and every optional
  field is present as `null`, never omitted (the frontend tests
  `=== null`). A response test walks each operation's schema.
- **Money and other decimals are JSON strings** with the scale of their
  column: 4 places for `numeric(19,4)` (`"1500.0000"`), 8 for FX rates
  (`numeric(19,8)`), 4 for withholding rates (`numeric(7,4)`), 2 for
  `confidence_score` (`numeric(5,2)`). Computed values (reports, stats,
  `expected_cash`) are rendered at 4 places. Decimals in request bodies are
  accepted as a JSON number or a string. `decimal.Decimal` end to end.
- **Rounding** follows each Python call site, not one helper: most use
  Python's default context, ROUND_HALF_EVEN to 4 places (`services/fx.py:72`,
  `billing_ap.py`, `stats.py`, the FX revaluation in `accounting.py`,
  `matching_engine.py:153`); `invoicing.py` and `quoting.py` use HALF_UP to
  4 places; `withholding.py:27-30` uses HALF_UP to 0 places for IDR and 2
  for other currencies. `internal/platform` gets named helpers
  (`RoundHalfEven4`, `RoundHalfUp4`, `RoundWithholding(ccy)`), each call site
  names one, and each is pinned by a test on the tie cases.
- **Dates** are `YYYY-MM-DD`. **Timestamps** are RFC 3339 in UTC with `Z`.
  The columns are `timestamp without time zone`; the code writes
  `datetime.utcnow()` and `server_default=CURRENT_TIMESTAMP` writes the
  session's local time, which is UTC only if the server's `TimeZone` is UTC.
  The local database is UTC; production is checked with `SHOW timezone`
  (read-only, with the owner's yes) before the comparison, and if it is not
  UTC the Go pool sets `TimeZone=UTC` and the plan says how existing rows
  are read. Python prints these without a zone; the frontend accepts both.
- **IDs** are UUID strings, except `account_id`, `rate_id` and payment
  account ids, which are JSON integers (serial columns).
- **PATCH is a partial update**: an omitted key leaves the field alone, an
  explicit `null` clears it. A small `Optional[T]` type in `internal/platform`
  carries the three states.
- **Unknown request fields are ignored**, as Python does (the bill
  record-payment dialog sends `receipt: null`). Huma's default of rejecting
  them is turned off; Huma's `$schema` member is dropped from responses.
- **Repeated query parameters** (`?status=OPEN&status=SENT`) are lists.
- **Route order**: the literal paths (`/customers/balances`,
  `/invoices/awaiting-finalization`, `/invoices/recurring-templates/rows`,
  `/payments/audit-log`, `/payments/awaiting-review`,
  `/accounting/fx-rates/sync`, `/accounting/fx-rates/latest`, `/users/me`)
  are not swallowed by `{id}`. The request key for a recurring schedule is
  `schedule` and the response key `billing_cycle_ref`, as today.
- **PDFs**: the invoice and quotation routes answer `application/pdf` with
  `Content-Disposition: inline; filename="invoice-<number or first 8 of
  id>.pdf"` (and `quotation-<number>.pdf`), opened by the browser with
  `window.open`, outside axios. The receipt has no route; it exists only as
  the attachment of the receipt email.
- **Document numbers**: invoices `WH{YYYY}{NNNN}`, quotations
  `Q{YYYY}{NNNN}`, receipts `RCT-{first 8 hex of payment id}`. Python finds
  the next number by scanning for the maximum; Go takes a transaction-scoped
  advisory lock per document type and year before scanning, so two requests
  cannot get the same number (the unique constraint stays as the backstop).
- **`Cache-Control: no-store`** on `/api/`, `/oauth/`, `/mcp`,
  `/.well-known/`. CORS from `CORS_ORIGINS` with credentials. The Sentry
  `sentry-trace` and `baggage` request headers are accepted.
- **The money rules** of `prd.md` and CLAUDE.md, unchanged: currency fixed
  at invoice creation; a payment in another currency is rejected, never
  converted; only ≥95% confidence auto-reconciles; any mismatch or ambiguity
  is `PENDING_MANUAL_REVIEW`; realized FX gain/loss on cross-currency
  matches; every automated action reversible. Ledger postings happen exactly
  where Python makes them (reconciliation clear and reverse), and nowhere
  else.

### Auth

- **Passwords**: argon2id through `gokit/auth`, with OWASP's recommended
  parameters for a server (19 MiB memory, 2 iterations, 1 lane), not the
  kit's 64 MiB default (which matches TAGARA's argon2-cffi). OWASP rates
  them equivalent in strength; the lower memory keeps a login from pushing
  the process far past the ~30 MB goal. No bcrypt code. The existing bcrypt
  hash will not verify; after the switch the owner runs
  `api set-password <email>` (password read from the terminal) in the Go
  container, or uses forgot-password.
- **Signing in for the comparison** (before the switch, while the only hash
  is bcrypt): `api issue-session <email>` prints a session token for an
  existing admin, signed with the same key the server uses. It runs only
  inside the container, writes nothing, and is how `compare` signs in to Go
  both locally and on production. Python is signed in with its normal login.
- **Session**: an HS256 token from `gokit/auth` in the HttpOnly cookie
  `billing_session` (path `/`, SameSite=Lax, **Secure in production**),
  carrying the user id, expiry (8 hours, or 30 days with "remember me") and
  the user's session epoch. No refresh token and no Redis. Logout clears the
  cookie. The 401 sentences stay `not authenticated` and `session expired`;
  a non-admin is 403 `admin required`.
- **Signing out everywhere**: a new column `users.session_epoch` (integer,
  default 0) is bumped by change-password, reset-password and admin reset; a
  token with an older epoch is 401. Python ignores the column.
- **Login throttle** (app code; Python has none). The site is proxied by
  Cloudflare (the name resolves to Cloudflare addresses), then Traefik, then
  nginx, so the client address is `CF-Connecting-IP`, falling back to the
  left-most `X-Forwarded-For`. Failed attempts are counted in a Postgres
  table and limited **per pair of email and client address** (10 in 15
  minutes) and **per client address** (30 in 15 minutes), then 429 with
  `Retry-After`. There is no limit on the email alone, so nobody can lock
  the one account by guessing at it from elsewhere (OWASP's advice against
  account lockout as a denial of service). A forged address header only
  lets an attacker spread their own guesses; the argon2id cost still bounds
  them.
- **Password reset tokens** are stored as their SHA-256 (64 hex characters,
  fits the existing `password_reset_token` column), valid one hour, single
  use. The response stays the same generic sentence whether or not the email
  exists.
- **Webhooks**: `X-Signature` HMAC-SHA256 of the raw body and the
  `X-Email-Token` header, both compared in constant time, with Python's
  statuses and sentences.

### MCP connector

- The official Go SDK, `github.com/modelcontextprotocol/go-sdk`
  (streamable HTTP), mounted at `/mcp/`; bare `/mcp` answers 307 to `/mcp/`.
  Not in the stack table: added here as the standard implementation.
- Host and Origin are checked against `MCP_PUBLIC_BASE_URL` (DNS-rebinding
  guard; a wrong Host is 421), as today.
- All 42 tools (41 in `mcp/tools/` plus `ping`) keep their names, argument
  schemas and result shapes. As `mcp/validate.py:12-40` does, an `update_*`
  tool whose `changes` holds an unknown key returns an `{"error": …}` tool
  result naming it; other argument checking is the SDK's schema validation. The five gated
  tools keep the two-step `confirm_token` with the same format
  (`"{expiry}.{hex HMAC-SHA256}"` over canonical JSON, 300 s, keyed by
  `MCP_CONFIRM_SECRET`).
- OAuth 2.1 server, same endpoints and metadata: dynamic client
  registration, authorization code with PKCE (S256 only), refresh rotation
  with reuse detection over `oauth_refresh_tokens`, RFC 7009 revoke. Errors
  keep the OAuth `{"error","error_description"}` shape (RFC 6749), not
  problem+json. Tokens keep the same HS256 claims and `MCP_TOKEN_SECRET`, so
  a connector authorised before the switch may keep working; if not, the
  owner reconnects it once.
- The authorize step uses the Go session cookie. When there is none it
  redirects to `{APP_BASE_URL}/login?return_to=<the authorize URL>`. Today
  that flow is broken: the login page only reads `next` and pushes it as a
  client route (`LoginView.vue:53`), so the browser lands on the dashboard
  and the connector never resumes. Fixed with the **one frontend edit** of
  this port: after a successful login, a `return_to` that is a same-origin
  path under `/oauth/` is followed with `window.location.assign`; anything
  else is ignored. The consent form carries a CSRF token bound to the
  session.
- Authorization codes become **single use** (a new table of spent code ids,
  pruned after expiry). Python's codes can be replayed within 120 s.

### Documents

- Maroto v2 builds the invoice, quotation and receipt from the same data
  Python's `services/pdf/context.py` assembles: logo, company block (name,
  address, NPWP), customer block, line items, discount, withholding, totals,
  payment instructions per currency, notes and footer. A clean design in the
  spirit of today's; not a pixel copy. The owner reviews the first
  versions and the design is adjusted from that review.
- Fonts are embedded TTF files (Maroto does not read the current woff2).
- The logo comes from `business_profile.logo_url`: a `data:` URI is decoded;
  an `http(s)` URL is fetched with a 5 s timeout and a 2 MB cap; the type is
  sniffed from the bytes; a failure falls back to the default logo.
- The reportlab fallback goes; there is one renderer.

### Email

- Resend through `resend-go`, from `RESEND_FROM_EMAIL`/`RESEND_FROM_NAME`,
  with `Reply-To` from `SMTP_REPLY_TO` (Python's variable, default
  `wenhao.kho@gmail.com`; the name is kept so nothing in Coolify changes);
  with no `RESEND_API_KEY` mail is logged, as in dev today. The SMTP sending
  path is dropped (production uses Resend).
- Sends the owner starts (invoice, quotation, receipt, password reset) stay
  **synchronous**, departing from the stack design's outbox: the frontend
  shows the failure and offers Retry, which needs the result in the
  response. The receipt failure stays 502; invoice and quotation send
  failures become 502 problem bodies (500 plain text today). The Resend
  idempotency key is the document id plus a hash of recipients and body, so
  a retry after a timeout cannot send twice.
- The recurring-draft digest and MCP reminder emails are sent the same way
  from their callers; a failure is logged and reported, as today.

### Background work

`gokit/sched` runs each job under `pg_try_advisory_lock` with Python's keys
(crc32 of the job id), so the two backends can never run the same job at
once. `sched` ticks on an interval; a job is due at its daily (or weekly)
UTC time and runs once per slot: each run records its slot in a new
`scheduled_job_runs` table, and a tick runs the job if a slot has passed
that is not recorded. **This is a change from Python**, whose job store is
in memory and skips runs missed while the process was down
(`workers/scheduler.py:56-57`): Go catches up one missed slot (coalesced)
after a restart or deploy. It is safe because each job is idempotent (the
recurring scan creates each child invoice at most once,
`workers/tasks/recurring.py:3-4`; the others converge on the same state).
A slot older than 24 hours is skipped and logged, not run.

| Job | When (UTC) | What |
|---|---|---|
| recurring-scan-and-generate | daily 02:00 | draft child invoices for due templates; digest email |
| usage-lock-cutoff-scan | daily 03:00 | lock usage drafts past their cut-off day |
| hosting-overdue-enforcement | daily 04:00 | Cloudflare DNS suspend / restore |
| fx-rates-weekly-sync | Monday 05:00 | Frankfurter rates into `fx_rates` |

The scheduler is off in the Go process until the switch-over
(`SCHEDULER_ENABLED=false`), and off in Python after it.

### Database

- `migrations/00001_baseline.sql` is generated by a copy of Pintas's
  `make-baseline.sh`: Alembic upgrade to `0028_business_npwp` in a scratch
  database, `pg_dump --schema-only`, cleaned, plus the seed rows (chart of
  accounts, FX gain/loss accounts, the business profile singleton). A test
  compares the goose-migrated schema with the Alembic one.
- On the existing database, `api adopt-baseline` checks that
  `alembic_version` is `0028_business_npwp` and that the schema matches,
  then records goose version 1 without running it. Goose runs from then on,
  at startup, under its session lock.
- New goose migrations, all additive so Python keeps working beside them:
  `users.session_epoch`, the login-attempt table, the spent OAuth code table,
  `scheduled_job_runs`.
- `alembic_version` is dropped by a goose migration when Python is deleted.
- Queries are sqlc, one `queries.sql` per feature; `numeric` maps to
  `decimal.Decimal`; JSONB columns (customer aliases and CC lists,
  `billing_cycle_ref`) and `text[]` (`reconciliation_log.reasons`) get typed
  Go structs.

### Tests

- Every Python test is listed in `TESTS.md` with its Go test, or a reason it
  is merged or not ported. All 525 are accounted for before the switch.
- Go tests run on pgtestdb (a cloned, goose-migrated database per test,
  `t.Parallel()`), API tests through Huma with a signed-in principal, a fixed
  clock, and the same seed data Python's fixtures use (FX USD→IDR 16000 and
  SGD→IDR 12000 on 2026-01-01).
- Resend, Cloudflare and Frankfurter are reached through `httptest` servers;
  the database is never faked.
- The mutation rule: for every guard (auth, admin role, webhook signature,
  currency mismatch, the 95% threshold, PENDING_MANUAL_REVIEW, balance and
  withholding limits, voided-document rules, document number lock, PKCE,
  refresh reuse, confirm tokens, throttle, session epoch), the plan names the
  test and records that removing the guard turned it red for the expected
  reason.

### The comparison

A Go command, `backend/cmd/compare`, replays the frontend's calls against
two base URLs and diffs the JSON. Allowed differences live in
`port/allowed-diffs.yaml`: decimal strings equal in value, timestamps equal
as instants, the error body shape (Python `{"detail"}` vs problem+json with
the same `detail`), and the notes in `PORT-NOTES.md`.

- **Locally, reads and writes**: both backends against a restored copy of
  the production database (the owner approves taking the dump; it stays on
  this machine), with both schedulers off. Writes run against two copies of
  the same dump, one per backend, and the resulting rows are compared too.
- **On Coolify, reads only**: the Go service beside Python on the
  production database, GET requests only, through the internal network.

### Builds and deploys

- `backend/Dockerfile`: the template's (`golang:1.27-bookworm` builder with
  the vendored modules, `-trimpath -ldflags="-s -w"`, distroless static
  nonroot, `api healthcheck`), listening on port 8000 as Python does, so
  nginx's upstream only changes host name. Coolify builds it from the repo.
- `docker-compose.coolify.yml` gains `wenhao-billing-api-go` (no public
  domain, `SCHEDULER_ENABLED=false`) beside `wenhao-billing-api`. The web
  container's `BACKEND_UPSTREAM` picks which one the site uses.
- Go settings use the Python variable names where the meaning is the same
  (`DATABASE_URL`, `SESSION_SECRET` as the token key, `CORS_ORIGINS`,
  `APP_BASE_URL`, `MCP_*`, `RESEND_*`, `CLOUDFLARE_API_TOKEN`, `SENTRY_*`,
  `WEBHOOK_HMAC_SECRET`, `EMAIL_WEBHOOK_TOKEN`, `BASE_CURRENCY`,
  `FX_SYNC_CURRENCIES`), so Coolify's generated secrets carry over.
  `DATABASE_URL`'s `postgresql+psycopg://` prefix is accepted and rewritten.

### Switch-over

Each step that deploys or changes production waits for the owner's yes.

1. Deploy the compose with the Go service (Python still serving, Go
   scheduler off). Measure Go's idle memory on the server: resident memory
   after start-up, and again ten minutes after a sign-in and a run of the
   comparison (the Go runtime returns freed memory to the system over a few
   minutes). Both are reported; the goal applies to the second.
2. Run the read comparison on production. Clean, or every difference
   allowed.
3. Switch: `BACKEND_UPSTREAM=wenhao-billing-api-go:8000`, Go
   `SCHEDULER_ENABLED=true`, Python `SCHEDULER_ENABLED=false`; redeploy. The
   owner runs `api set-password`, signs in, and checks the site in a browser
   (dashboard, an invoice PDF, a quotation, reports). Reconnect the MCP
   connector in Claude if it asks.
4. **Way back** (rehearsed locally before step 3): set `BACKEND_UPSTREAM`
   back to Python and swap the scheduler flags. Go's migrations are
   additive, so Python runs on the same database. The owner's password is
   then argon2id, which Python cannot read: reset it from Python with
   `scripts.seed_admin`.
5. After a week without a way back: delete `backend-python/` and the Python
   service, drop `alembic_version`, update CLAUDE.md and AGENTS.md.

## Best-practice changes (made in the port, not kept as port notes)

Each is small, safe for the one user, and something a reviewer would flag
in new code:

- Secure session cookie in production; sign-out everywhere on a password
  change (session epoch).
- Login throttle with 429 and `Retry-After`.
- Password reset tokens stored hashed, single use.
- OAuth authorization codes single use.
- The MCP login redirect actually returns to the connector (the one
  frontend edit).
- Document numbers allocated under a lock.
- Scheduled jobs catch up one missed run after a restart.
- 500s as problem+json with a generic sentence, never plain text; email
  send failures as 502.

Everything else odd in Python is kept as it is and written down in
`PORT-NOTES.md` (what Python does, where, what the fix would be), to fix
after the switch. Known now: usage lock's accrual hook raises
`NotImplementedError` (a matching invoice fails the job); email intake with
attachments raises `NotImplementedError` (400); `payments/{id}/log` returns
`[]` for an unknown payment; `GET /business-profile` creates the row
without committing; `as_of` defaults use server-local today.

## Done

- The checks green, run locally (Actions is off): `gokit`'s and
  `go-api-template`'s `./dev lint`, `./dev generated` and `./dev test`
  (template after the v0.2.0 bump), and wenhao-billing's `scripts/check.sh`.
- Every route in `ROUTES.md` ported; every Python test accounted for in
  `TESTS.md`; every guard's mutation recorded.
- The local comparison (reads and writes) and the production read
  comparison clean, or every difference allowed.
- wenhao-billing running on the shared VPS from the Go image, with its idle
  memory measured there against the ~30 MB goal.
- The frontend checked in a browser against the Go API: locally before the
  switch, and on production after it.
- The MCP connector working against Go from Claude.
- `backend-python/` deleted.

## Left to the builder

- Exact versions, Makefile/`dev` targets, CI caching.
- The Go package split inside the order above, and plan boundaries.
- The `code` value for each error sentence.
- The PDF layout details, within "Documents".
- The login throttle's table shape and pruning.
- Whether `compare` reuses Pintas's code or is written smaller.

## Calls I made (the owner may overrule)

- Port and compare both, not one or the other.
- One switch-over, not route by route.
- No bcrypt, no session carry-over, no Redis; session epoch for sign-out
  everywhere.
- Lists stay bare arrays; decimals keep their column scale; timestamps gain
  `Z`.
- User-started emails stay synchronous instead of going through an outbox.
- SMTP dropped; Resend only.
- The official MCP Go SDK.
- The best-practice changes listed above.
- argon2id at OWASP's 19 MiB / 2 iterations instead of the kit's 64 MiB.
- Login throttle per email-and-address and per address, never per email
  alone; client address from `CF-Connecting-IP`.
- `api issue-session` as the comparison's way to sign in to Go.
- Rounding helpers named per call site instead of the kit's `Quantize`.
- Port all 107 `/api/v1` routes, including the 11 the frontend never calls.
- Python stays runnable for one week after the switch, then is deleted.
