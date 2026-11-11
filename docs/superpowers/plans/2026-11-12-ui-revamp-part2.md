# UI Revamp Part 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the mockup's data-backed elements real data: dashboard analytics, Send reminder, Manual Review tags and
picker, and the small screen items.

**Architecture:** New Go queries and endpoints in the existing feature packages (`stats`, `invoices`, `payments`,
`fx`, `reports`, `jobs`), each covered by real-Postgres tests; then the Vue views consume them through
`@tanstack/vue-query`. Four PRs (A dashboard, B reminders, C Manual Review, D small items), each its own branch off
main, each merged only on the owner's yes.

**Tech Stack:** Go (chi + huma, pgx + sqlc, goose, shopspring/decimal), Postgres 18, Vue 3 + TS + PrimeVue 4 +
Chart.js, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-12-ui-revamp-part2-design.md` (owner-approved 2026-11-12). Read it first;
this plan does not repeat its field definitions.

## Global Constraints

- Money: `numeric(19,4)` / `decimal.Decimal` / `platform.Dec4` (JSON string). `float64` only for day counts,
  percentages and confidence, each with `//nolint:forbidigo // <why>` as `stats/summary.go` does.
- IDR equivalents: `fx.Convert`/`fx.GetRate` at `today`; a currency with no rate goes in `unconverted`, never zero.
- Open AR rule: `status IN ('OPEN','SENT','PARTIAL') AND is_template = false AND balance_due > 0`; overdue adds
  `due_date IS NOT NULL AND due_date < today`. Today is `platform.TodayUTC(clock)`.
- No `business_id` (single tenant). New routes use `platform.Admin(huma.Operation{...})`.
- Every Go test calls `t.Parallel()`. Run from `backend/` with `COMPOSE_PROJECT_NAME=wenhao-billing`:
  `./dev test ./internal/<pkg> -run <Name>`.
- After `queries.sql` or migration changes: `./dev sqlc`. After route/schema changes: `./dev openapi`.
  New migrations are `backend/migrations/000NN_<name>.sql` (next free number) with Up and Down.
- Frontend: `--wb-*` tokens, `src/theme/status.ts` tones, `<StatusTag>`, `<PageHeader>`, `<EmptyState>`,
  `<AppIcon>` (Lucide). Response types are hand-written TS interfaces beside the view, as `PaymentIntakeWidget.vue`
  does. Copy says "software house", never "agency".
- Each PR ends with `bash scripts/check.sh` and `bash scripts/e2e.sh` green, then a PR whose merge waits for the
  owner's yes.

## Review Focus

1. **A currency with no FX rate** (e.g. an AUD invoice): every dashboard figure excludes it and lists it in
   `unconverted`; nothing reads as zero or 500s. Tests in Tasks 2, 4, 5.
2. **Empty database / brand-new install:** every endpoint returns zeros, nulls and empty lists; every card shows
   `<EmptyState>`. Tests in Tasks 2, 3, 4 and the e2e in Task 7.
3. **Window edges:** an invoice issued exactly on `from` and on `to` is counted; one the day before `from` lands in
   `previous`. Test in Task 1 and Task 2.
4. **Reminder guard across invoices of one customer and across REST + MCP:** a second reminder to the same customer
   within 24 h is refused on both paths unless forced. Tests in Tasks 9 and 10.
5. **Reason text the mapper hasn't seen** (manual notes, future engine text): coded `OTHER`, never a write failure,
   and Go and SQL backfill agree on every known text. Test in Task 12.

---

## PR A — Dashboard analytics (branch `feat/revamp2-dashboard`)

### Task 1: Period windows

**Files:**
- Create: `backend/internal/stats/period.go`, `backend/internal/stats/period_test.go`

**Interfaces:**
- Produces:
  ```go
  type Period string // "30d" | "90d" | "12m"
  type Bucket struct{ Label string; From, To time.Time } // inclusive dates
  type Window struct {
      Period             Period
      From, To           time.Time // To = today
      PrevFrom, PrevTo   time.Time // equal length, ending the day before From
      Buckets            []Bucket  // days (30d), ISO weeks (90d), calendar months (12m), oldest first
  }
  func ParsePeriod(s string) (Period, error) // "" → "12m"; anything else unknown → error
  func NewWindow(p Period, today time.Time) Window
  ```

- [ ] **Step 1: Write failing tests** `TestNewWindow` (table, `t.Parallel()` per case), today = 2026-11-12:
  - `30d`: From 2026-10-14, To 2026-11-12, PrevFrom 2026-09-14, PrevTo 2026-10-13, 30 buckets, first `{From: 10-14, To: 10-14}`.
  - `90d`: From 2026-08-15, PrevTo 2026-08-14, buckets are ISO weeks clipped to [From, To]; first bucket From = 08-15, last To = 11-12.
  - `12m`: From 2025-11-13, To 2026-11-12, PrevFrom 2024-11-13, PrevTo 2025-11-12; 13 month buckets Nov 2025 (clipped from 11-13) … Nov 2026 (clipped to 11-12), labels "Nov", "Dec", … .
  - Buckets cover [From, To] with no gap or overlap (assert for all three).
  - `TestParsePeriod`: "" → "12m"; "30d","90d","12m" ok; "7d" → error.
- [ ] **Step 2:** `./dev test ./internal/stats -run 'TestNewWindow|TestParsePeriod'` → FAIL (undefined).
- [ ] **Step 3:** Implement in `period.go`. 30d = today−29 … today; 90d = today−89; 12m = today.AddDate(-1,0,1).
- [ ] **Step 4:** Same command → PASS.
- [ ] **Step 5:** Commit `feat(stats): period windows for the dashboard`.

### Task 2: Overview KPIs

**Files:**
- Modify: `backend/internal/stats/queries.sql` (append), `backend/internal/stats/api.go` (types)
- Create: `backend/internal/stats/overview.go`, `backend/internal/stats/overview_test.go`, `backend/internal/stats/convert.go`

**Interfaces:**
- Consumes: `NewWindow`, `Window` (Task 1); `MRR` (`stats/mrr.go`); `OpenARBase` (`stats/core.go`).
- Produces:
  ```go
  // convert.go: sum native per-currency rows into base at today; skipped currencies returned sorted.
  func toBase(ctx context.Context, db statsdb.DBTX, base string, today time.Time, rows []invoices.CurrencyAmount) (decimal.Decimal, []string, error)
  type KPIFlow struct { Amount, Previous platform.Dec4; Series []SeriesPoint }      // SeriesPoint{Label string; Amount platform.Dec4}
  type OverviewKPIs struct {
      Invoiced KPIFlow; Collected KPIFlow; CollectedShareOfInvoiced *float64
      OpenAR platform.Dec4; OpenARInvoiceCount, OpenARCustomerCount int
      Overdue platform.Dec4; OverdueShareOfOpenAR *float64
      MonthlyRecurring platform.Dec4; RecurringForeignShare *float64; RecurringTopForeignCurrency *string
  }
  type OverviewResponse struct { Period string; From, To, PreviousFrom, PreviousTo platform.Date; BaseCurrency string;
      Unconverted []string; KPIs OverviewKPIs; DaysToPay DaysToPay; Reconciliation ReconHealth; TopCustomers TopCustomers } // last three filled in Task 3
  func Overview(ctx context.Context, db statsdb.DBTX, base string, today time.Time, w Window) (OverviewResponse, error)
  ```
  JSON names as in the spec (`kpis.invoiced.previous`, `share_of_invoiced`, …), snake_case.
- New queries: `InvoicedByCurrencyBetween(from, to)` (issue_date in range, not template, status not in DRAFT/VOID),
  `CollectedByCurrencyBetween(from, to)` (CLEARED payments, `SUM(amount + COALESCE(wht_amount,0))`, payment_date in
  range), `OpenARCounts()` (count invoices, count distinct customer_id, open AR rule). Series = the Between queries per
  bucket (call per bucket, or one query grouped by `date_trunc` — builder's choice).

- [ ] **Step 1: Write failing tests** in `overview_test.go` (use `helpers_test.go` helpers; add a `paymentFull` helper with amount/wht/payment_date if missing):
  - `TestOverviewEmpty`: fresh DB → all amounts "0.0000", shares nil, `Unconverted` empty, series length = bucket count.
  - `TestOverviewInvoicedWindow`: IDR invoices issued on From (100), To (200), From−1 (50); a DRAFT, a VOID and a template inside the window → `Invoiced.Amount` "300.0000", `Previous` "50.0000".
  - `TestOverviewCollectedAddsWHT`: CLEARED payment amount 98, wht 2 in window; a PENDING_MANUAL_REVIEW one → Collected "100.0000"; with Invoiced 200 → share 50.
  - `TestOverviewUnconverted`: SGD invoice with an SGD→IDR rate 12000 and an AUD invoice with no rate → AUD in `Unconverted`, Invoiced = SGD×12000 only.
  - `TestOverviewOverdueShare`: open AR 400 of which 100 overdue → share 25; counts invoices=2, customers=2.
  - `TestOverviewRecurringForeignShare`: MRR IDR 100 + SGD 1 at 100 → share 50, top foreign "SGD".
- [ ] **Step 2:** `./dev sqlc` then `./dev test ./internal/stats -run TestOverview` → FAIL.
- [ ] **Step 3:** Implement `toBase`, the queries, and `Overview` (KPIs only; Task 3 fills the rest with zero values for now).
- [ ] **Step 4:** Same → PASS.
- [ ] **Step 5:** Commit `feat(stats): overview KPIs with period and previous window`.

### Task 3: Days to pay, reconciliation health, top customers, and the route

**Files:**
- Modify: `backend/internal/stats/queries.sql`, `overview.go`, `api.go`, `routes.go`, `overview_test.go`; `backend/openapi.json` (generated)

**Interfaces:**
- Produces:
  ```go
  type DaysToPay struct { Avg, PreviousAvg *float64 }
  type ReconHealth struct { AutoCleared, Manual, Held int; AvgConfidence *float64; AliasesInUse, CustomersWithAliases int }
  type TopCustomer struct { CustomerID uuid.UUID; Name string; Amount platform.Dec4; Share float64; Overdue platform.Dec4 }
  type TopCustomers struct { Rows []TopCustomer; Others struct{ Count int; Amount platform.Dec4; Share float64 }; Top3Share *float64 }
  ```
  Route: `GET /api/v1/stats/overview?period=` (OperationID `stats-overview`), 422 on an unknown period.
- New queries: `DaysToPayBetween(from, to)` (existing `DaysToPay` with an upper bound); `ReconCountsBetween(from, to)`
  — auto = CLEARED with a `reconciliation_log` CLEARED row whose `actor_user_id IS NULL`, manual = other CLEARED,
  held = PENDING_MANUAL_REVIEW or FLAGGED, by `payments.created_at` in range; `AvgConfidenceBetween` over non-null
  `confidence_score` and `intake_source <> 'MANUAL'`; `AliasStats()` = `SUM(jsonb_array_length(matching_aliases))`,
  count with length > 0; `InvoicedByCustomerCurrencyBetween(from, to)` and `OverdueByCustomerCurrency(today)`.

- [ ] **Step 1: Write failing tests:**
  - `TestOverviewDaysToPay`: payments 10 and 20 days after issue in window, 40 in previous → Avg 15, PreviousAvg 40; none → nil.
  - `TestOverviewRecon`: one engine-cleared (log actor null), one manual-entry cleared (actor set), one held, one FLAGGED, one created before From → 1/1/2; confidences 90 and 80 on engine rows → 85; customer aliases `["a","b"]` and `[]` → 2 aliases, 1 customer.
  - `TestOverviewTopCustomers`: 12 customers with distinct invoiced amounts → 10 rows biggest first, Others.Count 2, shares sum to 100 (±0.01), Top3Share; a customer with an overdue invoice has non-zero `Overdue`.
  - `TestOverviewRoute`: GET `?period=90d` → 200 with `period: "90d"`; `?period=7d` → 422; no param → `12m`.
- [ ] **Step 2:** `./dev sqlc`; `./dev test ./internal/stats -run TestOverview` → FAIL.
- [ ] **Step 3:** Implement; register the route; `./dev openapi`.
- [ ] **Step 4:** Same → PASS; `./dev test ./internal/stats` all PASS.
- [ ] **Step 5:** Commit `feat(stats): overview endpoint with days to pay, reconciliation and top customers`.

### Task 4: Receivables endpoint

**Files:**
- Create: `backend/internal/stats/receivables.go`, `receivables_test.go`
- Modify: `queries.sql`, `api.go`, `routes.go`, `backend/openapi.json`

**Interfaces:**
- Consumes: `toBase` (Task 2), `Upcoming` (`stats/upcoming.go`).
- Produces:
  ```go
  type AgingBucket struct { Key string; Amount platform.Dec4; Count int } // keys: not_due, d1_30, d31_60, d61_90, d90_plus
  type CashInBucket struct { Key string; OpenInvoices, Recurring platform.Dec4; Estimate bool } // d0_30, d31_60, d61_90
  type AttentionRow struct { InvoiceID, CustomerID uuid.UUID; InvoiceNumber *string; CustomerName string;
      BalanceDue, ExpectedCash platform.Dec4; Currency string; DueDate platform.Date; DaysLate int; LastReminderAt *platform.Time }
  type ReceivablesResponse struct { BaseCurrency string; Unconverted []string; OpenAR platform.Dec4; PastDueShare *float64;
      InvoiceCount, CustomerCount int; Aging []AgingBucket; CashIn []CashInBucket; OverdueExcluded platform.Dec4; NeedsAttention []AttentionRow }
  func Receivables(ctx context.Context, db statsdb.DBTX, base string, today time.Time) (ReceivablesResponse, error)
  ```
  Route `GET /api/v1/stats/receivables` (`stats-receivables`). `ExpectedCash` = `withholding.ExpectedCash(balance_due, wht_amount, wht_booked)`, native (not converted), in `Currency`. `LastReminderAt` stays nil in PR A.
- Queries: `AgingByCurrency(today)` grouping by bucket key in SQL (`CASE` on `today - due_date`, null due date →
  `not_due`); `OpenNotDueByCurrencyBetween(from, to)` for cash-in; `OverdueOldest(today, lim)`.

- [ ] **Step 1: Write failing tests:**
  - `TestReceivablesEmpty`: five aging buckets and three cash-in buckets all "0.0000", lists empty.
  - `TestReceivablesAging`: due today (not due), today−1 (d1_30), −30 (d1_30), −31 (d31_60), −91 (d90_plus), no due date (not_due) → counts and amounts per key; PastDueShare.
  - `TestReceivablesCashIn`: open invoice due today+5 → d0_30 OpenInvoices; recurring template next run today+40 → d31_60 Recurring; a usage draft → Estimate true; overdue invoice → only in OverdueExcluded.
  - `TestReceivablesNeedsAttention`: 8 overdue invoices → 6 rows, oldest due first, DaysLate correct; invoice with wht 2 unbooked and balance 100 → ExpectedCash "98.0000".
  - `TestReceivablesUnconverted`: AUD overdue with no rate → in Unconverted, excluded from amounts, still listed in NeedsAttention (native).
- [ ] **Step 2:** `./dev sqlc`; `./dev test ./internal/stats -run TestReceivables` → FAIL.
- [ ] **Step 3:** Implement and register; `./dev openapi`.
- [ ] **Step 4:** → PASS.
- [ ] **Step 5:** Commit `feat(stats): receivables endpoint (aging, cash in, needs attention)`.

### Task 5: Revenue-monthly per currency and footer

**Files:**
- Modify: `backend/internal/stats/revenue.go`, `api.go`, `queries.sql`, `revenue_test.go`, `backend/openapi.json`

**Interfaces:**
- Produces (added to `MonthlyRevenueResponse`, existing fields unchanged):
  ```go
  type CurrencyMonth struct { Currency string; Paid, Unpaid platform.Dec4; PaidBase, UnpaidBase *platform.Dec4 } // base nil when no rate
  // on each existing month bucket: ByCurrency []CurrencyMonth `json:"by_currency"`
  type RevenueFooter struct { InvoicedBase platform.Dec4; ForeignShare *float64; BestMonth *struct{ Label string; Amount platform.Dec4 }; FXExposure platform.Dec4 }
  // on the response: Footer RevenueFooter `json:"footer"`
  ```
- [ ] **Step 1: Failing tests** `TestRevenueByCurrency` (IDR invoice 100 with balance 40 → Paid 60, Unpaid 40; SGD with rate → base set; AUD no rate → base nil) and `TestRevenueFooter` (best month label/amount; foreign share; FXExposure = open AR in non-base currencies at today's rate). Existing revenue tests stay green unchanged.
- [ ] **Step 2:** → FAIL. **Step 3:** implement. **Step 4:** `./dev test ./internal/stats` → PASS; `./dev openapi`.
- [ ] **Step 5:** Commit `feat(stats): revenue by currency with paid/unpaid and footer`.

### Task 6: Dashboard frontend — period, KPI row, revenue card

**Files:**
- Modify: `frontend/src/views/PaymentIntakeWidget.vue`
- Create: `frontend/src/components/dashboard/PeriodSelector.vue`, `KpiTile.vue`, `Sparkline.vue`, `RevenueCard.vue`, `frontend/src/components/dashboard/types.ts` (TS mirrors of Tasks 2–5 responses)

**Interfaces:**
- Consumes: `/stats/overview?period=`, `/stats/revenue-monthly`.
- Produces: `PeriodSelector` (`v-model: '30d'|'90d'|'12m'`), `KpiTile` props `{label, value, unit, delta?: {text, tone:'good'|'bad'|'neutral'}, sub, series?: {label, amount}[]}`; `types.ts` exports used by Task 7.

- [ ] **Step 1:** Period is read from and written to `route.query.period` (replace, not push); query key `['stats-overview', period]`.
- [ ] **Step 2:** KPI row per the spec: sparklines only on Invoiced and Collected; delta = (amount − previous)/previous as ▲/▼ percent, green when up for Invoiced/Collected; Open AR shows "N invoices · M customers", Overdue "X% of open AR" (red), Monthly recurring "X% billed in <ccy>"; "—" delta when previous is 0.
- [ ] **Step 3:** Revenue card: tabs "All · IDR eq." + one per currency present in `by_currency`; stacked base view and native single-currency view; hatched unpaid; legend totals; footer stats. Chart colours via the existing `chartColors()`/theme watcher.
- [ ] **Step 4:** `cd frontend && npm run build` → no type errors; open `/dashboard` against the dev stack (`docker compose up -d --build backend`), switch periods, reload keeps the period.
- [ ] **Step 5:** Commit `feat(dashboard): period selector, KPI row and revenue by currency`.

### Task 7: Dashboard frontend — receivables cards, recon donut, top customers, e2e

**Files:**
- Create: `frontend/src/components/dashboard/AgingCard.vue`, `CashInCard.vue`, `ReconDonut.vue`, `TopCustomersCard.vue`, `NeedsAttentionCard.vue`, `frontend/e2e/dashboard.spec.ts`
- Modify: `PaymentIntakeWidget.vue`

- [ ] **Step 1:** Cards per the mockup and spec; query key `['stats-receivables']` (refetch 60 s). Every card renders `<EmptyState>` when empty; any response with `unconverted` shows "Not converted: AUD (no FX rate)". Needs attention links each row to the invoice; no Send reminder button yet. "Open manual review" and "All invoices" links as in the mockup.
- [ ] **Step 2: e2e** `dashboard.spec.ts`: period buttons change the URL and refetch (`waitForResponse` on `period=90d`); each card heading visible; at 375 px no horizontal scroll; dark mode has no light surfaces (reuse the helper from `theme.spec.ts`).
- [ ] **Step 3:** `bash scripts/e2e.sh e2e/dashboard.spec.ts` → PASS.
- [ ] **Step 4:** Commit `feat(dashboard): aging, cash in, reconciliation, top customers, needs attention`.

### Task 8: PR A gate

- [ ] `bash scripts/check.sh` → all pass; `bash scripts/e2e.sh` → no new failures (known: the FX `test.fixme`).
- [ ] Screenshots light/dark at 1440/900/375 vs the mockup; open PR "UI revamp part 2A: dashboard analytics"; merge waits for the owner's yes.

---

## PR B — Send reminder (branch `feat/revamp2-reminders`, off main after A merges)

### Task 9: Reminder log and shared send function

**Files:**
- Create: `backend/migrations/000NN_invoice_reminders.sql`, `backend/internal/invoices/reminders.go`, `reminders_test.go`
- Modify: `backend/internal/invoices/queries.sql`, `backend/internal/mcp/tools_gated.go` (+ its test)

**Interfaces:**
- Produces:
  ```go
  type ReminderIn struct { InvoiceID uuid.UUID; ToEmail *string; Force bool; Channel string /* "REST"|"MCP" */; ActorUserID *uuid.UUID }
  type Reminder struct { ReminderID, InvoiceID uuid.UUID; SentAt platform.Time; SentByUserID *uuid.UUID; Channel, Recipient string; Amount platform.Dec4; Currency string }
  type RecentReminderError struct { InvoiceID uuid.UUID; InvoiceNumber *string; SentAt time.Time } // Error() names both
  // PrepareReminder validates (VOID/PAID refused, template refused, DRAFT refused — same messages as the MCP tool today),
  // resolves the recipient and expected cash, and applies the 24 h per-customer guard unless Force.
  func PrepareReminder(ctx context.Context, tx pgx.Tx, in ReminderIn, now time.Time) (ReminderDraft, error)
  func SendReminder(ctx context.Context, tx pgx.Tx, mail email.Sender, d ReminderDraft, in ReminderIn, now time.Time) (Reminder, error) // sends, then inserts the row
  func ListReminders(ctx context.Context, db invoicesdb.DBTX, invoiceID uuid.UUID) ([]Reminder, error)
  func LastReminderByCustomer(ctx context.Context, db invoicesdb.DBTX, customerIDs []uuid.UUID) (map[uuid.UUID]time.Time, error)
  ```
  Table per spec: `invoice_reminders(reminder_id uuid pk, invoice_id uuid not null references invoices, sent_at timestamptz not null, sent_by_user_id uuid null references users, channel text not null check (channel in ('REST','MCP')), recipient text not null, amount numeric(19,4) not null, currency varchar(3) not null)`, index on `(invoice_id)` and on `sent_at`.
  The email text stays exactly as the MCP tool sends it today.
  The MCP tool keeps preview → confirm: preview calls `PrepareReminder` (so a recent reminder shows in the preview as a refusal unless `force`), confirm calls `SendReminder`. Add `p("force", tNBool)` to the tool input.

- [ ] **Step 1: Failing tests:** `TestReminderRefusals` (VOID, PAID, DRAFT, template → same refusal messages as before); `TestReminderGuardPerCustomer` (send for invoice X; then invoice Y of the same customer within 24 h → `RecentReminderError`; with Force → sent; another customer → sent; 25 h later via the test clock → sent); `TestReminderAmountExpectedCash` (balance 100, wht 2 unbooked → row Amount "98.0000"); MCP test: preview after a recent reminder refuses; `force: true` allows.
- [ ] **Step 2:** `./dev sqlc`; `./dev test ./internal/invoices ./internal/mcp -run Reminder` → FAIL.
- [ ] **Step 3:** Implement; move the MCP tool onto these functions.
- [ ] **Step 4:** → PASS, and `./dev test ./internal/mcp` all green (existing reminder tests unchanged except the new input).
- [ ] **Step 5:** Commit `feat(invoices): reminder log with a 24h per-customer guard; MCP uses it`.

### Task 10: Reminder REST routes and dashboard data

**Files:**
- Modify: `backend/internal/invoices/routes.go` (or a new `reminder_routes.go`), `backend/internal/stats/receivables.go`, tests, `backend/openapi.json`

**Interfaces:**
- `POST /api/v1/invoices/{invoice_id}/reminders` body `{to_email?: string, force?: bool}` → 201 `Reminder`; 409 with `{invoice_number, sent_at}` detail on `RecentReminderError`; 422 on refusals. `GET` same path → `[]Reminder` newest first.
- `Receivables` fills `AttentionRow.LastReminderAt` from `LastReminderByCustomer`.
- [ ] **Step 1: Failing tests** for 201, 409 then forced 201, 422 on a PAID invoice, GET order, and `needs_attention.last_reminder_at` set for every row of the reminded customer. Use the stub `email.Sender` the MCP tests use.
- [ ] **Step 2–4:** FAIL → implement → PASS; `./dev openapi`.
- [ ] **Step 5:** Commit `feat(invoices): REST send reminder and reminder history`.

### Task 11: Reminder UI

**Files:**
- Modify: `NeedsAttentionCard.vue`, `frontend/src/views/InvoiceFormView.vue` (history section, edit mode only)
- Create: `frontend/src/components/dashboard/ReminderDialog.vue`, `frontend/e2e/reminders.spec.ts`

- [ ] **Step 1:** "Send reminder" opens `ReminderDialog`: recipient, invoice number, expected cash (and balance due beside it when they differ); Send → POST; on 409 show "Reminded <customer> <time ago> for <invoice>. Send anyway?" which retries with `force: true`; on success invalidate `['stats-receivables']` and every row of that customer shows "reminded just now".
- [ ] **Step 2:** Invoice edit page lists reminders (date, recipient, amount, channel).
- [ ] **Step 3: e2e:** create an overdue invoice via API, send from dashboard, assert "reminded"; second send shows the 409 prompt. Dev email is the log sender, so nothing leaves the machine.
- [ ] **Step 4:** `bash scripts/e2e.sh e2e/reminders.spec.ts` → PASS. **Step 5:** commit; PR gate as Task 8 ("UI revamp part 2B: send reminder").

---

## PR C — Manual Review (branch `feat/revamp2-manual-review`)

### Task 12: Reason codes

**Files:**
- Create: `backend/migrations/000NN_reconciliation_reason_codes.sql`, `backend/internal/payments/reasons.go`, `reasons_test.go`
- Modify: `backend/internal/payments/store.go` (`logAction`), `queries.sql`, `recon_routes.go` (+ `ReasonCodes []string json:"reason_codes"` on `LogEntry` and audit rows), `backend/internal/stats/api.go` (`ActivityItem.ReasonCodes`)

**Interfaces:**
- Produces: `func ReasonCode(text string) string` — prefix rules (case-sensitive, as the code writes them):
  `"no open invoice matches"`→`NO_CANDIDATE`, `"currency mismatch:"`→`CURRENCY_MISMATCH`, `"amount mismatch:"`→`AMOUNT_MISMATCH`,
  `"confidence "` + contains `"below threshold"`→`LOW_CONFIDENCE`, `"ambiguous match:"`→`AMBIGUOUS`,
  the clear-failed sentence from `payments/service.go:~160` (use its exact prefix)→`CLEAR_FAILED`,
  `"exact amount+currency"`→`EXACT_MATCH`, `"manual-entry"`→`MANUAL_ENTRY`, `"manual approval against invoice"`→`MANUAL_APPROVAL`,
  `"alias match:"`→`ALIAS_MATCH`, else `OTHER`.
  `logAction` writes `reason_codes` = `ReasonCode` of each reason, same order.
  Migration: `ALTER TABLE reconciliation_log ADD COLUMN reason_codes text[] NOT NULL DEFAULT '{}'`, then an `UPDATE` building the array with the same rules as a SQL `CASE` over `unnest(reasons) WITH ORDINALITY`. Down drops the column.
- [ ] **Step 1: Failing tests:** `TestReasonCode` table over every text the engine/service/manual paths produce (build them by calling the real code paths where cheap, else copy the exact format strings) plus `"something new"`→`OTHER`; `TestReasonCodesBackfill`: insert log rows with each text and empty codes, run the migration's UPDATE SQL (read from the migration file), assert codes equal `ReasonCode` per text; `TestLogActionWritesCodes`.
- [ ] **Step 2–4:** FAIL → implement → PASS; `./dev sqlc`, `./dev openapi`.
- [ ] **Step 5:** Commit `feat(payments): reason codes on the reconciliation log`.

### Task 13: Alias-match reason on held payments

**Files:** Modify `backend/internal/matching/engine.go`, `engine_test.go`

**Interfaces:**
- `payerSimilarity` also returns which alias won (`(decimal.Decimal, *string)`); when the result is held
  (`StatusPendingManualReview`) and the best candidate's similarity came from an alias, append
  `fmt.Sprintf("alias match: '%s' (%s)", alias, ratio)` to `Reasons` last. Cleared results unchanged.
- [ ] **Step 1: Failing tests:** `TestAliasMatchReasonOnHeld` (payer equals an alias, amount mismatch → reasons end with the alias text); `TestAliasMatchNotOnCleared` (alias payer, exact amount → reasons exactly `["exact amount+currency, high payer+date confidence"]`); the existing engine table tests pass unchanged — add an assertion that `Status`, `Confidence` and `AdjustmentType` are identical with and without aliases-derived reasons on every existing case.
- [ ] **Step 2–4:** FAIL → implement → PASS. **Step 5:** Commit `feat(matching): note alias matches on held payments`.

### Task 14: Invoice search for the picker

**Files:** Modify `backend/internal/invoices/routes.go` (`listIn`), `queries.sql`, tests, `backend/openapi.json`

**Interfaces:** `listIn` gains `Q string query:"q"` (case-insensitive substring of invoice_number or customer name) and `Open bool query:"open"` (open AR rule). Empty `q` = no filter. Combined with existing filters by AND.
- [ ] **Step 1: Failing tests:** `q=wh2026` matches by number; `q=citra` by customer; `open=true` excludes PAID, DRAFT, VOID, templates and zero balance; `q` with `%` or `_` is matched literally.
- [ ] **Step 2–4:** FAIL → implement (escape LIKE wildcards) → PASS; `./dev sqlc`, `./dev openapi`. **Step 5:** commit.

### Task 15: Manual Review UI

**Files:** Modify `frontend/src/views/ManualReviewView.vue`, `frontend/src/theme/status.ts`; Create `frontend/src/components/review/InvoicePicker.vue`, `frontend/e2e/manual-review.spec.ts`

- [ ] **Step 1:** Via tag from `intake_source` (WEBHOOK → "Bank webhook", EMAIL_PARSER → "Email", MANUAL → "Manual").
- [ ] **Step 2:** Reason tags from `reason_codes` with the reason text as the explanation; tones in `status.ts` (mismatch/no-candidate/ambiguous/clear-failed red, low-confidence amber, alias/exact green, other neutral).
- [ ] **Step 3:** `InvoicePicker`: `AutoComplete` over `GET /invoices?open=true&q=` (debounced 250 ms), default filter to the payment's currency with a toggle for all; rows show number, customer, balance due, due date; inline warnings for currency mismatch and payment > balance due; choosing sets the `invoiceId` the existing submit sends.
- [ ] **Step 4: e2e:** a held payment (create via the webhook route in the test, as existing specs do) shows via and reason tags; picker finds an invoice by customer name; picking a different-currency invoice shows the warning. `bash scripts/e2e.sh e2e/manual-review.spec.ts` → PASS.
- [ ] **Step 5:** Commit; PR gate ("UI revamp part 2C: manual review").

---

## PR D — Small screen items (branch `feat/revamp2-extras`)

### Task 16: FX summary and last sync

**Files:** Create `backend/migrations/000NN_fx_sync_runs.sql`; Modify `backend/internal/fx/sync.go`, `routes.go`, `queries.sql`, `api.go`, tests, `backend/internal/jobs/jobs.go` (pass trigger), `frontend/src/views/FxRatesView.vue`

**Interfaces:**
- `func Sync(ctx, pool, cfg, client, trigger string, actor *uuid.UUID) (SyncResult, error)` — inserts an `fx_sync_runs` row (`run_id, ran_at, source, trigger 'SCHEDULED'|'MANUAL', created, updated, actor_user_id`) on success. The job passes `SCHEDULED`, the route `MANUAL` with the caller.
- `GET /api/v1/accounting/fx-rates/summary` → `{base_currency, currencies: [{currency, latest_rate Dec8, latest_as_of, previous_rate *Dec8, previous_as_of, change_pct *float64}], last_sync: {ran_at, trigger, source} | null}` — one entry per currency with any rate to base.
- [ ] **Step 1: Failing tests:** two dates of SGD rates → change_pct; one date → previous nil; manual sync writes a MANUAL run (use the existing fake Frankfurter server in fx tests); no runs → `last_sync` null.
- [ ] **Step 2–4:** FAIL → implement → PASS; frontend cards and "Last sync <time ago> · weekly / manual" on `FxRatesView.vue`. **Step 5:** commit.

### Task 17: Trial balance "Balanced"

**Files:** Modify `backend/internal/reports/api.go` (`TrialBalanceReport` + `Balanced bool json:"balanced"`, `Difference platform.Dec4 json:"difference"` = debit − credit), the trial balance builder, its test; `frontend/src/views/ReportsView.vue`.
- [ ] **Step 1:** Failing test: balanced ledger → `balanced: true`, difference "0.0000"; a hand-inserted unbalanced journal line → false and the difference.
- [ ] **Step 2–4:** FAIL → implement → PASS; Reports shows a green "Balanced" tag or a red "Out of balance by X". **Step 5:** commit.

### Task 18: Audit Log CSV

**Files:** Modify `backend/internal/payments/recon_routes.go`, `queries.sql` (the `AuditLog` query takes `row_limit` already; reuse with 50,000), tests; `frontend/src/views/AuditLogView.vue`.

**Interfaces:** `GET /api/v1/payments/audit-log.csv` with the list's filters minus `limit`; `Content-Type: text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="audit-log-YYYY-MM-DD.csv"`. Columns: `created_at,action,reason_codes,reasons,payer_name,amount,currency,payment_date,invoice_number,customer_name,actor_name,intake_source,confidence_score` (reason_codes only if PR C has merged, else omit). Written with `encoding/csv`; multi-value fields joined with `"; "`; timestamps RFC 3339 UTC. Register as a raw chi/huma streaming handler following how the PDF download route returns bytes (`invoices/documents.go`).
- [ ] **Step 1:** Failing test: filters respected; a payer name with a comma and a quote round-trips through `csv.Reader`; amount "1234.5000" unformatted; header row exact.
- [ ] **Step 2–4:** FAIL → implement → PASS; "Export CSV" button builds the URL from the current filters and downloads (`window.location` / anchor with the session cookie). **Step 5:** commit.

### Task 19: Invoices tiles, invoice header, next batch, AI icons

**Files:** Modify `backend/internal/stats/summary.go` (+ `OverdueCount, Due30dCount int`, `OldestOverdueDays *int`), `backend/internal/invoices/` detail response (+ `ClearedAt *platform.Time`, `ClearedBy *string` from the latest CLEARED/OVERRIDDEN log row over the invoice's payments, actor display name else email), a new `GET /api/v1/invoices/next-batch` → `{scheduler_enabled bool, next_batch_at *platform.Time}` (next slot = `jobs.DailySlot(jobs.RecurringScanHour)(now).AddDate(0,0,1)`; nil when the scheduler is off). **Deviation from the spec:** the spec puts `next_batch_at` on the awaiting-finalization response, but that response is a bare list; a separate endpoint avoids breaking it. Frontend: `InvoicesView.vue` tile second lines and "N days overdue"; `InvoiceFormView.vue` italic number H1, "Paid · cleared by X on date", notes sublabels; `AwaitingFinalizationView.vue` "Next batch: <local date time>" or "Scheduler off"; the AI Assistant view's decorative icons/badges (find the view by its "AI Assistant" route title in `router/index.ts`; compare with the mockup's AI Assistant screen) with `<AppIcon>`.
- [ ] **Step 1:** Failing Go tests: summary counts and oldest days; detail cleared fields (engine-cleared → ClearedBy nil and shown as "the matching engine"; manual → user name); next-batch before and after 02:00 UTC using the test clock, and scheduler off → null.
- [ ] **Step 2–4:** FAIL → implement → PASS; `./dev openapi`; frontend; `npm run build`.
- [ ] **Step 5:** Commit; extend `e2e/routes.spec.ts` for FX cards, Balanced, CSV download and next batch; PR gate ("UI revamp part 2D: screen extras").
