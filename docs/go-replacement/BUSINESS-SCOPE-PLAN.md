# Business ownership implementation plan

> For agentic workers: execute this plan task by task using the user-invoked `superpowers:subagent-driven-development`. The user has authorized agents and implementation in the isolated development worktree. No additional design approval is needed to start. Do not deploy, change production, or commit unless separately instructed.

**Goal:** Add durable `business_id` ownership to every Go application record and require trusted business scope for every application query and write.

**Architecture:** One configured business per running API and worker deployment. PostgreSQL owns the persistent business identity; configuration selects that identity, and explicit SQL predicates and composite foreign keys enforce ownership. Preserve existing entity identifiers and financial values. Migrate legacy data through an isolated copy, keeping Python and its database available as the rollback reference.

**Tech stack:** Go 1.27, PostgreSQL 16, pgx, sqlc v1.29.0, Goose. The Python/Alembic reference remains unchanged.

**Spec:** The user's explicit ownership decision, [SCOPING-DECISION.md](SCOPING-DECISION.md), [HANDOFF.md](HANDOFF.md), [../../prd.md](../../prd.md), and [../../AGENTS.md](../../AGENTS.md). The decision note records explicit ownership as Selected on 2026-11-06.

## Constraints

- Work only on development databases. Preserve source databases, uploads and Docker volumes. Reuse the existing Compose project; inspect labels and mounts before any cleanup.
- Keep all historical migrations immutable. Add Goose migration 8. Keep `AdoptAlembic` disabled and keep schema changes out of API startup.
- Preserve `DECIMAL(19,4)`, currency rejection, safe-stop reconciliation, manually reversible automation, and current transaction/lock ordering.
- Scope DELETE, locks, existence checks, counts, joins, aggregates and conflict handling as well as SELECT/INSERT/UPDATE.
- No implicit business default on application columns. No request body, header, cookie field, URL parameter, email content, or webhook payload may choose the deployment's scope.
- Keep the Python implementation and Alembic chain intact. Python does not gain permission to write the migrated Go database through this plan.
- Scope work does not resolve the separately recorded payment-ledger, credentials, integration, and API-parity gaps. Those remain cutover gates.

## Review focus

1. A valid foreign identifier from business B must behave like an absent identifier to an API configured for A; no existence leak or side effect.
2. Conflicting natural keys and retry keys in B must neither block A nor cause an upsert to mutate B.
3. An outbox snapshot, stale lease or signed session created for B must not authorize A to send mail, change hosting or authenticate.
4. Migration must preserve historical rows with incomplete references without silently deleting, relabeling or manufacturing financial records.
5. Restart, failed migration, backup restore and offline import must preserve the selected business identity and committed post-cutover writes.

## Decisions and schema contract

### Stable identity

Create `businesses (business_id uuid PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now())`. The first migration generates one UUID with PostgreSQL's built-in `gen_random_uuid()`, stores it, then uses that same row to backfill the existing single-business dataset. Generate once inside the migration transaction. Do not derive identity from company name, email, profile integer 1, a request, or a newly generated startup value.

Fresh initialization exposes the persisted ID through an explicit read-only bootstrap command. Deployment configuration must set `BUSINESS_ID` to that ID before the API starts. Missing, malformed, nil or nonexistent IDs fail before opening a listener or starting a worker. A restart never creates a business. Restore preserves the ID. An offline import uses a saved import manifest's UUID, so repeating the import into a new disposable target preserves identity; the import tool replaces only the newly created target's bootstrap identity before loading data. It must prove that the destination contains only its own known bootstrap rows first.

Use `internal/business.ID` with a private UUID field and `Parse(string) (ID, error)`, `String() string`, `UUID() pgtype.UUID`, and `Validate() error`. Reject the zero value at public service/worker construction boundaries. Pass this value explicitly rather than reading an environment variable inside services. Add `BusinessID business.ID` to `platform.SessionConfig`; existing handler construction can propagate the scope without a parallel global singleton.

The deployment still operates one company. A schema that can hold two business fixtures is useful for proving isolation; this is not a tenant-switching UI or a multi-tenant authentication redesign. Runtime configuration is trusted deployment control.

### Owned and global records

All 26 current application tables are owned. Add `business_id uuid NOT NULL REFERENCES businesses(business_id)` to each, without a persistent default:

| Group | Tables |
| --- | --- |
| Identity and settings | `users`, `business_profile`, `payment_account`, `oauth_clients`, `oauth_refresh_tokens` |
| Master data | `customers`, `vendors`, `projects`, `items`, `chart_of_accounts`, `fx_rates`, `cloudflare_targets` |
| Documents | `invoices`, `invoice_line_items`, `quotations`, `quotation_line_items`, `bills`, `bill_line_items` |
| Accounting and reconciliation | `payments`, `journal_entries`, `journal_lines`, `reconciliation_log` |
| Durable work | `recurring_draft_events`, `recurring_draft_deliveries`, `hosting_restore_intents`, `hosting_operation_leases` |

FX rates are owned because users can create/delete rates and automated sync can overwrite them. A rate observed from a public provider is still a business's accounting input. OAuth clients and refresh tokens are owned because registration, revocation and user grants belong to this deployment. External provider configuration remains deployment configuration and is bound to its configured business.

Only schema/migration metadata, catalog inspection, PostgreSQL health/time queries and migration-level locks are global. `businesses` is the ownership root; application lookup of it is by configured `business_id`. `goose_db_version` is global infrastructure metadata. `alembic_version` exists only on the source/reference database and remains forbidden by Go admission.

### Keys, profiles and relationships

Preserve existing primary keys and ID sequences. Add `UNIQUE (business_id, <existing primary-key columns>)` as needed for referenced rows. Replace every existing application foreign key with its composite equivalent, preserving delete action and nullability. Optional references use normal `MATCH SIMPLE`; `business_id` remains non-null, and a null entity ID still means no reference. Add business-leading indexes for joins, filters, ready queues and lock candidates.

Remove `ck_business_profile_singleton`, retain the original profile ID 1 for the legacy company, and add `UNIQUE (business_id)` to `business_profile`. Every profile query selects by business scope; it must never assume ID 1. Keep `payment_account.business_profile_id` for compatibility and enforce `(business_id, business_profile_id) -> business_profile(business_id, id)`. Profile creation uses the existing sequence; verify its next value after preserving ID 1.

The composite foreign-key inventory includes every existing edge in `00001_baseline.sql`, plus the worker migrations. In particular:

- Document lines to their document, item and any expense account; documents to customer/vendor/project; the invoice/quotation conversion cycle.
- Items to revenue/expense accounts; Cloudflare targets to items; payments to invoice/customer; projects to customer.
- Journal lines to entry/account; entries to reversing entry; reconciliation logs to payment/actor.
- Payment accounts to profile; hosting intents to invoice/item; operation leases to item; recurring deliveries to their event.

Do not invent hard foreign keys for historical outbox snapshots whose parent/user may legitimately have been deleted. `recurring_draft_events.invoice_id` and `recurring_draft_deliveries.user_id` currently lack those FKs. Keep those snapshots owned and require a scoped live invoice/user lookup before sending; cancel unavailable recipients/events under existing semantics. Do not cascade-delete delivery evidence just to make a migration pass.

`oauth_refresh_tokens.user_id` is currently text, with no FK. Inventory actual values during import. Add a same-business user relationship only after proving every value is a canonical user UUID, or preserve the text representation and enforce a scoped user lookup during refresh. Unknown or orphan tokens are rejected/revoked by the authentication path, with a migration report; never map them to another user. Token cleanup must itself be scoped. OAuth implementation remains a parity task if no Go route exists yet.

`journal_entries(source_type, source_id)` is polymorphic and has no source FK. Keep historical source values unchanged. For new payment postings, verify the payment and invoice through scoped, locked lookups inside the posting transaction; reversal reads the original by scope and preserves its source. All source lookups include scope. Any future source type must implement its own scoped validation. Existing `OTHER` and unknown historical entries remain reportable; migration must not fabricate source entities or silently relabel them. A universal polymorphic FK is outside this slice.

Replace natural-key uniqueness with business-prefixed uniqueness: account `code`, item `sku`, project `code` including both its duplicate unique index and constraint, quotation/invoice numbers, user `email`, FX currency-pair/date, payment intake-source/external-reference, and profile payment-account currency/default indexes. Preserve NULL semantics. Update every `ON CONFLICT` target together with its index. Password-reset token lookup and uniqueness become business-prefixed too. OAuth client IDs/JTIs and existing entity UUID primary keys may remain globally unique identifiers; their resolution must still be scoped.

Prefix worker uniqueness/partial indexes with business where they encode logical exclusivity, including hosting's active lease per item. Global entity primary keys can remain unique because preserving identifiers is intentional. Do not add extra idempotency rules for unrelated accounting work in this change.

### SQL contract

Every named sqlc statement touching an owned table takes `sqlc.arg(business_id)::uuid`. Root reads and mutations filter that column. Every owned join additionally equates the child and parent business IDs. INSERT explicitly names `business_id`; INSERT SELECT scopes its source and target. Upserts use a business-aware conflict key and never update ownership. `jsonb_populate_record` cannot write ownership from request JSON. Explicit response DTOs must prevent new DB columns leaking into existing API contracts.

Services carry `business.ID` through transactions, savepoints, FX conversion, ledger posting, PDFs, receipts, email, reporting and candidate matching. Worker scans, claim CTEs, lease renewal, cancellation, completion and stale-result repair all require it. Namespace business-level advisory locks by business and job; migration-wide locks stay global. Hash collisions may serialize unrelated work but must never weaken exclusion within one business. Preserve current entity lock ordering.

Treat signed sessions as business-bound. Include the configured business in newly signed payloads, compare it before user lookup, then load the user with both IDs. Reject old unbound sessions. Login, forgot/reset-password, invitations, administrator checks and password CLI lookups use configured scope. Keep reset enumeration responses unchanged. Future OAuth access/refresh/confirmation tokens must bind and validate the same business. A token claim cannot override configuration.

## Implementation tasks

### Task 1: Freeze the ownership inventory and add validated identity

Files: create `backend-go/internal/business/id.go`, `id_test.go`, `backend-go/internal/businessscope/manifest.go`, `manifest_test.go`; maintain the selected decision in `docs/go-replacement/SCOPING-DECISION.md` and update this plan's checklist.

Interfaces: `business.Parse`, `ID.String`, `ID.UUID`, `ID.Validate` as defined above. The manifest lists the 26 owned tables, current PKs, foreign keys, natural unique keys and permitted global statements. It is shared by migration tests and the SQL coverage check; it is not a dynamic runtime SQL generator.

- [x] Add failing ID tests for valid UUID, empty, malformed, all-zero and invalid zero-value IDs, plus stable parse/string round trip.
- [x] Implement identity and a manifest test that compares its owned-table set to the actual migration catalog, allowing only `businesses` and migration metadata as exceptions once v8 exists.
- [x] Inventory sqlc named queries and inline `Exec`, `Query`, `QueryRow`, `CopyFrom`, batch SQL, embedded SQL and commands. Record query location, read/write tables, scope source and global exception justification in `docs/go-replacement/BUSINESS-SCOPE-INVENTORY.md`.
- [x] Run `go test ./internal/business ./internal/businessscope`. Keep the runtime schema at v7 in this independently testable first slice.

### Task 2: Add and test the v8 schema without enabling old runtime queries

Files: create `backend-go/migrations/00008_business_scope.sql`, `business_scope_test.go`; expose migration 8 through a separate test migration filesystem until task 8 enables it in runtime `Files`; update `migrations/seeds_test.go` where necessary. Do not append migration 8 to the production `Files` filesystem while `MigrateFresh` still expects v7.

Interfaces: migration 8 transforms exact v7 into the schema contract above. It has no destructive Down migration; reversal is restore or export into a fresh legacy-schema target.

- [x] Test migration of populated v7 fixtures containing all 26 tables, null optional references, cyclic quote/invoice links, deleted-user outbox snapshots and unknown journal source types. Assert unchanged original values/IDs, complete ownership and unchanged totals.
- [x] Implement one transaction: create identity; add nullable ownership columns; backfill the one verified business; replace unique/FK/index definitions; validate constraints; set NOT NULL; verify no ownership defaults remain. Inventory any missing relationship before adding a new FK. Fail on invalid legacy references rather than deleting rows.
- [x] Add a second business fixture and test each existing FK edge for cross-business rejection and valid same-business/null behavior. Test duplicate natural keys across businesses and conflicts inside one business.
- [x] Verify migration failure leaves v7 and its data intact; successful retry/recheck preserves the UUID. Recompute the v8 catalog fingerprint only on a freshly generated PostgreSQL 16 database.
- [x] Run focused migration tests. Keep the live API admission at v7 until queries, writers and fixtures are converted. Do not run the old API against v8.

### Task 3: Convert generated queries and introduce explicit scope at call sites

Files: all `backend-go/queries/*.sql`, generated `internal/dbquery/*`, `internal/platform/auth.go`, `server.go`, `cmd/api/main.go`, `cmd/api/password.go` if present, and the inventory.

Interfaces: `SessionConfig.BusinessID business.ID`; generated query parameter structures carry `BusinessID pgtype.UUID`. All exported worker/service entry points that access records accept `scope business.ID` directly after their database/transaction argument.

- [ ] Add query isolation tests for listing, detail, lock, update, delete, insert-select, counts, pagination and upserts. Each test contains real business A and B rows.
- [ ] Update every named SQL query and regenerate using `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate`. Preserve historical baseline schema parsing and include migration 8 in sqlc's schema inputs.
- [ ] Propagate required scope through all compiling call sites. Never fix compilation by injecting a magic UUID/default. Do not change externally visible request/response shapes.
- [ ] Update profile lookup/creation and scoped payment-account replacement transaction. Test a valid A profile whose numeric ID is not 1.
- [ ] Compile all packages with `go test ./... -run '^$'` using the established test prerequisites. This task and dependent conversions are a development-only integration stage; startup is not released during it.

### Task 4: Bind authentication and administrative commands

Files: `internal/platform/auth.go`, `password_recovery.go`, `users*.go`, `cmd/api/main.go`, `cmd/api/password*.go`, session implementation and related tests discovered in the inventory.

- [ ] Test same email in A/B, a B user ID in A's signed session, an old unbound session, reset tokens in the other business, and administrator actions with foreign IDs.
- [ ] Bind sessions to configured business and scope login, user reload, recovery, invitations and administrative writes. Require validated scope in the password command; do not reveal whether a user exists in another business.
- [ ] Audit currently implemented OAuth/MCP code. Scope its records/tokens if present; if absent, record explicit acceptance requirements for its later implementation rather than claiming coverage.
- [ ] Run auth, users, recovery and password command tests. Assert the session signer never accepts caller-selected business scope.

### Task 5: Scope master data, documents and financial services

Files: `internal/platform` customer/vendor/item/project/account/profile, invoice/bill/quotation/payment, PDF/email/receipt handlers; `internal/ledger/posting.go`; `internal/fx/fx.go`; `internal/fxrevaluation` DB callers; affected tests.

- [ ] Add foreign-ID tests for every mutation family, including line item references, account IDs, project/customer checks, quotation conversion and replacement of child rows. Assert zero database mutation and zero mail/provider calls on rejection.
- [ ] Convert all inline SQL and lock/recheck paths. Include document numbering queries, source-quote checks, line counts, deletion guards, receipt data and indirect PDF/customer/profile lookups.
- [ ] Pass scope into `ledger.PostPayment`, `ledger.Reverse` and `fx.Convert`. Verify referenced source ownership in the transaction; scope account-code and FX selection. Test distinct FX rates and account IDs under the same natural keys in A/B, and cross-business source/reversal rejection.
- [ ] Preserve existing posting behavior and exact decimal results. Run focused platform and ledger/FX integration tests; compare Python contracts using separate unchanged Python fixtures.

### Task 6: Scope durable work and external effects

Files: `internal/platform/recurring*.go`, `usage*.go`, `hosting*.go`, `fx_sync.go`, `cmd/api/main.go`, worker tests, `DELIVERY_WORKERS.md`.

- [ ] Test scans that see both businesses but only create A work; claims that leave B leases untouched; and stale B lease tokens that cannot renew, cancel, complete or repair A work.
- [ ] Thread scope through all worker entry points and inline SQL. Include recurring recipient snapshots, batch IDs, item lock planning, hosting target fingerprints, per-item operation leases and FX provider upserts.
- [ ] Scope live recipient/user/invoice and hosting-target revalidation immediately before external effects. Use fake mail and Cloudflare providers; assert no call for foreign or unavailable owned records.
- [ ] Namespace business job locks and preserve at-least-once delivery and manual-review behavior. Test A and B can run independently while duplicate A workers remain excluded.
- [ ] Run worker race/lease tests and focused repetitions only for timing-sensitive failures. Never contact live providers.

### Task 7: Convert fixtures and add an executable coverage gate

Files: Go integration fixtures across `internal` and `migrations`; create `backend-go/internal/businessscope/sql_scope_test.go`; update the scope inventory.

- [ ] Keep Python fixtures on Alembic and Go fixtures on v8. Use explicit Go-owned fixture builders; do not install a DB default or broad string replacement that hides omitted `business_id` writes. Where differential tests share SQL, produce separate explicit seed variants and compare business outputs excluding the new internal ownership field.
- [ ] Add a catalog test requiring NOT NULL ownership and business FK on all owned tables, required composite FK edges, and no unprefixed natural unique keys that couple businesses unintentionally.
- [ ] Add a SQL call-site coverage gate that rejects new unclassified application SQL and requires explicit global exception annotations. Use parsing or exact inventory of statements rather than claiming a regex proves semantic isolation. Exercise dynamic builders and query fragments in integration tests.
- [ ] Build an endpoint/worker matrix covering A/B read, mutate, reference, upsert and effect behavior. Every applicable inventory entry must link to evidence; zero-row assertions alone do not prove external effects were suppressed.
- [ ] Run `go test -count=1 ./...` with real PostgreSQL 16 and the isolated Alembic reference. Verify sqlc regeneration is clean.

### Task 8: Admit only the exact scoped schema at runtime

Files: `migrations/runtime.go`, `admission.go`, `runtime_test.go`, `README.md`; `cmd/api/main.go` and new startup scope tests; deployment environment examples.

- [ ] Test v7 rejection, exact v8 acceptance, forged/partial/future Goose history rejection, unexpected catalog object rejection, and missing/invalid/nonexistent BUSINESS_ID rejection before listener/worker startup.
- [ ] Embed migration 8 for fresh databases; advance the expected exact history to versions 0 through 8 and the reviewed PostgreSQL 16 fingerprint. Keep separate names for historical v7 and current v8 signatures.
- [ ] Add `migrations.VerifyBusiness(ctx context.Context, raw string, scope business.ID) error` as a read-only identity/profile check after `VerifyCurrent`. Require exactly one profile for the configured identity and the seeded account prerequisites already required by runtime workflows. Never repair missing identity during startup.
- [ ] Add an explicit read-only bootstrap command that prints the business ID of a newly provisioned single-business database, failing on ambiguity. Keep normal startup independent of that command.
- [ ] Verify `MigrateFresh` repeats safely on exact v8 and rejects legacy/unknown schemas. Document that the API still does no migration and runtime credentials cannot perform DDL.

### Task 9: Provide a fenced development upgrade and legacy-copy rehearsal

Files: create `backend-go/cmd/dbscope/main.go`, focused migration/import helper files under `backend-go/internal/businessscope/`, integration tests, and `docs/go-replacement/BUSINESS-SCOPE-MIGRATION.md`.

Interfaces: administrative commands `inspect-source`, `upgrade-private-copy`, `import-private-copy`, `verify-copy`. Inputs are explicit source/destination URLs and a saved manifest; there is no automatic production discovery and no startup adoption. Define command flags and dry-run reports before implementing transfer.

- [ ] Implement source inspection that records exact Alembic 0028 or exact Goose v7 metadata/fingerprint, row counts, original keys/sequences, nulls/orphans, ledger debit/credit totals by currency/base currency, balances/statuses and pending external work. Reject mixed-company evidence or an unknown schema. Record the declared legacy company and stable destination UUID in the manifest.
- [ ] For an existing v7 database, restore its snapshot into a separately provisioned private PostgreSQL 16 instance, verify exact v7, apply migration 8, then verify exact v8 and the business/data invariants. No in-place upgrade of the live source is necessary. Versions 1 through 6 remain explicitly rejected until exact per-version fingerprints and transformations are separately tested; never stamp them to v7.
- [ ] Make the private migration boundary enforceable: a dedicated temporary server with no TCP listener, an owner-only Unix socket/data directory, no untrusted roles/processes, and connections controlled by the migration process. Verify those conditions; fail if they cannot be established. A flag asserting quiescence, advisory lock or one empty `pg_stat_activity` snapshot is insufficient. Concurrent independent DDL tests must demonstrate the boundary, not merely its cooperative lock.
- [ ] For Alembic, import business data into a fresh v8 target with explicit column mappings and manifest ownership. Keep Alembic metadata solely in the source. Replace only verified fresh target seed rows, preserve source IDs and sequence positions, then load dependency-ordered tables. For the quote/invoice cycle, load nullable cross-links last in the same transaction. Never disable integrity checks on arbitrary data.
- [ ] Preserve OAuth records as owned historical data, but invalidate incompatible active sessions/tokens at cutover. Preserve credentials byte-for-byte for audit/export; bcrypt compatibility or a tested reset rollout is a separate required cutover task.
- [ ] Test interrupted import rollback and clean retry into a new disposable target; verify no duplicates and the same manifest UUID. Inspect and compare every owned table, financial totals, constraints, sequences, identity, outbox state and files needed for documents. Keep providers and schedulers disabled during transfer.

### Task 10: Rehearse Python rollback and close the acceptance evidence

Files: `BUSINESS-SCOPE-MIGRATION.md`, `HANDOFF.md`, scope inventory, transfer/export tests and existing Go replacement acceptance records.

- [ ] Rehearse the zero-write rollback by switching a development client back to the untouched Python source. Never attach Python to the v8 schema: absent ownership defaults deliberately make that unsafe writer fail.
- [ ] Rehearse rollback after representative Go writes by fencing Go, exporting its current business data, and importing the legacy-compatible columns into a fresh Alembic-head rollback target. Include newly created/deleted documents, payments, ledger entries/reversals, balances, user updates, sequence values and file assets. Compare full state; do not restore only the old snapshot and lose Go writes.
- [ ] Preserve Go-only outbox, lease and provider-result evidence in an immutable export and reconcile accepted/uncertain external effects before re-enabling Python jobs. Keep unresolved side effects fenced or in manual review. A database restore does not unsend an email or reverse a Cloudflare action.
- [ ] Explicitly test Go password hashes/new users against the Python rollback path. If Python cannot authenticate them, the cutover runbook requires a tested compatible verifier/reset procedure before release; scope completion alone cannot claim rollback readiness.
- [ ] Run the full Go suite once after final changes, then the source/target comparison and rollback rehearsal. Record exact commands, revisions, tested database types and unresolved gates. Update HANDOFF only with observed evidence.

## Completion and first slice

The first implementable slice is task 1: a validated business ID type, a complete ownership/query inventory, and a selected decision note. It is safe to implement immediately, independently testable, and leaves current v7 startup behavior intact. Task 2 then proves data-preserving schema conversion on disposable v7 databases before the runtime changes.

Do not call explicit scoping complete until all current Go SQL paths, including inline worker/ledger/FX SQL and administrative commands, are covered; the exact v8 admission and two-business integration matrix pass; and the isolated migration rehearsal preserves original data. An absent Go feature cannot be certified scoped until implemented. Production cutover additionally requires the outstanding Go parity, accounting, authentication and rollback gates in HANDOFF.

The main risks are silently hard-coded profile ID 1, globally unique natural keys and upsert targets, unscoped JSON/inline queries, worker completion paths after external effects, legacy outbox/OAuth references, changed generated `SELECT *` results, shared Python/Go test seeds, and mistaking an admission snapshot for an independent-DDL fence.
