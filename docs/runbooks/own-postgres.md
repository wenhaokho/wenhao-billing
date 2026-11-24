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

The billing database on the shared Postgres is `000_wenhao_billing` (from the
old `DATABASE_URL`). The dump is a Coolify database backup, so a copy also lands
in R2 ("Cloudflare R2 Wenhao", bucket `wenhao`).

1. In Coolify, **stop** the `#000 - Wenhao - Billing` resource (no more writes).
2. Run the one-off backup on the shared Postgres: backup schedule
   `lmjpm073kp5dpiuxbginnhbs` (disabled, `databases_to_backup=000_wenhao_billing`,
   `dump_all=false`, saved to R2). In the Coolify UI: the `postgresql` database,
   Backups, that schedule, **Backup Now**. Through the Coolify MCP:
   `manage_backup update` with `data.backup_now=true`.
3. Wait for its execution to show `success`, and note the file name
   (`.../pg-dump-000_wenhao_billing-<ts>-<id>.dmp`, a `pg_dump -Fc` file).
4. Record the row counts the restore is checked against:

```bash
# host
SHARED=$(docker ps --format '{{.Names}}' | grep -E '^lctxyyam2v2sxynlf0qkbvbm')
SHARED_DB=000_wenhao_billing
[ "$(printf '%s\n' "$SHARED" | grep -c .)" = 1 ] || echo "STOP: shared Postgres is not exactly one container"
# The newest billing dump Coolify wrote on the host.
F=$(ls -t /data/coolify/backups/databases/*/postgresql-lctxyyam2v2sxynlf0qkbvbm/pg-dump-000_wenhao_billing-*.dmp | head -n 1)
ls -l "$F"
docker exec "$SHARED" psql -U postgres -d "$SHARED_DB" -tA \
  -c "SELECT 'invoices', count(*) FROM invoices UNION ALL SELECT 'payments', count(*) FROM payments UNION ALL SELECT 'journal_entries', count(*) FROM journal_entries UNION ALL SELECT 'users', count(*) FROM users" \
  | tee ~/billing-counts-before.txt
```

Expected: `$F` is the dump from step 3 (check the name and the time), and four
counts. The counts are only comparable if the app stayed stopped since the dump.
If the host copy is gone, download that file from R2 to the host and set `F`
to it.

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
restore, drop the `000_wenhao_billing` database (and its role) from the shared Postgres
and delete the `DATABASE_URL` variable from the Coolify resource.
