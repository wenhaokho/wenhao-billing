#!/usr/bin/env bash
# The project's checks, run locally through Docker (GitHub Actions is off for
# this repository). Exits non-zero if any check fails.
#
#   bash scripts/check.sh
#
# 1. compose paths: every build context and bind mount exists
# 2. frozen Python tests: pytest in the dev stack's `backend` service
#    against billing_test (starts db and backend if they are not running)
# 3. frontend: npm ci && npm run build (vue-tsc typecheck + vite build)
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -n "${MSYSTEM:-}" ]]; then export MSYS_NO_PATHCONV=1; root=$(pwd -W); else root=$(pwd); fi

echo "== compose paths"
bash scripts/check-compose-paths.sh

echo "== frozen Python tests"
docker compose up -d --wait db backend >/dev/null
docker compose exec -T -e TEST_DATABASE_URL=postgresql+psycopg://billing:billing@db:5432/billing_test \
  backend pytest -q -p no:cacheprovider

echo "== frontend build"
# A separate node_modules volume keeps the host's (or the dev container's) install untouched.
docker run --rm -v "$root/frontend:/app" -v wenhao-billing_check_node_modules:/app/node_modules -w /app \
  node:22-alpine sh -c "npm ci --no-audit --no-fund && npm run build"

echo "all checks passed"
