# Move production to the stack's own Postgres

`docker-compose.coolify.yml` now runs its own `wenhao-billing-postgres`
(PostgreSQL 18, volume `wenhao-billing-postgres-data`) instead of the shared
Coolify Postgres (`postgresql`, uuid `lctxyyam2v2sxynlf0qkbvbm`, also 18), the
same way pintas-booking runs `pintas-postgres`. The API's `DATABASE_URL` is
built inside the compose file; the Coolify `DATABASE_URL` variable is no longer
read.

The first deploy of this compose starts an **empty** database: the API
migrates it and the site comes up with no data and no users. This runbook
copies the data from the shared Postgres into it. The shared database is not
changed at any step, so it stays the rollback.

Conventions are those of `go-cutover.md`: *host* blocks run in bash on the
Coolify host, never paste a secret into a command, and stop at any `STOP` line.

## 1. Freeze and dump the shared database (before the deploy)

1. In Coolify, **stop** the `#000 - Wenhao - Billing` resource (no more writes).
2. Dump the billing database from the shared Postgres:

```bash
# host
SHARED=$(docker ps --format '{{.Names}}' | grep -E '^lctxyyam2v2sxynlf0qkbvbm')
SHARED_DB=billing   # the database name in the old DATABASE_URL; check it in Coolify
[ "$(printf '%s\n' "$SHARED" | grep -c .)" = 1 ] || echo "STOP: shared Postgres is not exactly one container"
F=~/billing-$(date -u +%Y%m%dT%H%M%SZ).dump
docker exec "$SHARED" pg_dump -U postgres -d "$SHARED_DB" -Fc --no-owner --no-acl > "$F"
ls -l "$F"
docker exec "$SHARED" psql -U postgres -d "$SHARED_DB" -tA \
  -c "SELECT 'invoices', count(*) FROM invoices UNION ALL SELECT 'payments', count(*) FROM payments UNION ALL SELECT 'journal_entries', count(*) FROM journal_entries UNION ALL SELECT 'users', count(*) FROM users" \
  | tee ~/billing-counts-before.txt
```

Expected: a dump file well over a few kB and the four counts. If a table name
does not exist, drop it from the query; keep the rest.

## 2. Deploy

Merge the branch and deploy the resource in Coolify. Coolify generates
`SERVICE_PASSWORD_POSTGRES` on this first deploy. Wait for all three containers
to be healthy.

## 3. Restore into the stack's Postgres

Stop the API and web containers, replace the empty database with the dump,
start them again.

```bash
# host
RID=y6pnb32gqyo05cmqzoyvxe3a
API=$(docker ps --format '{{.Names}}' | grep -E "^wenhao-billing-api-$RID")
WEB=$(docker ps --format '{{.Names}}' | grep -E "^wenhao-billing-web-$RID")
PG=$(docker ps --format '{{.Names}}' | grep -E "^wenhao-billing-postgres-$RID")
for v in API WEB PG; do
  [ "$(printf '%s\n' "${!v}" | grep -c .)" = 1 ] || echo "STOP: $v is not exactly one container"
done

docker stop "$WEB" "$API"
docker exec "$PG" psql -U billing -d postgres -v ON_ERROR_STOP=1 \
  -c 'DROP DATABASE billing WITH (FORCE)' -c 'CREATE DATABASE billing OWNER billing'
docker exec -i "$PG" pg_restore -U billing -d billing --no-owner --no-acl --exit-on-error < "$F"
docker exec "$PG" psql -U billing -d billing -tA \
  -c "SELECT 'invoices', count(*) FROM invoices UNION ALL SELECT 'payments', count(*) FROM payments UNION ALL SELECT 'journal_entries', count(*) FROM journal_entries UNION ALL SELECT 'users', count(*) FROM users" \
  | diff ~/billing-counts-before.txt - && echo COUNTS_MATCH

docker start "$API"; sleep 15; docker start "$WEB"
docker logs "$API" 2>&1 | tail -n 20
```

Expected: `COUNTS_MATCH`, and the API log shows it started (goose finds the
migrations already applied). Then sign in at https://billing.wenhao.id and open
an invoice and the ledger.

If the restore or the counts fail: stop here, keep the resource stopped, and
use the rollback below. Do not start the API on a half-restored database.

## 4. Backups

Coolify's scheduled backups cover the shared Postgres only, not a database
inside a compose resource. Until a backup of `wenhao-billing-postgres` is in
place, this deployment has **no** backups. Set one up before relying on it.

## Rollback

Until the shared database is dropped: deploy the previous commit of
`docker-compose.coolify.yml` (the one reading `DATABASE_URL`). It reconnects to
the shared Postgres as it was at step 1. Anything written after step 3 exists
only in the stack's database; dump it the same way before rolling back if it
must be kept.

## Afterwards

Once the new database has run for a while and its backups are proven by a
restore, drop the `billing` database (and its role) from the shared Postgres
and delete the `DATABASE_URL` variable from the Coolify resource.
