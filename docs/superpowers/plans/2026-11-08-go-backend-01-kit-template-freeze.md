# Go backend, plan 1: kit check, template bump, freeze and rename Python

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prepare for the Go port without writing Go app code: confirm `gokit` v0.2.0 covers what wenhao-billing needs, bump `go-api-template` to v0.2.0, move the frozen Python backend to `backend-python/`, give wenhao-billing its first CI, and get production running Python from the new path.

**Architecture:** Two repositories change. `LOQATO/go-api-template` gets a one-line dependency bump on its own branch and PR. wenhao-billing gets a branch `chore/backend-python` that renames the directory, updates every path that names it, adds a guard that every compose build context and bind mount exists, and adds `.github/workflows/ci.yml` (the guard, the frozen Python tests, the frontend build). Merging that branch to `main` is a production deploy, so it waits for the owner.

**Tech Stack:** git, Docker Compose, GitHub Actions, Go 1.27 tooling through the template's `./dev`, pytest (frozen suite), Vite.

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` ("Order of work" steps 1–3, "Where things live", "Constraints").

## The plans after this one

Written when the previous one is merged, against what was actually built:

2. Go foundation in `backend/`: config, baseline migration and `api adopt-baseline`, platform (principal, errors, JSON types, `Optional[T]`, rounding helpers), auth (argon2id, session cookie, epoch, throttle, `set-password`, `issue-session`), `ROUTES.md`, `TESTS.md`, `PORT-NOTES.md`, Go jobs in CI.
3. Master data: customers, vendors, items, chart of accounts, business profile, users.
4. FX rates and the ledger.
5. Invoices, recurring templates, quotations, projects, bills.
6. Payments, intake, matching engine, reconciliation; reports and stats.
7. Documents and integrations: Maroto PDFs, Resend, Cloudflare, Frankfurter, the scheduler.
8. MCP: OAuth server, `/mcp`, the 42 tools, the login-redirect frontend fix.
9. The comparison, clean locally on a copy of production data.
10. Side by side on Coolify, switch-over, Python deleted.

## Global Constraints

- The Python backend is frozen from Task 3 on: no change to its code, tests, migrations or dependencies, only to the path it lives at.
- Docker Compose **service names stay** (`backend` in `docker-compose.yml`, `wenhao-billing-api` in `docker-compose.coolify.yml`); only paths change. `docker compose exec backend …` keeps working.
- `docker-compose.coolify.yml` keeps every environment variable and default unchanged.
- No GitHub repository is created. `LOQATO/gokit` is not changed. `gokit` comes from the `v0.2.0` tag on GitHub, never from the local `D:\Development\gokit` checkout (it sits at v0.1.0 with someone else's uncommitted changes; do not touch it).
- `D:\Development\go-api-template` also has someone else's uncommitted change (`compose.yaml`); work in a separate `git worktree`, never in that checkout.
- Anything that deploys to production, merges to wenhao-billing's `main` (Coolify deploys from it), or changes Coolify settings waits for the owner's yes in chat.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`; PR bodies end with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

## Review Focus

- A path missed by the rename makes Coolify build an empty context or the dev stack mount empty directories, and the API container starts without code: Task 3's guard checks every `build.context` and bind-mount source in both compose files exists, and the guard runs in CI (Task 4).
- The dev stack after the rename still runs the frozen tests from the new path (`docker compose exec backend pytest` passes, same count as before: 525): Task 3, Step 6.
- The Python test suite in CI needs WeasyPrint's system libraries; without them PDF tests fail in CI but pass locally: Task 4 installs `libpango-1.0-0 libpangoft2-1.0-0 libharfbuzz-subset0 fonts-dejavu-core`, and the job must show 525 passed, not skipped.
- The template bump changes error bodies for its clients: v0.2.0 says output is byte for byte unchanged without the new options; Task 2 runs the template's API tests, which assert error bodies, and `./dev generated` (openapi.json unchanged).
- Production after the deploy still serves the API from the renamed path: Task 5 checks `/api/v1/auth/me` answers Python's 401 body (`{"detail":"not authenticated"}`) and the site loads, before declaring done.

---

### Task 1: Kit check and commit the spec

**Files:**
- Create: `docs/superpowers/plans/2026-11-08-go-backend-01-kit-template-freeze.md` (this file, already written)
- Commit: `docs/superpowers/specs/2026-11-08-go-backend-design.md`

**Interfaces:**
- Produces: branch `chore/backend-python` in wenhao-billing, used by Tasks 3–5.

- [ ] **Step 1: Branch**

```bash
git checkout -b chore/backend-python main
```

- [ ] **Step 2: Confirm the v0.2.0 API the spec relies on exists at the tag**

Run (from any directory; reads GitHub, not the local checkout):
```bash
gh api "repos/LOQATO/gokit/contents/apierr/apierr.go?ref=v0.2.0" -q .content | base64 -d | grep -n "ProblemJSON Format"
gh api "repos/LOQATO/gokit/contents/auth/password.go?ref=v0.2.0" -q .content | base64 -d | grep -n "^func \|Params"
gh api "repos/LOQATO/gokit/contents/sched/sched.go?ref=v0.2.0" -q .content | base64 -d | grep -n "LockKey\|Every"
gh api "repos/LOQATO/gokit/contents/db/db.go?ref=v0.2.0" -q .content | base64 -d | grep -n "^func InTx\|^func Migrate\|^func Open"
```
Expected: each grep prints at least one line, and `auth/password.go` shows hashing parameters that can be passed in (needed for the spec's 19 MiB / 2 / 1). If the parameters cannot be passed, stop and report: that is a kit gap for the admission rule, not something to patch here.

- [ ] **Step 3: Commit the spec and this plan**

```bash
git add docs/superpowers/specs/2026-11-08-go-backend-design.md docs/superpowers/plans/2026-11-08-go-backend-01-kit-template-freeze.md
git commit -m "docs: Go backend design and plan 1"
```

---

### Task 2: Bump go-api-template to gokit v0.2.0

**Files (in `LOQATO/go-api-template`):**
- Modify: `go.mod`, `go.sum`, `vendor/` (regenerated)

**Interfaces:**
- Produces: template `main` on `github.com/LOQATO/gokit v0.2.0`, which plan 2 copies.

- [ ] **Step 1: Separate worktree on a branch**

```bash
cd /d/Development/go-api-template
git fetch origin
git worktree add ../go-api-template-gokit-v020 -b chore/gokit-v0.2.0 origin/main
cd ../go-api-template-gokit-v020
```
Expected: a clean worktree (`git status` prints nothing).

- [ ] **Step 2: Upgrade and vendor**

Run: `./dev vendor github.com/LOQATO/gokit@v0.2.0`
Expected: `go.mod` shows `github.com/LOQATO/gokit v0.2.0`; `vendor/github.com/LOQATO/gokit/apierr/huma.go` contains `DetailsKey`.

- [ ] **Step 3: Run the template's checks**

Run: `./dev lint && ./dev generated && ./dev test`
Expected: lint clean; `generated` prints no diff (openapi.json unchanged, since v0.2.0 output is unchanged without the new options); all tests pass. If an error-body test fails, stop and report the diff: it contradicts the kit's CHANGELOG.

- [ ] **Step 4: Commit, push, open the PR**

```bash
git add go.mod go.sum vendor
git commit -m "deps: gokit v0.2.0"
git push -u origin chore/gokit-v0.2.0
gh pr create --repo LOQATO/go-api-template --base main --title "deps: gokit v0.2.0" --body "Bumps gokit to v0.2.0 (envelope DetailsKey and FieldPath options; output unchanged without them). Matches what pintas-booking and wenhao-billing use."
```

- [ ] **Step 5: CI green, owner merges**

Run: `gh pr checks --repo LOQATO/go-api-template --watch`
Expected: lint, generated and test pass. Ask the owner to merge (the template's `release.yml` publishes an image on `main`). After the merge: `git worktree remove ../go-api-template-gokit-v020`.

---

### Task 3: Freeze and rename the Python backend, with a path guard

**Files (wenhao-billing, branch `chore/backend-python`):**
- Move: `backend/` → `backend-python/` (`git mv`, whole tree)
- Create: `backend-python/FROZEN.md`
- Create: `scripts/check-compose-paths.sh`
- Modify: `docker-compose.yml` (`backend.build.context`, the four bind mounts)
- Modify: `docker-compose.coolify.yml` (`wenhao-billing-api.build.context`)
- Modify: `CLAUDE.md`, `AGENTS.md` (paths and a freeze note)

**Interfaces:**
- Produces: `scripts/check-compose-paths.sh` — exits 0 when every `build.context` and every bind-mount source in `docker-compose.yml` and `docker-compose.coolify.yml` exists in the working tree; otherwise prints each missing path with its compose file and service, and exits 1. Run by CI in Task 4.

- [ ] **Step 1: Write the guard, and see it fail against the unrenamed compose files pointed at the new path**

`scripts/check-compose-paths.sh` (bash, `set -euo pipefail`): for each of the two compose files, run `docker compose -f <file> config --format json` with placeholder values for the variables the Coolify file requires (`DATABASE_URL=x` and the `SERVICE_*` names it uses, set only for this command), then with `jq` list `.services[].build.context` and each `.services[].volumes[]` whose `.type == "bind"` `.source`, and test each path exists. Paths in the JSON are absolute; print them relative to the repo root.

Mutation check (do it, then undo it): change `docker-compose.yml`'s `context: ./backend` to `./backend-python` **before** the move.
Run: `bash scripts/check-compose-paths.sh`
Expected: exit 1, printing `docker-compose.yml backend: backend-python (missing)`. Undo the change.

- [ ] **Step 2: Run the guard on the current tree**

Run: `bash scripts/check-compose-paths.sh`
Expected: exit 0.

- [ ] **Step 3: Move the directory**

```bash
git mv backend backend-python
```
Run: `bash scripts/check-compose-paths.sh`
Expected: exit 1, listing `backend` (build context, both files) and the four `backend/...` bind mounts as missing. This is the guard catching the real rename.

- [ ] **Step 4: Update the paths**

- `docker-compose.yml`: `context: ./backend-python`; mounts `./backend-python/app:/app/app`, `./backend-python/alembic:/app/alembic`, `./backend-python/scripts:/app/scripts`, `./backend-python/tests:/app/tests`. Service name stays `backend`.
- `docker-compose.coolify.yml`: `context: ./backend-python`. Nothing else.
- `CLAUDE.md` and `AGENTS.md`: every `backend/` path becomes `backend-python/` (`backend-python/app/`, `backend-python/Dockerfile.prod`, "inside `backend-python/`", `backend-python/scripts/`), and add under Stack: "**Frozen:** the Python backend in `backend-python/` is being replaced by a Go backend in `backend/` (see `docs/superpowers/specs/2026-11-08-go-backend-design.md`). Do not change `backend-python/` except its path."

Run: `bash scripts/check-compose-paths.sh`
Expected: exit 0.

- [ ] **Step 5: Write `backend-python/FROZEN.md`**

Content: the freeze commit is the parent of this commit (`git rev-parse HEAD` before committing — write the full sha); Alembic head `0028_business_npwp`; the test count at the freeze (from Step 6); the rule "no change to code, tests, migrations or dependencies until it is deleted after the Go switch-over; only its path moved".

- [ ] **Step 6: The dev stack runs the frozen tests from the new path**

```bash
docker compose up -d --build backend
docker compose exec -T -e TEST_DATABASE_URL=postgresql+psycopg://billing:billing@db:5432/billing_test backend pytest -q -p no:cacheprovider
```
Expected: `525 passed` (the count before the rename). Write the count into `FROZEN.md`.

Run: `git grep -n -E "\./backend/|backend/app|backend/Dockerfile|inside \`backend/\`" -- ':!docs/superpowers' ':!backend-python'`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "chore: move the frozen Python backend to backend-python/; compose path guard"
```

---

### Task 4: CI for wenhao-billing

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `scripts/check-compose-paths.sh` (Task 3).
- Produces: jobs `compose-paths`, `python-frozen`, `frontend`. Plan 2 adds the Go jobs to this file.

- [ ] **Step 1: Write the workflow**

`on: push, pull_request`; `permissions: contents: read`. Three jobs on `ubuntu-latest`:
- `compose-paths`: checkout, `bash scripts/check-compose-paths.sh`.
- `python-frozen`: service `postgres:16-alpine` (user/password/db `billing`/`billing`/`billing_test`, health check `pg_isready`, port 5432); `actions/setup-python` 3.12; `apt-get install -y libpango-1.0-0 libpangoft2-1.0-0 libharfbuzz-subset0 fonts-dejavu-core`; in `backend-python/`: `pip install -e ".[dev]"`, then `pytest -q -p no:cacheprovider` with `TEST_DATABASE_URL=postgresql+psycopg://billing:billing@localhost:5432/billing_test`.
- `frontend`: `actions/setup-node` 22 with npm cache on `frontend/package-lock.json`; in `frontend/`: `npm ci`, `npm run build`.

- [ ] **Step 2: Push and watch**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: compose path guard, frozen Python tests, frontend build"
git push -u origin chore/backend-python
gh run watch --exit-status
```
Expected: all three jobs green; the `python-frozen` log shows `525 passed` (not skips: a skip means `TEST_DATABASE_URL` did not reach Postgres).

- [ ] **Step 3: Mutation check of the CI guard**

On a throwaway commit, change `docker-compose.coolify.yml`'s context to `./backend`, push, and confirm `compose-paths` fails with `docker-compose.coolify.yml wenhao-billing-api: backend (missing)`. Then drop the commit (`git reset --hard HEAD~1 && git push --force-with-lease`). Record the run URL in the PR description.

---

### Task 5: PR, owner-approved merge, production check

**Files:** none.

- [ ] **Step 1: Open the PR**

```bash
gh pr create --base main --title "chore: move the frozen Python backend to backend-python/" --body-file -
```
Body: what moved and why (spec link), the guard and its mutation run, CI links, "Merging deploys production: Coolify builds `wenhao-billing-api` from `./backend-python`. Service names and environment are unchanged.", ending with the attribution line.

- [ ] **Step 2: Owner's yes, then merge**

Ask the owner in chat to approve the merge (it deploys). On yes: `gh pr merge --merge`.

- [ ] **Step 3: Production check after Coolify redeploys**

Run:
```bash
curl -s -o /dev/null -w "%{http_code}\n" https://billing.wenhao.id/
curl -s https://billing.wenhao.id/api/v1/auth/me
```
Expected: `200`, then `{"detail":"not authenticated"}`. Ask the owner to sign in once and open the dashboard. If either fails, the way back is reverting the merge commit on `main` (owner's yes).

- [ ] **Step 4: Sync local main**

```bash
git checkout main && git pull --ff-only && git branch -d chore/backend-python
```
