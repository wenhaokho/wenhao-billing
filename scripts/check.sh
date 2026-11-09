#!/usr/bin/env bash
# The project's checks, run locally through Docker (GitHub Actions is off for
# this repository). Exits non-zero if any check fails.
#
#   bash scripts/check.sh
#
# 1. compose paths: every build context and bind mount exists
# 2. frozen Python tests: pytest in the dev stack's `backend` service
#    against billing_test (starts db and backend if they are not running,
#    creates billing_test if it is missing). Fails if any test is skipped:
#    the suite skips every database test when Postgres is unreachable, and
#    pytest still exits 0. CHECK_TEST_HOST and CHECK_TEST_DB override the
#    database host and name (default db, billing_test).
# 3. frontend: npm ci && npm run build (vue-tsc typecheck + vite build)
# 4. go (backend/): lint, generated code current (sqlc + openapi.json), and
#    go test -race. Fails if any Go test is skipped: gokit/testkit skips every
#    database test when PGTESTDB_URL is unset, and go test still exits 0.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -n "${MSYSTEM:-}" ]]; then export MSYS_NO_PATHCONV=1; root=$(pwd -W); else root=$(pwd); fi

echo "== compose paths"
bash scripts/check-compose-paths.sh

echo "== frozen Python tests"
test_host=${CHECK_TEST_HOST:-db}
test_db=${CHECK_TEST_DB:-billing_test}
docker compose up -d --wait db backend >/dev/null
if ! docker compose exec -T db psql -U billing -d billing -tAc \
    "select 1 from pg_database where datname = '$test_db'" | grep -q 1; then
  docker compose exec -T db createdb -U billing "$test_db"
fi
out=$(mktemp)
status=0
docker compose exec -T -e "TEST_DATABASE_URL=postgresql+psycopg://billing:billing@$test_host:5432/$test_db" \
  backend pytest -q -p no:cacheprovider -rs >"$out" 2>&1 || status=$?
tail -n 3 "$out"
if [[ $status -ne 0 ]]; then rm -f "$out"; exit "$status"; fi
if grep -Eq '[0-9]+ skipped' "$out"; then
  echo "frozen Python tests: some tests were skipped (is Postgres reachable at $test_host/$test_db?)" >&2
  rm -f "$out"; exit 1
fi
rm -f "$out"

echo "== frontend build"
# A separate node_modules volume keeps the host's (or the dev container's) install untouched.
docker run --rm -v "$root/frontend:/app" -v wenhao-billing_check_node_modules:/app/node_modules -w /app \
  node:22-alpine sh -c "npm ci --no-audit --no-fund && npm run build"

echo "== frontend return_to check"
# The frontend has no test runner; the MCP login bounce's return_to guard is
# checked by a plain node script on the same image.
docker run --rm -v "$root/frontend:/app" -w /app \
  node:22-alpine node --experimental-strip-types --no-warnings src/router/returnTo.check.mjs

echo "== go"
bash backend/dev lint
bash backend/dev generated
gotest=$(mktemp)
status=0
bash backend/dev test -count=1 -json ./... >"$gotest" 2>&1 || status=$?
if [[ $status -ne 0 ]]; then
  grep -E '"Action":"(fail|output)"' "$gotest" | grep -E '"Output":"(--- FAIL|FAIL|panic)|"Action":"fail"' | head -n 30 >&2 || true
  tail -n 5 "$gotest" >&2
  rm -f "$gotest"; exit "$status"
fi
# Test-level skips only: a package without tests also reports a package-level skip.
skipped=$(grep '"Action":"skip"' "$gotest" | grep '"Test":' || true)
if [[ -n "$skipped" ]]; then
  echo "go tests: some tests were skipped (is PGTESTDB_URL set and Postgres reachable?):" >&2
  echo "$skipped" | grep -o '"Package":"[^"]*","Test":"[^"]*"' | head -n 20 >&2
  rm -f "$gotest"; exit 1
fi
passed=$(grep -c '"Action":"pass","Package":"[^"]*","Test"' "$gotest" || true)
if [[ "${passed:-0}" -le 0 ]]; then
  echo "go tests: no test passed (empty or unparsable output)" >&2
  rm -f "$gotest"; exit 1
fi
echo "go tests: $passed passed, none skipped"
rm -f "$gotest"

echo "all checks passed"
