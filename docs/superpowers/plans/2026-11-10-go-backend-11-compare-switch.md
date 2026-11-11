# Go backend, plan 11: comparison, side by side, switch-over

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Refreshed 2026-11-10 against `main` at `9fb4981`** (plans 1–10 merged; `feat/go-send-finalized-only` treated as landed). Tasks 1–5 are local and touch nothing in production. Tasks 6–10 touch production. Every step marked **OWNER YES REQUIRED** waits for the owner's yes in chat, one yes per step.

**Goal:** Prove Go answers the frontend as Python does, run it beside Python on Coolify, switch the site to it with a rehearsed way back, and delete Python a week later.

**Architecture:**
- `backend/cmd/compare` replays a corpus of the frontend's calls against two base URLs. It diffs the JSON under `backend/port/allowed-diffs.yaml`.
- Scripts run it locally against a restored dump: reads on one copy, writes and rows on two copies.
- The Go image ships `/compare` beside `/api`, so the production read comparison runs inside Coolify's network.
- `docker-compose.coolify.yml` gains `wenhao-billing-api-go`. `BACKEND_UPSTREAM` and both scheduler flags become Coolify variables, so the switch and the way back are environment changes plus a redeploy, with no git change.

**Tech Stack:** Go 1.27 (stdlib `net/http`, `encoding/json`, `gopkg.in/yaml.v2`), pgx v5, pgtestdb, Docker Compose, Coolify. `yaml.v2` is already vendored and listed in `go.mod` as indirect. Import it and drop the `// indirect` marker; no revendoring is needed (`./dev vendor` needs `gh auth`).

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md`. Sections: "Behaviour that must be carried exactly" (Dates), "MCP connector", "Email", "Background work", "Database", "The comparison", "Builds and deploys", "Switch-over", "Done".

**Inventory:** `docs/superpowers/plans/2026-11-10-go-backend-11-compare-switch-inventory.md`:
- §1: what exists and what is missing;
- §2: the frontend call corpus;
- §3: allowed differences;
- §4: the switch prerequisites and their status;
- §5: the env-var surface;
- §6: risks and rollback points.

## Global Constraints

**OWNER YES REQUIRED: the full list.** These steps touch production. Each needs its own yes in chat:

| # | Step | Task |
|---|---|---|
| P1 | `SHOW timezone;` on production (read-only) | 6.1 |
| P2 | `pg_dump` of production to this machine | 6.2 |
| P3 | Read Coolify env **names** (`list_env_keys`, values never) | 7.1 |
| P4 | Set the new Coolify variables (`GO_SCHEDULER_ENABLED=false`, `PY_SCHEDULER_ENABLED=true`, `BACKEND_UPSTREAM=wenhao-billing-api:8000`) | 7.2 |
| P5 | `api adopt-baseline` on production (writes `goose_db_version`) | 8.1 |
| P6 | Merge the compose PR, which deploys Go beside Python; serve applies 00002–00007 to production | 8.2 |
| P7 | The read comparison against production | 8.3 |
| P8 | The switch: env change and redeploy | 9.1 |
| P9 | `api set-password` for the owner on production (the owner types the password) | 9.2 |
| P10 | The way back, only if needed: env change, redeploy, `seed_admin` | 9.5 |
| P11 | Find and void duplicate recurring children (PN 35), if Task 6 found any | post-switch |
| P12 | Merge the Python removal, which deploys and drops `alembic_version` | 10.3 |

- A yes for one step never covers the next. Merging to `main` deploys (Coolify builds from it), so every merge is an owner yes, including the merge of local-only work.
- The production dump stays on this machine, outside the repository and the scratchpad's published paths. It is deleted at the end of Task 6.
- Claude never types a password. The owner types each one into stdin.
- No Coolify MCP call writes anything without its own yes: `manage_env`, `deploy`, `control`.

**Prerequisites: status on `main` `9fb4981`**

| Item | Status |
|---|---|
| Every `ROUTES.md` route `ported` (107 `/api/v1`, 7 OAuth and discovery, `/mcp` and `/mcp/`) | done |
| Every ported `TESTS.md` row has a mutation; every not-ported row has a reason (503 ported, 13 not ported) | done |
| `TESTS.md` has no `todo` row | **4 left**: Task 1 |
| Hosting restore and enforcement (`hosting.Restorer`, `jobs.HostingEnforcement`) | done (plan 9) |
| Scheduler with `SCHEDULER_ENABLED` (Go default `false`, `config.go:103`), `scheduled_job_runs` (00004) | done (plan 9) |
| Resend sender with idempotency key (`email.IdempotencyKey`); real receipt renderer (PN 82) | done (plan 9) |
| MCP and OAuth (00005–00007) and the frontend `return_to` edit (`check.sh` "frontend return_to check") | done (plan 10) |
| Only finalized invoices can be sent (PN 112) | landing on `feat/go-send-finalized-only`; treat as landed |
| `bash scripts/check.sh` passes | re-run in Task 1 |

**The scheduler: never both at once**
- Python and Go use the same advisory-lock keys (`crc32(job_id)`: `jobs.LockKey`, `backend-python/app/workers/scheduler.py:41`). That stops *concurrent* runs only.
- Python writes no `scheduled_job_runs` rows. With both schedulers on, a slot runs twice, one run after the other (PN 119). Exactly one scheduler is on at any time.
- Coolify variables are shared by every service of the compose resource, so one `SCHEDULER_ENABLED` variable cannot hold two values. The compose maps distinct names:
  - Python: `SCHEDULER_ENABLED: ${PY_SCHEDULER_ENABLED:-true}`;
  - Go: `SCHEDULER_ENABLED: ${GO_SCHEDULER_ENABLED:-false}`.
- On its first start with the scheduler on, Go runs each job's latest slot from the past 24 hours that has no row. Python has usually run that slot already, so it runs a second time. This is safe only because each job is idempotent (spec "Background work"). Task 5 proves it locally, and the switch avoids 02:00–05:59 UTC, which is 09:00–12:59 WIB.

**Migrations 00002–00007 are additive.** Python keeps working on the same database during the compare week and after a way back:
- 00002: `users.session_epoch NOT NULL DEFAULT 0`; Python inserts get the default.
- 00003: `login_attempts`.
- 00004: `scheduled_job_runs`.
- 00005: `oauth_spent_codes`.
- 00006: `mcp_spent_confirm_tokens`.
- 00007: an index on Python's `oauth_refresh_tokens(user_id)`.

All are new tables or an index; Python's models never name them. 00007 takes a short write lock while it builds, on a table of a few rows. Python's `alembic upgrade head` at start stays a no-op at `0028_business_npwp`.

**The comparison**
- It never sends mail or calls Cloudflare or Frankfurter:
  - local runs leave `RESEND_API_KEY` and `CLOUDFLARE_API_TOKEN` unset (Go then logs mail and skips Cloudflare);
  - `FX_PROVIDER_BASE_URL` points at a local stub;
  - both schedulers are off.
- The production read comparison sends GET only. The one exception is Python's `POST /auth/login`, which writes nothing (`backend-python/app/api/v1/routers/auth.py:44-60`).
- Each allowed difference names its PORT-NOTES number or spec line. No wildcard covers a whole route.

**Tests, commits, delegation**
- Tests run on pgtestdb where a database is needed. Every test and subtest calls `t.Parallel()` first; lint enforces it.
- Commits name their paths. Messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Branches:
  - `feat/go-compare-switch` for Tasks 1–4;
  - `feat/go-switch-compose` for Task 5;
  - `chore/remove-python` for Task 10.
- Code tasks are implemented by opencode in a worktree and reviewed by Claude (memory: opencode implements, Claude reviews). **The owner chooses the opencode model** before Task 1 starts.

## Review Focus

1. A decimal `"1500.0000"` vs `"1500"` compares equal, and so do `"-0.0000"` vs `"0"` (PN 99). `"1500.0001"` vs `"1500"` does not. Task 2: `TestDecimalStringsEqualByValue`.
2. A list whose Python order was undefined (PN 40/41/44/47/53/78/100) compares as a multiset only on the routes listed for it, and in order everywhere else. Task 2: `TestUnorderedOnlyWhereAllowed`.
3. When both backends create an invoice in a write comparison, Python's new id maps to Go's before rows and later calls are compared. Task 4: `TestIdMappingAcrossWrites`.
4. nginx keeps serving while the Go container is down, as long as `BACKEND_UPSTREAM` points at Python. The web `depends_on` names the Python API only until Task 10. Task 5: compose check.
5. Exactly one scheduler is on in every state of the runbook (side by side, switched, way back). The first Go start after Python's run creates no second invoice, sends no second digest, and makes no Cloudflare change. Task 5: rehearsal steps 3 and 6.
6. After the way back, the owner can sign in to Python. Task 5: the rehearsal's last check.

---

## Local work (no production contact)

### Task 1: Close the last prerequisites

**Files:**
- Modify: `backend/TESTS.md`.
- Create: a Go test for the bill payment rule if none exists, plus one Sentry test.

The four `todo` rows:

| Python test | Decision |
|---|---|
| `test_record_payment_withholding.py::test_bill_payment_schema_still_requires_positive_amount` | Map it to the bills record-payment test that refuses `amount: 0` with 422. Write that test if it is missing, and record its mutation. |
| `test_recurring_scheduling.py::test_fix_recurring_cycle_keys_plan_and_apply` | Not ported. Reason: the Python one-off repair script `scripts/fix_recurring_cycle_keys.py`. Go's trigger takes one key spelling under a lock (PN 35). Its dry run on the production copy is Task 6 step 5. |
| `test_recurring_scheduling.py::test_fix_script_ignores_void_and_fixes_live_key` | Same as the row above. |
| `test_sentry_disabled_in_tests.py::test_create_app_does_not_enable_sentry_under_pytest` | Port it as a test that `apitest.New` starts no Sentry client (no `SENTRY_DSN`), or mark it not ported with the reason that `observe` is off without a DSN. Pick whichever has a real mutation. |

- [ ] **Step 1:** Make the four rows not `todo`. Re-run the prerequisite table above against `main` and stop on any gap.
- [ ] **Step 2:** `bash scripts/check.sh` passes. Commit `test(go): close the last TESTS.md rows`.

---

### Task 2: The diff engine and allowed differences

**Files:**
- Create: `backend/internal/compare/diff.go`, `allowed.go`, `diff_test.go`, `allowed_test.go`, `backend/port/allowed-diffs.yaml`

**Interfaces:**
- `type Allowed struct { … }` is loaded from YAML. Entries take the form `{route: "GET /api/v1/…", path: "$.items[*].expected_cash", rule: …, note: "PN 50"}`. Rules:
  - `decimal-equal`;
  - `instant-equal`;
  - `ignore`;
  - `unordered`;
  - `number-equal` (`10` vs `10.0`, PN 104);
  - `status`;
  - `detail-shape`.

  There are also global rules for decimals, instants and the error shape.
- `func Diff(route string, py, gov Response, a Allowed) []Difference`, where:
  - `type Response struct { Status int; Header http.Header; Body []byte }`;
  - `type Difference struct { Path, Python, Go, Rule string }`.

Rules (spec "The comparison"):
- Decimal strings compare by value, and `-0.0000` equals `0` (PN 99).
- RFC 3339 and naive timestamps compare as instants. A naive value is UTC unless `--python-tz` says otherwise (Task 6 step 1).
- Python's `{"detail": d}` equals problem+json with the same `detail` and status.
- OAuth errors keep `{"error","error_description"}` on both sides.
- PDFs compare status, `Content-Type` and `Content-Disposition` only.
- Every other difference is reported.

`allowed-diffs.yaml` gets one entry per inventory §3 item, with its note. These include:
- PN 6: a tie that Go holds;
- PN 97/98: balance sheet `as_of` and the open-AR rule;
- PN 104 and 108: `10.0` and the `Z`;
- PN 112: a DRAFT invoice send is 400 in Go.

- [ ] **Step 1: Failing tests.**
  - `TestDecimalStringsEqualByValue`, including negative zero.
  - `TestInstantsEqualAcrossFormats` (`2026-01-01T00:00:00` vs `…Z`).
  - `TestErrorShapeEqual`; a different `detail` is reported.
  - `TestUnorderedOnlyWhereAllowed`.
  - `TestIgnoreNeedsNote`: loading an entry without `note` fails.
  - `TestNullVsMissingIsDifference`.
  - `TestPdfComparesHeadersOnly`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: compare decimals as strings. The decimal test goes red.
  - Commit `feat(go): comparison diff engine and allowed differences`.

---

### Task 3: The corpus and the read runner

**Files:**
- Create: `backend/cmd/compare/main.go`, `backend/internal/compare/corpus.go`, `runner.go`, `runner_test.go`, `backend/port/compare-corpus.yaml`
- Modify: `backend/Dockerfile`. Add a second `go build … -o /out/compare ./cmd/compare` and `COPY --from=build /out/compare /compare`. `ENTRYPOINT` stays `/api`.

**Interfaces:**
- `compare reads --python URL --go URL --python-email E [--python-password-stdin | --python-cookie-stdin] --go-token-stdin --corpus FILE --allowed FILE [--python-tz ZONE] --out report.json`
- Exit codes: 0 when clean, 1 when there are differences, 2 on error.

**Corpus:** every GET in inventory §2, plus GETs the frontend never calls (`/invoices/recurring-templates`, `/accounting/fx-rates/latest`, `/payments/{id}`), with the frontend's query variants:
- repeated `status`;
- `?active=true`;
- `as_of`.

It also includes the two `/.well-known/oauth-*` documents. Both backends must print the same issuer and endpoints from `MCP_PUBLIC_BASE_URL`.
- `/mcp` (JSON-RPC over POST) is outside the read comparison; its own tests and the owner's check in Task 9 cover it.
- Path ids are discovered from list responses (the first, last and a middle element) rather than hard-coded.
- `window.open` PDFs are included.

**Sign-in:**
- Python: `POST /auth/login` with the email and a password from stdin. Alternatively, the owner's Python `billing_session` cookie from stdin, which sends no POST at all.
- Go: a token from `api issue-session <email>` on stdin, sent as the `billing_session` cookie (`platform.SessionCookie`).

- [ ] **Step 1: Failing tests** with two `httptest` servers:
  - `TestRunnerDiscoversIdsFromLists`;
  - `TestRunnerReportsOnlyUnallowed`;
  - `TestRunnerNeverSendsNonGet`: only Python's login POST may pass;
  - `TestRunnerExitCodes`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: let a second POST through. `TestRunnerNeverSendsNonGet` goes red.
  - Commit `feat(go): compare reads runner and corpus`.

---

### Task 4: The write runner and row comparison

**Files:**
- Create: `backend/internal/compare/writes.go`, `rows.go`, `writes_test.go`, `backend/port/compare-writes.yaml`

**Interfaces:**
- `compare writes --python URL --python-db DSN --go URL --go-db DSN …`

Scenarios are ordered flows over the inventory §2 writes:
- master data CRUD;
- invoice create → finalize → record payment → payments list → reverse;
- recurring template → trigger;
- quotation → accept → convert;
- bill → pay → void;
- webhook intake (signed with the shared local `WEBHOOK_HMAC_SECRET`) → approve → reverse.

The flows exclude every send, forgot-password, `fx-rates/sync`, and any PAID transition on an invoice with hosting lines.

Each response's created ids map Python → Go, and later requests use each backend's own ids. Afterwards, `rows.go` compares the touched tables row by row:
- ids are mapped;
- `created_at`, `updated_at`, `posted_at` and generated numbers are normalised by named rules;
- journal lines compare as multisets per entry;
- Go-only tables are not compared: `goose_db_version`, `login_attempts`, `scheduled_job_runs`, `oauth_spent_codes`, `mcp_spent_confirm_tokens`.

- [ ] **Step 1: Failing tests** on pgtestdb, with two databases and two fake servers:
  - `TestIdMappingAcrossWrites`;
  - `TestRowsDiffMapsIds`;
  - `TestJournalLinesAsMultiset`;
  - `TestScenarioStopsAtFirstHardFailure`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: skip the id mapping. The mapping test goes red.
  - Commit `feat(go): compare writes runner and row diff`.
  - Open the PR for Tasks 1–4. Merging waits for the owner's yes: it deploys, but only an image change, and Go is not in the Coolify compose yet.

---

### Task 5: Compose, runbook and a local rehearsal of the switch and the way back

**Files:**
- Modify: `docker-compose.coolify.yml`:
  - Add `wenhao-billing-api-go`:
    - build `./backend`, `Dockerfile`;
    - image `wenhao-billing-api-go:coolify`;
    - no `SERVICE_FQDN_*`;
    - healthcheck `["CMD","/api","healthcheck"]`.
  - Go's environment uses the Python service's values for every name in inventory §5: `DATABASE_URL`, `SESSION_SECRET`, `WEBHOOK_HMAC_SECRET`, `EMAIL_WEBHOOK_TOKEN`, `CORS_ORIGINS`, `APP_BASE_URL`, `MCP_*` (with `MCP_PUBLIC_BASE_URL: ${SERVICE_URL_WEB_80:-https://billing.wenhao.id}`), `RESEND_*`, `CLOUDFLARE_API_TOKEN`, `SENTRY_*`. Leave `ENVIRONMENT` unset.
  - Go gets `SCHEDULER_ENABLED: ${GO_SCHEDULER_ENABLED:-false}`; Python gets `SCHEDULER_ENABLED: ${PY_SCHEDULER_ENABLED:-true}`.
  - Web gets `BACKEND_UPSTREAM: ${BACKEND_UPSTREAM:-wenhao-billing-api:8000}`. The web `depends_on` stays on the Python API only (Review Focus 4).
  - Update the header comment.
- Modify: `.env.coolify.example`. Add `BACKEND_UPSTREAM`, `PY_SCHEDULER_ENABLED` and `GO_SCHEDULER_ENABLED` with their defaults.
- Modify: `docker-compose.yml`. Add a tools-profile `api-py-cmp` service:
  - build `./backend-python`;
  - `DATABASE_URL: ${PY_CMP_DATABASE_URL:-…/billing_py_scratch}`;
  - `SCHEDULER_ENABLED: "false"`;
  - port `${PY_CMP_HOST_PORT:-8002}:8000`.

  Also add a tools-profile `fx-stub` service, and give `api-go` `FX_PROVIDER_BASE_URL: http://fx-stub`. Add no new project or network, and make no change under `backend-python/`.
- Create: `scripts/compare-local.sh`, `scripts/rehearse-switch.sh`, `scripts/rehearse-switch.compose.yml` (a local Postgres beside the Coolify compose), `docs/runbooks/go-switch-over.md`.

- [ ] **Step 1: The runbook.** For each production step it gives:
  - the exact commands;
  - the **OWNER YES** marker;
  - the expected output;
  - the rollback at that point.

  Sections: timezone, dump, env names, adopt, deploy, memory, reads, switch, set-password, MCP reconnect, watch, way back, remove Python. Distroless details:
  - `docker exec -i <go> /api set-password <email>` (stdin);
  - `{ IFS= read -rsp 'Python password: ' PW; printf '%s\n' "$PW"; docker exec <go> /api issue-session <email>; } | docker exec -i <go> /compare reads … --python-password-stdin --go-token-stdin` (stdin carries the Python password first, then the Go token; the password is typed into `read -s` and never echoed, and the token is issued inside the pipe, so neither touches the screen);
  - Python's reset never puts the password in argv history: `docker exec -it <py> sh -c 'read -rs P; python -m scripts.seed_admin <email> "$P"'`.
- [ ] **Step 2: Script the rehearsal.** It uses `docker-compose.coolify.yml` locally (the `.env.coolify.example` route) with a restored dev database. Each check asserts HTTP 200 from `/api/v1/auth/me` through nginx. curl treats `http://localhost` as secure, so the Secure cookie is sent.
  1. Python serves. `PY_SCHEDULER_ENABLED=true`.
  2. Adopt, then add Go with `GO_SCHEDULER_ENABLED=false`. Python still serves, and Go's container is healthy.
  3. **Scheduler overlap check.** Trigger Python's recurring scan for today's slot on a template that is due. Count invoices, `email` log lines and FX rows.
  4. Switch: `BACKEND_UPSTREAM=wenhao-billing-api-go:8000`, `GO_SCHEDULER_ENABLED=true`, `PY_SCHEDULER_ENABLED=false`, redeploy.
     - Go runs the latest slots. Assert no new child invoice, no digest mail, an unchanged FX row count (or an upsert of the same rows), and that each job now has a `scheduled_job_runs` row for today's slot.
  5. `set-password` and sign in through nginx.
  6. **MCP carry-over.** Register a client and get a refresh token from Python before step 4. After step 4, a refresh against Go works. After step 5 it is `invalid_grant` (PN 157), and a fresh authorize through Go's login (`return_to`) works.
  7. Way back: upstream to Python, swap the flags, redeploy. Python's scheduler starts and Go's stops. Run `seed_admin`, then sign in to Python through nginx. Python accepts a Go-issued refresh token (same secret, same table).
  8. Kill Go's container while the upstream is Python. The site still serves (Review Focus 4).
- [ ] **Step 3: Run it twice, clean.** Commit, then open the PR on `feat/go-switch-compose`.
  - **Do not merge yet.** Merging deploys Go beside Python, which is Task 8 step 2.
  - If step 3 or 4 shows a second run that is not idempotent, stop and report. The fallback is a runbook statement that inserts today's `scheduled_job_runs` rows before the switch (its own OWNER YES).

---

## Production work (each step OWNER YES REQUIRED)

### Task 6: Local comparison on a production copy

- [ ] **Step 1: Time zone. OWNER YES REQUIRED (P1).** Run `SHOW timezone;` on production (read-only).
  - If it is UTC, record that.
  - If it is not, stop and add a code task before step 3: the Go pool sets `TimeZone=UTC`, and reads of existing naive timestamps convert from the server zone, with a test on pgtestdb set to that zone. The comparison then runs with `--python-tz <zone>`.
- [ ] **Step 2: Dump. OWNER YES REQUIRED (P2).** Take a `pg_dump -Fc` of production to a file outside the repository.
  - Restore it twice into the local `db`, as `billing_cmp_py` and `billing_cmp_go`.
  - Set a known local password on each:
    - Python copy: `seed_admin`, run in the `api-py-cmp` container;
    - Go copy: `api set-password`, after `api adopt-baseline` on that copy.
  - Adopt must pass on the copy. If it refuses on drift, production would refuse too: stop and report.
  - Account 6000 (PN 164): after restoring `billing_cmp_py`, run on it the same statement Go's migration `00008_default_expense_account.sql` runs, so Python's copy also has account 6000 and the chart of accounts (and the account ids the write run creates) line up with Go's:
    `INSERT INTO chart_of_accounts (code, name, type) VALUES ('6000', 'Uncategorised Expense', 'EXPENSE') ON CONFLICT (code) DO NOTHING;`
    Without it the read run reports the Go-only account (`chart-of-accounts` `$[14]`, and `chart-of-accounts/15` 404 against 200), which no allowed entry can express narrowly.
  - Order of a run: fresh restore of both copies → the 6000 insert on `billing_cmp_py` → adopt and passwords → `api backpost-ledger --apply` on `billing_cmp_go` (PN 166) → reads → writes. The write run changes both copies, so a read re-run needs a fresh restore first.
- [ ] **Step 3: Reads.** Run `scripts/compare-local.sh reads`. Fix each Go defect in a fix PR, or add an allowed entry with its PORT-NOTES note. Repeat until the run is clean.
  - Allowed files: pass `--allowed port/allowed-diffs.yaml --allowed port/allowed-diffs-prodcopy.yaml`. The second file (production-copy run only) ignores the exact trial-balance, balance-sheet and FX-revaluation paths that differ because Go's copy holds the back-post and Python's ledger is empty (PN 166; the corpus labels the three cases `PN 166 (production-copy run only)`).
  - How several files combine: every entry of every file applies; none replaces another. An `ignore` hides its path whatever another entry says, and a literal route in a later file stops an earlier file's `{param}` entries applying to that route (a literal route is preferred). The loader prints each such overlap to stderr: on this run exactly 5 are expected (the overlay's `$.rows`, `$.groups` and `$.lines` ignores over the shipped unordered and decimal-equal entries of those fields). Any other overlap warning means an overlay changed the shipped file's meaning: stop and check it.
  - Ledger self-checks: also pass `--ledger-matches-aging`. The runner checks Go's three ledger answers against themselves (trial balance balanced and equal to its rows' sums; each balance-sheet group equal to its lines and assets = liabilities + equity + income − expense; FX totals equal to the lines' gains and losses) and, with the flag, Go's 1100 and 2000 per currency against Go's AR and AP aging totals. Each failure is a difference (`rule: self-check`) and fails the run. A customer advance (an overpaid invoice, PN 166) leaves 1100 below the AR aging and fails the cross-check: review it against the back-post's advance list. Never pass the flag before the back-post (Task 8's run). The remaining hand checks (bank and payment totals, withholding, 4900, the FX bank figure) are listed in the overlay file's header: do them on each run.
  - Skips: run with `--allow-skips`. Production has no vendors, items, projects, quotations or bills, so 9 read cases skip with "list empty or failed": vendor-detail, item-detail, project-detail, project-summary, project-invoices, the `quotations ?project_id={project_id}` variant, quotation-detail, quotation-pdf and bill-detail. Record that reason with the run. Those routes are covered by the write comparison's master-data, quotation and bill scenarios and by Go's own tests.
  - Read the stderr warnings and the report's `unused_allowed`: an entry listed there never applied to anything in the run. On the production copy some entries are expected there (status entries for bad input the reads never send); one naming a field the answer does not have is a dead entry to fix.
- [ ] **Step 4: Writes.** Run `scripts/compare-local.sh writes` until it is clean in the same way.
  - Pass `--allowed port/allowed-diffs.yaml` (not the production-copy file): the step answers need its PN 91 `confidence_score` entries. A manual payment's `external_ref` (`manual-` and 12 hex digits, random on each side) is masked by the tool in answers and in the payments row.
- [ ] **Step 4b: Back-post dry run (plan 12, PN 166).** On `billing_cmp_go`, run `api backpost-ledger` (no `--apply`: it writes nothing).
  - Report to the owner: its counts (invoices issued, bills opened, payments restated, payments posted, legacy bill payments, customer advances), the per-account base totals, every missing rate, bad account and `inconsistent` line, the owner-decision list (CLEARED payments on VOID, DRAFT, template or zero invoices, never posted), the customer-advance list (PAID invoices overpaid under Python: number, customer, currency, overpaid amount and its base; the negative receivable stays on the ledger as the customer's credit), and every warning (VOID bills partly paid, null issue dates, payments on the ledger before their invoice's issue date, face ≠ `balance_due` on invoices with no payment).
  - Exit 1 means refused: the owner enters each missing rate (or fixes each account, or decides each inconsistent payment) in production before the switch, then the dry run is repeated on a fresh copy until it exits 0.
  - Gate: exit 0 is not enough. The owner reviews every warning line, every owner-decision line and every customer-advance line of the last dry run, and records a decision for each (accept, or fix in production and rerun; for an advance, refund it or apply it to a later invoice). Task 9 Step 0 cannot ask for its `--apply` yes until that review is recorded.
- [ ] **Step 5: Data checks** on `billing_cmp_py` (local copy, read-only).
  - Run the dry-run (plan) mode of `python -m scripts.fix_recurring_cycle_keys`, and a query for two non-VOID children with one template and one cycle date (PN 35).
  - Run a query for usage drafts whose `cut_off_day` falls in the coming week (PN 1/48: the Go job errors on them as Python's does).
  - Report the counts to the owner.
- [ ] **Step 6: Memory.** Measure local idle RSS, for reference only.
- [ ] **Step 7: Clean up.** Delete the dump file and both copies. Commit the corpus and allowed-diff changes.

### Task 7: Production configuration

- [ ] **Step 1: Names. OWNER YES REQUIRED (P3).** Run `list_env_keys` on the billing resource (names only). Confirm these are present:
  - `DATABASE_URL`;
  - `SERVICE_BASE64_64_{SESSION,WEBHOOK,EMAILTOKEN,MCPCONFIRM,MCPTOKEN}`;
  - `SERVICE_URL_WEB_80`;
  - `RESEND_API_KEY`;
  - `CLOUDFLARE_API_TOKEN`;
  - `SENTRY_DSN`.

  With `ENVIRONMENT` unset, Go is in production mode and refuses to start in each of these cases:
  - `SESSION_SECRET` under 32 bytes;
  - an empty or default `WEBHOOK_HMAC_SECRET` or `EMAIL_WEBHOOK_TOKEN` (PN 90);
  - an `MCP_*_SECRET` under 16 bytes or at its default (PN 153).

  The `SERVICE_BASE64_64_*` values pass all three checks.
- [ ] **Step 2: New variables. OWNER YES REQUIRED (P4).** Set:
  - `GO_SCHEDULER_ENABLED=false`;
  - `PY_SCHEDULER_ENABLED=true`;
  - `BACKEND_UPSTREAM=wenhao-billing-api:8000`.

  These equal the compose defaults, so nothing changes until the next deploy. They make the state explicit.

### Task 8: Side by side on Coolify (spec switch-over steps 1–2)

- [ ] **Step 1: Adopt. OWNER YES REQUIRED (P5).** Run `api adopt-baseline` against production, with the Go image as a one-off container on the shared network.
  - It refuses a head other than `0028_business_npwp`, or drift. Stop and report if it does.
  - It writes `goose_db_version` (versions 0 and 1).
  - Tell the owner that the next step's serve applies 00002–00007 to production, and that all of them are additive (Global Constraints).
  - Rollback: none needed. Python ignores `goose_db_version`.
- [ ] **Step 2: Deploy. OWNER YES REQUIRED (P6).** Merge the Task 5 PR, which deploys.
  - Confirm that both API services are healthy, that the site still serves Python, and that Go logs `scheduler is off (SCHEDULER_ENABLED)`.
  - Measure Go's RSS after start-up.
  - Rollback point A: revert the compose PR. Go's tables stay; they are harmless.
- [ ] **Step 3: Read comparison. OWNER YES REQUIRED (P7).** Run `/compare reads` inside the Go container, against `http://wenhao-billing-api:8000` and `http://127.0.0.1:8000`.
  - The owner types the Python password, or pastes the cookie, into stdin.
  - The Go token comes from `issue-session` through a pipe.
  - The run must be clean, or every difference allowed. A Go defect goes to a fix PR first, which needs its own merge yes.
- [ ] **Step 4: Memory again.** Measure RSS ten minutes after the run. Report both figures against the ~30 MB goal.

### Task 9: Switch (spec step 3) and, only if needed, the way back (step 4)

- [ ] **Step 0: Back-post. OWNER YES REQUIRED.** After the migrations (Task 8) and before traffic moves (Step 1), with Python's scheduler and writes stopped (runbook), run `api backpost-ledger` once more (dry run) and compare it with Task 6 Step 4b; then run `api backpost-ledger --apply` in the Go container (PN 166).
  - Gate before the yes: the owner has reviewed every warning line, every owner-decision line and every customer-advance line of this dry run (any not in Task 6 Step 4b's recorded review is reviewed now) and recorded a decision for each. Ask for the `--apply` yes only after that; a line nobody has reviewed means stop.
  - Exit 0 and `applied: committed` is required. Exit 1 (refused) or 2 writes nothing: stop and report.
  - Then the trial-balance check: Σ `base_amount_debit` = Σ `base_amount_credit` over `journal_lines` (read-only), and a second dry run reports 0 of everything except the customer advances, which it lists again unchanged.
  - Rollback: none needed for Python, which ignores the entries and `bill_payments` (PN 166, the way back).
- [ ] **Step 1: Switch. OWNER YES REQUIRED (P8).** The switch runs outside 02:00–05:59 UTC.
  - In Coolify set `BACKEND_UPSTREAM=wenhao-billing-api-go:8000`, `GO_SCHEDULER_ENABLED=true` and `PY_SCHEDULER_ENABLED=false`, then redeploy.
  - Go logs `scheduler started`, and Python logs no scheduler start.
  - Everyone signs in again: the cookie formats differ.
- [ ] **Step 2: Password. OWNER YES REQUIRED (P9).** The owner runs `set-password` (runbook). This also revokes the owner's MCP refresh tokens (PN 157).
- [ ] **Step 3: Owner checks.** The owner signs in and checks:
  - the dashboard;
  - an invoice PDF;
  - a quotation;
  - reports;
  - the review queue;
  - that a DRAFT invoice send is refused;
  - **reconnecting the MCP connector in Claude** (expected once, see "MCP connectors at the switch" below);
  - `/.well-known/oauth-authorization-server`, whose issuer must be the public origin.

  Record what they see.
- [ ] **Step 4: Watch.** Watch Sentry and the Go logs for 24 hours.
  - The next day, confirm the slots in `scheduled_job_runs` (four jobs; the FX job only after a Monday).
  - A usage-lock error is expected only if Task 6 step 5 found a due usage draft (PN 1).
- [ ] **Step 5 (only on the owner's call): Way back. OWNER YES REQUIRED (P10).** Follow the runbook's way-back section exactly as rehearsed:
  - set `BACKEND_UPSTREAM=wenhao-billing-api:8000`, `GO_SCHEDULER_ENABLED=false` and `PY_SCHEDULER_ENABLED=true`, then redeploy;
  - the owner runs `seed_admin`;
  - the MCP connector keeps working.

**MCP connectors at the switch.**
- Go's tokens are byte-compatible with Python's:
  - the same HS256 claims in PyJWT order (`oauth/tokens.go`);
  - the same `MCP_TOKEN_SECRET`;
  - the same `oauth_clients` and `oauth_refresh_tokens` rows;
  - the same issuer, as long as `MCP_PUBLIC_BASE_URL` is the same.
- So an existing connector would carry over. The owner's `set-password` in step 2 is unavoidable, because Go cannot read bcrypt, and it revokes every refresh token of the owner. The connector's access token works for at most one more hour (`MCP_ACCESS_TOKEN_TTL_SECONDS=3600`), then its refresh fails with `invalid_grant`, and the owner reconnects once. Owner accepted this one reconnect (2026-11-10); no token-keeping option for `set-password`.
- Clients registered with a plain `http://` or custom-scheme redirect URI must register again (PN 150). Claude's HTTPS callback is unaffected.
- On the way back, Python accepts Go-issued tokens, and `seed_admin` revokes nothing, so the connector keeps working.

### Task 10: Remove Python (spec step 5)

- [ ] **Step 1: Wait a week** with no way back.
- [ ] **Step 2: Remove the code.** On branch `chore/remove-python` (local work):
  - delete `backend-python/`;
  - remove the Python service from both compose files, `PY_SCHEDULER_ENABLED` from `.env.coolify.example`, and the `api-py-cmp` service;
  - in the Coolify compose:
    - point the web `depends_on` at `wenhao-billing-api-go`;
    - set the `BACKEND_UPSTREAM` default to `wenhao-billing-api-go:8000`;
    - set Go's `SCHEDULER_ENABLED` default to true;
  - make nginx's `/(docs|redoc|openapi.json)` route `/(docs|openapi.json)` (Go has no `/redoc`);
  - add goose migration `00008_drop_alembic_version.sql`;
  - make `api adopt-baseline` refuse with a clear sentence once `alembic_version` is gone;
  - remove the "frozen Python tests" step from `scripts/check.sh`, and its header line;
  - update CLAUDE.md, and AGENTS.md (the owner's uncommitted edit is in the main worktree: ask before touching it).
- [ ] **Step 3: Check and merge.** `bash scripts/check.sh` passes. Open the PR. **OWNER YES REQUIRED (P12)** to merge; the merge deploys. There is no way back after it.
- [ ] **Step 4: Close out.** Hand the owner the "After the switch" table below to schedule. P11 (PN 35 voids) is its own OWNER YES.

---

## After the switch: PORT-NOTES follow-ups

Every `After the switch:` line in `backend/PORT-NOTES.md` that is not "none" (159 notes; the lines for 2, 112 and 124 are now "none" by the owner's 2026-11-10 decisions). "Backlog" means post-switch work for the owner to schedule; it never blocks the switch.

| PN | Follow-up | Owner |
|---|---|---|
| 1, 48 | Wire a usage-metering source, or retire the usage-lock scan and keep the manual lock | Backlog. Task 6.5 counts the drafts that are due; Task 9.4 expects the error |
| 3 | `GET /payments/{id}/log`: 404 for an unknown payment | Backlog |
| 4 | Business-profile GET: commit, or stay read-only with defaults | Backlog |
| 5, 95 | One business "today" (Asia/Jakarta) via `platform.TodayUTC`, plus the reports screen's browser date; the email intake zone | Backlog. Task 6.1 settles the DB zone |
| 7, 94 | Reversal restores the relieved balance and the earlier status; held payments get no invoice until approved | Backlog |
| 8, 73, 81 | Post manual invoice and bill payments to the ledger; keep a bill payment row; refuse a payment on a PAID invoice | Backlog |
| 18 | Refuse a whitespace-only `hosting_domain` or target | Backlog |
| 19 | List every refused price field in one 422 | Backlog |
| 21 | Check `role` against the known roles | Backlog |
| 23 | `display_name: null` clears it | Backlog |
| 33 | If Frankfurter moves, change `FX_PROVIDER_BASE_URL` | Backlog (ops) |
| 34 | Decide whether milestone invoices get their number at finalize | Backlog (owner decision) |
| 35 | Find and void duplicate recurring children | Task 6.5 finds them; the voids are P11, post-switch |
| 39, 68 | Write routes return 404 for a missing row | Backlog |
| 40 | Keep templates out of the customer views | Backlog |
| 41 | Drafts in "unpaid"; calendar-month window | Backlog (owner decision) |
| 42 | Void refused while payments exist; store the reason; end templates rather than void them | Backlog |
| 43 | PATCH refuses templates or recomputes the due date | Backlog |
| 44 | Round the subtotal from rounded lines; cap a percent discount at 100 | Backlog |
| 45 | Date children by cycle; warn before triggering an ended template | Backlog |
| 46 | Hosting window from the frequency; label only hosting lines | Backlog |
| 47 | Previous cycle by `cycle_key`; validate end dates; flag a past `next_run_date` | Backlog |
| 52, 77 | One currency rule: upper case, known to the ledger, no change once lines exist | Backlog |
| 53, 78 | `limit` or paging on the awaiting-finalization, project, bill and project-invoice lists | Backlog |
| 54 | Keep locked usage totals on PATCH, or refuse the edit | Backlog |
| 62 | Project delete message names quotations, or skips VOID and templates | Backlog |
| 63 | Project summary sums per currency | Backlog |
| 64 | Convert only ACCEPTED quotations; an explicit `due_in_days` wins | Backlog |
| 65 | A job that expires SENT quotations; refuse accepting an expired one | Backlog |
| 66 | Close the quotation state machine; void takes a reason | Backlog |
| 67 | Regenerate a cleared quotation number; refuse empty `line_items` | Backlog |
| 69 | An explicit bill open action; a bill status machine | Backlog |
| 70 | Recompute bill totals on every edit; refuse an amount below what is paid | Backlog |
| 71 | One rounding rule for every document | Backlog |
| 72 | A bill needs a line; due date from terms; unique `(vendor_id, bill_number)` | Backlog |
| 74 | Typed bill-void body, reason required, REST bill delete | Backlog |
| 75 | Optional body on accept, convert and bill void | Backlog |
| 79 | Refuse another customer's project; warn on a currency mismatch | Backlog |
| 82 | Real receipt renderer and Resend before the switch | Done on `main` (plan 9); checked in the prerequisites |
| 85 | Manual approval refuses PAID and refuses or splits an overpayment | Backlog |
| 90 | Intake secrets set in Coolify before Go's first start | Task 7.1 |
| 101 | A current-result line on the balance sheet; inactive accounts in the trial balance | Backlog (owner decision) |
| 102 | Dashboard counts match their names | Backlog |
| 103 | Monthly revenue at each month-end rate | Backlog |
| 104 | `avg_days_to_pay` per invoice | Backlog. Task 2 allows `10` vs `10.0` |
| 105 | One upcoming-invoice rule and one cut-off parser | Backlog |
| 106 | Revaluation residue line; upper-case rate lookup | Backlog |
| 107 | AR aging filters `issue_date <= as_of`; place NULL due dates | Backlog |
| 108 | `nullif(display_name, '')` in recent activity | Backlog. Task 2 allows the `Z` |
| 116 | Drop or write the two unused hosting item columns | Backlog |
| 126, 135 | Answered 2026-11-10: keep the PO/SO cell and footer; keep the 422 on a bad `to_email`; only finalized invoices can be sent (landed) | Done, nothing to do |
| 127 | Iterate the PDF design toward the HTML | Backlog (owner review in Task 9.3) |
| 129 | Split an item taller than a page | Backlog |
| 132 | Draft digest only to active or admin users | Backlog (owner decision) |
| 153 | `MCP_CONFIRM_SECRET` and `MCP_TOKEN_SECRET` set in Coolify | Task 7.1 |

**"none" lines that still bind this plan:**
- PN 6, 86, 97, 98, 99, 100, 104, 108, 112: expected compare differences; Task 2 allowed entries.
- PN 119, 120: one scheduler at a time, and the catch-up run; Global Constraints and Task 5.
- PN 148, 150, 154, 157: MCP reconnect; Task 9.
- PN 131: the SMTP, `PDF_ENGINE` and reportlab removals happen with Task 10.
