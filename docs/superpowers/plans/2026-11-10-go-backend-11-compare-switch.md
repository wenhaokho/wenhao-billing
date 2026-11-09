# Go backend, plan 11: comparison, side by side, switch-over (DRAFT)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Draft, written 2026-11-10 before plans 7–10 merged.** Before execution, a refresh pass checks every count, file and config name below against `main`. Tasks 1–4 are code; Tasks 5–9 touch production, and **every step that deploys, writes to or changes production waits for the owner's yes in chat**.

**Goal:** Prove Go answers the frontend as Python does, run it beside Python on Coolify, switch the site to it with a rehearsed way back, and delete Python a week later.

**Architecture:**
- `backend/cmd/compare` replays a corpus of the frontend's calls against two base URLs and diffs the JSON under `backend/port/allowed-diffs.yaml`.
- Scripts run it locally, on a restored dump for reads and on two dump copies for writes and rows.
- The `/compare` binary ships in the Go image, so the production read comparison runs inside Coolify's network.
- The Coolify compose gains `wenhao-billing-api-go`, and `BACKEND_UPSTREAM` becomes a variable, so switching (and switching back) is an environment change.

**Tech Stack:** Go 1.27 (stdlib `net/http`, `encoding/json`, `gopkg.in/yaml.v3` if not already vendored, else the vendored YAML library), pgx v5, Docker Compose, Coolify.

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` ("The comparison", "Builds and deploys", "Switch-over", "Database", "Dates", "Done").

**Inventory:** `docs/superpowers/plans/2026-11-10-go-backend-11-compare-switch-inventory.md`:
- §2 is the frontend call corpus;
- §3 lists the allowed differences;
- §4 is the switch prerequisites (22 rows);
- §5 is the env-var surface;
- §6 covers risks and rollback points.

## Global Constraints

**Owner approval**
- The owner approves, in chat, each of:
  - taking a production dump;
  - `SHOW timezone` on production;
  - `api adopt-baseline` on production;
  - each Coolify deploy;
  - the read comparison on production;
  - the switch;
  - the way back;
  - deleting Python.

  One yes covers one step. A yes for one step never covers the next.
- The production dump stays on this machine and is deleted after Task 4. It is never committed and never put in the scratchpad's published paths.

**Prerequisites**
- **Prerequisites before Task 5:** these must all be true on `main`:
  - every `ROUTES.md` route is `ported`;
  - every `TESTS.md` row is `ported`, merged, or not-ported with a reason;
  - every guard has a recorded mutation;
  - the hosting restore is ported (plan 9);
  - the scheduler exists with `SCHEDULER_ENABLED` (plan 9);
  - the MCP server and its frontend edit are in (plan 10);
  - `bash scripts/check.sh` passes.

  Task 5's first step checks this list and stops if anything is missing.

**The comparison**
- The comparison never sends mail or calls Cloudflare or Frankfurter:
  - `RESEND_API_KEY` and `CLOUDFLARE_API_TOKEN` are unset for local runs;
  - `FX_PROVIDER_BASE_URL` points at a local stub;
  - the production read comparison sends GET only.
- Allowed differences are named, each with its PORT-NOTES number or spec line. Nothing is allowed by a wildcard over a whole route.

**Tests and commits**
- Tests run on pgtestdb where a database is needed. Every test and subtest calls `t.Parallel()` first.
- Commits name their paths. Messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- The branch is `feat/go-compare-switch` for Tasks 1–4. Production tasks commit only compose and runbook changes, each on its own small branch and PR. **Merging to `main` deploys**, so every merge is an owner yes.

## Review Focus

1. A decimal `"1500.0000"` vs `"1500"` must compare equal, but `"1500.0001"` vs `"1500"` must not: Task 1 `TestDecimalStringsEqualByValue`.
2. A list whose Python order was undefined (PN 40/41/44/47/53/78) must compare as a multiset only on the routes listed for it, and in order everywhere else: Task 1 `TestUnorderedOnlyWhereAllowed`.
3. A write comparison where both backends create an invoice must map Python's new id to Go's before comparing rows and later calls: Task 3 `TestIdMappingAcrossWrites`.
4. nginx must keep serving the site if the Go container is down while `BACKEND_UPSTREAM` still points at Python: Task 4 `compose-check` step (web `depends_on` only the upstream in use).
5. The way back must leave the owner able to sign in to Python: Task 6 rehearsal, the final step's login check.

---

### Task 1: The diff engine and allowed differences

**Files:**
- Create: `backend/internal/compare/diff.go`, `allowed.go`, `diff_test.go`, `allowed_test.go`, `backend/port/allowed-diffs.yaml`

**Interfaces:**
- Produces:
  - `type Allowed struct { … }`, loaded from YAML. Entries:
    - `{route: "GET /api/v1/…", path: "$.items[*].expected_cash", rule: "decimal-equal"|"instant-equal"|"ignore"|"unordered"|"python-zero-vs-go-4dp"|"status"|"detail-shape", note: "PN 50"}`;
    - global rules for decimals, instants and the error shape.
  - `func Diff(route string, py, gov Response, a Allowed) []Difference`, where `type Response struct { Status int; Header http.Header; Body []byte }` and `type Difference struct { Path, Python, Go, Rule string }`.

Rules (spec "The comparison"):
- Decimal strings are compared by value.
- RFC 3339 and naive timestamps are compared as instants. A naive value is UTC unless the run's `--python-tz` says otherwise.
- Python `{"detail": d}` equals problem+json with the same `detail` and status.
- PDFs compare status, `Content-Type` and `Content-Disposition` only.
- Every other difference is reported.

`allowed-diffs.yaml` gets one entry per inventory §3 item, with its note.

- [ ] **Step 1: Failing tests.**
  - `TestDecimalStringsEqualByValue`.
  - `TestInstantsEqualAcrossFormats` (`2026-01-01T00:00:00` vs `2026-01-01T00:00:00Z`).
  - `TestErrorShapeEqual`, plus a different `detail` is reported.
  - `TestUnorderedOnlyWhereAllowed`.
  - `TestIgnoreNeedsNote`: loading an entry without `note` fails.
  - `TestNullVsMissingIsDifference`.
  - `TestPdfComparesHeadersOnly`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: compare decimals as strings. The decimal test goes red.
  - Commit `feat(go): comparison diff engine and allowed differences`.

---

### Task 2: The corpus and the read runner

**Files:**
- Create: `backend/cmd/compare/main.go`, `backend/internal/compare/corpus.go`, `runner.go`, `runner_test.go`, `backend/port/compare-corpus.yaml`

**Interfaces:**
- Produces:
  - `compare reads --python URL --go URL [--python-password-stdin] [--python-email E] [--go-token-stdin] --corpus FILE --allowed FILE --out report.json`.
  - Exit codes: 0 when clean, 1 when there are differences, 2 on error.

**Corpus:** every GET in inventory §2, plus the GETs the frontend never calls, with query variants as the frontend sends them:
- repeated `status`;
- `?active=true`;
- `as_of`.

Path ids are discovered from list responses (first, last and a middle element) rather than hard-coded. `window.open` PDFs are included.

**Sign-in:**
- Python: `POST /auth/login` with the owner's email and a password read from stdin.
- Go: a token from `api issue-session` read from stdin, sent as the `billing_session` cookie.

The report lists each difference and the route totals.

- [ ] **Step 1: Failing tests.** These use two `httptest` servers:
  - `TestRunnerDiscoversIdsFromLists`;
  - `TestRunnerReportsOnlyUnallowed`;
  - `TestRunnerNeverSendsNonGet` in reads mode;
  - `TestRunnerExitCodes`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: let a POST through in reads mode. `TestRunnerNeverSendsNonGet` goes red.
  - Also build `/compare` into `backend/Dockerfile`, beside `/api`.
  - Commit `feat(go): compare reads runner and corpus`.

---

### Task 3: The write runner and row comparison

**Files:**
- Create: `backend/internal/compare/writes.go`, `rows.go`, `writes_test.go`, `backend/port/compare-writes.yaml`

**Interfaces:**
- Produces: `compare writes --python URL --python-db DSN --go URL --go-db DSN …`.

Scenarios are ordered flows over the inventory §2 writes:
- master data CRUD;
- invoice create → finalize → record payment → payments list → reverse;
- recurring template → trigger;
- quotation → accept → convert;
- bill → pay → void;
- webhook intake (signed with the local secret), approve, reverse.

The flows send no mail, sync nothing and change no hosting.

Each response's created ids are mapped Python → Go. Later requests use each backend's own ids. Afterwards, `rows.go` compares the touched tables row by row:
- ids are mapped;
- `created_at`, `updated_at`, `posted_at` and generated numbers are normalised by the named rules;
- journal lines are compared as multisets per entry.

- [ ] **Step 1: Failing tests.** On pgtestdb with two databases and two fake servers:
  - `TestIdMappingAcrossWrites`;
  - `TestRowsDiffMapsIds`;
  - `TestJournalLinesAsMultiset`;
  - `TestScenarioStopsAtFirstHardFailure`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: skip id mapping. The mapping test goes red.
  - Commit `feat(go): compare writes runner and row diff`.

---

### Task 4: The local comparison on a production copy

**Files:**
- Create: `scripts/compare-local.sh`
- Modify: `docker-compose.yml`. The `api-go` (tools profile) service gains a second database copy and an FX stub. Use no new compose project or network.

- [ ] **Step 1: Prepare the databases.**
  - **Owner yes:** take a `pg_dump` of production to a local file outside the repository.
  - Restore it twice into the local `db`, as `billing_cmp_py` and `billing_cmp_go`.
  - Set a known local password on both copies: Python with `scripts.seed_admin`, Go with `api set-password`.
  - Run `api adopt-baseline` on the Go copy.
- [ ] **Step 2: Reads.** Run `compare reads` with both servers' schedulers off. Fix each Go defect, or add an allowed entry with a PORT-NOTES note. Repeat until the run is clean.
- [ ] **Step 3: Writes.** Run `compare writes` until it is clean in the same way.
- [ ] **Step 4: Memory.** Measure local idle RSS for reference only. The goal applies on the server.
- [ ] **Step 5: Clean up and merge.**
  - Delete the dump and both copies.
  - Commit the scripts and the corpus changes.
  - Open the PR. Merging waits for the owner.

---

### Task 5: Production prerequisites

- [ ] **Step 1: Check the prerequisite list** (Global Constraints) against `main`. Stop and report any gap.
- [ ] **Step 2: Time zone.** **Owner yes:** run `SHOW timezone;` on production (read-only).
  - If it is UTC, record that.
  - If it is not, add a plan step: the Go pool sets `TimeZone=UTC`, and reads of existing naive timestamps are converted from the server zone. The plan says how, with a test on pgtestdb set to that zone.
- [ ] **Step 3: Config.**
  - Give every Go env name in inventory §5 a Coolify value. Production uses the Python service's generated secrets: `SESSION_SECRET`, `MCP_*`, `WEBHOOK_HMAC_SECRET`, `EMAIL_WEBHOOK_TOKEN`, `RESEND_*`, `CLOUDFLARE_API_TOKEN`.
  - Leave `ENVIRONMENT` unset (production) and set `SCHEDULER_ENABLED=false`.
  - Check names only, through `list_env_keys`. Values are never printed.

---

### Task 6: Rehearse the switch and the way back locally

**Files:**
- Create: `scripts/rehearse-switch.sh`, `docs/runbooks/go-switch-over.md`

- [ ] **Step 1: Write the runbook.** For each step of the switch it says:
  - the exact commands;
  - which ones need the owner;
  - the expected output;
  - the rollback at that point.

  Its sections:
  - adopt;
  - deploy;
  - memory;
  - reads;
  - switch;
  - way back;
  - delete Python.

  Include the distroless details:
  - `docker exec -i <go container> /api set-password <email>` (stdin);
  - `docker exec <go container> /compare reads …`.
- [ ] **Step 2: Script the rehearsal** against the local compose stack, with a restored dev database:
  1. Python serves.
  2. Go is added.
  3. nginx's `BACKEND_UPSTREAM` changes to Go.
  4. The scheduler flags swap.
  5. Run `set-password` and sign in through nginx.
  6. Switch back to Python and swap the flags back.
  7. Run `seed_admin` and sign in to Python through nginx.

  Each check asserts an HTTP 200 from `/api/v1/auth/me`.
- [ ] **Step 3: Run it twice, clean.** Commit, open the PR, and wait for the owner.

---

### Task 7: Side by side on Coolify (steps 1–2 of the spec)

**Files:**
- Modify: `docker-compose.coolify.yml`:
  - add `wenhao-billing-api-go`: build `./backend`, `Dockerfile`, no `SERVICE_FQDN_*`, healthcheck `["CMD","/api","healthcheck"]`, `SCHEDULER_ENABLED=false`, the env names of inventory §5;
  - set `BACKEND_UPSTREAM: ${BACKEND_UPSTREAM:-wenhao-billing-api:8000}`;
  - keep the web `depends_on` on the Python API only (Review Focus 4).

- [ ] **Step 1: Adopt.** **Owner yes:** run `api adopt-baseline` against production, with the Go image as a one-off. It refuses on a head other than `0028_business_npwp` or on drift; stop and report if it does. This writes `goose_db_version`. Serve then applies the additive migrations, so say so to the owner when asking.
- [ ] **Step 2: Deploy.** **Owner yes:** merge the compose PR, which deploys.
  - Confirm both API services are healthy and the site still serves Python.
  - Measure Go's RSS after start-up.
- [ ] **Step 3: Read comparison.** **Owner yes:** run `/compare reads` inside the Go container, against `http://wenhao-billing-api:8000` and `http://127.0.0.1:8000`.
  - The owner types the Python password into stdin.
  - The Go token comes from `issue-session`.
  - Clean, or every difference allowed. A Go defect goes back to a fix PR first.
- [ ] **Step 4: Memory again.** Measure RSS ten minutes after the run, and report both figures against the ~30 MB goal.

---

### Task 8: Switch (spec step 3) and, only if needed, the way back (step 4)

- [ ] **Step 1: Switch.** **Owner yes:** in Coolify, set `BACKEND_UPSTREAM=wenhao-billing-api-go:8000`, Go `SCHEDULER_ENABLED=true` and Python `SCHEDULER_ENABLED=false`. Change the web `depends_on` to the Go API, then redeploy.
- [ ] **Step 2: Owner checks.** The owner runs `set-password` (runbook), signs in, and checks:
  - the dashboard;
  - an invoice PDF;
  - a quotation;
  - reports;
  - the review queue;
  - reconnecting the MCP connector in Claude.

  Record what they see.
- [ ] **Step 3: Watch.** Watch Sentry and the Go logs for 24 hours. The next day, confirm the scheduled jobs' slots are recorded in `scheduled_job_runs`.
- [ ] **Step 4 (only on the owner's call): way back.** Follow the runbook's way-back section exactly as rehearsed.

---

### Task 9: Remove Python (spec step 5)

- [ ] **Step 1: Wait a week.** Do this only after a week with no way back, and with the **owner's yes**.
- [ ] **Step 2: Remove the code.** Branch `chore/remove-python`:
  - delete `backend-python/` and the Python service from both compose files;
  - drop `alembic_version` with goose migration `000NN_drop_alembic_version.sql`;
  - make `api adopt-baseline` refuse with a clear sentence once `alembic_version` is gone;
  - remove the Python step from `scripts/check.sh`;
  - update CLAUDE.md, and AGENTS.md if Codex's edit has landed; ask first if it has not.
- [ ] **Step 3: Check and merge.** `bash scripts/check.sh` must pass. Open the PR. **Owner yes** to merge, which deploys.
- [ ] **Step 4: Close out.** Turn the "After the switch" items in PORT-NOTES (inventory §4 row 20) into follow-up tasks for the owner to schedule.
