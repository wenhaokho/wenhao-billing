# Go backend, plan 8: reports and stats

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Refreshed 2026-11-10 against `main` at 6bf89c5 (plan 7 merged).** Checked: `invoices.todayUTC`/`utcDate` (core.go:395-398), `fx.GetRate`/`Convert`, `invoices.CurrencyAmount`, `schedule.Parse`/`IsPaused`/`Current`/`NextAfter` all exist as named; `platform.TodayUTC` and `fx.RateOn` do not yet (Task 1 adds them).

**Goal:** Go answers the 5 accounting report routes and the 3 stats routes with Python's contract, and exposes the dashboard core as a function the MCP plan can call.

**Architecture:** Two read-only packages:
- `internal/reports` holds trial balance, balance sheet, FX revaluation, and AR and AP aging.
- `internal/stats` holds the dashboard (core and activity/upcoming), revenue-monthly and invoices-summary.

Each package has its own `queries.sql` and generated `*db/`, and every function takes `today time.Time` from one helper. Rates come from `fx` (plus one new query that also returns `as_of_date`). Schedules come from `invoices/schedule`.

**Tech Stack:** Go 1.27, Huma v2.40 on chi v5, pgx v5, sqlc 1.31.1, shopspring/decimal, gokit v0.2.0, Postgres 16, Docker Compose (through `backend/dev`).

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` ("Behaviour that must be carried exactly": decimals, rounding per call site, dates, lists).

**Contract inventory:** `docs/superpowers/plans/2026-11-10-go-backend-08-reports-stats-inventory.md`:
- §0 lists the shapes in field order;
- §1 covers the routes;
- §2 gives the computation rules;
- §3 lists the 60 in-scope tests;
- §4 names the Go code to reuse;
- §5 lists 19 oddities.

It is the reference for exact values.

## Global Constraints

**Python and port notes**
- `backend-python/` is frozen. Behaviour Go keeps goes into `backend/PORT-NOTES.md`, and so does every "Go changes these" item.

**Routes**
- All 8 routes are GET and admin only: 401 `not authenticated`, 403 `admin required`.
- `as_of` is an optional `YYYY-MM-DD`. It defaults to **today**: the UTC calendar date of `platform.Clock`, through one exported helper `platform.TodayUTC(c Clock) time.Time`, which replaces `invoices.todayUTC`.
- A bad date is 422.

**Decimals and rounding**
- Decimals are `platform.Dec4`, always printed at 4 places. `rate` is `Dec8`, and `confidence_score` is `Dec2`.
- Rounding is **half-even** (`RoundHalfEven4`) at exactly Python's quantize sites: `fx.Convert`, revaluation, MRR and monthly revenue. Everything else is an exact sum.
- MRR divides with `DivRound(x, 28)` and rounds the per-currency sum.
- `avg_days_to_pay` is a JSON number or null, and is not money: `float64` with a `//nolint:forbidigo` reason.

**Order**
- Deterministic everywhere (Go change 4):
  - per-currency lists are `ORDER BY currency`, then Python's stable sort;
  - aging rows sort by `(lower(party_name), currency, party_id)`;
  - recent_activity is `created_at DESC, log_id DESC`;
  - `totals_by_currency` and the monthly dicts are JSON objects with keys in currency order.

**Tests**
- Tests run on pgtestdb through `internal/apitest`, with FX USD→IDR 16000 and SGD→IDR 12000 on 2026-01-01.
- **Every test and subtest calls `t.Parallel()` first.**
- The clock is apitest's.
- Every guard in a "Mutations" line is removed once and watched going red.

**Commits and branch**
- Commits name their paths and end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- The branch is `feat/go-reports-stats` from `main`. No push or PR until the final review.

## Go changes these (each a port note)

1. **The balance sheet honours `as_of`.** Lines are summed only from entries with `posted_at < as_of + 1 day` (inventory §5.1); Python sums every line ever posted.
2. **One open-AR rule.** The dashboard's `open_ar_by_currency` and `open_ar_base_by_currency` use the same filter as AR aging, invoices-summary and customer balances: `status IN (OPEN,SENT,PARTIAL) AND is_template = false AND balance_due > 0` (§5.3).
3. **No negative zero or bare zero.** An unchanged LIABILITY revaluation prints `"0.0000"`, and untouched zero values print `"0.0000"` (§5.6, §5.7). This is an allowed comparison difference.
4. **Deterministic order** as in Global Constraints (§5.8).

**Kept (port notes):**
- The balance sheet lists P&L groups and has no result line.
- `auto_cleared_last_30d` counts every CLEARED payment.
- Revenue uses today's rate for every month.
- `avg_days_to_pay` is per payment.
- Upcoming invoices: recurring cycles strictly after today, usage cut-offs today or later, and the `isinstance(int)` cut-off rule.
- Revaluation keeps residue buckets and its rate query does not upper-case.
- AR aging treats a NULL due date as current, with no `issue_date` filter.
- The trial balance includes inactive accounts.
- `sent_count` and `customer_count` are as wide as in Python.
- An empty `actor_name` is kept.
- UTC "today" (PN 5).

## Review Focus

1. A balance sheet asked for last month must not include this month's postings: Task 2 `TestBalanceSheetHonoursAsOf`.
2. A USD liability with a rate that has not moved must show `0.0000` unrealised, with neither gain nor loss totals touched: Task 3 `TestRevaluationUnchangedLiabilityIsZero`.
3. An invoice due exactly 30, 31, 60, 61, 90 and 91 days ago must land in d_1_30, d_31_60, d_31_60, d_61_90, d_61_90 and d_90_plus: Task 4 `TestAgingBucketEdges`.
4. Weekly MRR of 100 must be `433.3333`, and a crafted sum landing on a 4-place tie must round half-even: Task 5 `TestMRRWeeklyAndTie`.
5. A template with a balance must not appear in the dashboard's open AR (Go change 2): Task 5 `TestOpenARExcludesTemplatesAndZeroBalances`.

---

### Task 1: Shared today and the FX rate with its date

**Files:**
- Modify:
  - `backend/internal/platform/clock.go` (`TodayUTC`);
  - `backend/internal/invoices/core.go` and callers (use it);
  - `backend/internal/fx/queries.sql` and `fx.go`.
- Test: `platform/clock_test.go`, `fx/fx_test.go`

**Interfaces:**
- Produces:
  - `func TodayUTC(c Clock) time.Time`;
  - `func RateOn(ctx context.Context, q *fxdb.Queries, from string, on time.Time, to string) (rate decimal.Decimal, asOf time.Time, ok bool, err error)`. It does no upper-casing, and same-currency gives 1 with `as_of` equal to `on`. It serves revaluation's own query (inventory §1 fx-revaluation).

- [ ] **Step 1: Failing tests.**
  - `TestTodayUTCAcrossMidnightJakarta`: 2026-01-10 03:00 +07:00 gives 2026-01-09.
  - `TestRateOnReturnsDate`.
  - `TestRateOnMissing`.
  - `TestRateOnCaseSensitive`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: return the newest rate overall instead of the newest on or before `on`. The date test goes red.
  - Commit `feat(go): shared UTC today; fx rate with its date`.

---

### Task 2: Trial balance and balance sheet

**Files:**
- Create: `backend/internal/reports/api.go`, `routes.go`, `ledger.go`, `queries.sql`, generated `reportsdb/`
- Modify: `backend/sqlc.yaml`, `backend/cmd/api/main.go`
- Test: `backend/internal/reports/ledger_test.go`

The rules are inventory §1 trial-balance and balance-sheet, with Go change 1.

**Trial balance**
- Inner joins, cutoff `posted_at < as_of + 1 day`.
- Grouped by account and currency, ordered by `code, currency`.
- `net`/`base_net` are debit − credit.
- Totals are the sums of the base columns.

**Balance sheet**
- The sign follows the account type: `ASSET`/`EXPENSE`/`COGS` are debit − credit; any other type is credit − debit.
- An account is skipped only when its balance, base debit and base credit are all 0.
- Groups come in the fixed order of the six types, and only those present are shown.
- `expense_total_base` is EXPENSE plus COGS.

- [ ] **Step 1: Failing tests.**
  - `TestTrialBalanceEmpty`.
  - `TestTrialBalanceGroupsByAccountAndCurrency`.
  - `TestTrialBalanceCutoffInclusiveOfDay`: a posting at 23:59:59.999999 on `as_of` is in, and the next day is out.
  - `TestTrialBalanceIncludesReversalPairs`: they net to 0.
  - `TestTrialBalanceInactiveAccountIncluded`.
  - `TestBalanceSheetSignsPerType`.
  - `TestBalanceSheetKeepsOffsettingAccount`.
  - `TestBalanceSheetGroupOrder`.
  - `TestBalanceSheetHonoursAsOf`.
  - `TestReportsRequireAdmin`.
  - `TestAsOfDefaultsToUTCToday`.
  - `TestBadAsOfIs422`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Move the cutoff into the outer-join ON, as Python does (the as-of test).
  - Use one sign for all types (the signs test).

  Commit `feat(go): trial balance and balance sheet`.

---

### Task 3: FX revaluation

**Files:**
- Create: `backend/internal/reports/revaluation.go`
- Test: `revaluation_test.go`

The rules are inventory §1 fx-revaluation.
- **Buckets:** ASSET and LIABILITY accounts only, where currency ≠ base, with the cutoff in WHERE.
- **Face and original base:** each takes its own sign per type.
- **Skipped buckets:** a bucket is skipped when both face and original base are 0.
- **Rate:** `RateOn` is called once per currency per request.
- **No rate:** the null fields are set, `rate_missing` is true, and `missing_rates` is sorted.
- **Revalued base:** `revalued_base = RoundHalfEven4(face × rate)`.
- **Unrealised gain or loss:** `unrealised = (revalued − original) × sign`, which adds to the gain or the loss total.

- [ ] **Step 1: Failing tests.**
  - `TestRevaluationAssetGain`.
  - `TestRevaluationLiabilityLoss`.
  - `TestRevaluationUnchangedLiabilityIsZero`.
  - `TestRevaluationMissingRate`.
  - `TestRevaluationSkipsBaseCurrencyAndIncomeAccounts`.
  - `TestRevaluationResidueBucketKept` (kept).
  - `TestRevaluationRoundsHalfEven`.
  - `TestRevaluationUsesNewestRateOnOrBefore`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: half-up rounding. The half-even test goes red.
  - Commit `feat(go): fx revaluation report`.

---

### Task 4: AR and AP aging

**Files:**
- Create: `backend/internal/reports/aging.go`
- Test: `aging_test.go`

The rules are inventory §1 ar-aging and ap-aging.
- **AR:** OPEN/SENT/PARTIAL, not a template, balance > 0.
- **AP:** bills that are OPEN/PARTIAL with a balance > 0.
- **Key:** `(customer_id or "-", currency)`. A party with no name shows as `(no customer)` / `(no vendor)`.
- **Days:** `as_of − due_date`, or 0 when the due date is null.
- **Buckets:** `≤0` current; `≤30`, `≤60`, `≤90`; then 90+.
- **Per-currency totals:** per-currency totals are kept as well.
- **`kind`:** `AR` / `AP`.

- [ ] **Step 1: Failing tests.**
  - `TestAgingBucketEdges`, including 0 → current and −5 → current.
  - `TestAgingNullDueDateIsCurrent`.
  - `TestAgingGroupsPerCustomerAndCurrency`.
  - `TestAgingTotalsByCurrency`.
  - `TestAgingExcludesDraftTemplateAndZeroBalance`.
  - `TestAgingNoCustomerRow`.
  - `TestAPAgingExcludesSent`.
  - `TestAgingOrderDeterministic`.
  - `TestAgingEmpty`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: use `< 30` for the second bucket. The edge test goes red.
  - Commit `feat(go): AR and AP aging`.

---

### Task 5: Dashboard core

**Files:**
- Create: `backend/internal/stats/core.go`, `mrr.go`, `queries.sql`, generated `statsdb/`
- Modify: `backend/sqlc.yaml`
- Test: `core_test.go`, `mrr_test.go`, `openar_test.go`

**Interfaces:**
- Produces:
  - `type DashboardCore struct`, with inventory §2.7's 12 fields in that order, using `invoices.CurrencyAmount` and `CurrencyBaseAmount{Currency string; BaseAmount platform.Dec4}`;
  - `func Dashboard(ctx context.Context, q *statsdb.Queries, base string, today time.Time, now time.Time) (DashboardCore, error)`;
  - `func MRR(…) ([]invoices.CurrencyAmount, error)`;
  - `func OpenARBase(…) ([]CurrencyBaseAmount, []string, error)`.

Rules: inventory §2.1–§2.3, with Go change 2.
- **Templates:** they are parsed through `schedule.Parse`, skipped when paused, unparseable or ended.
- **`auto_cleared_last_30d`:** it counts `created_at >= now − 30 days`, with `now` the clock's UTC instant.

- [ ] **Step 1: Failing tests.** Port:
  - the 10 service-level tests of `test_dashboard_stats.py` (inventory §3; the route ones belong to Task 6);
  - the 9 of `test_mrr_stats.py`;
  - the 5 of `test_open_ar_base.py`.

  New:
  - `TestMRRWeeklyAndTie`;
  - `TestOpenARExcludesTemplatesAndZeroBalances`;
  - `TestPipelineTieOrderDeterministic`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Drop the paused check (the paused test).
  - Convert per invoice instead of per currency sum (the open-AR-base test).

  Commit `feat(go): dashboard core stats`.

---

### Task 6: Dashboard route, recent activity and upcoming invoices

**Files:**
- Create: `backend/internal/stats/upcoming.go`, `activity.go`, `routes.go`, `api.go`
- Modify: `backend/cmd/api/main.go`
- Test: `upcoming_test.go`, `dashboard_route_test.go`

The rules are inventory §1 dashboard and §2.5.
- **Upcoming horizon:** today + 90 days, inclusive.
- **Recurring:** `NextAfter(today)`, one row per template.
- **Usage:** DRAFT, not locked, `cut_off_day` an int from 1 to 31 (`true` counts as 1, while `"20"` and `20.0` are skipped), next day of month on or after today, clamped to the month's length.
- **Upcoming sort:** `(next_date, invoice_id)`.
- **Activity:** 10 rows, with the customer through `coalesce(payment.customer_id, invoice.customer_id)` and `actor_name = coalesce(display_name, email)`.

- [ ] **Step 1: Failing tests.** Port:
  - the 9 of `test_upcoming_invoices.py`;
  - the 2 dashboard route tests and the 2 recent-activity tests of `test_dashboard_stats.py`.

  New:
  - `TestUsageCutOffDayTypes`;
  - `TestUsageCutOffClampsToMonthEnd` (31 in February);
  - `TestRecurringCycleStartingTodayNotListed` (kept);
  - `TestDashboardRequiresAdmin`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Make the horizon exclusive (the window test).
  - Accept `"20"` as a cut-off (the types test).

  Commit `feat(go): dashboard route with activity and upcoming`.

---

### Task 7: Revenue by month and the invoices summary

**Files:**
- Create: `backend/internal/stats/revenue.go`, `summary.go`
- Test: `revenue_test.go`, `summary_test.go`

The rules are inventory §1 and §2.4 and §2.6.

**Revenue by month**
- 12 labels, oldest first.
- Native sums.
- Unpaid only when not zero.
- Today's rate per currency.
- Unconverted currencies are kept in the native totals only.
- `currencies` is the sorted list of currencies with base totals.

**Invoices summary**
- The overdue / due-in-30 buckets per inventory §2.6.
- `avg_days_to_pay` is the mean over CLEARED payments of PAID invoices in the last 90 days, per payment, or null.

- [ ] **Step 1: Failing tests.** Port:
  - the 9 of `test_revenue_monthly_base.py`;
  - the 16 of `test_invoices_summary.py`.

  New:
  - `TestRevenueMonthlyRoute`;
  - `TestInvoicesSummaryRoute` (`avg_days_to_pay` is the JSON number `13.333333333333334`; Python's `10.0` is Go's `10`, equal as numbers, which is an allowed difference, noted);
  - `TestRevenueFutureMonthDropped`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Include due-today in overdue (the due-today test).
  - Count payments older than 90 days (the 90-day test).

  Commit `feat(go): monthly revenue and invoices summary`.

---

### Task 8: Inventories, port notes, checks

- [ ] **Step 1: Routes.** Mark the 8 routes `ported` (`go` = `reports`, `stats`).
- [ ] **Step 2: Tests.** Mark the 60 Python tests with their Go names and mutations. The ported count rises by 60 over the number at the start of the plan.
- [ ] **Step 3: Port notes.** Write one entry per "Go changes these" item and per kept behaviour, each with Python file:line, the Go test, and "After the switch". Update PN 5.
- [ ] **Step 4: Checks.** `bash scripts/check.sh` → `all checks passed`, none skipped.
- [ ] **Step 5: Commit.** Commit `docs(go): reports and stats ported; port notes`.
