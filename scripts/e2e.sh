#!/usr/bin/env bash
# Runs the frontend's Playwright smoke suite (frontend/e2e) against its own
# database, never the dev `billing` one.
#
#   bash scripts/e2e.sh [playwright args]     e.g. bash scripts/e2e.sh e2e/tabs.spec.ts
#
# It drops and recreates the `billing_e2e` database in the shared dev `db`,
# builds and starts the compose profile `e2e` (backend-e2e, which migrates
# billing_e2e at start-up, and frontend-e2e, the vite dev server proxying to
# it) from this checkout, inserts the admin e2e@wenhao.test with a placeholder
# hash, gives it a fresh random password through `api set-password` (stdin,
# never a command-line value), and runs Playwright in
# mcr.microsoft.com/playwright on the stack's network. Every run starts from
# the migrations' seed data, and what the specs create stays in billing_e2e
# (kept after the run for inspecting failures, dropped by the next run). The
# dev backend and frontend are not touched. The e2e containers are stopped on
# exit. The password reaches the container as an environment variable named
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

E2E_DB=billing_e2e
e2e_compose() { docker compose --profile e2e "$@"; }
trap 'e2e_compose stop backend-e2e frontend-e2e >/dev/null 2>&1 || true' EXIT

if ! up_log=$(docker compose up -d --wait db 2>&1); then
  printf '%s
' "$up_log" >&2
  fail "could not start db"
fi

# A fresh database each run: backend-e2e stops first (it migrates only at
# start-up), then billing_e2e is dropped and recreated. Only billing_e2e.
e2e_compose rm -sf backend-e2e >/dev/null 2>&1 || fail "could not remove backend-e2e"
docker compose exec -T db psql -U billing -d postgres -v ON_ERROR_STOP=1 -q >/dev/null <<SQL   || fail "could not recreate the ${E2E_DB} database"
DROP DATABASE IF EXISTS ${E2E_DB} WITH (FORCE);
CREATE DATABASE ${E2E_DB};
SQL

if ! up_log=$(e2e_compose up -d --build --wait backend-e2e frontend-e2e 2>&1); then
  printf '%s
' "$up_log" >&2
  fail "could not start backend-e2e and frontend-e2e"
fi

# The vite dev server has no healthcheck: wait until it serves the SPA.
frontend_up() { e2e_compose exec -T frontend-e2e wget -q -O /dev/null http://127.0.0.1:5173/login 2>/dev/null; }
for _ in $(seq 1 90); do frontend_up && break; sleep 1; done
frontend_up || fail "frontend-e2e did not answer on :5173"

# Vite rejects requests whose Host is not localhost or an IP address
# (server.allowedHosts), so the browser addresses frontend-e2e by its IP on
# the stack's network rather than by name. Read after the wait, so it is the
# address of the container that answered.
FRONTEND_CID=$(e2e_compose ps -q frontend-e2e) || fail "could not list the frontend-e2e container"
[[ -n "$FRONTEND_CID" ]] || fail "frontend-e2e container not running"
FRONTEND_IP=$(docker inspect -f "{{with index .NetworkSettings.Networks \"$NETWORK\"}}{{.IPAddress}}{{end}}" "$FRONTEND_CID" 2>/dev/null)   || fail "could not inspect the frontend-e2e container"
[[ -n "$FRONTEND_IP" ]] || fail "frontend-e2e is not attached to network $NETWORK"
export BASE_URL="http://${FRONTEND_IP}:5173"

# The e2e user, an admin, in the fresh billing_e2e only.
export E2E_EMAIL=e2e@wenhao.test
docker compose exec -T db psql -U billing -d "$E2E_DB" -v ON_ERROR_STOP=1 -q >/dev/null <<SQL   || fail "could not insert ${E2E_EMAIL} into ${E2E_DB}"
INSERT INTO users (user_id, email, password_hash, role, display_name)
VALUES (gen_random_uuid(), '${E2E_EMAIL}', 'e2e-placeholder-not-a-hash', 'admin', 'E2E smoke');
SQL

E2E_PASSWORD=$(openssl rand -base64 33 | tr -d '\r\n/+=')
[[ ${#E2E_PASSWORD} -ge 24 ]] || fail "could not generate a password"
export E2E_PASSWORD
printf '%s\n' "$E2E_PASSWORD" \
  | e2e_compose exec -T backend-e2e /api set-password "$E2E_EMAIL" >/dev/null 2>&1 \
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
