#!/usr/bin/env bash
# Local rehearsal of the production switch-over (plan 11, Task 5 step 2).
#
# It runs the Coolify compose on this machine, beside a local Postgres, against
# a restored dev database, and walks the whole switch and the way back:
#
#   1. Python serves (PY_SCHEDULER_ENABLED=true);
#   2. adopt the baseline, then add Go with GO_SCHEDULER_ENABLED=false; Python
#      still serves and Go's container is healthy;
#   3. the scheduler overlap check: run Python's four jobs for today's slots
#      (an open-ended recurring template due today, an overdue hosting item
#      whose suspension goes to the Cloudflare stub), and count child invoices,
#      digest mails, Cloudflare calls, hosting state and FX rows;
#   4. switch the upstream to Go with GO_SCHEDULER_ENABLED=true and
#      PY_SCHEDULER_ENABLED=false, redeploy, and prove Go's catch-up run adds
#      no child invoice, sends no digest, makes no Cloudflare call, changes no
#      hosting state, leaves the FX rows alone, and records today's slot per
#      daily job;
#   5. set-password and sign in through nginx;
#   6. MCP carry-over: a Python-issued refresh token refreshes against Go after
#      the switch, fails with invalid_grant after set-password (PN 157), and a
#      fresh authorize through Go's login works;
#   7. the way back: upstream to Python, flags swapped, redeploy, seed_admin,
#      sign in to Python, and Python accepts a Go-issued refresh token;
#   8. stop Go while the upstream is Python and recreate nginx: the site still
#      serves (Review Focus 4).
#
# Every state asserts, from the containers' own environment, that exactly one
# scheduler is on (Review Focus 5), and which backend nginx reaches: /redoc is
# 200 from Python and 404 from Go. Signed-in checks read HTTP 200 from
# /api/v1/auth/me through nginx; curl treats http://localhost as a secure
# context, so the Secure session cookie is sent.
#
# It never contacts production, Coolify, mail, Cloudflare or Frankfurter: mail
# is off (no RESEND_API_KEY), and Cloudflare and FX go to provider-stub.
#
#   bash scripts/rehearse-switch.sh
#
# Knobs (all optional):
#   REHEARSE_SOURCE    migrate (default: Python's alembic builds an empty
#                      database, then its seed scripts fill it) or dev (restore
#                      a pg_dump of the dev `billing`; see the adopt note below)
#   REHEARSE_DUMP      a `pg_dump -Fc` to restore instead
#   REHEARSE_HTTP_PORT the published nginx port (default 8080)
#   REHEARSE_EMAIL     the admin email to sign in as (default admin@example.com)
#   REHEARSE_KEEP=1    leave the containers up at the end
#
# The password the rehearsal sets on both backends is generated here; it only
# ever travels on stdin or in a request body piped to curl, never on a command
# line, in a shell history, or on the screen.
set -euo pipefail
cd "$(dirname "$0")/.."
# Git Bash would rewrite container paths such as /api into Windows paths.
[[ -n "${MSYSTEM:-}" ]] && export MSYS_NO_PATHCONV=1

export COMPOSE_PROJECT_NAME=wenhao-billing-rehearsal
HTTP_PORT=${REHEARSE_HTTP_PORT:-8080}
BASE="http://localhost:${HTTP_PORT}"
EMAIL=${REHEARSE_EMAIL:-admin@example.com}
umask 077
WORK=$(mktemp -d)
# docker needs a Windows path for the env file when Git Bash does not convert.
[[ -n "${MSYSTEM:-}" ]] && WORK=$(cygpath -m "$WORK")
ENVFILE="$WORK/rehearse.env"
JAR="$WORK/jar"
DISCARD="$WORK/discard"   # curl's -o target (a native curl cannot open /dev/null)

step() { printf '\n== %s\n' "$*"; }
ok()   { printf '   ok: %s\n' "$*"; }
fail() { printf '   FAIL: %s\n' "$*" >&2; exit 1; }
expect_eq() { # expected actual label
  [[ "$1" == "$2" ]] || fail "$3: expected $1, got $2"
  ok "$3 ($2)"
}

dc() {
  docker compose --env-file "$ENVFILE" \
    -f docker-compose.coolify.yml -f scripts/rehearse-switch.compose.yml "$@"
}
devd() { COMPOSE_PROJECT_NAME=wenhao-billing docker compose -f docker-compose.yml "$@"; }

cleanup() {
  local code=$?
  if [[ "${REHEARSE_KEEP:-0}" == 1 ]]; then
    printf '\nleft up: project %s, nginx on %s\n' "$COMPOSE_PROJECT_NAME" "$BASE"
  else
    [[ -f "$ENVFILE" ]] && dc down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
  exit "$code"
}
trap cleanup EXIT

psqlq() { dc exec -T shared-postgres psql -U billing -d billing -v ON_ERROR_STOP=1 -At -c "$1"; }
# openssl on Windows ends its line with CRLF: strip both, or the CR becomes
# part of the password.
secret() { openssl rand -base64 48 | tr -d '\r\n'; }

# set_flag rewrites one line of the env file (no sed -i, so it works on GNU and
# BSD). The file is 0600 and the value never touches a command line.
set_flag() { # name value
  local name=$1 value=$2 tmp
  tmp=$(mktemp)
  if grep -q "^${name}=" "$ENVFILE"; then
    sed "s|^${name}=.*|${name}=${value}|" "$ENVFILE" > "$tmp"
  else
    cat "$ENVFILE" > "$tmp"
    printf '%s=%s\n' "$name" "$value" >> "$tmp"
  fi
  mv "$tmp" "$ENVFILE"
}

# deploy writes the three switch variables and reconciles the stack, as one
# Coolify redeploy does.
deploy() { # upstream py_sched go_sched
  set_flag BACKEND_UPSTREAM "$1"
  set_flag PY_SCHEDULER_ENABLED "$2"
  set_flag GO_SCHEDULER_ENABLED "$3"
  dc up -d wenhao-billing-api wenhao-billing-api-go wenhao-billing-web >/dev/null
}

wait_healthy() { # service
  local svc=$1 i health
  for i in $(seq 1 90); do
    health=$(dc ps --format '{{.Health}}' "$svc" 2>/dev/null || true)
    [[ "$health" == "healthy" ]] && { ok "$svc healthy"; return 0; }
    sleep 2
  done
  dc logs --tail 80 "$svc" >&2 || true
  fail "$svc never became healthy"
}

# sched_env prints SCHEDULER_ENABLED from a service container's own environment
# ("absent" when the service has no container).
sched_env() { # service
  local id
  id=$(dc ps -a -q "$1" 2>/dev/null || true)
  [[ -n "$id" ]] || { echo absent; return; }
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$id" \
    | sed -n 's/^SCHEDULER_ENABLED=//p'
}

# assert_schedulers: the running containers carry exactly the expected flags,
# and exactly one of them is true (Review Focus 5).
assert_schedulers() { # expected_py expected_go
  local py go
  py=$(sched_env wenhao-billing-api)
  go=$(sched_env wenhao-billing-api-go)
  expect_eq "$1" "$py" "python container SCHEDULER_ENABLED"
  expect_eq "$2" "$go" "go container SCHEDULER_ENABLED"
  [[ $( { echo "$py"; echo "$go"; } | grep -c '^true$') == 1 ]] \
    || fail "not exactly one scheduler on (python=$py go=$go)"
  ok "exactly one scheduler is on"
}

# --- HTTP helpers -----------------------------------------------------------
code_of() { curl -sS -o "$DISCARD" -w '%{http_code}' "$@"; }

# assert_backend: /redoc through nginx is 200 from Python and 404 from Go.
assert_backend() { # python|go
  local want=200
  [[ "$1" == go ]] && want=404
  expect_eq "$want" "$(code_of "$BASE/redoc")" "nginx reaches $1 (/redoc)"
}

# get_json GETs a path with the session cookie and prints the body.
get_json() { curl -fsS -b "$JAR" -c "$JAR" "$BASE$1"; }

# first_id GETs a list path and prints field of its first element.
first_id() { # path field
  get_json "$1" | python -c 'import json,sys; d=json.load(sys.stdin); d=d if isinstance(d,list) else d.get("items") or d.get("data") or []; print(d[0][sys.argv[1]] if d else "")' "$2"
}

# login signs in through nginx and stores the cookie jar. The body is piped
# (printf is a shell builtin), so the password is never on a command line.
login() { # password
  rm -f "$JAR"
  printf '{"email":"%s","password":"%s","remember":false}' "$EMAIL" "$1" \
    | curl -fsS -c "$JAR" -H 'Content-Type: application/json' --data @- \
        "$BASE/api/v1/auth/login" >/dev/null
}

# assert_me signs in and reads /api/v1/auth/me through nginx; it must be 200.
assert_me() { # password label
  login "$1"
  expect_eq 200 "$(code_of -b "$JAR" "$BASE/api/v1/auth/me")" "$2: /api/v1/auth/me through nginx"
}

# seed_python sets the Python copy's admin password with scripts.seed_admin,
# which takes it in argv. No shell is involved: python reads the password from
# stdin and hands it to seed_admin in-process, so it is never in an argv, and a
# short or empty read is refused instead of stored.
SEED_ADMIN_STDIN='import runpy, sys
pw = sys.stdin.readline().rstrip("\n")
if len(pw) < 8:
    sys.exit("refusing: the password read from stdin is shorter than 8 characters")
sys.argv = ["seed_admin", sys.argv[1], pw]
runpy.run_module("scripts.seed_admin", run_name="__main__")'
seed_python() { # password
  printf '%s\n' "$1" | dc exec -T wenhao-billing-api python -c "$SEED_ADMIN_STDIN" "$EMAIL"
}

# set_go sets the Go copy's password; the distroless binary reads it from stdin.
set_go() { # password
  printf '%s\n' "$1" | dc exec -T wenhao-billing-api-go /api set-password "$EMAIL" >/dev/null
}

# --- MCP / OAuth helpers ----------------------------------------------------
# All request bodies are piped (--data @-); no token is ever in argv.
mcp_register() { # -> client_id
  printf '%s' '{"client_name":"rehearsal","redirect_uris":["https://localhost/callback"]}' \
    | curl -fsS -X POST "$BASE/oauth/register" -H 'Content-Type: application/json' --data @- \
    | python -c 'import json,sys; print(json.load(sys.stdin)["client_id"])'
}

# mcp_code runs the full authorize + consent form with the current session and
# prints the authorization code.
mcp_code() { # client_id verifier
  local cid=$1 verifier=$2 challenge state page csrf body loc
  challenge=$(printf '%s' "$verifier" | openssl dgst -sha256 -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')
  state="rehearse-$$-$RANDOM"
  page=$(curl -fsS -b "$JAR" -c "$JAR" \
    "$BASE/oauth/authorize?response_type=code&client_id=$cid&redirect_uri=https%3A%2F%2Flocalhost%2Fcallback&code_challenge=$challenge&code_challenge_method=S256&state=$state")
  csrf=$(printf '%s' "$page" | grep -o 'name="csrf_token" value="[^"]*"' | sed 's/.*value="//;s/"$//')
  [[ -n "$csrf" ]] || fail "consent page carried no csrf_token"
  body=$(printf 'response_type=code&client_id=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=%s&csrf_token=%s&decision=approve' \
    "$cid" "https://localhost/callback" "$challenge" "$state" "$csrf")
  loc=$(printf '%s' "$body" | curl -sS -o "$DISCARD" -D - -b "$JAR" -c "$JAR" -X POST \
    "$BASE/oauth/authorize/consent" --data @- | tr -d '\r' | sed -n 's/^[Ll]ocation: //p')
  printf '%s' "$loc" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p'
}

# mcp_token exchanges a code for a token pair and prints the refresh_token.
mcp_token() { # code verifier client_id
  local body
  body=$(printf 'grant_type=authorization_code&code=%s&redirect_uri=%s&client_id=%s&code_verifier=%s' \
    "$1" "https://localhost/callback" "$3" "$2")
  printf '%s' "$body" | curl -fsS -X POST "$BASE/oauth/token" --data @- \
    | python -c 'import json,sys; print(json.load(sys.stdin)["refresh_token"])'
}

# mcp_refresh refreshes and prints the HTTP code; the body is in $WORK/refresh.json.
mcp_refresh() { # refresh_token client_id
  local body
  body=$(printf 'grant_type=refresh_token&refresh_token=%s&client_id=%s' "$1" "$2")
  printf '%s' "$body" | curl -sS -o "$WORK/refresh.json" -w '%{http_code}' -X POST "$BASE/oauth/token" --data @-
}

# --- counters ---------------------------------------------------------------
count_child_invoices() { psqlq "SELECT count(*) FROM invoices WHERE invoice_type='RECURRING' AND is_template IS NOT TRUE"; }
count_fx_rows() { psqlq "SELECT count(*) FROM fx_rates"; }
# One digest of every hosting item and Cloudflare target column a job writes.
hosting_state() {
  psqlq "SELECT md5(coalesce(string_agg(row_text, '|' ORDER BY row_text), '')) FROM (
           SELECT i.item_id::text || ':' || coalesce(i.hosting_status, '') || ':' ||
                  coalesce(t.provider_status, '') || ':' || coalesce(t.last_error, '') || ':' ||
                  coalesce(t.last_action_at::text, '') || ':' || coalesce(t.updated_at::text, '') AS row_text
           FROM items i LEFT JOIN cloudflare_targets t ON t.item_id = i.item_id
           WHERE i.is_hosting) s"
}
count_suspended() { psqlq "SELECT count(*) FROM items WHERE is_hosting AND hosting_status='SUSPENDED'"; }
count_cloudflare_calls() { dc logs provider-stub 2>/dev/null | grep -c 'stub CLOUDFLARE PATCH' || true; }
count_go_digests() { dc logs wenhao-billing-api-go 2>/dev/null | grep -c '\[email disabled\] would send.*draft invoice' || true; }

# Python's four scheduled jobs, run once in the API container exactly as its
# scheduler would at today's slots. Logging is switched on for this process
# only (the API's own INFO logs are suppressed), so the mail-disabled lines of
# the draft digest can be counted.
PY_JOBS='import logging, sys
logging.basicConfig(level=logging.INFO, stream=sys.stdout, format="LOG %(message)s")
from app.workers.tasks.recurring import scan_and_generate
from app.workers.tasks.usage_lock import cutoff_scan
from app.workers.tasks.hosting_enforcement import daily_hosting_enforcement
from app.workers.tasks.fx_sync import sync_fx_rates
print("RESULT recurring", scan_and_generate())
print("RESULT usage-lock", cutoff_scan())
print("RESULT hosting", daily_hosting_enforcement())
print("RESULT fx", sync_fx_rates())'

# ===========================================================================
step "setup: env file and the local Postgres"
{
  echo "DATABASE_URL=postgresql+psycopg://billing:billing@shared-postgres:5432/billing"
  echo "SERVICE_FQDN_WEB_80=localhost:${HTTP_PORT}"
  echo "SERVICE_URL_WEB_80=${BASE}"
  echo "SERVICE_BASE64_64_SESSION=$(secret)"
  echo "SERVICE_BASE64_64_WEBHOOK=$(secret)"
  echo "SERVICE_BASE64_64_EMAILTOKEN=$(secret)"
  echo "SERVICE_BASE64_64_MCPCONFIRM=$(secret)"
  echo "SERVICE_BASE64_64_MCPTOKEN=$(secret)"
  echo "RESEND_API_KEY="
  # A placeholder so both APIs call Cloudflare at all; the overlay points
  # CLOUDFLARE_API_BASE_URL at provider-stub, checked below before any job.
  echo "CLOUDFLARE_API_TOKEN=rehearsal-not-a-real-token"
  echo "SENTRY_DSN="
  echo "BACKEND_UPSTREAM=wenhao-billing-api:8000"
  echo "PY_SCHEDULER_ENABLED=true"
  echo "GO_SCHEDULER_ENABLED=false"
} > "$ENVFILE"
PW=$(secret)          # the local admin password, generated here
[[ -n "$PW" ]] || fail "no password generated"
ok "env file written to a 0600 temp file; secrets generated"

# A leftover from an interrupted run would make the counts meaningless.
dc down -v --remove-orphans >/dev/null 2>&1 || true
dc up -d --wait shared-postgres provider-stub >/dev/null
# The database: by default Python's own `alembic upgrade head` builds it on its
# first start (step 1), the way a database Python created has its schema, and
# Python's seed scripts fill it. REHEARSE_SOURCE=dev restores a pg_dump of the
# dev `billing` instead, and REHEARSE_DUMP restores a given dump. Note: a
# pg_dump/pg_restore round trip rewrites the text of the ck_items_type CHECK,
# and `api adopt-baseline` (step 2) then refuses the copy as drifted — a known
# Go issue (task 5 report), not a rehearsal fault.
if [[ -n "${REHEARSE_DUMP:-}" ]]; then
  dc exec -T shared-postgres pg_restore -U billing -d billing --no-owner --no-privileges < "$REHEARSE_DUMP"
  ok "restored $REHEARSE_DUMP into shared-postgres"
elif [[ "${REHEARSE_SOURCE:-migrate}" == dev ]]; then
  devd up -d --wait db >/dev/null
  devd exec -T db pg_dump -U billing -Fc --no-owner --no-privileges billing \
    | dc exec -T shared-postgres pg_restore -U billing -d billing --no-owner --no-privileges
  ok "restored the dev database into shared-postgres (read-only pg_dump of the dev db)"
else
  ok "empty shared-postgres: Python's alembic upgrade head builds the schema at its first start"
fi

# ===========================================================================
step "step 1: Python serves (PY_SCHEDULER_ENABLED=true)"
dc build wenhao-billing-api wenhao-billing-api-go wenhao-billing-web >/dev/null
ok "built the three images from this working tree"
dc up -d wenhao-billing-api wenhao-billing-web >/dev/null
wait_healthy wenhao-billing-api
wait_healthy wenhao-billing-web
assert_schedulers true absent
seed_python "$PW"
assert_me "$PW" "python serves"
assert_backend python

# Demo data for the overlap check (the dev database may be empty): customers,
# items and hosting items, all through Python's own seed scripts. Each skips
# when its data exists.
for s in seed_dummy seed_master_data seed_hosting; do
  dc exec -T wenhao-billing-api python -m "scripts.$s" >/dev/null
done
ok "seeded demo data (seed_dummy, seed_master_data, seed_hosting)"

# ===========================================================================
step "step 2: adopt, then add Go with the scheduler off"
dc run --rm --no-deps wenhao-billing-api-go adopt-baseline
ok "adopt-baseline returned 0"
dc up -d wenhao-billing-api-go >/dev/null
wait_healthy wenhao-billing-api-go
dc logs wenhao-billing-api-go 2>/dev/null | grep -q 'scheduler is off' \
  || fail "Go did not log that its scheduler is off"
ok "Go logged 'scheduler is off'"
assert_schedulers true false
assert_me "$PW" "python still serves with Go beside it"
assert_backend python

# ===========================================================================
step "step 3: scheduler overlap check (Python runs today's slots)"
# Both APIs must send Cloudflare and FX to the stub before any job runs.
for svc in wenhao-billing-api wenhao-billing-api-go; do
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$(dc ps -q "$svc")" \
    | grep -qx 'CLOUDFLARE_API_BASE_URL=http://provider-stub:8080/client/v4' \
    || fail "$svc does not send Cloudflare calls to provider-stub"
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$(dc ps -q "$svc")" \
    | grep -qx 'FX_PROVIDER_BASE_URL=http://provider-stub:8080' \
    || fail "$svc does not send FX calls to provider-stub"
done
ok "both APIs send Cloudflare and FX calls to provider-stub"

# A due, open-ended recurring template: a MONTHLY cycle starting today, so a
# second scan of today's slot has a cycle it could wrongly generate again.
CUSTOMER_ID=$(first_id "/api/v1/customers?active=true" customer_id)
ITEM_ID=$(first_id "/api/v1/items" item_id)
[[ -n "$CUSTOMER_ID" && -n "$ITEM_ID" ]] \
  || fail "the restored database needs an active customer and an item to build a due template"
TODAY=$(date -u +%F)
printf '{"customer_id":"%s","currency":"IDR","payment_terms":"Net 30","line_items":[{"item_id":"%s","description":"Rehearsal recurring","quantity":"1.0000","unit_price":"1000.0000","position":0}],"schedule":{"frequency":"MONTHLY","interval":1,"start_date":"%s","end_mode":"NEVER"}}' \
  "$CUSTOMER_ID" "$ITEM_ID" "$TODAY" \
  | curl -fsS -b "$JAR" -H 'Content-Type: application/json' --data @- \
      "$BASE/api/v1/invoices/recurring-template" >/dev/null
ok "created an open-ended MONTHLY recurring template due today ($TODAY)"

# An overdue hosting item that is still live: Python's enforcement must
# suspend it through the Cloudflare stub, and Go's catch-up must leave it.
OVERDUE=$(psqlq "WITH u AS (UPDATE items i SET hosting_status = 'ACTIVE'
  FROM cloudflare_targets t
  WHERE t.item_id = i.item_id AND i.is_hosting AND i.hosting_suspension_enabled
    AND EXISTS (SELECT 1 FROM invoice_line_items li JOIN invoices v ON v.invoice_id = li.invoice_id
                WHERE li.item_id = i.item_id AND NOT v.is_template
                  AND v.status IN ('OPEN','SENT','PARTIAL') AND v.balance_due > 0
                  AND v.due_date + coalesce(i.hosting_grace_days, 0) < current_date)
  RETURNING 1) SELECT count(*) FROM u")
psqlq "UPDATE cloudflare_targets t SET provider_status = 'ACTIVE' FROM items i
  WHERE t.item_id = i.item_id AND i.is_hosting AND i.hosting_status = 'ACTIVE'" >/dev/null
[[ "$OVERDUE" -ge 1 ]] || fail "no overdue hosting item with a Cloudflare target: the hosting check would be vacuous"
ok "set $OVERDUE overdue hosting item(s) live again (ACTIVE item and target)"

CF_BEFORE_PY=$(count_cloudflare_calls)
PY_OUT=$(dc exec -T wenhao-billing-api python -c "$PY_JOBS")
printf '%s\n' "$PY_OUT" | grep '^RESULT' | sed 's/^/   python /'
DIGESTS_PY=$(printf '%s\n' "$PY_OUT" | grep -c '\[email disabled\] would send.*draft invoice' || true)
INVOICES_PY=$(count_child_invoices)
FX_PY=$(count_fx_rows)
CF_PY=$(count_cloudflare_calls)
HOSTING_PY=$(hosting_state)
SUSPENDED_PY=$(count_suspended)
[[ "$DIGESTS_PY" -ge 1 ]] || fail "Python's scan sent no draft digest: the overlap check would be vacuous"
[[ $((CF_PY - CF_BEFORE_PY)) -ge 1 ]] || fail "Python's enforcement made no Cloudflare call: the hosting check would be vacuous"
ok "after Python's run: child invoices=$INVOICES_PY digests=$DIGESTS_PY cloudflare calls=$((CF_PY - CF_BEFORE_PY)) suspended items=$SUSPENDED_PY fx rows=$FX_PY"

# MCP carry-over, part 1: a client and a refresh token minted by Python.
MCP_CID=$(mcp_register)
MCP_VERIFIER=$(openssl rand -base64 48 | tr -d '\r\n=+/' | cut -c1-64)
PY_REFRESH=$(mcp_token "$(mcp_code "$MCP_CID" "$MCP_VERIFIER")" "$MCP_VERIFIER" "$MCP_CID")
[[ -n "$PY_REFRESH" ]] || fail "Python issued no refresh token"
ok "registered an MCP client and got a Python refresh token"

# ===========================================================================
step "step 4: switch the upstream to Go and turn its scheduler on"
deploy "wenhao-billing-api-go:8000" false true
wait_healthy wenhao-billing-api
wait_healthy wenhao-billing-api-go
wait_healthy wenhao-billing-web
assert_schedulers false true
dc logs wenhao-billing-api-go 2>/dev/null | grep -q 'scheduler started' \
  || fail "Go did not log 'scheduler started'"
ok "Go logged 'scheduler started'"
assert_backend go

# The runner ticks at once; wait until each daily job has recorded its latest
# slot (every daily slot is within the last 24 h).
SLOTS=0
for _ in $(seq 1 45); do
  SLOTS=$(psqlq "SELECT count(*) FROM scheduled_job_runs WHERE finished_at IS NOT NULL AND slot > now() - interval '24 hours' AND job_id IN ('recurring-scan-and-generate','usage-lock-cutoff-scan','hosting-overdue-enforcement')")
  [[ "$SLOTS" == 3 ]] && break
  sleep 2
done
expect_eq 3 "$SLOTS" "each daily job recorded its latest slot"
psqlq "SELECT job_id || ' slot=' || to_char(slot AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI') || 'Z error=' || coalesce(error, '-') FROM scheduled_job_runs ORDER BY job_id" | sed 's/^/   go run: /'
# A slot older than 24 h is recorded as skipped (its error says so); any other
# error is a failed run.
expect_eq 0 "$(psqlq "SELECT count(*) FROM scheduled_job_runs WHERE error IS NOT NULL AND error NOT LIKE '%is older than%: skipped'")" "no Go job recorded a failure"

expect_eq "$INVOICES_PY" "$(count_child_invoices)" "Go's catch-up added no child invoice"
expect_eq 0 "$(count_go_digests)" "Go's catch-up sent no digest mail"
expect_eq "$CF_PY" "$(count_cloudflare_calls)" "Go's catch-up made no Cloudflare call (total calls)"
expect_eq "$HOSTING_PY" "$(hosting_state)" "Go's catch-up left every hosting item and target unchanged (digest)"
expect_eq "$FX_PY" "$(count_fx_rows)" "Go's catch-up left the FX row count alone"

# MCP carry-over, part 2: the Python refresh token works against Go.
CODE=$(mcp_refresh "$PY_REFRESH" "$MCP_CID")
expect_eq 200 "$CODE" "Go accepted the Python-issued refresh token"
GO_REFRESH=$(python -c 'import json,sys; print(json.load(open(sys.argv[1]))["refresh_token"])' "$WORK/refresh.json")

# ===========================================================================
step "step 5: set-password and sign in through nginx"
set_go "$PW"
assert_me "$PW" "sign-in through Go after set-password"

# ===========================================================================
step "step 6: MCP after set-password"
# PN 157: set-password revoked the owner's refresh tokens, so the rotated token
# now fails with invalid_grant.
CODE=$(mcp_refresh "$GO_REFRESH" "$MCP_CID")
expect_eq 400 "$CODE" "refresh after set-password is refused"
python -c 'import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if d.get("error")=="invalid_grant" else 1)' "$WORK/refresh.json" \
  || fail "the refusal was not invalid_grant"
ok "the rotated token is invalid_grant after set-password (PN 157)"

# A fresh authorize through Go's login works. Without a session, authorize
# bounces to the SPA login carrying return_to back to this same path/query.
CHALLENGE="E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
RETURN_LOC=$(curl -sS -o "$DISCARD" -D - "$BASE/oauth/authorize?response_type=code&client_id=$MCP_CID&redirect_uri=https%3A%2F%2Flocalhost%2Fcallback&code_challenge=$CHALLENGE&code_challenge_method=S256" \
  | tr -d '\r' | sed -n 's/^[Ll]ocation: //p')
printf '%s' "$RETURN_LOC" | grep -q 'return_to=%2Foauth%2Fauthorize' \
  || fail "authorize without a session did not bounce to /login with return_to"
ok "authorize without a session bounces to the login with return_to"
login "$PW"
GO_LIVE_VERIFIER=$(openssl rand -base64 48 | tr -d '\r\n=+/' | cut -c1-64)
GO_LIVE_REFRESH=$(mcp_token "$(mcp_code "$MCP_CID" "$GO_LIVE_VERIFIER")" "$GO_LIVE_VERIFIER" "$MCP_CID")
[[ -n "$GO_LIVE_REFRESH" ]] || fail "a fresh authorize through Go issued no refresh token"
ok "a fresh authorize through Go's login issued a live refresh token"

# ===========================================================================
step "step 7: the way back — upstream to Python, flags swapped"
deploy "wenhao-billing-api:8000" true false
wait_healthy wenhao-billing-api
wait_healthy wenhao-billing-api-go
wait_healthy wenhao-billing-web
assert_schedulers true false
dc logs wenhao-billing-api-go 2>/dev/null | grep -q 'scheduler is off' \
  || fail "Go did not log that its scheduler is off on the way back"
ok "Go logged 'scheduler is off'"
assert_backend python
seed_python "$PW"
assert_me "$PW" "the owner signs in to Python again"
# Python accepts a Go-issued refresh token: same secret, same table.
CODE=$(mcp_refresh "$GO_LIVE_REFRESH" "$MCP_CID")
expect_eq 200 "$CODE" "Python accepted a Go-issued refresh token"

# ===========================================================================
step "step 8: Go stopped, nginx recreated — the site still serves"
dc stop wenhao-billing-api-go >/dev/null
dc up -d --no-deps --force-recreate wenhao-billing-web >/dev/null
wait_healthy wenhao-billing-web
expect_eq 401 "$(code_of "$BASE/api/v1/auth/me")" "/api/v1/auth/me without a session (401, not 502)"
assert_backend python
assert_me "$PW" "the site serves with Go's container down (Review Focus 4)"

unset PW
step "rehearsal complete"
