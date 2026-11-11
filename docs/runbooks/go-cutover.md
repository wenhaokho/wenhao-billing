# Go cut-over runbook

The day production stops running the Python API and starts running the Go API,
in **one Coolify deploy** (owner decision 2026-11-11: replace directly; no
side-by-side, no Python way back). The rollback is the pre-deploy `pg_dump`
backup restored into the database plus a redeploy of the previous commit
(section 7).

What the deploy does: `docker-compose.coolify.yml` now builds the
`wenhao-billing-api` service from `backend/` (the Go image). At start-up the Go
API finds `alembic_version` (`0028_business_npwp`) and no `goose_db_version`,
**adopts** the database (checks the Alembic head and the whole schema against
the baseline; on any drift it refuses, writes nothing and exits), then applies
its own migrations 00002 onwards (all additive). The scheduler stays off
(`GO_SCHEDULER_ENABLED` defaults to `false`) until section 5. See
`backend/PORT-NOTES.md` 170 (auto-adopt) and 166 (back-post).

Every step that changes production is marked **OWNER YES** and waits for the
owner's yes. A yes for one step never covers the next.

Rehearsed locally (2026-11-11) on a throwaway `postgres:18` container with the
built Go image: an Alembic-shaped database dumped as in 1b, the image's start
(adopt, migrations 1→9, scheduler off, healthy), `set-password` on stdin, the
back-post dry run, and section 7's R2–R4 (only the six Go tables dropped,
restore exit 0, the table list equal to the archive's, goose absent); a second
start then adopted again, and a start on a drifted copy (a dropped column)
exited 1 with the column named and wrote nothing. Step 1e was run verbatim
on a synthetic dump (including a table owned by a role the PC does not have):
restore exit 0, counts equal, adopt and `to_version 9`.

## When

- **Not between 02:00 and 05:59 UTC** (09:00–12:59 WIB). Python's daily jobs
  run at 02:00 (recurring), 03:00 (usage lock), 04:00 (hosting) and Mondays
  05:00 (FX) UTC. Deploying after 06:00 UTC means Python has already run
  today's slots.
- **Not Monday 05:00 UTC to Tuesday 04:59 UTC**, so that turning Go's scheduler
  on (section 5) does not run the FX catch-up, which the plan-11 rehearsal
  never exercised. Tuesday 06:00 UTC to Sunday is the window.
- Go's scheduler must be on **the same UTC day** as the deploy (before 02:00
  UTC the next day): its catch-up only covers the last 24 hours, so a day with
  no scheduler is a day of missed recurring invoices, usage locks and hosting
  checks. **If section 4 is not applied by about 23:00 UTC on the cut-over
  day, do section 5 anyway** and finish section 4 afterwards: the recurring
  scan only does the work of its slot's day (`backend/internal/jobs/jobs.go`,
  `RecurringScan`), so a skipped day is never made up, while running the jobs
  before the back-post is safe (hosting enforcement reads `balance_due`, not
  the ledger).
- Nobody uses the site between the backup (section 1) and the deploy
  (section 2): anything recorded after the backup is lost if the rollback is
  needed.

## The one rule

**A password, a token or a connection string never goes on the command line,
into the shell history, or onto the screen.** Secrets are read into a no-echo
prompt (`read -rsp`) and piped on stdin, or consumed inside a pipe. Do not
paste a secret into a command, do not `set -x`, and `unset` a variable that
held one when the step is done. The owner's email is not a secret.

## Conventions

Commands marked *host* run on the Coolify host over SSH, in **bash** (type
`bash` first if the login shell is `sh`). Commands marked *PC* run on the
owner's Windows PC in **Git Bash**, never Windows PowerShell 5.1: its `>`
re-encodes output as UTF-16 text and corrupts a binary dump. Every *PC* block
uses `S`, the backup folder outside OneDrive; set it in each new Git Bash
window: `S=$(cygpath -m "$USERPROFILE/scratch")`.

Coolify names a compose resource's containers `<service>-<resource id>` (this
app: `y6pnb32gqyo05cmqzoyvxe3a`); the host runs other projects too, so the
names are matched from their start. Run this block once per shell, **and again
after every deploy or restart** (container names stay, but the checks re-run):

```bash
# host
EMAIL=<owner admin email>
API=$(docker ps --format '{{.Names}}' | grep -E '^wenhao-billing-api-')
WEB=$(docker ps --format '{{.Names}}' | grep -E '^wenhao-billing-web-')

# The database: host and name come from the API container's DATABASE_URL,
# which is never printed (sed keeps only the host, then only the name).
apienv() { docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$API"; }
DB_HOST=$(apienv | sed -n 's|^DATABASE_URL=.*@\([^:/?]*\).*|\1|p')
DB_NAME=$(apienv | sed -n 's|^DATABASE_URL=.*@[^/]*/\([^?]*\).*|\1|p')
DB=$(docker ps --format '{{.Names}}' | grep -Fx -- "$DB_HOST")
[ -n "$DB" ] || DB=$(docker ps -q | xargs docker inspect \
    -f '{{.Name}} {{range .NetworkSettings.Networks}}{{join .Aliases " "}} {{end}}' \
  | awk -v h="$DB_HOST" '{for (i = 2; i <= NF; i++) if ($i == h) {sub("^/", "", $1); print $1}}' | sort -u)

echo "api=$API web=$WEB db=$DB dbname=$DB_NAME"   # names only, never a value
for v in API WEB DB; do
  [ "$(printf '%s\n' "${!v}" | grep -c .)" = 1 ] || echo "STOP: $v is not exactly one container"
done
[ -n "$DB_NAME" ] || echo "STOP: no database name in DATABASE_URL"
```

Expected: one name each for `api`, `web`, `db`, the first two ending in this
app's resource id, and no `STOP` line. If a `STOP` prints, stop: a wrong
container must never be the target of the commands below. (Before the deploy
`api` is the Python container, after it the Go one; both carry the same
`DATABASE_URL`.)

Helpers, defined in the same shell:

```bash
# host
# psql inside the database container, as its own user over the local socket
# (no password). Extra arguments go to psql: echo 'SELECT 1;' | dbq -tA
dbq() { docker exec -i "$DB" sh -c 'db=$1; shift; psql -U "$POSTGRES_USER" -d "$db" -v ON_ERROR_STOP=1 "$@"' sh "$DB_NAME" "$@"; }

# The scheduler flag the API container is really running with, and its log line.
sched_check() {
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$API" | grep '^SCHEDULER_ENABLED='
  docker logs "$API" 2>&1 | grep -E '"msg":"scheduler (is off|started)' | tail -n 1
}
```

Which API answers, from anywhere: `/redoc` is a Python page and Go has none.

```bash
curl -s -o /dev/null -w 'redoc %{http_code}\n'   https://billing.wenhao.id/redoc            # 200 = Python, 404 = Go
curl -s -o /dev/null -w 'auth/me %{http_code}\n' https://billing.wenhao.id/api/v1/auth/me   # 401 = an API answered; 502/503 = none did
```

---

## 0. Pre-merge check: the Coolify application (read-only)

The cut-over assumes Coolify runs nothing besides the compose file: no job or
command that still calls Python (`alembic`, `python -m scripts…`) or that
starts before the Go API has adopted the database. Re-check on the day, in the
Coolify UI, the billing application (`y6pnb32gqyo05cmqzoyvxe3a`):

| Where in the Coolify UI | Expected |
|---|---|
| *Scheduled Tasks* | none |
| *Configuration* → *General* → Pre-deployment / Post-deployment command | both empty |
| *Configuration* → *General* → Build Pack / custom build and start commands | Docker Compose; no custom build or start command |
| *Configuration* → *General* → Docker Compose location | `/docker-compose.coolify.yml` |
| *Configuration* → *General* → Domains of `wenhao-billing-web` | `https://billing.wenhao.id`, `https://api-billing.wenhao.id` |

Result on 2026-11-11 (controller, read-only through the Coolify API): no
scheduled tasks, no pre/post-deployment commands, no custom build or start
commands, compose location `/docker-compose.coolify.yml`, web domains
`https://billing.wenhao.id` and `https://api-billing.wenhao.id`. All as
expected.

**If anything differs** (a scheduled task or command naming `alembic`,
`python` or `scripts.`, another compose path, a domain on the API service):
stop and report before section 1; such a task would fail, or run Python
against the adopted database, after the deploy.

## 1. Backup — **OWNER YES**

A `pg_dump -Fc` of the billing database, taken **right before** the merge. It
is the rollback: keep it until the Go API has run cleanly for a week.

**Recommended: take the site offline for the backup window.** After the
Conventions block (it needs the web container running), stop the web
container, so no browser, payment webhook or email intake reaches Python
between the backup and the deploy (anything it recorded would be missing from
the backup). The deploy in section 2 recreates and starts it. *host*:

```bash
docker stop "$WEB"
```

If you stop here and do not deploy (a check below fails), bring the site back
with `docker start "$WEB"`.

### 1a. Pre-check (read-only), *host*

```bash
dbq -tA <<'SQL'
SELECT 'alembic ' || string_agg(version_num, ',') FROM alembic_version;
SELECT 'goose ' || coalesce(to_regclass('public.goose_db_version')::text, 'absent');
SELECT 'invoices ' || count(*) FROM invoices;
SELECT 'payments ' || count(*) FROM payments;
SELECT 'journal_entries ' || count(*) FROM journal_entries;
SELECT 'account_6000 ' || count(*) FROM chart_of_accounts WHERE code = '6000';
SQL
```

Expected: `alembic 0028_business_npwp`, `goose absent`, and four counts
(`account_6000` is 0 unless the owner created account 6000 in Python).
**Write the four counts down**: 1e and section 7 compare them. If the head
differs or goose is present, stop: the deploy would refuse to start.

### 1b. Dump streamed to the PC (preferred), *PC*

Nothing is written on the server and no password is typed (`pg_dump` runs as
the database container's own user over its local socket). Type the `db` and
`dbname` names the Conventions block printed in place of `<db>` and
`<dbname>`. The folder is outside OneDrive.

```bash
# PC, Git Bash
S=$(cygpath -m "$USERPROFILE/scratch")   # C:/Users/<user>/scratch
( umask 077; mkdir -p "$S"
  ssh -T <coolify-host> "docker exec <db> sh -c 'pg_dump -U \"\$POSTGRES_USER\" -Fc -d <dbname>'" \
    > "$S/billing-prod-pre-go.pgc" )
echo "ssh exit=$?"
ls -l "$S/billing-prod-pre-go.pgc"
```

Expected: `ssh exit=0` and a file of a few MB. An empty or tiny file means
`pg_dump` failed: read the error, delete the file, fix, and repeat.

### 1c. Dump via Bitvise SFTP (alternative)

On the *host*: dump into a mode-700 directory, note its checksum.

```bash
# host
umask 077; mkdir -p ~/wb-cutover && chmod 700 ~/wb-cutover
docker exec "$DB" sh -c 'pg_dump -U "$POSTGRES_USER" -Fc -d "$1"' sh "$DB_NAME" > ~/wb-cutover/billing-prod-pre-go.pgc
echo "pg_dump exit=$?"
ls -l ~/wb-cutover/billing-prod-pre-go.pgc
sha256sum ~/wb-cutover/billing-prod-pre-go.pgc
```

In Bitvise's SFTP window, download `~/wb-cutover/billing-prod-pre-go.pgc` to
`C:\Users\<user>\scratch\` (binary mode is Bitvise's default for SFTP). On the
*PC*, compare the checksum:

```bash
# PC, Git Bash
sha256sum "$S/billing-prod-pre-go.pgc"
```

The two sums must be identical. Then delete the server copy:

```bash
# host
shred -u ~/wb-cutover/billing-prod-pre-go.pgc 2>/dev/null || rm -f ~/wb-cutover/billing-prod-pre-go.pgc
ls -la ~/wb-cutover
```

### 1d. Verify the archive with a PostgreSQL 18 client, *PC*

Production runs PostgreSQL 18.3, so the archive needs an 18 `pg_restore`:

```bash
# PC, Git Bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$S:/d:ro" postgres:18 \
  pg_restore --list /d/billing-prod-pre-go.pgc > "$S/billing-prod-pre-go.list"
echo "exit=$?"; head -n 12 "$S/billing-prod-pre-go.list"
grep -cE ' TABLE DATA public ' "$S/billing-prod-pre-go.list"
grep -E ' TABLE DATA public (alembic_version|invoices|journal_entries|users) ' "$S/billing-prod-pre-go.list"
```

Expected: `exit=0`; a header starting `; Archive created at …` with
`;     dbname: <dbname>`, `;     Format: CUSTOM` and
`;     Dumped from database version: 18.3`; `23` `TABLE DATA` entries (the 22
tables and `alembic_version`); and the four named ones listed. **If the
listing fails, there is no backup: do not go on.**

### 1e. Restore the backup and adopt it locally, *PC* (before merging)

`pg_restore --list` only reads the archive's table of contents, so a truncated
download can still list. This step restores the whole archive into a
throwaway PostgreSQL 18 on the PC, compares the 1a counts, and starts the Go
image built from this branch against that copy: it proves the backup restores
**and** that today's production schema adopts, before production is touched.

In a Git Bash window, in a checkout of the branch being merged
(`feat/go-replace-python`), with `S` set (Conventions):

```bash
# PC, Git Bash
export MSYS_NO_PATHCONV=1
docker build -q -t wb-go-cutover backend
docker network create wb-cutover-check
docker run -d --name wb-restore --network wb-cutover-check \
  -e POSTGRES_USER=billing -e POSTGRES_PASSWORD=local-check -e POSTGRES_DB=billing postgres:18
until docker exec wb-restore pg_isready -U billing -d billing >/dev/null 2>&1; do sleep 1; done; sleep 3
docker cp "$S/billing-prod-pre-go.pgc" wb-restore:/tmp/backup.pgc
docker exec wb-restore pg_restore -U billing -d billing --no-owner --no-acl --exit-on-error /tmp/backup.pgc
echo "restore exit=$?"
docker exec -i wb-restore psql -U billing -d billing -tA <<'SQL'
SELECT 'alembic ' || string_agg(version_num, ',') FROM alembic_version;
SELECT 'goose ' || coalesce(to_regclass('public.goose_db_version')::text, 'absent');
SELECT 'invoices ' || count(*) FROM invoices;
SELECT 'payments ' || count(*) FROM payments;
SELECT 'journal_entries ' || count(*) FROM journal_entries;
SELECT 'account_6000 ' || count(*) FROM chart_of_accounts WHERE code = '6000';
SQL
```

Expected: `restore exit=0`, then exactly the 1a values (`alembic
0028_business_npwp`, `goose absent`, the four counts). (`--no-owner --no-acl`:
the production roles do not exist on the PC; ownership plays no part in the
schema check.)

Then start the Go image against the copy. The secrets are dummy values for
this local copy only (`ENVIRONMENT=development` accepts them); the scheduler
is off and no mail is sent (no `RESEND_API_KEY`):

```bash
# PC, Git Bash
docker run -d --name wb-go --network wb-cutover-check \
  -e DATABASE_URL=postgresql+psycopg://billing:local-check@wb-restore:5432/billing \
  -e ENVIRONMENT=development -e SCHEDULER_ENABLED=false -e MCP_ENABLED=false \
  -e SESSION_SECRET=local-check-session-secret-0123456789abcdef \
  -e WEBHOOK_HMAC_SECRET=local-check-webhook-secret-0123456789abcdef \
  -e EMAIL_WEBHOOK_TOKEN=local-check-email-token-0123456789abcdef \
  -e MCP_CONFIRM_SECRET=local-check-mcp-confirm-0123456789abcdef \
  -e MCP_TOKEN_SECRET=local-check-mcp-token-0123456789abcdef \
  -e CORS_ORIGINS='["http://localhost"]' -e APP_BASE_URL=http://localhost \
  wb-go-cutover
sleep 10
docker logs wb-go 2>&1 | grep -E '"msg":"(adopt|migrations|listening)|api stopped'
echo "SELECT max(version_id) FROM goose_db_version WHERE is_applied;" | docker exec -i wb-restore psql -U billing -d billing -tA
```

Expected:

```
{"time":"…","level":"INFO","msg":"adopt: the Alembic database is now owned by goose at the baseline","env":"development","alembic_head":"0028_business_npwp"}
{"time":"…","level":"INFO","msg":"migrations: database is up to date","env":"development","from_version":1,"to_version":9}
{"time":"…","level":"INFO","msg":"listening","env":"development","port":8000}
9
```

Then remove everything, including the copy's data volume (it holds production
data):

```bash
# PC, Git Bash
docker rm -f -v wb-go wb-restore
docker network rm wb-cutover-check
docker image rm wb-go-cutover
unset MSYS_NO_PATHCONV
```

**If the restore fails or a count differs:** the backup is incomplete; delete
it, take it again (1b/1c), and repeat 1d and 1e. **If Go logs `api stopped`
with `auto-adopt failed …`:** production's schema has drifted from the
baseline, and the deploy would fail the same way: do not merge; report the
listed lines (clean up as above first). In both cases, `docker start "$WEB"`
on the host if it was stopped.

Rollback: none — a dump writes nothing to production.

## 2. Merge → Coolify deploys — **OWNER YES**

Merge the PR (`feat/go-replace-python`) on GitHub. Coolify builds both images
and recreates the containers; Python's container is replaced by Go's.

**Watch the deploy:** Coolify UI → the billing application → *Deployments* →
the running deployment's log, until it says the deployment finished (or
failed). Then, on the *host*, run the Conventions block again (`api` is now
the Go container: its image is the Go one) and read the Go start-up log:

```bash
# host
docker inspect -f '{{.Config.Image}}' "$API"
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E '^wenhao-billing-'
docker logs "$API" 2>&1 | grep -E '"msg":"(adopt|migrations|scheduler|listening)|api stopped' | head -n 20
```

Expected (the log is JSON, one object per line):

```
{"time":"…","level":"INFO","msg":"adopt: the Alembic database is now owned by goose at the baseline","env":"production","alembic_head":"0028_business_npwp"}
{"time":"…","level":"INFO","msg":"migrations: database is up to date","env":"production","from_version":1,"to_version":9}
{"time":"…","level":"INFO","msg":"scheduler is off (SCHEDULER_ENABLED)","env":"production"}
{"time":"…","level":"INFO","msg":"listening","env":"production","port":8000}
```

(`to_version` is the last migration in `backend/migrations/`, 9 at the time of
writing.) Then:

```bash
curl -s -o /dev/null -w 'redoc %{http_code}\n'   https://billing.wenhao.id/redoc            # 404: Go answers
curl -s -o /dev/null -w 'auth/me %{http_code}\n' https://billing.wenhao.id/api/v1/auth/me   # 401
echo "SELECT version_id, is_applied FROM goose_db_version ORDER BY id;" | dbq
```

Expected: both containers `(healthy)`, `redoc 404`, `auth/me 401`, and goose
rows 0 and 1 (the adopt) then 2 through the last version.

**How to tell it failed:**

- the Coolify deployment shows *Failed*, or the API container is
  `Restarting`/`unhealthy`, or the web container is not running (it waits for
  a healthy API);
- the site answers 502/503 (or Traefik's 404) instead of `auth/me 401`;
- the log has `"level":"ERROR","msg":"api stopped"`. Its `err` says why:
  - `auto-adopt failed, nothing was written: adopt: the schema differs from
    Alembic 0028_business_npwp …` followed by `only in this database:` /
    `missing from this database:` lines — schema drift;
  - `… alembic_version is "…", want "0028_business_npwp"` — wrong head;
  - a sentence naming an environment variable (for example `SESSION_SECRET
    must be at least 32 bytes`) — a missing or invalid value; this one is
    logged as plain text (`level=ERROR msg="api stopped" …`), before the JSON
    logger is set up;
  - `db: migrate: …` — a migration failed (each goose migration runs in its
    own transaction, so the failing one is rolled back).

If the Conventions block finds no `api` container (it exited and is not
restarting), run it with `docker ps -a` in place of `docker ps` on the `API=`
line; `docker logs` and `docker inspect` work on a stopped container.

**If it failed:** copy the `api stopped` line (it holds no secret) for the
report, then check whether Go wrote anything:

```bash
echo "SELECT coalesce(to_regclass('public.goose_db_version')::text, 'absent');" | dbq -tA
```

- `absent`: the adopt refused and **nothing was written**. No restore is
  needed: only redeploy the previous commit (section 7, step R5).
- `goose_db_version`: Go adopted (and possibly migrated) before failing: run
  the whole of section 7.

## 3. Set the owner's password — **OWNER YES**

The Python-era hash is bcrypt, which Go does not accept: the owner sets a new
password with `set-password`, which reads it from stdin (the image has no
shell). The owner types it into a no-echo prompt on the *host*; it is never on
the command line, in the history or on the screen:

```bash
# host
IFS= read -rsp 'New password: ' PW; echo
if [ "${#PW}" -ge 8 ]; then
  printf '%s\n' "$PW" | docker exec -i "$API" /api set-password "$EMAIL"
  echo "exit=$?"
else
  echo 'too short: nothing was set'
fi
unset PW
```

Expected:

```
level=INFO msg="set-password: password set; every session of the user is signed out" email=<email>
exit=0
```

This also revokes the owner's MCP refresh tokens (section 6).

**Other users** still have Python (bcrypt) passwords, which Go does not
accept either. List them, and for each one either run this block again with
`EMAIL` set to that user (and pass the password on to them out of band), or
have them use *Forgot password* on the sign-in page (needs `RESEND_API_KEY`
set, so the reset mail is sent):

```bash
echo "SELECT email, is_active FROM users ORDER BY email;" | dbq
```

**Sign-in check:** in a browser, open https://billing.wenhao.id, sign in with
the new password, and open the dashboard. Expected: signed in, dashboard
loads.

**If it fails:** `no user with email` — check the address (`echo "SELECT email
FROM users;" | dbq`). A sign-in refused after `exit=0` — re-run the block
(re-running with another password is the recovery). Rollback: none needed.

## 4. Ledger back-post — **OWNER YES** (dry run), **OWNER YES** (apply)

Python never posted issue entries for invoices and bills and booked foreign
payments at the payment date's rate; `api backpost-ledger` posts the Python-era
documents and restates their payments (PORT-NOTES 166). Until it is applied,
payments on Python-era invoices are refused or held and their AR/AP is missing
from the ledger reports, so do it right after section 3.

### 4a. Dry run (writes nothing), *host*

```bash
# host
umask 077; mkdir -p ~/wb-cutover && chmod 700 ~/wb-cutover
docker exec "$API" /api backpost-ledger > ~/wb-cutover/backpost-dry.txt 2>&1
echo "exit=$?"
cat ~/wb-cutover/backpost-dry.txt
```

The output holds business data (document numbers, customer names, amounts) but
no secret. Expected: `exit=0` and the last line `dry run: nothing written`,
after the counts (`invoices issued: …`, `bills opened: …`, `payments restated
at issue basis: …`, `payments posted (had no entry): …`, `legacy bill payments:
…`, `customer advances (overpaid invoices): …`) and the per-account totals.

`exit=1` with `backpost-ledger refused: nothing written` lists what blocks it:
missing FX rates (`… no USD→IDR rate on or before …`: enter those rates in the
app, then repeat 4a), bad share accounts, payments whose ledger lines do not
pair up, or `inconsistent` documents. Stop and report these; fix only what the
owner decides. `exit=2` is an error: report the line.

**Owner review gate.** The owner reads, and decides on, every line under:

- `owner decisions (CLEARED payments on invoices never posted)` — a payment on
  a VOID/DRAFT/template/zero invoice; the back-post never touches it; the
  owner decides what to do with each;
- `Customer advances (overpaid invoices) — owner to refund or apply` — an
  invoice paid more than once; the negative receivable stays as the customer's
  credit; the owner refunds or applies it later;
- `warnings` — documents with no issue date, payments dated before the issue,
  receivable gaps carried over from Python, VOID bills with a balance;
- every `legacy bill payment: BILL …` line (a bill Python marked paid without a
  payment date; it is relieved on the bill's own date).

**Do not go on until the owner says yes to this output.**

### 4b. Apply

Take the site offline so nothing changes between the reviewed dry run and the
apply (the scheduler is still off; stopping the web container stops browser,
webhook and MCP traffic). Repeat the dry run and compare it with the reviewed
one, then apply:

```bash
# host
docker stop "$WEB"
docker exec "$API" /api backpost-ledger > ~/wb-cutover/backpost-dry2.txt 2>&1; echo "dry2 exit=$?"
diff ~/wb-cutover/backpost-dry.txt ~/wb-cutover/backpost-dry2.txt && echo "SAME AS REVIEWED"
```

Only if it prints `SAME AS REVIEWED`:

```bash
# host
docker exec "$API" /api backpost-ledger --apply > ~/wb-cutover/backpost-apply.txt 2>&1
echo "exit=$?"; tail -n 25 ~/wb-cutover/backpost-apply.txt
docker exec "$API" /api backpost-ledger > ~/wb-cutover/backpost-after.txt 2>&1; echo "after exit=$?"
grep -E '^(invoices issued|bills opened|payments restated|payments posted|legacy bill payments|customer advances)' ~/wb-cutover/backpost-after.txt
docker start "$WEB"
```

Expected: `exit=0` and `applied: committed`; the second dry run reports 0 for
everything but `customer advances`, which lists the same advances again. Then
`auth/me 401` from outside again once the web container is healthy.

**If the outputs differ** (something changed meanwhile): `docker start "$WEB"`,
go back to 4a with the new output, and review again. **If `--apply` refuses or
errors:** nothing was written (one transaction); `docker start "$WEB"`, report
the output. The site works without the back-post, but Python-era payments stay
blocked until it is applied.

## 5. Turn the scheduler on — **OWNER YES**

After section 4, **the same UTC day** as the deploy, outside 02:00–05:59 UTC.
If section 4 is not applied by about 23:00 UTC that day, do this section
anyway and apply section 4 afterwards (see *When*).
In Coolify (the billing application → *Environment Variables*) set
`GO_SCHEDULER_ENABLED` to `true` (add it if it is not there), save, and click
**Restart**. Then, on the *host*, the Conventions block again and:

```bash
# host
sched_check
```

Expected:

```
SCHEDULER_ENABLED=true
{"time":"…","level":"INFO","msg":"scheduler started","env":"production","jobs":4}
```

If it still shows `SCHEDULER_ENABLED=false`, the restart kept the old
environment: click **Redeploy** instead (the adopt and migrations are no-ops
now) and check again.

On start, Go's scheduler runs each job's latest slot from the past 24 hours
again (today's slots, which Python already ran in the morning). Every job is
idempotent: the plan-11 rehearsal showed no second recurring invoice or digest and no
second hosting change. The next day, confirm the slots ran:

```bash
echo "SELECT job_id, slot, finished_at, error FROM scheduled_job_runs ORDER BY slot DESC LIMIT 12;" | dbq
```

Expected: a row per job for its latest slot (`recurring-scan-and-generate`,
`usage-lock-cutoff-scan`, `hosting-overdue-enforcement`, and
`fx-rates-weekly-sync` after a Monday), `error` empty.

**If something is wrong:** set `GO_SCHEDULER_ENABLED=false` and restart; the
jobs stop. Report the `scheduled job failed` log lines
(`docker logs "$API" 2>&1 | grep 'scheduled job failed'`).

## 6. Checks

Signed in as the owner in the browser:

- `https://billing.wenhao.id/api/v1/auth/me` opened in the browser returns the
  owner's JSON (200);
- **Invoices**: the list loads with the expected count (compare the
  `invoices` number from 1a) and an invoice opens with its PDF;
- **Reports**: the trial balance loads and its debit and credit totals are
  equal; AR aging shows the open invoices;
- **MCP connector**: the next refresh fails (`set-password` revoked the
  refresh tokens), at most an hour after section 3 (the access token's
  lifetime). Reconnect the billing connector in Claude once, then ask it to
  list recent invoices. The discovery document must name the public origin:

```bash
curl -s https://billing.wenhao.id/.well-known/oauth-authorization-server \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["issuer"])'
```

Expected: `https://billing.wenhao.id`.

The same API checks from the *host* without a browser; the session token is
consumed inside the pipe and never shown:

```bash
# host
docker exec "$API" /api issue-session "$EMAIL" | {
  IFS= read -r T
  for p in /api/v1/auth/me /api/v1/invoices /api/v1/accounting/reports/trial-balance; do
    curl -s -o /dev/null -w "$p %{http_code}\n" -H @<(printf 'Cookie: billing_session=%s\n' "$T") "https://billing.wenhao.id$p"
  done
}
```

Expected: `200` for all three. (The cookie reaches `curl` through a file
descriptor written by the shell's `printf` builtin, so the token is never on
any process's command line. This needs bash and curl 7.55 or later on
Linux, as on the host; Windows' curl in Git Bash cannot read the descriptor.)

Watch Sentry and `docker logs -f --tail 200 "$API"` for the first day.

## 7. Rollback: restore the backup, then redeploy the previous commit — **OWNER YES**

> **Warning.** A restore puts the database back to the moment of the backup.
> **Everything recorded after it is lost**: every invoice, payment, bill,
> customer change, password, OAuth client and ledger entry made in Go after
> the deploy (and anything made in Python between the backup and the deploy),
> including the back-post. Write down what was entered since the deploy before
> restoring, so it can be entered again in Python.

If section 2 failed with goose `absent`, nothing was written: skip to R5.

**R1. Stop the Go API** so nothing writes during the restore (the site answers
502/503 until R5). *host*:

```bash
docker update --restart=no "$API"
docker stop "$API"
```

`--restart=no` keeps Docker (and a host reboot) from starting the Go API
again in the middle of the restore. The site is down from here until R5:
work through R2–R5 promptly.

**R2. Put the archive on the host.** From the *PC* (or upload it with Bitvise
SFTP into `~/wb-cutover/`):

```bash
# PC, Git Bash
ssh <coolify-host> 'umask 077; mkdir -p ~/wb-cutover && chmod 700 ~/wb-cutover'
scp "$S/billing-prod-pre-go.pgc" <coolify-host>:wb-cutover/
```

Then copy it into the database container (a seekable file for `pg_restore`;
the database container runs PostgreSQL 18, so its `pg_restore` is the 18
client), list the archive, and find the tables Go added. *host*:

```bash
docker cp ~/wb-cutover/billing-prod-pre-go.pgc "$DB":/tmp/restore.pgc && echo "copied"
docker exec "$DB" pg_restore --list /tmp/restore.pgc > ~/wb-cutover/archive.list; echo "list exit=$?"
awk '$4 == "TABLE" && $5 == "public" {print $6}' ~/wb-cutover/archive.list | LC_ALL=C sort > ~/wb-cutover/archive-tables.txt
echo "SELECT tablename FROM pg_tables WHERE schemaname = 'public';" | dbq -tA | LC_ALL=C sort > ~/wb-cutover/live-tables.txt
echo "archive tables: $(grep -c . ~/wb-cutover/archive-tables.txt)"
for t in alembic_version invoices journal_entries users; do
  grep -qx "$t" ~/wb-cutover/archive-tables.txt || echo "STOP: the archive has no table $t"
done

# The tables Go created (migrations 00002 onwards). Only these are ever dropped.
GO_TABLES=" bill_payments goose_db_version login_attempts mcp_spent_confirm_tokens oauth_spent_codes scheduled_job_runs "
EXTRA=$(LC_ALL=C comm -13 ~/wb-cutover/archive-tables.txt ~/wb-cutover/live-tables.txt)
for t in $EXTRA; do
  case "$GO_TABLES" in *" $t "*) echo "go table: $t" ;; *) echo "STOP: $t is in the database but neither in the archive nor a Go table" ;; esac
done
```

Expected: `copied`, `list exit=0`, `archive tables: 23`, no `STOP`, and a
`go table:` line for each of Go's tables (all six after a normal deploy).
**If any `STOP` prints, stop and report: do not drop anything.** An empty or
failed listing would make every table look like an extra one; the checks
above exist so that never reaches the drop. (`--clean` only drops what is in
the archive, so Go's tables must be dropped first, or a later Go deploy would
find `goose_db_version` and skip the adopt. A table added by a later Go
migration is not in the list: add it to `GO_TABLES` only after checking it is
in `backend/migrations/`.)

**R3. Drop Go's tables, then restore.** *host*, only after R2 printed no
`STOP`:

```bash
for t in $EXTRA; do
  case "$GO_TABLES" in *" $t "*) echo "DROP TABLE IF EXISTS public.\"$t\" CASCADE;" ;; esac
done | dbq
docker exec "$DB" sh -c 'pg_restore -U "$POSTGRES_USER" -d "$1" --clean --if-exists --single-transaction --exit-on-error /tmp/restore.pgc' sh "$DB_NAME"
echo "pg_restore exit=$?"
```

Expected: one `DROP TABLE` per Go table (a `NOTICE: drop cascades to …` is
fine: those constraints belong to tables the restore recreates), then
`pg_restore exit=0` and no error. `--single-transaction` makes the restore all
or nothing: on an error the database is as it was before this `pg_restore`
(Go's tables already dropped, the rest unchanged). Report the error and do not
deploy.

**R4. Verify.** *host*:

```bash
dbq -tA <<'SQL'
SELECT 'alembic ' || string_agg(version_num, ',') FROM alembic_version;
SELECT 'goose ' || coalesce(to_regclass('public.goose_db_version')::text, 'absent');
SELECT 'session_epoch columns ' || count(*) FROM information_schema.columns
 WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'session_epoch';
SELECT 'invoices ' || count(*) FROM invoices;
SELECT 'payments ' || count(*) FROM payments;
SELECT 'journal_entries ' || count(*) FROM journal_entries;
SELECT 'account_6000 ' || count(*) FROM chart_of_accounts WHERE code = '6000';
SELECT 'refresh_user_idx ' || coalesce(to_regclass('public.oauth_refresh_tokens_user_id_idx')::text, 'absent');
SQL
docker exec "$DB" rm -f /tmp/restore.pgc
shred -u ~/wb-cutover/billing-prod-pre-go.pgc 2>/dev/null || rm -f ~/wb-cutover/billing-prod-pre-go.pgc
```

Expected: `alembic 0028_business_npwp`, `goose absent`, `session_epoch columns
0`, the four counts written down in 1a (`account_6000` back to its 1a value:
Go's migration 00008 adds account 6000), and `refresh_user_idx absent` (Go's
migration 00007's index). Keep the PC copy of the archive.

**R5. Redeploy the previous commit** (Python). Either:

- **GitHub (preferred, keeps `main` true to production):** on the merged PR,
  click *Revert*, and merge the revert PR; Coolify deploys it. To land the Go
  change again later, revert the revert (a plain re-merge of the branch does
  nothing: Git considers its commits merged already). Or
- **Coolify:** the billing application → *Configuration* → *General* → the
  Git commit SHA field: set it to the commit before the merge (the merge
  commit's first parent; for a PR merged right after `main` at `ba8a1f6`, that
  is `ba8a1f6f72f576d185381d7509258669c4ea8ee0`; check with
  `git rev-parse <merge-commit>^1`), save, **Deploy**. Clear the field again
  once `main` is reverted, or the next deploy stays pinned.

Then the Conventions block again and:

```bash
docker inspect -f '{{.Config.Image}}' "$API"
docker logs "$API" 2>&1 | tail -n 20
curl -s -o /dev/null -w 'redoc %{http_code}\n'   https://billing.wenhao.id/redoc            # 200: Python answers
curl -s -o /dev/null -w 'auth/me %{http_code}\n' https://billing.wenhao.id/api/v1/auth/me   # 401
```

Expected: the Python image, `alembic upgrade head` a no-op at
`0028_business_npwp`, uvicorn started, `redoc 200`, `auth/me 401`. Python's
scheduler is on again by its own default. The owner signs in with the **old**
(pre-cut-over) password, since the users table is back to the backup. The MCP
connector may need one reconnect.

**If R5 fails** (Python does not start): report the log; the restored database
is unchanged by a failed start, so the deploy can be retried.

## Afterwards

- Keep the PC archive until the Go API has run a clean week, then delete it
  (`rm "$S/billing-prod-pre-go.pgc"*`).
- On the host: `rm -rf ~/wb-cutover` once section 4's outputs are no longer
  needed (they hold business data).
- Removing `backend-python/` and `alembic_version` is a separate change.
