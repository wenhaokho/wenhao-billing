# Task 9 report

Implemented `ledger.ValidateBalance([]ledger.Line) error`. `Line` holds the currency and raw/base debit and credit values as `decimal.Decimal`. The function sums without rounding, rejects a nonzero base net first, and checks raw net only when exactly one currency occurs. Errors include the applicable currency and net. It performs no persistence or HTTP work.

Tests in `backend-go/internal/ledger/balance_test.go` cover empty lines, a balanced single-currency entry, positive and negative base and raw nets at 0.0001 precision, a base-balanced multi-currency entry with raw imbalances, and a multi-currency base imbalance. Before implementation, the test command failed to build because the requested interface did not exist. After implementation, `go test ./internal/ledger` passed.

Mutation checks (each guard independently disabled, then restored):

- Base guard disabled: `go test ./internal/ledger -run 'TestValidateBalance/positive_base_net' -count=1` failed with `got <nil>, want error containing "base currency: net=0.0001"`.
- Raw guard disabled: `go test ./internal/ledger -run 'TestValidateBalance/positive_raw_net' -count=1` failed with `got <nil>, want error containing "USD: net=0.0001"`.

After restoration, `PATH=/tmp/billing-task6-venv/bin:$PATH PGTESTDB_URL=postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable go test ./...` passed all Go packages, including migrations.

Limitations: This is a pure invariant check; transaction posting integration and database persistence are outside this task. Decimal string formatting may omit trailing zeros while retaining the exact numeric net.
