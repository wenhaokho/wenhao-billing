# Task 7 report

Implemented an opt-in `api set-password <email>` command. It rejects extra arguments and non-terminal stdin, reads both entries without echo, validates 6–128 Unicode characters and equality, uses gokit Argon2id defaults, and updates the locked existing user while clearing reset fields in one transaction. Errors from the database and terminal are replaced with generic errors. SQL source and sqlc v1.29.0 configuration and generated pgx code are under `backend-go`; the generation command is in `queries/README.md`. No Python code or production data was touched.

## Verification

- First test-first run: `cd backend-go && go test ./cmd/api -run 'TestPasswordInputValidation|TestCommandRejects'` failed at compilation because the command and validator did not exist yet. The initial integration run also failed before implementation because dependencies needed updating; no claim is made that those failures demonstrated runtime behavior.
- `cd backend-go && go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate` succeeded.
- `cd backend-go && PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./cmd/api -run TestSetPasswordDatabase` passed using a pgtestdb-created database. It checks Argon2id verification, unknown user, reset fields, unchanged second user and rollback after a database trigger rejects the update. A single-connection pool makes a missing rollback observable.
- `cd backend-go && go test ./cmd/api -run 'TestPasswordInputValidation|TestCommandRejects|TestPasswordErrors|TestPasswordCommandRedacts'` passed. Validation happens before opening a database, so invalid input cannot change rows.
- `cd backend-go && PATH=/tmp/billing-task6-venv/bin:$PATH PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./...` passed before the last test changes; a final run is recorded below.

## Mutation evidence

Temporary mutations were reverted after each test. Commands ran `go test -count=1 ./cmd/api -run <test>` with the same isolated `PGTESTDB_URL` where needed.

- Replacing the length condition with `n < 0` failed `TestPasswordInputValidation/short` and `/long`; replacing the comparison with `false` failed `/mismatch`.
- Permitting extra arguments failed `TestCommandRejectsExtraArgumentsAndNonterminal` because the expected usage error was absent. Removing the terminal guard failed the same test because the expected terminal error was absent.
- Removing either reset-token or expiration clearing failed `TestSetPasswordDatabase` with `reset fields or profile incorrect`. Expanding the update to include the second user failed with `other user changed`.
- Returning success for an unknown user failed `TestSetPasswordDatabase` with `unknown email accepted`.
- Adding the supplied password to the mismatch error failed `TestPasswordErrorsDoNotRevealSecrets` with `password leaked`.
- Removing the deferred transaction rollback caused `go test -count=1 -timeout 5s ./cmd/api -run TestSetPasswordDatabase` to time out: the failed update retained the only pool connection, preventing the subsequent read and cleanup.

Limitations: the rollback mutation produces a timeout rather than a concise assertion because pgxpool cleanup waits for the leaked connection. Mutation trials do not individually cover every error branch, the hashing failure branch, or the connection-error redaction branch. The terminal behavior is tested through the private command helper rather than a pseudo-terminal. The tests use synthetic passwords and an isolated database only; no command was run against production.

Final full-suite run after the single-connection rollback test: `PATH=/tmp/billing-task6-venv/bin:$PATH PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./...` passed (`cmd/api`, `internal/platform`, `internal/withholding`, and `migrations`; `internal/dbquery` has no test files).

## Review round 1: executable and terminal coverage

Added tests that build and execute the real `api` entrypoint. A pipe test checks that noninteractive input is rejected; another checks that an extra password argument is rejected without appearing in command output. Pseudo-terminal tests inspect `ECHO` while each password is read, verify no supplied value appears in terminal output, exercise rejected short and mismatched inputs and an unknown email, and successfully reset an existing account and verify the stored Argon2id hash. All user rows and all columns are snapshotted before and after each rejected operation. After success, the other user's entire row and every non-password/reset field of the target user are compared. These tests use a pgtestdb-created database and synthetic values only. This supersedes the earlier pseudo-terminal limitation.

Focused command: `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./cmd/api -run 'TestActualCLI|TestSetPasswordDatabase'` passed.

The following temporary production-code mutations were each run against the new real-entrypoint tests, observed failing, and reverted before the green run:

- Force the terminal check true: `TestActualCLIPipeAndArguments/pipe` failed because the terminal rejection was absent.
- Accept a third argument: `TestActualCLIPipeAndArguments/argument` failed because the usage rejection was absent.
- Replace `term.ReadPassword` with ordinary stdin reading: `TestSetPasswordDatabase` failed with `password input echoes`.
- Print the entered password after reading it: `TestSetPasswordDatabase` failed with `password appeared in terminal output`.

Each mutation was run with `go test -count=1 -timeout 30s ./cmd/api -run <test>` and the isolated `PGTESTDB_URL`. The new coverage was added to existing behavior, so its red evidence comes from these deliberate guard mutations, not from a pre-implementation failure.

Final review-round full suite: `PATH=/tmp/billing-task6-venv/bin:$PATH PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test -count=1 ./...` passed, including migrations.
