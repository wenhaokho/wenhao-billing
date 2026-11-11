#!/usr/bin/env bash
# Runs plan 11's comparison locally against a restored dump (Task 6 steps 3–4).
#
# Both backends and both databases are local: the dev stack's Postgres holds
# the two copies `billing_cmp_py` (Python) and `billing_cmp_go` (Go), restored
# from one `pg_dump` (Task 6 step 2), and the two APIs run beside each other —
# the frozen Python on :8002 (`api-py-cmp`, scheduler off) and Go on :8001
# (`api-go`), both with the scheduler off and FX pointed at the local stub. The
# comparison itself runs in the `go` tools container, which has the repository
# mounted, so the corpus, the allowed differences and the scenarios are read
# from the working tree.
#
#   bash scripts/compare-local.sh reads
#   bash scripts/compare-local.sh writes
#
# It creates the two databases' admin password (kept out of every command line:
# it reaches the backends and the comparison on stdin) and adopts the Go copy
# before serving it (once; a second run skips adopt). The three services it
# starts are removed again on exit. PY_CMP_HOST_PORT / GO_API_HOST_PORT move the
# published ports when 8002 / 8001 are taken. It never contacts production,
# Coolify, mail, Cloudflare or Frankfurter.
set -euo pipefail
cd "$(dirname "$0")/.."
# Git Bash would rewrite container paths such as /api and /src into Windows paths.
[[ -n "${MSYSTEM:-}" ]] && export MSYS_NO_PATHCONV=1
export COMPOSE_PROJECT_NAME=wenhao-billing

MODE=${1:-}
case "$MODE" in
  reads|writes) ;;
  *) sed -n '2,14p' "$0"; exit 2 ;;
esac

EMAIL=${CMP_EMAIL:-admin@example.com}
PY_DB=billing_cmp_py
GO_DB=billing_cmp_go
export PY_CMP_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/$PY_DB"
export GO_DATABASE_URL="postgresql://billing:billing@db:5432/$GO_DB"
# The runner reads the Python copy itself, through pgx: no SQLAlchemy prefix.
PY_RUNNER_DB="postgresql://billing:billing@db:5432/$PY_DB"
PW=${PY_CMP_PASSWORD:-$(openssl rand -base64 48 | tr -d '\r\n')}
[[ -n "$PW" ]] || { echo "FAIL: no password" >&2; exit 1; }
# The report goes through the repo-mounted /src (no host bind mount, so it works
# on Windows too), is then moved out of the working tree to CMP_REPORT (default
# $HOME/scratch/compare-<mode>.json, 0600: it holds the dump's data), and the
# in-tree copy is removed on exit.
REPORT_HOST="backend/.compare-report-$MODE.json"
REPORT_OUT=${CMP_REPORT:-$HOME/scratch/compare-$MODE.json}
TOOLS=(api-py-cmp fx-stub api-go)

dc() { docker compose --profile tools "$@"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# The three services point at the scratch copies; if one is already running,
# someone else is using it, so refuse rather than repoint it.
for svc in "${TOOLS[@]}"; do
  [[ -z "$(dc ps -q "$svc" 2>/dev/null)" ]] \
    || fail "$svc is already running in project $COMPOSE_PROJECT_NAME; stop it first"
done
cleanup() {
  local code=$?
  rm -f "$REPORT_HOST"
  dc rm -sf "${TOOLS[@]}" >/dev/null 2>&1 || true
  exit "$code"
}
trap cleanup EXIT

# The dev Postgres must be up before the copies can be checked.
dc up -d --wait db >/dev/null

# A missing copy means the dump was not restored: say so instead of comparing
# against the dev stack's own `billing` (never named here).
for name in "$PY_DB" "$GO_DB"; do
  dc exec -T db psql -U billing -d postgres -tAc \
    "select 1 from pg_database where datname='$name'" | grep -q 1 \
    || fail "database $name is missing: restore the dump into $PY_DB and $GO_DB first (Task 6 step 2)"
done

# Both copies must already hold the admin (CMP_EMAIL; the owner's email for a
# production dump): Go's set-password changes an existing user only.
for name in "$PY_DB" "$GO_DB"; do
  [[ "$(dc exec -T db psql -U billing -d "$name" -tAc "select count(*) from users where email = '$EMAIL'")" == 1 ]] \
    || fail "$name has no user $EMAIL: set CMP_EMAIL to an admin in the dump"
done

# Go's serve refuses an un-adopted database, so adopt the copy before starting
# it. adopt-baseline refuses a database goose already owns, so a second run
# skips it.
if [[ -n "$(dc exec -T db psql -U billing -d "$GO_DB" -tAc "select to_regclass('goose_db_version')")" ]]; then
  echo "== $GO_DB is already adopted (goose_db_version exists)"
else
  dc run --rm --no-deps api-go adopt-baseline
fi

dc up -d "${TOOLS[@]}" >/dev/null

wait_py() {
  local i
  for i in $(seq 1 60); do
    dc exec -T api-py-cmp python -c \
      'import urllib.request; urllib.request.urlopen("http://localhost:8000/docs", timeout=2)' \
      >/dev/null 2>&1 && return 0
    sleep 2
  done
  dc logs --tail 60 api-py-cmp >&2 || true
  fail "api-py-cmp never became healthy"
}
wait_go() {
  local i
  for i in $(seq 1 60); do
    dc exec -T api-go /api healthcheck >/dev/null 2>&1 && return 0
    sleep 2
  done
  dc logs --tail 60 api-go >&2 || true
  fail "api-go never became healthy"
}
wait_py
wait_go

# A known local password on each copy. seed_admin takes it in argv, so python
# reads it from stdin and hands it over in-process: no shell, no argv, and a
# short or empty read is refused instead of stored.
printf '%s\n' "$PW" | dc exec -T api-py-cmp python -c 'import runpy, sys
pw = sys.stdin.readline().rstrip("\n")
if len(pw) < 8:
    sys.exit("refusing: the password read from stdin is shorter than 8 characters")
sys.argv = ["seed_admin", sys.argv[1], pw]
runpy.run_module("scripts.seed_admin", run_name="__main__")' "$EMAIL" >/dev/null
printf '%s\n' "$PW" | dc exec -T api-go /api set-password "$EMAIL" >/dev/null

# The Go token is issued inside a pipe; it is never echoed or logged.
GOTOKEN=$(dc exec -T api-go /api issue-session "$EMAIL" | tail -n1)
[[ -n "$GOTOKEN" ]] || fail "api-go issued no session token"

compare() {
  # stdin carries the Python password (first line) then the Go token (second).
  # The webhook scenario signs with the backends' shared WEBHOOK_HMAC_SECRET:
  # the published dev default, which api-py-cmp sets and api-go (development
  # mode) falls back to. It is not a secret.
  printf '%s\n%s\n' "$PW" "$GOTOKEN" | dc run --rm -T \
    -e WEBHOOK_HMAC_SECRET=dev-webhook-secret-change-me go \
    go run ./cmd/compare "$@"
}

status=0
case "$MODE" in
  reads)
    compare reads \
      --python http://api-py-cmp:8000 --go http://api-go:8000 \
      --python-email "$EMAIL" --python-password-stdin --go-token-stdin \
      --corpus /src/port/compare-corpus.yaml \
      --allowed /src/port/allowed-diffs.yaml \
      --allowed /src/port/allowed-diffs-prodcopy.yaml \
      --allow-skips --ledger-matches-aging \
      --out "/src/.compare-report-$MODE.json" || status=$?
    ;;
  writes)
    compare writes \
      --python http://api-py-cmp:8000 --python-db "$PY_RUNNER_DB" \
      --go http://api-go:8000 --go-db "$GO_DATABASE_URL" \
      --python-email "$EMAIL" --python-password-stdin --go-token-stdin \
      --scenarios /src/port/compare-writes.yaml \
      --allowed /src/port/allowed-diffs.yaml \
      --out "/src/.compare-report-$MODE.json" || status=$?
    ;;
esac
unset PW GOTOKEN

if [[ -f "$REPORT_HOST" ]]; then
  ( umask 077; mkdir -p "$(dirname "$REPORT_OUT")"; cp "$REPORT_HOST" "$REPORT_OUT" )
  echo "report: $REPORT_OUT"
fi
exit "$status"
