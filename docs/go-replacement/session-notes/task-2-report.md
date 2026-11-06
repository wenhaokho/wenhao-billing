# Task 2 report

Added fresh-install reference rows directly to `backend-go/migrations/00001_baseline.sql` and real-Postgres comparison tests in `backend-go/migrations/seeds_test.go`. The test checks reference Alembic head `0028_business_npwp` before comparison. It compares all account fields and IDs, the IDR identity rate fields and ID, the complete business profile except its nondeterministic timestamp, and the empty payment-account table. The FX timestamp is excluded; reference migration date may differ, so the test checks the reference date is not in the future and the new database date equals its current date. Reference FX selection is limited to the migration's IDR/IDR `SEED` row because Python tests can add FX rates.

Reviewed all migration insert/update operations: revisions 0002, 0008, and 0027 seed 14 accounts in insertion order; 0006 seeds IDR/IDR; 0010 inserts profile ID 1. Revision 0022 copies nonempty old profile payment instructions, which are null on a fresh install, so it creates no payment account. Other updates backfill existing journal lines or customer rows and affect no rows on a clean install. There are no Go post-baseline migrations. The chart and FX sequences advance via default IDs; profile ID 1 is explicit, so the Go baseline advances its sequence to prevent reuse (unlike Alembic). Tests check all three next values exceed seeded IDs.

Commands from `backend-go` (the URL points only to the task's isolated reference):

```sh
PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_test?sslmode=disable' go test -count=1 ./migrations -run TestFreshBaselineSeedsMatchAlembic -v
```

Before implementation this failed with named `chart_of_accounts`, `identity_rate`, and `business_profile` seed mismatch assertions: Alembic contained rows and goose contained none. After implementation, removed the identity-rate INSERT temporarily and ran:

```sh
PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_test?sslmode=disable' go test -count=1 ./migrations -run 'TestFreshBaselineSeedsMatchAlembic/identity_rate$' -v
```

Output included `identity_rate seed mismatch: Alembic [{"rate_id":1,"from_currency":"IDR","to_currency":"IDR","rate":1.00000000,"source":"SEED"}]; goose []` and `FAIL`. Restored the INSERT and ran:

```sh
PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_test?sslmode=disable' go test -count=1 ./...
```

Output: `ok github.com/wenhaokho/wenhao-billing/backend/migrations 0.450s`.

Limitations: the isolated reference had already been migrated rather than being freshly migrated during this task; its revision and seed cardinalities were verified, and FX test data is excluded as described. The date assertion assumes the fresh migration and its check occur on the same database calendar date. No production operation was performed.

## Round 1 follow-up

The target FX query now reads **all** rows; the source query still selects only the identity seed so the test can also run against the supplied reference containing unrelated test FX rates. A separate clean reference was created on the task-only Postgres server and migrated through the actual complete Alembic chain. From the repository root, the commands were:

```sh
docker exec codex-billing-go-test-db createdb -U billing billing_task2_clean
cd backend
DATABASE_URL='postgresql+psycopg://billing:billing@127.0.0.1:57793/billing_task2_clean' /tmp/codex-billing-py312/bin/alembic upgrade head
```

The log started with `Running upgrade  -> 0001_initial` and ended with `Running upgrade 0027_withholding_tax -> 0028_business_npwp`. `/tmp/codex-billing-py312` is the existing task environment containing the backend dependencies; any equivalent environment installed from `backend/pyproject.toml` can run `alembic upgrade head`. Verified the clean reference with:

```sh
docker exec codex-billing-go-test-db psql -U billing -d billing_task2_clean -Atc "select (select version_num from alembic_version), (select count(*) from fx_rates), (select count(*) from chart_of_accounts), (select count(*) from business_profile), (select count(*) from payment_account)"
```

Output: `0028_business_npwp|1|14|1|0`. This reference was read only after migration; pgtestdb creates separate disposable goose targets.

Replaced the midnight-sensitive date check. Both databases now assert the identity date equals its own `created_at::date` and that the creation timestamp is not in the future. Both defaults use the migration transaction's database time; this permits cached pgtestdb templates and midnight crossings without requiring matching migration dates between databases.

Temporarily appended an extra USD/IDR seed INSERT to the Go baseline, then from `backend-go` ran:

```sh
PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./migrations -run 'TestFreshBaselineSeedsMatchAlembic/identity_rate$' -v
```

The named assertion failed as expected, showing one Alembic IDR row versus the Go IDR row plus `{"rate_id":2,"from_currency":"USD","to_currency":"IDR","rate":16000.00000000,"source":"SEED"}`. Restored the baseline and ran:

```sh
PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./...
```

Output: `ok github.com/wenhaokho/wenhao-billing/backend/migrations 0.673s`. The earlier limitation about an already-migrated reference and the midnight assumption is superseded by this follow-up. The clean disposable reference remains available as `billing_task2_clean` for subsequent verification.
