#!/usr/bin/env bash
# Fails when a compose file builds from, or bind-mounts, a path that does not
# exist in the working tree, so a moved directory (backend/ -> backend-python/)
# cannot leave Coolify building an empty context or the dev stack mounting
# empty folders. Checks docker-compose.yml and docker-compose.coolify.yml.
#
#   bash scripts/check-compose-paths.sh
set -euo pipefail
cd "$(dirname "$0")/.."

status=0
for file in docker-compose.yml docker-compose.coolify.yml; do
  # Unset ${VAR} references only produce warnings; a broken file still fails here.
  if ! json=$(docker compose -f "$file" --profile '*' config --format json 2>/dev/null); then
    echo "$file: docker compose config failed" >&2
    docker compose -f "$file" --profile '*' config >/dev/null
    exit 1
  fi
  printf '%s' "$json" | python -c '
import json, os, sys
file, root = sys.argv[1], os.getcwd()
missing = 0
for name, svc in json.load(sys.stdin)["services"].items():
    paths = []
    build = svc.get("build")
    if build:
        paths.append(build["context"])
    for vol in svc.get("volumes", []):
        if vol.get("type") == "bind":
            paths.append(vol["source"])
    for path in paths:
        if not os.path.exists(path):
            rel = os.path.relpath(path, root).replace(os.sep, "/")
            print(f"{file} {name}: {rel} (missing)")
            missing = 1
sys.exit(missing)
' "$file" || status=1
done
exit $status
