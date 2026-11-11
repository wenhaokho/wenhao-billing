# UI revamp, part 2: the mockup features that need new data — design

Date: 2026-11-12 · Status: draft for owner approval

## Why

Part 1 (PR #81 PrimeVue 4, PR #84 brand restyle) moved every screen onto the mockup's look. It left out every mockup
element that had no data behind it; PR #84's description lists them under "Mockup elements left for part 2". This
spec adds that data and the screens that show it. Part 3 (invoice/receipt PDFs, PR #82) has already shipped.

The owner handed the scope and approach to Claude ("just follow your recommendation", 2026-11-12). Every choice
below that isn't a fact about the code is Claude's call and is marked **(call)** where it isn't obvious.

Reference: the mockup is `docs/redesign/revamp/Wenhao Billing Revamp.dc.html` in the main checkout (it is not
committed; open it from `D:\Development\wenhao-billing\docs\redesign\revamp\`). The mockup's numbers are sample data.

## Goals

- The dashboard answers two questions at a glance: **who owes us and what do we chase today** (aging, needs attention
  with Send reminder, expected cash in) and **how is the business doing over a period** (KPIs with deltas, revenue by
  currency, top customers, reconciliation health).
- Every number comes from the database through a Go endpoint. No money arithmetic in the browser beyond formatting.
- The smaller items on other screens (FX, Reports, Audit Log, Manual Review, Invoices, invoice edit, Awaiting
  Finalization, AI Assistant) are filled in.
- Every reminder (dashboard or MCP) is recorded, and a customer gets at most one in 24 hours unless the user insists.

## Non-goals

- No change to how payments are matched or cleared. The engine gains one informational reason ("alias match") and
  structured reason codes; scores, thresholds and decisions stay identical, and a test pins that.
- No change to manual-review rules: the server keeps accepting what it accepts today (any non-DRAFT, non-VOID invoice).
  The new picker only makes choosing easier and warns on a currency mismatch.
- No historical FX: amounts in other currencies are converted to IDR at the latest rate, as the dashboard does today
  and as the mockup's subtitle says ("foreign currency converted at the latest rate").
- No P&L report, no job-history screen, no audit table beyond `reconciliation_log`.
- No `business_id` scoping. See Constraints.

## Anti-goals

- A number that looks precise but is invented. Where the data can't support a mockup element honestly (sparkline
  history for balances, see KPIs), the element is left out rather than approximated.
- A reminder sent without the user seeing who it goes to and for how much.

## Constraints (facts from the code)

- **Single tenant.** There is no `business_id` column anywhere; `business_profile` is the single row `id = 1`.
  CLAUDE.md's business_id rule predates that and does not apply; queries stay unscoped as every existing query is.
- Money is `numeric(19,4)` in Postgres, `shopspring/decimal` in Go, and `platform.Dec4` strings in JSON. Lint forbids
  `float64` except marked non-money values (day counts, percentages, confidence).
- Open AR rule (used everywhere today): `status IN ('OPEN','SENT','PARTIAL') AND is_template = false AND
  balance_due > 0`. Overdue adds `due_date < today`. "Today" is `platform.TodayUTC(clock)`, as in `stats/summary.go`.
- IDR equivalents use `fx.Convert` / `fx.GetRate` with today's rate (`stats/core.go`, `stats/revenue.go`). A currency
  with no rate is listed as unconverted, never silently dropped or treated as zero.
- Payments: `status` is `CLEARED`, `PENDING_MANUAL_REVIEW` or `FLAGGED`; `intake_source` is `WEBHOOK`,
  `EMAIL_PARSER` or `MANUAL`; `confidence_score numeric(5,2)`; one nullable `invoice_id`. Hold reasons are
  free-text `reconciliation_log.reasons text[]`, actions `CLEARED`, `HELD`, `OVERRIDDEN`, `REVERSED`.
- Customer aliases are `customers.matching_aliases jsonb`.
- Every Go test calls `t.Parallel()` and runs against a real Postgres (`backend/dev test`); schema changes come with a
  goose migration, `./dev sqlc`, and `./dev openapi` for route/schema changes.
- Each merge to main deploys to production and needs the owner's yes.

## Approaches considered

1. **One `GET /stats/overview` returning every dashboard card.** One request, but one slow or failing query blanks the
   whole dashboard, and the period selector would refetch point-in-time cards that don't depend on it.
2. **Endpoints grouped by what they depend on (chosen).** Period-driven figures in one endpoint, point-in-time
   receivables figures in another, the revenue chart extended in place. Each card group loads and fails on its own,
   the period selector refetches only what changes, and it matches the existing `/stats/*` layout.
3. **Compute in the browser from the list endpoints.** Rejected: it means money arithmetic in JS and loading every
   invoice and payment.

Chosen: 2. Picked by Claude on the owner's hand-over.

## Design

Delivery is four PRs, in this order, each merged only on the owner's yes **(call)**:

| PR | Contents |
|----|----------|
| A | Dashboard analytics (backend + frontend) |
| B | Send reminder (REST endpoint, reminder log, dashboard button) |
| C | Manual Review: via tag, reason tags, invoice picker |
| D | Small screen items: FX cards and last sync, Reports "Balanced", Audit Log CSV, Invoices KPI lines, invoice edit header, Awaiting Finalization next batch, AI Assistant icons |

### A. Dashboard analytics

**Period.** `period` query parameter: `30d`, `90d` or `12m` (default `12m`). The window is `[today − N, today]`
(12m = 12 calendar months back from today, inclusive). The comparison window is the equal-length window just before
it. Sparkline buckets: days for 30d, ISO weeks for 90d, calendar months for 12m **(call)**.

**`GET /api/v1/stats/overview?period=` (new, admin).** Response:

- `period`, `from`, `to`, `previous_from`, `previous_to`, `base_currency`, `unconverted` (currencies left out
  for lack of a rate).
- `kpis`:
  - `invoiced`: sum of `amount` of non-template, non-DRAFT, non-VOID invoices with `issue_date` in the window,
    IDR-eq.; `previous` the same for the comparison window; `series` one value per bucket.
  - `collected`: sum of `amount + wht_amount` of `CLEARED` payments with `payment_date` in the window, IDR-eq.;
    `previous`; `series`; `share_of_invoiced` (percent, null when invoiced is zero). PPh withheld by the customer is
    added back so Collected is on the same gross basis as Invoiced **(call: otherwise every PPh 23 client reads ~2%
    short and looks like a bug)**.
  - `open_ar`: today's open AR, IDR-eq. No series and no delta **(call: anti-goal — open AR in the past can't be
    reconstructed from current balances without inventing it; the ledger has it but at booking-date rates, which would
    disagree with the headline number)**. Second line: invoice count and customer count.
  - `overdue`: today's overdue, IDR-eq.; `share_of_open_ar` percent. No series.
  - `monthly_recurring`: the existing MRR (`stats/mrr.go`), IDR-eq.; `foreign_share` percent of it billed in a
    currency other than the base, with the largest such currency named.
- `days_to_pay`: `avg` and `previous_avg` (numbers, null when no payments), from the existing `DaysToPay` logic with
  the window as bounds (both ends, not just `since`).
- `reconciliation`: over payments with `created_at` in the window: `auto_cleared` (CLEARED with a `CLEARED`
  reconciliation_log row whose `actor_user_id` is null), `manual` (CLEARED otherwise — overridden or `MANUAL` intake),
  `held` (`PENDING_MANUAL_REVIEW` or `FLAGGED`), `avg_confidence` over engine-scored payments (non-null
  `confidence_score`, non-`MANUAL` intake), `aliases_in_use` (total aliases) and `customers_with_aliases`. The builder
  confirms the auto-vs-manual split against how `reconciliation_log` rows are actually written and documents it on the
  field.
- `top_customers`: customers ranked by invoiced IDR-eq. in the window (same invoice rule as `kpis.invoiced`), top 10,
  each with `customer_id`, `name`, `amount`, `share` percent and `overdue` (today's overdue IDR-eq., zero if none);
  plus `others` (`count`, `amount`, `share`) and `top3_share`.

**`GET /api/v1/stats/receivables` (new, admin).** Point-in-time, no period.

- `open_ar`, `past_due_share`, `invoice_count`, `customer_count`.
- `aging`: buckets `not_due`, `d1_30`, `d31_60`, `d61_90`, `d90_plus` by `today − due_date`, each `amount` IDR-eq.
  and `count`. Invoices with no `due_date` count as not due.
- `cash_in`: buckets `d0_30`, `d31_60`, `d61_90` ahead of today, each with `open_invoices` (open AR not yet overdue,
  by `due_date`) and `recurring` (the next runs of recurring templates and usage drafts in that bucket, from
  `stats/upcoming.go`, `estimate` true when any is a usage estimate), plus `overdue_excluded` (overdue total, shown as
  "not in forecast until collected").
- `needs_attention`: overdue invoices, oldest due date first, limit 6: `invoice_id`, `invoice_number`,
  `customer_id`, `customer_name`, `balance_due`, `expected_cash` (balance less PPh not yet booked,
  `withholding.ExpectedCash`, the figure the reminder asks for) and `currency` (native), `due_date`, `days_late`, and
  `last_reminder_at` (the customer's latest reminder; null until PR B lands).

**`GET /api/v1/stats/revenue-monthly` (extended).** Keep the current fields; add per month and per currency the native
`paid` and `unpaid` amounts (paid = `amount − balance_due`) and their IDR-eq., and add a `footer`: invoiced IDR-eq.
over the 12 months, `foreign_share`, `best_month` (label and amount) and `fx_exposure` (open AR in non-base
currencies, IDR-eq.). The chart's currency tabs switch between the stacked IDR-eq. view and one currency's native
amounts, as in the mockup.

**Frontend.** `PaymentIntakeWidget.vue` (the `/dashboard` view) gets the mockup layout: period selector in the page
header, the KPI row (sparklines only on Invoiced and Collected; delta shown as ▲/▼ percent vs the previous window,
green/red by direction of good), revenue card with currency tabs and legend, then aging + days to pay, expected cash
in, reconciliation donut, top customers, needs attention, and the existing recent activity. Data via
`@tanstack/vue-query`, one query per endpoint; period in the query key and the URL (`?period=`), so reload keeps it.
Each card shows `<EmptyState>` when it has nothing, and an "unconverted: X" note when a currency had no rate. Charts
follow the theme and dark mode as part 1's charts do. "Send reminder" appears in PR B.

### B. Send reminder

- Extract the reminder logic from the MCP tool (`mcp/tools_gated.go`, `send_payment_reminder`) into the invoices
  package; MCP and REST both call it, so the refusal rules stay in one place (VOID, PAID, DRAFT and templates refused).
- New migration: table `invoice_reminders` (`reminder_id uuid pk`, `invoice_id fk`, `sent_at timestamptz`,
  `sent_by_user_id` nullable, `channel` `'REST'|'MCP'`, `recipient text`). Both paths insert a row.
- `POST /api/v1/invoices/{id}/reminders` (admin) sends it and returns the row. `GET` on the same path lists them.
- **Repeat guard, per customer, in the shared function** **(call)**: if any reminder went to the same customer in the
  last 24 hours, sending refuses (REST: 409 naming the earlier invoice and time) unless `force: true`. The MCP tool
  gets a matching `force` input and reports the refusal in its preview, so neither path can bypass it.
- **Amount:** the reminder asks for the expected cash (balance less unbooked PPh), as the MCP tool does today. The
  confirm dialog, the email and the dashboard row all show that figure, with the balance due beside it when they
  differ.
- Dashboard: "Send reminder" opens a confirm dialog naming the recipient email, the invoice and the amount; on send,
  every row of that customer shows "reminded just now". `needs_attention.last_reminder_at` is filled from the table.
  The invoice detail page shows the reminder history.

### C. Manual Review

- **Via:** show `intake_source` as a tag (Bank webhook / Email / Manual). Frontend only if the review list already
  returns it; otherwise add it to the response.
- **Reason tags:** new migration adds `reconciliation_log.reason_codes text[] NOT NULL DEFAULT '{}'`. Codes are
  derived from the reason text by one Go function, `payments.ReasonCode(text)`, called in `logAction` for every row
  written (engine, service and manual paths) **(call: one mapping, used at write time and mirrored by the backfill, so
  codes can't drift from the text)**. Codes: `NO_CANDIDATE`, `CURRENCY_MISMATCH`, `AMOUNT_MISMATCH`,
  `LOW_CONFIDENCE`, `AMBIGUOUS`, `CLEAR_FAILED`, `EXACT_MATCH`, `MANUAL_ENTRY`, `MANUAL_APPROVAL`, `ALIAS_MATCH`,
  and `OTHER` for text it doesn't recognise. The migration backfills existing rows with the same prefix rules in SQL; a
  test runs every known text through both and compares.
- **Alias match:** the engine adds an informational "alias match: '<alias>' (<ratio>)" reason when the best payer
  similarity came from an alias rather than the customer name, **only on held payments** **(call: cleared payments'
  stored reasons stay byte-identical, so the audit log, MCP output and API don't change for them)**. The engine test
  pins decisions, scores and cleared-payment reasons unchanged.
- Tags are coloured through `src/theme/status.ts`; the text stays as the tag's explanation, as in the mockup.
- **Invoice picker:** extend `GET /invoices` with `q` (invoice number or customer name, case-insensitive substring)
  and `open=true` (the open AR rule). The review panel's picker searches with `open=true`, defaults to the payment's
  currency, shows number, customer, balance due and due date, and warns (does not block) when the chosen invoice's
  currency differs from the payment's or the payment exceeds the balance due.

### D. Small screen items

- **FX Rates:** a card per non-base currency with the latest rate, the previous rate (the latest row with an earlier
  `as_of_date`) and the change percent; and "Last sync" from a new `fx_sync_runs` table (`run_id`, `ran_at`,
  `source`, `trigger` `'SCHEDULED'|'MANUAL'`, `created`, `updated`, `actor_user_id`) written by both the weekly job and
  the manual sync. New `GET /api/v1/accounting/fx-rates/summary` returns both.
- **Reports:** "Balanced" (or "Out of balance by X") on the trial balance from the existing `total_debit_base` and
  `total_credit_base`, compared as decimals in the API response: add `balanced bool` and `difference` to the trial
  balance response rather than comparing strings in the browser **(call)**.
- **Audit Log:** `GET /api/v1/payments/audit-log.csv` with the same filters as the list, no 500-row cap (hard cap
  50,000), `Content-Disposition: attachment`, money as plain decimal strings, timestamps ISO 8601 UTC. An Export CSV
  button on `AuditLogView.vue` passes the current filters.
- **Invoices list:** the KPI tiles' second lines and the "N days overdue" note, from `/stats/invoices-summary` plus a
  count per tile (extend that response with counts and the oldest overdue days).
- **Invoice edit:** the italic invoice-number H1 and the notes sublabels (frontend only); the "Paid · cleared by X on
  date" line from the latest `CLEARED`/`OVERRIDDEN` reconciliation_log row of the invoice's payments (add
  `cleared_at` and `cleared_by` to the invoice detail response).
- **Awaiting Finalization:** "Next batch: <date time>" — the next `RecurringScanHour` slot, returned as
  `next_batch_at` on the awaiting-finalization response, computed with `jobs.DailySlot` so it can't drift from the
  scheduler; shown as "scheduler off" when `SCHEDULER_ENABLED` is false.
- **AI Assistant:** the decorative icons and badges, frontend only, with `<AppIcon>`.

## Testing

- Go: each new query and endpoint gets tests against the real Postgres with seeded invoices/payments in several
  currencies, including: a currency with no FX rate (lands in `unconverted`), an invoice with no due date, a template
  and a VOID invoice (excluded), window edges (first and last day), an empty database (zeros and nulls, not errors).
  The engine test pins that decisions and scores are unchanged with reason codes added. The backfill migration is
  tested on rows with each known reason prefix.
- Frontend: Playwright specs (`bash scripts/e2e.sh`) for the period selector (URL and refetch), each card's empty state,
  Send reminder confirm/409 flow, the picker's currency warning, and the CSV download; light, dark, 1440/900/375 as
  in part 1.
- Gate: `bash scripts/check.sh` passes before each PR.

## Left to the builder

- Exact SQL shape (one query with `GROUP BY` buckets or a few), names of Go types, and component splits.
- Copy for tooltips and empty states (software house wording).
- Whether `stats/overview` reuses `DashboardCore` pieces or calls the underlying queries directly.
