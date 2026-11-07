# Business scoping decision

**Status: Selected — explicit `business_id` ownership for the single-tenant deployment.** The user chose to add `business_id` to the schema and scope all Go queries on 2026-11-06. No production changes have been made as part of this decision.

## Current behavior

The PRD requires a `business_id` on every record and scoping for all SELECT, INSERT, and UPDATE queries. AGENTS.md repeats the query requirement and describes the system as single-tenant. [PRD](../../prd.md) · [AGENTS.md](../../AGENTS.md)

Both backends connect to one configured database. The 22 application tables shared by Alembic and the Goose baseline and the four Go-only worker tables have no `business_id` column. The only existing company association is `payment_account.business_profile_id`. Both backends use profile ID 1, which the schema seeds and enforces as a singleton. Sessions identify users and carry no tenant selection. [Alembic profile migration](../../backend/alembic/versions/0010_business_profile.py) · [Go baseline](../../backend-go/migrations/00001_baseline.sql) · [Go profile queries](../../backend-go/queries/business_profile.sql)

Python trial balance joins chart of accounts, journal lines, and journal entries by entity ID and filters by posting date. The Go replacement has no trial-balance route pending the scoping decision. [Python trial balance](../../backend/app/api/v1/routers/accounting.py#L241) · [Go handoff](HANDOFF.md)

## Options

| Option | Required changes | Effect on cutover |
| --- | --- | --- |
| One database per company | Amend the PRD and AGENTS.md to make the database the isolation boundary. Give each deployment its own database, credentials, and integration configuration. | No ownership backfill or query rewrite. Trial balance can aggregate within the deployment's database. |
| Explicit ownership for one company per deployment | After confirming that the existing data belongs to one company, seed or reuse a stable business identity and backfill all owned rows. Enforce ownership with non-null keys and foreign keys that prevent cross-business references. Derive scope from trusted runtime configuration and apply it to reads, writes, joins, and job processing. | Requires schema and query changes. Update Python as well if it must run against the migrated database during rollback. |

For explicit ownership, define the policy for FX rates and OAuth records and distinguish application records from migration metadata. Include line items, journal references, reconciliation, payment deduplication, and Go worker queues in the review. Go has 135 named sqlc queries plus inline SQL. Python's API, ORM relationships, MCP tools, scripts, and jobs also need review wherever they read or write owned records. Keep ownership out of caller-supplied request fields. If a legacy database contains data from multiple companies, use a reviewed ownership mapping for the backfill.

## Migration and rollback

Go startup requires the exact Goose v7 schema and migration history. Alembic adoption is disabled, and the current entry point cannot upgrade existing Goose databases from versions 1 through 6. Both options require a supported path for moving legacy data into an admitted Go schema. [Migration admission](../../backend-go/migrations/README.md)

Rehearse either a fenced in-place migration with a tested restore or an offline copy into a separately provisioned Go database. Stop or fence writes and external job execution for the final transfer. Check row counts, ownership if applicable, ledger totals, balances, constraints, and sequence positions before switching traffic. Rehearse Python rollback and account for Go writes made after the switch before reopening Python traffic. Only one backend should process scheduled jobs and external deliveries during the transition.

Implement explicit ownership for the existing single-tenant project. The migration and rollback plan must preserve the existing data and account for Python compatibility; production cutover remains separately gated.
