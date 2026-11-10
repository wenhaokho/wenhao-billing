# Go switch-over runbook

The commands for the production steps of plan 11, in order: measure the time
zone, dump the database, confirm the environment names, adopt the baseline,
deploy Go beside Python, watch memory, run the read comparison, switch, set the
password, reconnect the connector, watch for a day, and — only if needed — go
back. A day after a clean week, Python is removed.

Every step that deploys or changes production is marked **OWNER YES** and waits
for the owner's yes in chat. A yes for one step never covers the next.

## The one rule

**A password or a token never goes on the command line, into the shell history,
or onto the screen.** Every command below either:

- reads a secret with `read -s` into a variable and pipes it (`printf '%s\n'
  "$PW" | …`), never `cmd "$PW"`;
- reads it from a running container's own environment with `docker inspect >>
  file`, never `docker inspect` onto the screen;
- or issues it inside a pipe so it is consumed immediately
  (`docker exec <go> /api issue-session <email> | …`).

The shell's `history` records the command text, not a secret a command reads
from stdin. Do not paste a secret into a command, and do not `set -x` in a shell
that holds one.

## Conventions

All commands run on the Coolify host (SSH), in a shell whose `docker` talks to
the Coolify daemon. Run this once per shell session to name the three
containers; Coolify prefixes the compose service names, so discover them rather
than guessing:

```sh
API_GO=$(docker ps --format '{{.Names}}' | grep -- '-api-go-'   | head -n1)
API_PY=$(docker ps --format '{{.Names}}' | grep -- '-api-'      | grep -v -- '-api-go-' | head -n1)
WEB=$(docker ps    --format '{{.Names}}' | grep -- '-web-'      | head -n1)
DB=$(docker ps     --format '{{.Names}}' | grep -Ei 'postgres|billing-db' | head -n1)
echo "go=$API_GO py=$API_PY web=$WEB db=$DB"   # names only, never a value
```

The shared network (the one both the APIs and Postgres are on):

```sh
NET=$(docker inspect --format \
  '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{"\n"}}{{end}}' "$API_PY" | head -n1)
```

`EMAIL` is the owner's admin email (`admin@…`); it is not a secret, so it may
appear in commands. The paths used below:

- `backend/port/compare-corpus.yaml` and `backend/port/allowed-diffs.yaml` live
  in the repository checkout;
- inside the Go container, `/compare` is the comparison command and `/api` is
  the API (both binaries; the image is distroless and has no shell).

The repository checkout used for `docker cp` and `docker build` is called
`$REPO` below; `cd "$REPO"` before the steps that read files from it.

---

## 1. Time zone (plan Task 6.1) — **OWNER YES** (P1)

Read-only. In Coolify, open the **shared Postgres** resource's terminal (its own
shell connects over the local socket, so no password is typed), then:

```sh
psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c 'SHOW timezone;'
```

Expected output:

```
     TimeZone
-----------------
 UTC
(1 row)
```

**If it is `UTC`:** record it and continue. The comparison later runs without
`--python-tz`.

**If it is anything else:** stop and report. That is a code task before the read
comparison: the Go pool sets `TimeZone=UTC`, existing naive timestamps are read
from the server zone, and the comparison then runs with `--python-tz <zone>`.

Rollback: none — this reads one value and writes nothing.

## 2. Dump (plan Task 6.2) — **OWNER YES** (P2)

Take a `pg_dump -Fc` of production to a file outside the repository. Run it from
the Postgres container's own shell (local socket, no password on any command
line) and stream the binary back to this machine:

```sh
# On the Coolify host, in the Postgres container's shell:
pg_dump -U "$POSTGRES_USER" -Fc -d "$POSTGRES_DB" > /tmp/billing-prod.pgc

# Back on this machine, copy it somewhere outside the repository, e.g.:
#   (the local path is used only for the comparison; delete it after Task 6.7)
scp <coolify-host>:/tmp/billing-prod.pgc "$HOME/scratch/billing-prod.pgc"
rm -f /tmp/billing-prod.pgc        # on the Coolify host
```

Expected output: a file of a few MB; `pg_restore --list` on it prints the table
of contents.

Rollback: none — a dump writes nothing to production. The file is deleted at the
end of Task 6.

## 3. Environment names (plan Task 7.1) — **OWNER YES** (P3)

In the Coolify MCP, call `list_env_keys` on the billing resource. **Names only;
never ask for or print a value.** Expected names present:

```
DATABASE_URL
SERVICE_BASE64_64_SESSION
SERVICE_BASE64_64_WEBHOOK
SERVICE_BASE64_64_EMAILTOKEN
SERVICE_BASE64_64_MCPCONFIRM
SERVICE_BASE64_64_MCPTOKEN
SERVICE_URL_WEB_80
RESEND_API_KEY
CLOUDFLARE_API_TOKEN
SENTRY_DSN
```

With `ENVIRONMENT` unset, Go is in production mode and refuses to start when
`SESSION_SECRET` is under 32 bytes, when `WEBHOOK_HMAC_SECRET` or
`EMAIL_WEBHOOK_TOKEN` is empty or a published default, or when an `MCP_*_SECRET`
is under 16 bytes or a published default. The generated
`SERVICE_BASE64_64_*` values pass all three checks.

**If a name is missing:** stop and report. Do not read or set a value in this
step.

Rollback: none — this reads the key list.

## 4. Adopt the baseline (plan Task 8.1) — **OWNER YES** (P5)

**Run this before Go is deployed.** `adopt-baseline` writes `goose_db_version`
(versions 0 and 1) so the next Go serve can run migrations 00002–00007. It
refuses a database whose Alembic head is not `0028_business_npwp`, or whose
schema has drifted.

Build the image from the checkout (Coolify will build it again at the deploy;
this is the same `backend/Dockerfile`), and run adopt as a one-off container.
The connection string and the shared secrets are read from the **running Python
container's own environment** into a temporary `0600` env file, so no value is
ever on the command line, in the history, or on the screen:

```sh
cd "$REPO"
docker build -t wenhao-billing-api-go:coolify -f backend/Dockerfile backend

umask 077
dtmp=$(mktemp)
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$API_PY" \
  | grep -E '^(DATABASE_URL|SESSION_SECRET|WEBHOOK_HMAC_SECRET|EMAIL_WEBHOOK_TOKEN|MCP_CONFIRM_SECRET|MCP_TOKEN_SECRET)=' \
  > "$dtmp"          # written to a file, never shown

docker run --rm --network "$NET" --env-file "$dtmp" \
  wenhao-billing-api-go:coolify /api adopt-baseline

shred -u "$dtmp" 2>/dev/null || rm -f "$dtmp"
unset dtmp
```

Note: only those six names are copied. `ENVIRONMENT` is **not** copied, so the
one-off container is in production mode and its checks run as they will in
service.

Expected output (stderr, from `slog`):

```
level=INFO msg="adopt-baseline: goose now owns the database at the baseline; the next serve applies the later migrations"
```

and an exit code of 0. It writes `goose_db_version` and nothing else.

**If it refuses:** stop and report. Drift or a head past `0028_business_npwp`
would block the deploy the same way; Task 6 exposes it on the dump copy first.

Rollback: none needed. Python ignores `goose_db_version`.

## 5. Deploy side by side (plan Task 8.2) — **OWNER YES** (P6)

Merge the Task 5 compose PR (`feat/go-switch-compose`). Coolify builds and
redeploys. The defaults keep Python serving and both schedulers right:

- `BACKEND_UPSTREAM=wenhao-billing-api:8000` (the web upstream stays Python);
- `PY_SCHEDULER_ENABLED=true` (Python runs the jobs);
- `GO_SCHEDULER_ENABLED=false` (Go's scheduler is off).

Wait for both APIs healthy, then check:

```sh
docker ps --format '{{.Names}}\t{{.Status}}' | grep -- '-api'
docker logs --tail 50 "$API_GO" 2>&1 | grep -E 'scheduler|error'
```

Expected:

- `wenhao-billing-api` (Python) and `wenhao-billing-api-go` both `(healthy)`;
- Go's log has `level=INFO msg="scheduler is off (SCHEDULER_ENABLED)"` and no
  migration error;
- through nginx, the site still serves Python (the owner's check below).

The site still serves Python:

```sh
docker exec "$API_PY" python - <<'PY'
import urllib.request
# 200 with a session, or 401 without one: either way Python answered, not Go.
print(urllib.request.urlopen("http://127.0.0.1:8000/docs", timeout=3).status)
PY
```

Expected: `200`.

**Rollback point A:** revert the compose PR and redeploy. Nothing user-facing
changed, and Go's tables (`goose_db_version`, 00002–00007) stay; they are
harmless.

## 6. Memory (plan Task 8.4)

Measure Go's resident memory idle, then ten minutes after the read comparison.

```sh
docker stats --no-stream --format '{{.Name}} {{.MemUsage}} {{.MemPerc}}' "$API_GO"
```

Expected: about 30 MB idle (the goal). Record both figures; the local reference
is from plan Task 6.6.

Rollback: none.

## 7. Read comparison (plan Task 8.3) — **OWNER YES** (P7)

Reads only (the one non-GET is Python's `POST /auth/login`, which writes
nothing). Put the corpus and the allowed-difference file into the Go container,
then run `/compare reads` inside it. The Python password is typed into a
no-echo prompt; the Go token is issued inside the pipe. Neither is echoed:

```sh
cd "$REPO"
docker cp backend/port/compare-corpus.yaml "$API_GO":/tmp/compare-corpus.yaml
docker cp backend/port/allowed-diffs.yaml   "$API_GO":/tmp/allowed-diffs.yaml

{ IFS= read -rsp 'Python password: ' PW; printf '%s\n' "$PW"; \
  docker exec "$API_GO" /api issue-session "$EMAIL"; } \
  | docker exec -i "$API_GO" /compare reads \
      --python http://wenhao-billing-api:8000 \
      --go http://127.0.0.1:8000 \
      --python-email "$EMAIL" \
      --python-password-stdin --go-token-stdin \
      --corpus /tmp/compare-corpus.yaml \
      --allowed /tmp/allowed-diffs.yaml \
      --out /tmp/reads-report.json

# The report holds no credential; copy it out to read it.
docker cp "$API_GO":/tmp/reads-report.json ./reads-report.json
```

`--python` is Python by its compose service name; `--go` is this same container
on loopback. Exit codes: 0 clean, 1 differences or skips, 2 error.

Expected output:

```
compare reads: cases=NNN differences=0 skipped=0 report=/tmp/reads-report.json
```

and exit code 0. Read `reads-report.json`; every entry in `differences` must be
allowed by `allowed-diffs.yaml` with its PORT-NOTES number, or be fixed.

**If there is a difference:** a Go defect goes to a fix PR first, which needs
its own merge yes; an expected difference gets an allowed entry with its note.
Do not switch until the run is clean.

Rollback: none — the run is read-only.

## 8. Switch (plan Task 9.1) — **OWNER YES** (P8)

The switch runs **outside 02:00–05:59 UTC** (09:00–12:59 WIB): Go's first start
with the scheduler on runs each job's latest slot from the past 24 hours, and
that is safe only because every job is idempotent. In Coolify set, in one
redeploy:

| Variable | Value |
|---|---|
| `BACKEND_UPSTREAM` | `wenhao-billing-api-go:8000` |
| `GO_SCHEDULER_ENABLED` | `true` |
| `PY_SCHEDULER_ENABLED` | `false` |

Then redeploy and check:

```sh
docker logs --tail 50 "$API_GO" 2>&1 | grep -E 'scheduler|migrat'
docker logs --tail 50 "$API_PY" 2>&1 | grep -iE 'scheduler|BackgroundScheduler'
```

Expected:

- Go: `level=INFO msg="scheduler started" jobs=4` (and no migration error);
- Python: no scheduler start line (its scheduler is off);
- through nginx, the site now answers from Go (the owner checks in Task 9.3).

Everyone signs in again: the two backends use different `billing_session`
cookie formats.

**Rollback point B (the way back):** section 12. It is rehearsed locally first
(plan Task 5).

## 9. Set the password (plan Task 9.2) — **OWNER YES** (P9)

The owner's hash is argon2id after the switch; Python cannot read it, and Go
cannot read the old bcrypt hash. The owner sets a new one. The image has no
shell, so `set-password` reads the password from stdin; the owner types it into
a no-echo prompt on the host:

```sh
IFS= read -rsp 'New password: ' PW; printf '%s\n' "$PW" \
  | docker exec -i "$API_GO" /api set-password "$EMAIL"
```

Expected:

```
level=INFO msg="set-password: password set; every session of the user is signed out" email=<email>
```

and an exit code of 0. This also revokes the owner's MCP refresh tokens
(PN 157).

Rollback: none. Re-running it with another password is the recovery.

## 10. MCP connector reconnect (plan Task 9.3)

Expected once, at most about an hour after the switch:

- an existing connector whose access token has not expired keeps working until
  it does (its lifetime is `MCP_ACCESS_TOKEN_TTL_SECONDS`, default 3600 s);
- the next refresh then fails with `invalid_grant` (PN 157: `set-password`
  revoked the refresh tokens);
- the owner reconnects the MCP connector in Claude once. Claude's HTTPS
  callback is unaffected by PN 150.

Confirm the discovery document names the public origin:

```sh
curl -s https://billing.wenhao.id/.well-known/oauth-authorization-server \
  | python -c 'import json,sys; print(json.load(sys.stdin)["issuer"])'
```

Expected: `https://billing.wenhao.id`.

Rollback: none — a reconnect writes only an OAuth client row.

## 11. Watch (plan Task 9.4)

Watch Sentry and the Go logs for 24 hours:

```sh
docker logs -f --tail 200 "$API_GO"
```

The next day, confirm the slots ran:

```sh
docker exec "$DB" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c \
  "SELECT job_id, slot, finished_at, error FROM scheduled_job_runs \
   ORDER BY slot DESC LIMIT 12;"
```

Expected: a row per job for its latest slot — `recurring-scan-and-generate`,
`usage-lock-cutoff-scan`, `hosting-overdue-enforcement`, and
`fx-rates-weekly-sync` only after a Monday (05:00 UTC). A usage-lock error is
expected only if Task 6 step 5 found a due usage draft (PN 1).

## 12. Way back (plan Task 9.5) — **OWNER YES** (P10), only if needed

Exactly as rehearsed. In Coolify set, in one redeploy:

| Variable | Value |
|---|---|
| `BACKEND_UPSTREAM` | `wenhao-billing-api:8000` |
| `GO_SCHEDULER_ENABLED` | `false` |
| `PY_SCHEDULER_ENABLED` | `true` |

Then Python's hash form must be restored, because Python cannot read the
argon2id hash Go wrote. `scripts.seed_admin` takes the password in `argv`, so
read it with `read -rs` inside the container — it never lands in the argv or the
history:

```sh
docker exec -it "$API_PY" sh -c 'read -rs P; python -m scripts.seed_admin '"$EMAIL"' "$P"'
```

Expected: `created admin <email>` or `updated admin <email>`, and the exit code
0.

Check:

```sh
docker logs --tail 50 "$API_PY" 2>&1 | grep -iE 'scheduler|BackgroundScheduler'   # Python started
docker logs --tail 50 "$API_GO" 2>&1 | grep -E 'scheduler'                          # Go is off
```

Then the owner signs in to Python through nginx. Python accepts a Go-issued MCP
refresh token (same secret, same `oauth_refresh_tokens` table); the connector
keeps working, and `seed_admin` revokes nothing.

**nginx keeps serving while Go is down.** With the upstream back on Python, kill
Go's container and confirm the site still serves:

```sh
docker stop "$API_GO"
curl -s -o /dev/null -w '%{http_code}\n' https://billing.wenhao.id/      # 200 (the SPA)
docker start "$API_GO"
```

This is Review Focus 4: the web `depends_on` names the Python API only, so
nginx does not wait for Go and does not lose its upstream.

Rollback of the way back: it is itself the rollback; there is nothing further.

## 13. Remove Python (plan Task 10) — **OWNER YES** (P12)

Only after a full week with no way back. On branch `chore/remove-python`:
delete `backend-python/`, remove the Python service and `api-py-cmp` from the
compose files and `PY_SCHEDULER_ENABLED` from `.env.coolify.example`, point the
web `depends_on` at `wenhao-billing-api-go`, default `BACKEND_UPSTREAM` to Go,
default Go's `SCHEDULER_ENABLED` to true, add goose migration
`00008_drop_alembic_version.sql`, make `api adopt-baseline` refuse once
`alembic_version` is gone, drop the "frozen Python tests" step from
`scripts/check.sh`, and update the docs. `bash scripts/check.sh` must pass. The
merge deploys and drops `alembic_version`; **there is no way back after it.**

Rollback: none — deleting the code and `alembic_version` is one-way.
