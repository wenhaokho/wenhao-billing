# Task 4 report

Implemented the pure withholding port in `backend-go/internal/withholding/withholding.go` and tests in `backend-go/internal/withholding/withholding_test.go`. No Python, shared kit, routes, or database code was changed.

The public API provides `Type` constants `None`, `PPH21`, `PPH23`; `Result` with `Total`, `Withholding`, and `Base`; `Normalize`, `Compute`, `RoundCurrency`, and `ExpectedCash`. Normalize applies defaults and validates the rate. Compute preserves Python's separate calculation behavior, including nil and explicit zero rates; callers should normalize before computing gross-up, since an unvalidated rate of 100 causes division by zero.

The tests port every Python case and cover both defaults, nil and explicit zero, negative and upper rate boundaries, fractional rates, negative rounding ties, other currencies, and booked withholding exceeding withholding. Gross-up checks that total minus withholding equals base.

Verification:

- Wrote tests first. `go test ./internal/withholding` failed to compile because the intended new API did not yet exist (undefined types and functions). After implementation it passed.
- Temporarily removed the unknown type guard. `go test ./internal/withholding -run TestNormalize -count=1` failed `TestNormalize/unknown`, reporting nil error instead of the expected unknown-type error.
- Temporarily removed the lower rate guard. The same command failed `TestNormalize/zero` and `TestNormalize/negative`, reporting nil error instead of the expected rate error.
- Temporarily removed the upper rate guard. The same command failed `TestNormalize/hundred` and `TestNormalize/over_hundred` for the same expected reason. Each mutation was restored before the next and the original code restored afterward.
- `PGTESTDB_URL='postgres://billing:billing@127.0.0.1:57793/billing_task2_clean?sslmode=disable' go test ./...` passed for withholding and migrations.

Review correction: Python's default Decimal context uses 28 significant digits and half-even rounding on arithmetic before the final half-up currency quantize. Added a regression for base `0.804`, rate `19.99999999999999999999999999`, USD gross-up. It first failed with total `1.00` and withholding `0.196`; after the correction it returns `1.01` and `0.206`. Added tests for half-even ties, negative rounding, and repeating quotients. The full requested suite passed again with `go test ./... -count=1` and the requested `PGTESTDB_URL`.

Remaining numerical limitations: the port implements Python's default 28-digit, half-even arithmetic context, not a caller-modified Python Decimal context. Python's exponent limits, status flags, traps, and `quantize` InvalidOperation for results exceeding context precision are not modeled. Extremely large exponents can also exceed shopspring/decimal or available resources. Neither implementation uses binary floating point.
