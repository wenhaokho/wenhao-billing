# Go switch-over runbook

The commands for the production steps of plan 11, in order: measure the time
zone, dump the database, confirm the environment names, set the switch
variables, adopt the baseline, deploy Go beside Python, watch memory, run the
read comparison, switch, set the password, reconnect the connector, watch for a
day, and — only if needed — go back. After a clean week, Python is removed.

Every step that deploys or changes production is marked **OWNER YES** and waits
for the owner's yes in chat. A yes for one step never covers the next.

The switch and the way back were rehearsed locally with
`scripts/rehearse-switch.sh` (plan Task 5), which runs these same variable
changes against `docker-compose.coolify.yml`.

## The one rule

**A password or a token never goes on the command line, into the shell history,
or onto the screen.** Every command below either:

- reads a secret into a no-echo prompt (`read -rsp`, or Python's `getpass`) and
  pipes it on stdin (`printf '%s\n' "$PW" | …`), never `cmd "$PW"`;
- copies it from a running container's own environment into a `0600` file
  (`docker inspect … > file`), never onto the screen;
- or issues it inside a pipe so it is consumed immediately
  (`docker exec <go> /api issue-session <email> | …`).

The shell's `history` records the command text, not a secret a command reads
from stdin. Do not paste a secret into a command, do not `set -x` in a shell
that holds one, and `unset PW` when a step is done.

## Conventions

All commands except the dump (section 2) run on the Coolify host (SSH), in a
**bash** shell whose `docker` talks to the Coolify daemon. If the login shell is
`sh`/`dash`, type `bash` first: `read -s` and the helpers below need bash.

Coolify names a compose resource's containers `<service>-<resource id>`. The
host also runs other projects, so match the service names from the start of the
container name. Run this block once per shell, and **again after every
redeploy**:

```bash
API_PY=$(docker ps --format '{{.Names}}' | grep -E '^wenhao-billing-api-' | grep -vE '^wenhao-billing-api-go-')
API_GO=$(docker ps --format '{{.Names}}' | grep -E '^wenhao-billing-api-go-')
WEB=$(docker ps --format '{{.Names}}' | grep -E '^wenhao-billing-web-')

# The database: host and name come from Python's DATABASE_URL, which is never
# printed (sed keeps only the host, then only the database name).
pyenv() { docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$API_PY"; }
DB_HOST=$(pyenv | sed -n 's|^DATABASE_URL=.*@\([^:/?]*\).*|\1|p')
DB_NAME=$(pyenv | sed -n 's|^DATABASE_URL=.*@[^/]*/\([^?]*\).*|\1|p')
DB=$(docker ps --format '{{.Names}}' | grep -Fx -- "$DB_HOST")
[ -n "$DB" ] || DB=$(docker ps -q | xargs docker inspect \
    -f '{{.Name}} {{range .NetworkSettings.Networks}}{{join .Aliases " "}} {{end}}' \
  | awk -v h="$DB_HOST" '{for (i = 2; i <= NF; i++) if ($i == h) {sub("^/", "", $1); print $1}}' | sort -u)

echo "py=$API_PY go=$API_GO web=$WEB db=$DB dbname=$DB_NAME"   # names only, never a value
for v in API_PY WEB DB; do
  [ "$(printf '%s\n' "${!v}" | grep -c .)" = 1 ] || echo "STOP: $v is not exactly one container"
done
```

Expected: one name for each of `py`, `web`, `db` (and `go` once Go is
deployed), all of this resource, and no `STOP` line. If a `STOP` line prints,
stop: a wrong container must never be the target of the commands below.

Two helpers, defined in the same shell:

```bash
# psql inside the database container. The quoted sh -c makes $POSTGRES_USER
# expand in the container (its own user, over the local socket: no password).
# Use it with a here-document or a pipe: echo 'SHOW timezone;' | dbq
dbq() { docker exec -i "$DB" sh -c 'psql -U "$POSTGRES_USER" -d "$1" -v ON_ERROR_STOP=1' sh "$DB_NAME"; }

# The scheduler flag each API container is really running with. Python's own
# scheduler log lines are INFO and suppressed, so the container environment is
# the check: exactly one line must end in =true.
sched_check() {
  for c in "$API_PY" "$API_GO"; do
    [ -n "$c" ] || continue
    printf '%s ' "$c"
    docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$c" | grep '^SCHEDULER_ENABLED='
  done
}
```

Which backend nginx reaches, from anywhere: `/redoc` is a Python page and Go
has none.

```bash
curl -s -o /dev/null -w 'redoc %{http_code}\n' https://billing.wenhao.id/redoc           # 200 = Python, 404 = Go
curl -s -o /dev/null -w 'auth/me %{http_code}\n' https://billing.wenhao.id/api/v1/auth/me  # 401 = an API answered; 502 = nginx reached none
```

`EMAIL` is the owner's admin email (`admin@…`); it is not a secret, so it may
appear in commands.

**The repository on the host (`$REPO`).** Sections 4 and 7 need `backend/` from
the commit Coolify deploys (the Go image is built from it, and the comparison
reads `backend/port/*.yaml`). The host has no checkout; copy it over SSH from
this machine, so no Git credentials go to the server:

```bash
# On this machine, in the repository:
git fetch origin
git archive --format=tar origin/main backend \
  | ssh <coolify-host> 'umask 077; rm -rf ~/wb-switch && mkdir ~/wb-switch && tar -x -C ~/wb-switch'
```

On the host, `REPO=~/wb-switch`. It is deleted in section 14.

Inside the Go container, `/compare` is the comparison command and `/api` is the
API (both binaries; the image is distroless and has no shell).

---

## 1. Time zone (plan Task 6.1) — **OWNER YES** (P1)

Read-only. On the host, after the Conventions block:

```bash
echo 'SHOW timezone;' | dbq
```

Expected output:

```
 TimeZone
----------
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

A `pg_dump -Fc` of production, streamed over SSH straight into a file on this
machine, outside the repository. Nothing is written on the server, and no
password is typed (`pg_dump` runs as the container's own user over its local
socket).

On the host, print the two names (Conventions block first):

```bash
echo "db=$DB dbname=$DB_NAME"
```

On this machine, with those two names typed in place of `<db>` and `<dbname>`:

```bash
( umask 077; mkdir -p "$HOME/scratch"
  ssh -T <coolify-host> "docker exec <db> sh -c 'pg_dump -U \"\$POSTGRES_USER\" -Fc -d <dbname>'" \
    > "$HOME/scratch/billing-prod.pgc" )
ls -l "$HOME/scratch/billing-prod.pgc"
docker compose exec -T db pg_restore --list < "$HOME/scratch/billing-prod.pgc" | head -n 5
```

Expected: no error from `ssh`, a file of a few MB, and `pg_restore --list`
printing the archive header (`; Archive created at …`, `; dbname: …`). An empty
or tiny file means `pg_dump` failed: read the `ssh` error, delete the file, and
stop.

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

## 4. Set the switch variables (plan Task 7.2) — **OWNER YES** (P4)

In the Coolify UI (the billing resource's Environment Variables), or with the
Coolify MCP `manage_env` under this yes, add:

| Variable | Value |
|---|---|
| `BACKEND_UPSTREAM` | `wenhao-billing-api:8000` |
| `PY_SCHEDULER_ENABLED` | `true` |
| `GO_SCHEDULER_ENABLED` | `false` |

These equal the compose defaults, so they change nothing; they make the state
explicit and are the three values every later step flips. **Do not redeploy**
in this step: the current compose does not read them yet.

Expected: `list_env_keys` now also lists the three names.

Rollback: delete the three variables; the defaults are the same values.

## 5. Adopt the baseline (plan Task 8.1) — **OWNER YES** (P5)

**Run this before Go is deployed.** `adopt-baseline` writes `goose_db_version`
(versions 0 and 1) so the next Go serve can run migrations 00002–00007. It
refuses a database whose Alembic head is not `0028_business_npwp`, or whose
schema has drifted.

Build the image from `$REPO` (Coolify builds the same `backend/Dockerfile`
again at the deploy) and run adopt as a one-off container in Python's network
namespace (`--network container:`), so it reaches the database exactly as
Python does. The connection string and the shared secrets are copied from the
**running Python container's own environment** into a `0600` file, so no value
is ever on the command line, in the history, or on the screen. The subshell
`( … )` keeps `umask 077` and the variables from leaking into your shell:

```bash
docker build -t wenhao-billing-api-go:coolify -f "$REPO/backend/Dockerfile" "$REPO/backend"

( umask 077
  dtmp=$(mktemp)
  pyenv | grep -E '^(DATABASE_URL|SESSION_SECRET|WEBHOOK_HMAC_SECRET|EMAIL_WEBHOOK_TOKEN|MCP_CONFIRM_SECRET|MCP_TOKEN_SECRET)=' \
    > "$dtmp"          # written to a file, never shown
  docker run --rm --network container:"$API_PY" --env-file "$dtmp" \
    wenhao-billing-api-go:coolify adopt-baseline
  echo "exit=$?"
  shred -u "$dtmp" 2>/dev/null || rm -f "$dtmp" )
```

Only those six names are copied. `ENVIRONMENT` is **not** copied, so the
one-off container is in production mode and its checks run as they will in
service.

Expected output (stderr, from `slog`), then `exit=0`:

```
level=INFO msg="adopt-baseline: goose now owns the database at the baseline; the next serve applies the later migrations"
```

It writes `goose_db_version` and nothing else.

**If it refuses:** stop and report. Drift or a head past `0028_business_npwp`
would block the deploy the same way; Task 6 exposes it on the dump copy first.

Rollback: none needed. Python ignores `goose_db_version`.

## 6. Deploy side by side (plan Task 8.2) — **OWNER YES** (P6)

Merge the Task 5 compose PR (`feat/go-switch-compose`). Coolify builds and
redeploys. The section 4 values keep Python serving and only Python's scheduler
on. Its first serve applies migrations 00002–00007 (all additive).

Wait for both APIs healthy, run the Conventions block again (Go now exists),
then check:

```bash
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E '^wenhao-billing-'
docker logs --tail 50 "$API_GO" 2>&1 | grep -E 'scheduler|migrat|error'
sched_check
curl -s -o /dev/null -w 'redoc %{http_code}\n' https://billing.wenhao.id/redoc
```

Expected:

- the Python, Go and web containers all `(healthy)`;
- Go's log has `level=INFO msg="scheduler is off (SCHEDULER_ENABLED)"` and no
  migration error;
- `sched_check` prints Python `SCHEDULER_ENABLED=true` and Go
  `SCHEDULER_ENABLED=false` — exactly one `true`;
- `redoc 200`: nginx still reaches Python.

**If both flags say `true`, or none does:** set the section 4 values right and
redeploy at once; two live schedulers run a slot twice.

**Rollback point A:** revert the compose PR and redeploy. Nothing user-facing
changed, and Go's tables (`goose_db_version`, 00002–00007) stay; they are
harmless.

## 7. Memory (plan Task 8.4)

Measure Go's resident memory idle, then ten minutes after the read comparison.

```bash
docker stats --no-stream --format '{{.Name}} {{.MemUsage}} {{.MemPerc}}' "$API_GO"
```

Expected: about 30 MB idle (the goal). Record both figures; the local reference
is from plan Task 6.6.

Rollback: none.

## 8. Read comparison (plan Task 8.3) — **OWNER YES** (P7)

Reads only (the one non-GET is Python's `POST /auth/login`, which writes
nothing). Put the corpus and the allowed-difference file into the Go container,
then run `/compare reads` inside it. The Python password is typed into a
no-echo prompt; the Go token is issued inside the pipe. Neither is echoed, and
both live only in the pipeline's subshell:

```bash
docker cp "$REPO/backend/port/compare-corpus.yaml" "$API_GO":/tmp/compare-corpus.yaml
docker cp "$REPO/backend/port/allowed-diffs.yaml"   "$API_GO":/tmp/allowed-diffs.yaml

{ IFS= read -rsp 'Python password: ' PW; echo >&2; [ -n "$PW" ] || exit 1
  printf '%s\n' "$PW"; unset PW
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

## 9. Switch (plan Task 9.1) — **OWNER YES** (P8)

**When:** outside 02:00–05:59 UTC (09:00–12:59 WIB). Python's daily slots run at
02:00, 03:00, 04:00 and (Mondays) 05:00 UTC. Outside that window no slot fires
while the old and new containers overlap during the redeploy, and Python has
finished today's runs. Go's first start with the scheduler on then runs each
job's latest slot from the past 24 hours a second time; that is safe because
every job is idempotent, which the rehearsal proved (no second child invoice,
no second digest, no Cloudflare call, no hosting or FX change).

In Coolify set, in one redeploy:

| Variable | Value |
|---|---|
| `BACKEND_UPSTREAM` | `wenhao-billing-api-go:8000` |
| `GO_SCHEDULER_ENABLED` | `true` |
| `PY_SCHEDULER_ENABLED` | `false` |

Then redeploy, run the Conventions block again, and check:

```bash
sched_check
docker logs --tail 50 "$API_GO" 2>&1 | grep -E 'scheduler|migrat'
curl -s -o /dev/null -w 'redoc %{http_code}\n'   https://billing.wenhao.id/redoc
curl -s -o /dev/null -w 'auth/me %{http_code}\n' https://billing.wenhao.id/api/v1/auth/me
```

Expected:

- `sched_check`: Python `SCHEDULER_ENABLED=false`, Go `SCHEDULER_ENABLED=true`;
- Go: `level=INFO msg="scheduler started" jobs=4` and no migration error;
- `redoc 404` (nginx now reaches Go) and `auth/me 401` (Go answers; a 502
  means nginx reaches nothing — take the way back).

Everyone signs in again: the two backends use different `billing_session`
cookie formats. The owner cannot sign in until section 10.

**Rollback point B (the way back):** section 13. It was rehearsed locally first
(plan Task 5).

**Contingency — only if the local rehearsal showed a second, non-idempotent
run.** If a slot did real work a second time in the rehearsal, do not switch.
Insert today's `slot` rows for the three daily jobs **before** the switch
(**OWNER YES**, its own step, the fallback from the plan), so Go's catch-up
finds them and skips:

```bash
dbq <<'SQL'
WITH jobs(job_id, hour) AS (
  VALUES ('recurring-scan-and-generate', 2),
         ('usage-lock-cutoff-scan', 3),
         ('hosting-overdue-enforcement', 4)
), today AS (SELECT (now() AT TIME ZONE 'UTC')::date AS d)
INSERT INTO scheduled_job_runs (job_id, slot, started_at, finished_at)
SELECT j.job_id,
       ((t.d + make_time(j.hour, 0, 0)) AT TIME ZONE 'UTC')
         - CASE WHEN ((t.d + make_time(j.hour, 0, 0)) AT TIME ZONE 'UTC') > now()
                THEN interval '1 day' ELSE interval '0' END,
       now(), now()
FROM jobs j CROSS JOIN today t
ON CONFLICT (job_id, slot) DO NOTHING;
SQL
```

(The FX job runs only on a Monday; switch on another day.)

## 10. Set the password (plan Task 9.2) — **OWNER YES** (P9)

The owner's hash is argon2id after the switch; Python cannot read it, and Go
cannot read the old bcrypt hash. The owner sets a new one. The image has no
shell, so `set-password` reads the password from stdin; the owner types it into
a no-echo prompt on the host:

```bash
IFS= read -rsp 'New password: ' PW; echo
if [ "${#PW}" -ge 8 ]; then
  printf '%s\n' "$PW" | docker exec -i "$API_GO" /api set-password "$EMAIL"
else
  echo 'too short: nothing was set'
fi
unset PW
```

Expected:

```
level=INFO msg="set-password: password set; every session of the user is signed out" email=<email>
```

and an exit code of 0. This also revokes the owner's MCP refresh tokens
(PN 157). Then the owner signs in at https://billing.wenhao.id.

Rollback: none. Re-running it with another password is the recovery.

## 11. MCP connector reconnect (plan Task 9.3)

Expected once, at most about an hour after the switch:

- an existing connector whose access token has not expired keeps working until
  it does (its lifetime is `MCP_ACCESS_TOKEN_TTL_SECONDS`, default 3600 s);
- the next refresh then fails with `invalid_grant` (PN 157: `set-password`
  revoked the refresh tokens);
- the owner reconnects the MCP connector in Claude once. Claude's HTTPS
  callback is unaffected by PN 150.

Confirm the discovery document names the public origin:

```bash
curl -s https://billing.wenhao.id/.well-known/oauth-authorization-server \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["issuer"])'
```

Expected: `https://billing.wenhao.id`.

Rollback: none — a reconnect writes only an OAuth client row.

## 12. Watch (plan Task 9.4)

Watch Sentry and the Go logs for 24 hours:

```bash
docker logs -f --tail 200 "$API_GO"
```

The next day, confirm the slots ran:

```bash
echo "SELECT job_id, slot, finished_at, error FROM scheduled_job_runs ORDER BY slot DESC LIMIT 12;" | dbq
```

Expected: a row per job for its latest slot — `recurring-scan-and-generate`,
`usage-lock-cutoff-scan`, `hosting-overdue-enforcement`, and
`fx-rates-weekly-sync` only after a Monday (05:00 UTC). A usage-lock error is
expected only if Task 6 step 5 found a due usage draft (PN 1).

## 13. Way back (plan Task 9.5) — **OWNER YES** (P10), only if needed

**When:** like the switch, outside 02:00–05:59 UTC. Then it is one redeploy.
In Coolify set:

| Variable | Value |
|---|---|
| `BACKEND_UPSTREAM` | `wenhao-billing-api:8000` |
| `GO_SCHEDULER_ENABLED` | `false` |
| `PY_SCHEDULER_ENABLED` | `true` |

**If it cannot wait for the window to close**, use two redeploys so the two
schedulers never overlap:

1. set `BACKEND_UPSTREAM=wenhao-billing-api:8000`, `GO_SCHEDULER_ENABLED=false`
   and `PY_SCHEDULER_ENABLED=false`, redeploy; `sched_check` must show both
   `false`;
2. set `PY_SCHEDULER_ENABLED=true`, redeploy.

Python's scheduler never catches up a slot it missed. If a slot hour passed
between the two redeploys and Go had not yet run it (check
`scheduled_job_runs`, section 12), run that one job by hand in Python, for
example the recurring scan:

```bash
docker exec "$API_PY" python -c 'from app.workers.tasks.recurring import scan_and_generate; print(scan_and_generate())'
```

(The others: `app.workers.tasks.usage_lock.cutoff_scan`,
`app.workers.tasks.hosting_enforcement.daily_hosting_enforcement`,
`app.workers.tasks.fx_sync.sync_fx_rates`.)

After the (last) redeploy, run the Conventions block again, then check:

```bash
sched_check
docker logs --tail 50 "$API_GO" 2>&1 | grep -E 'scheduler'
curl -s -o /dev/null -w 'redoc %{http_code}\n'   https://billing.wenhao.id/redoc
curl -s -o /dev/null -w 'auth/me %{http_code}\n' https://billing.wenhao.id/api/v1/auth/me
```

Expected: Python `SCHEDULER_ENABLED=true` and Go `SCHEDULER_ENABLED=false`;
Go logs `scheduler is off (SCHEDULER_ENABLED)`; `redoc 200` (nginx reaches
Python); `auth/me 401` (not 502).

Then Python's hash form must be restored, because Python cannot read the
argon2id hash Go wrote. `scripts.seed_admin` takes the password in `argv`, so
no shell is used: Python prompts with `getpass` (no echo) and hands the
password to `seed_admin` inside the same process — it never reaches an argv,
the history or the screen, and a short or empty entry changes nothing:

```bash
docker exec -it "$API_PY" python -c 'import getpass, runpy, sys
pw = getpass.getpass("New password: ")
if len(pw) < 8:
    sys.exit("too short: nothing was changed")
sys.argv = ["seed_admin", sys.argv[1], pw]
runpy.run_module("scripts.seed_admin", run_name="__main__")' "$EMAIL"
```

Expected: `updated admin <email>`, and the exit code 0.

Then the owner signs in to Python at https://billing.wenhao.id. Python accepts a
Go-issued MCP refresh token (same secret, same `oauth_refresh_tokens` table);
the connector keeps working, and `seed_admin` revokes nothing.

**nginx keeps serving while Go is down.** With the upstream back on Python,
stop Go's container and confirm the site still serves:

```bash
docker stop "$API_GO"
curl -s -o /dev/null -w 'auth/me %{http_code}\n' https://billing.wenhao.id/api/v1/auth/me   # 401, not 502
curl -s -o /dev/null -w 'spa %{http_code}\n'     https://billing.wenhao.id/                 # 200
docker start "$API_GO"
```

This is Review Focus 4: the web `depends_on` names the Python API only, and
nginx resolves the API host per request, so it neither waits for Go nor fails
when Go is gone.

Rollback of the way back: it is itself the rollback; the switch (section 9) can
be repeated later, followed by section 10 again.

## 14. Remove Python (plan Task 10) — **OWNER YES** (P12)

Only after a full week with no way back. On branch `chore/remove-python`:
delete `backend-python/`, remove the Python service and `api-py-cmp` from the
compose files and `PY_SCHEDULER_ENABLED` from `.env.coolify.example`, point the
web `depends_on` at `wenhao-billing-api-go`, default `BACKEND_UPSTREAM` to Go,
default Go's `SCHEDULER_ENABLED` to true, make nginx's
`/(docs|redoc|openapi.json)` route `/(docs|openapi.json)`, add goose migration
`00008_drop_alembic_version.sql`, make `api adopt-baseline` refuse once
`alembic_version` is gone, drop the "frozen Python tests" step from
`scripts/check.sh`, and update the docs. `bash scripts/check.sh` must pass. The
merge deploys and drops `alembic_version`; **there is no way back after it.**

Also delete the repository copy on the host: `rm -rf ~/wb-switch`.

Rollback: none — deleting the code and `alembic_version` is one-way.
