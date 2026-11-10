#!/usr/bin/env bash
# Local rehearsal of the production switch-over (plan 11, Task 5 step 2).
#
# It runs the Coolify compose on this machine, beside a local Postgres, against
# a restored dev database, and walks the whole switch and the way back:
#
#   1. Python serves (PY_SCHEDULER_ENABLED=true);
#   2. adopt the baseline, then add Go with GO_SCHEDULER_ENABLED=false; Python
#      still serves and Go's container is healthy;
#   3. the scheduler overlap check: run Python's recurring scan for today's
#      slot on a due template, and count invoices, email log lines and FX rows;
#   4. switch the upstream to Go with GO_SCHEDULER_ENABLED=true and
#      PY_SCHEDULER_ENABLED=false, redeploy, and prove Go's catch-up run adds
#      no child invoice, sends no digest, leaves the FX rows alone, and records
#      today's slot per job;
#   5. set-password and sign in through nginx;
#   6. MCP carry-over: a Python-issued refresh token refreshes against Go after
#      the switch, fails with invalid_grant after set-password (PN 157), and a
#      fresh authorize through Go's login works;
#   7. the way back: upstream to Python, flags swapped, redeploy, seed_admin,
#      sign in to Python, and Python accepts a Go-issued refresh token;
#   8. kill Go while the upstream is Python: the site still serves.
#
# It never contacts production, Coolify, mail, Cloudflare or Frankfurter. Every
# check reads HTTP 200 from /api/v1/auth/me through nginx; curl treats
# http://localhost as a secure context, so the Secure session cookie is sent.
#
#   bash scripts/rehearse-switch.sh
#
# Knobs (all optional):
#   REHEARSE_DUMP      a `pg_dump -Fc` to restore instead of dumping the dev db
#   REHEARSE_HTTP_PORT the published nginx port (default 8080)
#   REHEARSE_EMAIL     the admin email to sign in as (default admin@example.com)
#   REHEARSE_KEEP=1    leave the containers up at the end
#
# The password the rehearsal sets on both backends is generated here; it only
# ever travels on stdin or in the 0600 env file, never on a command line, in a
# shell history, or on the screen.
set -euo pipefail
cd "$(dirname "$0")/.."

export COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-wenhao-billing-rehearsal}
HTTP_PORT=${REHEARSE_HTTP_PORT:-8080}
BASE="http://localhost:${HTTP_PORT}"
EMAIL=${REHEARSE_EMAIL:-admin@example.com}
WORK=$(mktemp -d)
ENVFILE="$WORK/rehearse.env"
JAR="$WORK/jar"

step() { printf '\n== %s\n' "$*"; }
ok()   { printf '   ok: %s\n' "$*"; }
fail() { printf '   FAIL: %s\n' "$*" >&2; exit 1; }
expect_eq() { # expected actual label
  [[ "$1" == "$2" ]] || fail "$3: expected $1, got $2"
  ok "$3 ($2)"
}

cleanup() {
  if [[ "${REHEARSE_KEEP:-0}" == 1 ]]; then
    printf '\nleft up: project %s, nginx on %s\n' "$COMPOSE_PROJECT_NAME" "$BASE"
  else
    dc down -v >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

dc() {
  docker compose --env-file "$ENVFILE" \
    -f docker-compose.coolify.yml -f scripts/rehearse-switch.compose.yml "$@"
}
devd() { COMPOSE_PROJECT_NAME=wenhao-billing docker compose -f docker-compose.yml "$@"; }
psqlq() { dc exec -T shared-postgres psql -U billing -d billing -At -c "$1"; }
secret() { openssl rand -base64 48 | tr -d '\n'; }

# set_flag rewrites one line of the env file (no sed -i, so it works on GNU and
# BSD). The file is 0600 and the value never touches a command line.
set_flag() { # name value
  local name=$1 value=$2 tmp
  tmp=$(mktemp); chmod 600 "$tmp"
  if grep -q "^${name}=" "$ENVFILE"; then
    sed "s|^${name}=.*|${name}=${value}|" "$ENVFILE" > "$tmp"
  else
    cat "$ENVFILE" > "$tmp"
    printf '%s=%s\n' "$name" "$value" >> "$tmp"
  fi
  mv "$tmp" "$ENVFILE"
}

# deploy writes the three switch variables and reconciles the stack. The API
# containers are brought up before nginx, so the upstream host always resolves
# when nginx starts.
deploy() { # upstream py_sched go_sched
  set_flag BACKEND_UPSTREAM "$1"
  set_flag PY_SCHEDULER_ENABLED "$2"
  set_flag GO_SCHEDULER_ENABLED "$3"
  dc up -d wenhao-billing-api wenhao-billing-api-go >/dev/null
  dc up -d wenhao-billing-web >/dev/null
}

wait_healthy() { # service
  local svc=$1 i health
  for i in $(seq 1 60); do
    health=$(dc ps --format '{{.Health}}' "$svc" 2>/dev/null || true)
    [[ "$health" == "healthy" ]] && { ok "$svc healthy"; return 0; }
    sleep 2
  done
  dc logs --tail 80 "$svc" >&2 || true
  fail "$svc never became healthy"
}

# --- HTTP helpers -----------------------------------------------------------
# get_json GETs a path with the session cookie and prints the body.
get_json() { curl -fsS -b "$JAR" -c "$JAR" "$BASE$1"; }

# first_id GETs a list path and prints field of its first element.
first_id() { # path field
  get_json "$1" | python -c 'import json,sys; d=json.load(sys.stdin); d=d if isinstance(d,list) else d.get("items") or d.get("data") or []; print(d[0][sys.argv[1]] if d else "")' "$2"
}

# login signs in through nginx and stores the cookie jar. The body is piped, so
# the password is never on the command line.
login() { # password
  printf '{"email":"%s","password":"%s","remember":false}' "$EMAIL" "$1" \
    | curl -fsS -c "$JAR" -H 'Content-Type: application/json' --data @- \
        "$BASE/api/v1/auth/login" >/dev/null
}

# assert_me signs in and reads /api/v1/auth/me through nginx; it must be 200.
assert_me() { # password label
  login "$1"
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' -b "$JAR" "$BASE/api/v1/auth/me")
  expect_eq 200 "$code" "$2: /api/v1/auth/me through nginx"
}

# seed_python sets the Python copy's admin password. seed_admin takes it in
# argv, so it is read inside the container with `read -rs` from the pipe: the
# host command line and history never hold it.
seed_python() { # password
  printf '%s\n' "$1" | dc exec -T wenhao-billing-api \
    sh -c 'read -rs P; python -m scripts.seed_admin "$1" "$P"' sh "$EMAIL" >/dev/null
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
# prints the authorization code (and sets MCP_STATE).
mcp_code() { # client_id verifier
  local cid=$1 verifier=$2 challenge state page csrf body loc
  challenge=$(printf '%s' "$verifier" | openssl dgst -sha256 -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')
  state="rehearse-$$-$RANDOM"
  MCP_STATE=$state
  page=$(curl -fsS -b "$JAR" -c "$JAR" \
    "$BASE/oauth/authorize?response_type=code&client_id=$cid&redirect_uri=https%3A%2F%2Flocalhost%2Fcallback&code_challenge=$challenge&code_challenge_method=S256&state=$state")
  csrf=$(printf '%s' "$page" | grep -o 'name="csrf_token" value="[^"]*"' | sed 's/.*value="//;s/"$//')
  [[ -n "$csrf" ]] || fail "consent page carried no csrf_token"
  body=$(printf 'response_type=code&client_id=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=%s&csrf_token=%s&decision=approve' \
    "$cid" "https://localhost/callback" "$challenge" "$state" "$csrf")
  loc=$(printf '%s' "$body" | curl -sS -o /dev/null -D - -b "$JAR" -c "$JAR" -X POST \
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

# mcp_refresh refreshes and prints "<http_code> <error>" (and on 200 writes the
# new refresh token to $WORK/refreshed and prints "<code> ok").
mcp_refresh() { # refresh_token client_id -> prints http code; refreshed body in $WORK/refresh.json
  local body
  body=$(printf 'grant_type=refresh_token&refresh_token=%s&client_id=%s' "$1" "$2")
  printf '%s' "$body" | curl -sS -o "$WORK/refresh.json" -w '%{http_code}' -X POST "$BASE/oauth/token" --data @-
}

# --- counters ---------------------------------------------------------------
count_child_invoices() { psqlq "SELECT count(*) FROM invoices WHERE invoice_type='RECURRING' AND is_template IS NOT TRUE"; }
count_fx_rows() { psqlq "SELECT count(*) FROM fx_rates"; }
count_py_digests() { dc logs wenhao-billing-api 2>/dev/null | grep -c '\[email disabled\] would send.*draft invoice' || true; }
count_go_digests() { dc logs wenhao-billing-api-go 2>/dev/null | grep -c '\[email disabled\] would send.*draft invoice' || true; }

# ===========================================================================
step "setup: env file and the local Postgres"
umask 077
{
  echo "DATABASE_URL=postgresql+psycopg://billing:billing@shared-postgres:5432/billing"
  echo "SERVICE_URL_WEB_80=http://localhost"
  echo "SERVICE_BASE64_64_SESSION=$(secret)"
  echo "SERVICE_BASE64_64_WEBHOOK=$(secret)"
  echo "SERVICE_BASE64_64_EMAILTOKEN=$(secret)"
  echo "SERVICE_BASE64_64_MCPCONFIRM=$(secret)"
  echo "SERVICE_BASE64_64_MCPTOKEN=$(secret)"
  echo "RESEND_API_KEY="
  echo "CLOUDFLARE_API_TOKEN="
  echo "SENTRY_DSN="
  echo "BACKEND_UPSTREAM=wenhao-billing-api:8000"
  echo "PY_SCHEDULER_ENABLED=true"
  echo "GO_SCHEDULER_ENABLED=false"
} > "$ENVFILE"
chmod 600 "$ENVFILE"
PW=$(secret)          # the local admin password, generated here
ok "env file written to a 0600 temp file; secrets generated"

dc up -d --wait shared-postgres
if [[ -n "${REHEARSE_DUMP:-}" ]]; then
  dc exec -T shared-postgres pg_restore -U billing -d billing --no-owner --no-privileges < "$REHEARSE_DUMP"
else
  devd up -d --wait db
  devd exec -T db pg_dump -U billing -Fc --no-owner --no-privileges billing \
    | dc exec -T shared-postgres pg_restore -U billing -d billing --no-owner --no-privileges
fi
ok "restored the dev database into shared-postgres"

# ===========================================================================
step "step 1: Python serves (PY_SCHEDULER_ENABLED=true)"
dc up -d --build wenhao-billing-api wenhao-billing-web
wait_healthy wenhao-billing-api
wait_healthy wenhao-billing-web
seed_python "$PW"
assert_me "$PW" "python serves"

# ===========================================================================
step "step 2: adopt, then add Go with the scheduler off"
dc run --rm --no-deps wenhao-billing-api-go adopt-baseline
ok "adopt-baseline returned 0"
dc up -d wenhao-billing-api-go
wait_healthy wenhao-billing-api-go
dc logs wenhao-billing-api-go 2>/dev/null | grep -q 'scheduler is off' \
  || fail "Go did not log that its scheduler is off"
assert_me "$PW" "python still serves with Go beside it"

# ===========================================================================
step "step 3: scheduler overlap check (Python's scan for today's slot)"
# The restored database must hold a due recurring template. The dev dump does
# not, so create one through Python's own API: a MONTHLY cycle starting today.
CUSTOMER_ID=$(first_id "/api/v1/customers?active=true" customer_id)
PROJECT_ID=$(first_id "/api/v1/projects?status=ACTIVE" project_id)
ITEM_ID=$(first_id "/api/v1/items" item_id)
[[ -n "$CUSTOMER_ID" && -n "$PROJECT_ID" && -n "$ITEM_ID" ]] \
  || fail "the restored database needs an active customer, project and item to build a due template"
TODAY=$(date -u +%F)
printf '{"customer_id":"%s","project_id":"%s","invoice_type":"RECURRING","currency":"IDR","issue_date":"%s","due_date":"%s","payment_terms":"Net 30","line_items":[{"item_id":"%s","description":"Rehearsal recurring","quantity":"1.0000","unit_price":"1000.0000","position":1}],"schedule":{"frequency":"MONTHLY","interval":1,"start_date":"%s","end_mode":"AFTER_N","end_after_cycles":1}}' \
  "$CUSTOMER_ID" "$PROJECT_ID" "$TODAY" "$TODAY" "$ITEM_ID" "$TODAY" \
  | curl -fsS -b "$JAR" -H 'Content-Type: application/json' --data @- \
      "$BASE/api/v1/invoices/recurring-template" >/dev/null
ok "created a recurring template due today ($TODAY)"

TEMPLATE_GENERATED=$(dc exec -T wenhao-billing-api python -c \
  'from app.workers.tasks.recurring import scan_and_generate; print(scan_and_generate())')
[[ "${TEMPLATE_GENERATED:-0}" -ge 1 ]] \
  || fail "Python's scan generated no draft: the overlap check would be vacuous"
INVOICES_PY=$(count_child_invoices)
DIGESTS_PY=$(count_py_digests)
FX_PY=$(count_fx_rows)
ok "after Python's scan: invoices=$INVOICES_PY digests=$DIGESTS_PY fx=$FX_PY (generated=$TEMPLATE_GENERATED)"

# MCP carry-over, part 1: a client and a refresh token minted by Python.
MCP_CID=$(mcp_register)
MCP_VERIFIER=$(openssl rand -base64 48 | tr -d '\n=+/' | cut -c1-64)
PY_REFRESH=$(mcp_token "$(mcp_code "$MCP_CID" "$MCP_VERIFIER")" "$MCP_VERIFIER" "$MCP_CID")
[[ -n "$PY_REFRESH" ]] || fail "Python issued no refresh token"
ok "registered an MCP client and got a Python refresh token"

# ===========================================================================
step "step 4: switch the upstream to Go and turn its scheduler on"
deploy "wenhao-billing-api-go:8000" false true
wait_healthy wenhao-billing-api-go
wait_healthy wenhao-billing-web
# The runner ticks at once and then every minute; give the catch-up a moment.
sleep 15
dc logs wenhao-billing-api-go 2>/dev/null | grep -q 'scheduler started' \
  || fail "Go did not log 'scheduler started'"
ok "Go logged 'scheduler started'"

INVOICES_GO=$(count_child_invoices)
FX_GO=$(count_fx_rows)
DIGESTS_GO=$(count_go_digests)
expect_eq "$INVOICES_PY" "$INVOICES_GO" "Go's catch-up added no child invoice"
expect_eq "$FX_PY" "$FX_GO" "Go's catch-up left the FX row count alone"
expect_eq 0 "$DIGESTS_GO" "Go's catch-up sent no digest mail"
# Every daily job's latest slot is within 24 h, so each has a row for it.
SLOTS=$(psqlq "SELECT count(*) FROM scheduled_job_runs WHERE slot > now() - interval '24 hours' AND job_id IN ('recurring-scan-and-generate','usage-lock-cutoff-scan','hosting-overdue-enforcement')")
expect_eq 3 "$SLOTS" "each daily job recorded today's slot"

# MCP carry-over, part 2: the Python refresh token works against Go.
CODE=$(mcp_refresh "$PY_REFRESH" "$MCP_CID")
[[ "$CODE" == "200" ]] || fail "Go refused the Python-issued refresh token (HTTP $CODE)"
GO_REFRESH=$(python -c 'import json,sys; print(json.load(open(sys.argv[1]))["refresh_token"])' "$WORK/refresh.json")
ok "Go accepted the Python-issued refresh token and rotated it"

# ===========================================================================
step "step 5: set-password and sign in through nginx"
set_go "$PW"
assert_me "$PW" "sign-in through Go after set-password"

# PN 157: set-password revoked the owner's refresh tokens, so the rotated token
# now fails with invalid_grant.
CODE=$(mcp_refresh "$GO_REFRESH" "$MCP_CID")
[[ "$CODE" == "400" ]] || fail "expected 400 invalid_grant after set-password, got HTTP $CODE"
python -c 'import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if d.get("error")=="invalid_grant" else 1)' "$WORK/refresh.json" \
  || fail "the refusal was not invalid_grant"
ok "the rotated token is invalid_grant after set-password (PN 157)"

# A fresh authorize through Go's login works. Without a session, authorize
# bounces to the SPA login carrying return_to back to this same path/query.
RETURN_LOC=$(curl -sS -o /dev/null -D - "$BASE/oauth/authorize?response_type=code&client_id=$MCP_CID&redirect_uri=https%3A%2F%2Flocalhost%2Fcallback&code_challenge=x&code_challenge_method=S256" \
  | tr -d '\r' | sed -n 's/^[Ll]ocation: //p')
printf '%s' "$RETURN_LOC" | grep -q 'return_to=%2Foauth%2Fauthorize' \
  || fail "authorize without a session did not bounce to /login with return_to"
login "$PW"
GO_LIVE_VERIFIER=$(openssl rand -base64 48 | tr -d '\n=+/' | cut -c1-64)
GO_LIVE_REFRESH=$(mcp_token "$(mcp_code "$MCP_CID" "$GO_LIVE_VERIFIER")" "$GO_LIVE_VERIFIER" "$MCP_CID")
[[ -n "$GO_LIVE_REFRESH" ]] || fail "a fresh authorize through Go issued no refresh token"
ok "a fresh authorize through Go's login issued a live refresh token"

# ===========================================================================
step "step 7: the way back — upstream to Python, flags swapped"
deploy "wenhao-billing-api:8000" true false
wait_healthy wenhao-billing-api
wait_healthy wenhao-billing-web
dc logs wenhao-billing-api 2>/dev/null | grep -qi 'scheduler' \
  || fail "Python did not start its scheduler on the way back"
dc logs wenhao-billing-api-go 2>/dev/null | grep -q 'scheduler is off' \
  || fail "Go did not stop its scheduler on the way back"
ok "Python's scheduler started and Go's stopped"
seed_python "$PW"
assert_me "$PW" "the owner signs in to Python again"
# Python accepts a Go-issued refresh token: same secret, same table.
CODE=$(mcp_refresh "$GO_LIVE_REFRESH" "$MCP_CID")
[[ "$CODE" == "200" ]] || fail "Python refused a Go-issued refresh token (HTTP $CODE)"
ok "Python accepted a Go-issued refresh token"

# ===========================================================================
step "step 8: kill Go while the upstream is Python — the site still serves"
dc stop wenhao-billing-api-go >/dev/null
assert_me "$PW" "the site serves with Go's container down (Review Focus 4)"
dc start wenhao-billing-api-go >/dev/null

step "rehearsal complete: run it a second time to confirm it is idempotent"
