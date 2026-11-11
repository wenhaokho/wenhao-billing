#!/usr/bin/env bash
# Runs the frontend's Playwright smoke suite (frontend/e2e) against the dev stack.
#
#   bash scripts/e2e.sh [playwright args]     e.g. bash scripts/e2e.sh e2e/tabs.spec.ts
#
# It brings up db, backend and frontend in the shared dev stack, makes sure the
# dev `billing` database has the user e2e@wenhao.test (inserted with a
# placeholder hash when missing; dev database only), gives that user a fresh
# random password through `api set-password` (stdin, never a command-line
# value), and runs Playwright in mcr.microsoft.com/playwright on the stack's
# network. The password reaches the container as an environment variable named
# on the command line (-e E2E_PASSWORD), never as a value there, and is never
# printed. Exits with Playwright's status.
set -euo pipefail
cd "$(dirname "$0")/.."
# Git Bash would rewrite container paths such as /api and /work into Windows paths.
[[ -n "${MSYSTEM:-}" ]] && export MSYS_NO_PATHCONV=1
export COMPOSE_PROJECT_NAME=wenhao-billing

# Must equal frontend/package.json's exact @playwright/test version.
PW_VERSION=1.64.1
PW_IMAGE="mcr.microsoft.com/playwright:v${PW_VERSION}-noble"
NETWORK=wenhao-billing_default
# The suite's own node_modules (glibc, Playwright's image), kept out of the
# worktree; reinstalled when package-lock.json changes.
NODE_MODULES_VOLUME=wenhao-billing_e2e_node_modules
FRONTEND_DIR="$(pwd)/frontend"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

grep -q "\"@playwright/test\": \"${PW_VERSION}\"" frontend/package.json \
  || fail "frontend/package.json does not pin @playwright/test ${PW_VERSION}"

if ! up_log=$(docker compose up -d --wait db backend frontend 2>&1); then
  printf '%s\n' "$up_log" >&2
  fail "could not start db, backend and frontend"
fi

# The vite dev server has no healthcheck: wait until it serves the SPA.
FRONTEND_CID=$(docker compose ps -q frontend)
[[ -n "$FRONTEND_CID" ]] || fail "frontend container not running"
# Vite rejects requests whose Host is not localhost or an IP address
# (server.allowedHosts), so the browser addresses the frontend container by
# its IP on the stack's network rather than by the name `frontend`.
FRONTEND_IP=$(docker inspect -f "{{(index .NetworkSettings.Networks \"$NETWORK\").IPAddress}}" "$FRONTEND_CID")
[[ -n "$FRONTEND_IP" ]] || fail "frontend has no address on $NETWORK"
export BASE_URL="http://${FRONTEND_IP}:5173"
frontend_up() { docker compose exec -T frontend wget -q -O /dev/null http://127.0.0.1:5173/login 2>/dev/null; }
for _ in $(seq 1 60); do frontend_up && break; sleep 1; done
frontend_up || fail "frontend did not answer on :5173"

# The e2e user: an admin like the first admin row. Dev database only.
export E2E_EMAIL=e2e@wenhao.test
docker compose exec -T db psql -U billing -d billing -v ON_ERROR_STOP=1 -q >/dev/null <<SQL
INSERT INTO users (user_id, email, password_hash, role, display_name)
SELECT gen_random_uuid(), '${E2E_EMAIL}', 'e2e-placeholder-not-a-hash', u.role, 'E2E smoke'
FROM (SELECT role FROM users WHERE role = 'admin' ORDER BY created_at NULLS LAST LIMIT 1) u
WHERE NOT EXISTS (SELECT 1 FROM users WHERE email = '${E2E_EMAIL}');
SQL
docker compose exec -T db psql -U billing -d billing -tAc \
  "SELECT 1 FROM users WHERE email = '${E2E_EMAIL}'" | grep -q 1 \
  || fail "no ${E2E_EMAIL} user (the dev database has no admin to copy)"

E2E_PASSWORD=$(openssl rand -base64 33 | tr -d '\r\n/+=')
[[ ${#E2E_PASSWORD} -ge 24 ]] || fail "could not generate a password"
export E2E_PASSWORD
printf '%s\n' "$E2E_PASSWORD" \
  | docker compose exec -T backend /api set-password "$E2E_EMAIL" >/dev/null 2>&1 \
  || fail "set-password failed for ${E2E_EMAIL}"

set +e
docker run --rm --init --ipc=host \
  --network "$NETWORK" \
  -v "${FRONTEND_DIR}:/work" \
  -v "${NODE_MODULES_VOLUME}:/work/node_modules" \
  -w /work \
  -e BASE_URL -e E2E_EMAIL -e E2E_PASSWORD -e CI=1 \
  "$PW_IMAGE" \
  bash -c '
    set -e
    stamp=node_modules/.e2e-lock.sha
    want=$(sha256sum package-lock.json | cut -d" " -f1)
    if [ "$(cat "$stamp" 2>/dev/null)" != "$want" ]; then
      echo "e2e: installing node_modules (package-lock.json changed)" >&2
      npm ci --no-audit --no-fund --loglevel=error >&2
      echo "$want" > "$stamp"
    fi
    exec npx playwright test "$@"
  ' e2e "$@"
status=$?
set -e
exit "$status"
