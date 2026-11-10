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
# before serving it. It never contacts production, Coolify, mail, Cloudflare or
# Frankfurter.
set -euo pipefail
cd "$(dirname "$0")/.."
export COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-wenhao-billing}

MODE=${1:-}
case "$MODE" in
  reads|writes) ;;
  *) sed -n '2,12p' "$0"; exit 2 ;;
esac

EMAIL=${CMP_EMAIL:-admin@example.com}
PY_DB=billing_cmp_py
GO_DB=billing_cmp_go
export PY_CMP_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/$PY_DB"
export GO_DATABASE_URL="postgresql://billing:billing@db:5432/$GO_DB"
PW=${PY_CMP_PASSWORD:-$(openssl rand -base64 48 | tr -d '\n')}
# The report goes through the repo-mounted /src (no host bind mount, so it works
# on Windows too) and is removed on exit.
REPORT_HOST="backend/.compare-report-$MODE.json"
trap 'rm -f "$REPORT_HOST"' EXIT

dc() { docker compose --profile tools "$@"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# A missing copy means the dump was not restored: say so instead of comparing
# against the dev stack's own `billing` (never named here).
for name in "$PY_DB" "$GO_DB"; do
  dc exec -T db psql -U billing -d postgres -tAc \
    "select 1 from pg_database where datname='$name'" | grep -q 1 \
    || fail "database $name is missing: restore the dump into $PY_DB and $GO_DB first (Task 6 step 2)"
done

# Go's serve refuses an un-adopted database, so adopt the copy before starting.
dc up -d --wait db
dc run --rm --no-deps api-go adopt-baseline

dc up -d api-py-cmp fx-stub api-go

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

# A known local password on each copy. seed_admin takes it in argv, so it is
# read inside the container with `read -rs` from the pipe; the host command
# line and history never hold it.
printf '%s\n' "$PW" | dc exec -T api-py-cmp \
  sh -c 'read -rs P; python -m scripts.seed_admin "$1" "$P"' sh "$EMAIL" >/dev/null
printf '%s\n' "$PW" | dc exec -T api-go /api set-password "$EMAIL" >/dev/null

# The Go token is issued inside a pipe; it is never echoed or logged.
GOTOKEN=$(dc exec -T api-go /api issue-session "$EMAIL" | tail -n1)
[[ -n "$GOTOKEN" ]] || fail "api-go issued no session token"

compare() {
  # stdin carries the Python password (first line) then the Go token (second).
  printf '%s\n%s\n' "$PW" "$GOTOKEN" | dc run --rm -T go \
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
      --out "/src/.compare-report-$MODE.json" || status=$?
    ;;
  writes)
    compare writes \
      --python http://api-py-cmp:8000 --python-db "$PY_CMP_DATABASE_URL" \
      --go http://api-go:8000 --go-db "$GO_DATABASE_URL" \
      --python-email "$EMAIL" --python-password-stdin --go-token-stdin \
      --scenarios /src/port/compare-writes.yaml \
      --allowed /src/port/allowed-diffs.yaml \
      --out "/src/.compare-report-$MODE.json" || status=$?
    ;;
esac

if [[ -f "$REPORT_HOST" ]]; then
  python -c 'import json,sys; d=json.load(open(sys.argv[1])); print("report:", d.get("differences"), "differences,", d.get("summary",{}).get("skipped"), "skipped")' "$REPORT_HOST"
fi
exit "$status"
