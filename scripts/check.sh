#!/usr/bin/env bash
# The project's checks, run locally through Docker (GitHub Actions is off for
# this repository). Exits non-zero if any check fails.
#
#   bash scripts/check.sh
#
# 1. compose paths: every build context and bind mount exists
# 2. frontend: npm ci && npm run build (vue-tsc typecheck + vite build),
#    then the return_to guard's node check
# 3. go (backend/): lint, generated code current (sqlc + openapi.json), and
#    go test (GO_TEST_RACE=1 adds -race). Fails if any Go test is skipped: gokit/testkit skips every
#    database test when PGTESTDB_URL is unset, and go test still exits 0.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -n "${MSYSTEM:-}" ]]; then export MSYS_NO_PATHCONV=1; root=$(pwd -W); else root=$(pwd); fi

echo "== compose paths"
bash scripts/check-compose-paths.sh

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
