# Task 72: Weekly FX synchronization

The managed scheduler runs the existing provider configuration every Monday at 05:00 UTC. A shared transactional batch is used by the manual endpoint and the scheduled path. The scheduled path holds Python's CRC32 `fx-rates-weekly-sync` session advisory lock and executes the transaction on that same connection, while the batch obtains the existing transaction-scoped manual-writer lock. Shutdown cancels the job; unlock uses a bounded cleanup and discards the connection if unlock cannot be verified. Scheduler results use fixed job and status fields without provider or database error text. The public Frankfurter default needs no credentials, and explicit provider settings are shared with the manual endpoint. The generic scheduler disable path prevents job execution.

Evidence (local fake provider, isolated PostgreSQL databases):

- `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` passed.
- `TestNextFXSyncUTCWeekBoundary` passed for UTC boundaries and a non-UTC input.
- `TestWeeklyFXSyncPoolOneAndCancellation` passed for create, update, repeat and a one-connection pool.
- `TestWeeklyFXSyncPythonWorkerAndReplicaLock` passed comparing the actual Python worker's initial/repeated result and stored row, including identity and missing pair. A separate Python process holding the Python scheduler key caused the Go worker to skip without calling the provider.
- `TestWeeklyFXSyncRollbackAndSharedWriterLock` passed with an injected second-row write failure and shared lock contention. `TestFXSyncCoordinatesDistinctAdminWrites` passed with scheduled sync competing with manual sync, create, and delete.
- `TestWeeklyFXSyncProviderFailureAndCancellation` passed for HTTP failure, invalid rate, timeout, cancellation and no partial write. Existing `TestFXSyncPostgres` covers malformed responses and manual-path differential behavior; `TestFXProviderTimeoutAndConfiguration` covers timeout and configuration.
- Restored mutation guards: moving the schedule to 06:00 failed the schedule test; bypassing the job lock failed the separate-process contention assertion; forcing unchanged rows to update failed the idempotence assertion; committing each provider row separately failed the rollback assertion. Changes were restored after each test.

Limitations: the differential test exercises Python's worker for initial and repeated runs, not every failure or update scenario. Log secrecy relies on the scheduler's fixed error label and existing generic logging tests; no live provider or production service was contacted. Preliminary full-suite runs were interrupted after test additions and to avoid overlapping them; final full-suite result is recorded below.

Final verification: `PATH=/tmp/codex-billing-py312/bin:$PATH PYTHONPATH=/home/wenhao/.codex/worktrees/billing-go-replacement/wenhao-billing/backend PGTESTDB_URL=postgres://billing:billing@127.0.0.1:52677/billing_alembic_ref?sslmode=disable go test -count=1 ./...` passed all packages; platform completed in 317.011 seconds and migrations in 23.586 seconds.

## Review follow-up: bounded writer-lock wait

The scheduler now creates a one-minute deadline for the entire run, including provider requests and the shared FX writer lock. Timeout cancels the PostgreSQL operation; rollback and advisory unlock have bounded cleanup. A job-lock collision returns a distinct sentinel and logs `status=skipped`, while deadline, error, and success use fixed `timeout`, `failed`, and `finished` statuses without error details. Parent cancellation suppresses status logging.

`TestWeeklyFXBlockedWriterDeadlineReleasesPool` holds the manual-writer lock on a separate pool and verifies deadline termination, no partial writes, and reuse of a max-one connection pool. `TestWeeklyFXJobStatusAndDeadlineRedacted` checks each status, an injected secret error, and deadline enforcement. The separate-process Python job-lock test verifies skipped contention. Focused tests passed. Removing the per-job timeout caused the deadline test to hit its two-second test timeout; the mutation was restored.

Mixed Python and Go FX writers are unsupported without a deployment fence: Python manual writers do not take the Go transaction-scoped FX writer lock. The shared Python-compatible scheduler job lock alone does not serialize those manual writers.

Review follow-up final verification: the requested full `go test -count=1 ./...` command passed all packages after the restored mutation; platform took 348.979 seconds and migrations 66.431 seconds.
