# Plan 8 contract inventory: reports and stats (frozen Python -> Go)

Read-only inventory. All paths are relative to `backend-python/` unless prefixed. Abbreviations:

| Abbr | File |
|---|---|
| RA | app/api/v1/routers/accounting.py (the five reports) |
| RS | app/api/v1/routers/stats.py |
| SS | app/services/stats.py |
| SCH | app/schemas/reports.py |
| SFX | app/services/fx.py (Go: backend/internal/fx) |
| RSC | app/services/recurring_schedule.py (Go: backend/internal/invoices/schedule) |
| DEPS | app/api/deps.py |
| CFG | app/config.py |
| DB | app/db/session.py |
| MCPM | app/mcp/tools/read_misc.py |
| SER | app/mcp/serialize.py |
| PN | backend/PORT-NOTES.md |
| DDL | backend/migrations/00001_baseline.sql |
| FE | frontend/src/views/ |
| MONEY | frontend/src/utils/money.ts |

Global facts:
- Routers: `stats.router` (RS:26, prefix `/stats`) and `accounting.router` (RA:42, prefix `/accounting`) are mounted under `/api/v1` (app/main.py:94, :97). All 8 routes are GET, admin-only via `Depends(current_admin)` (DEPS:18-34: 401 `"not authenticated"`, 401 `"session expired"`, 403 `"admin required"`). No route writes, commits or locks.
- Validation errors are FastAPI's default 422 (only `as_of` can fail: a value that is not a date).
- **"Today" is server-local** everywhere in scope: `Query(default_factory=date.today)` on all five reports (RA:243, :314, :389, :558, :607), `date.today()` in the stats routes (RS:191, :251, :286) and in `compute_dashboard_stats` (SS:464). PN 5 already records this. Go today uses the UTC calendar date (`todayUTC`, backend/internal/invoices/core.go:393-395), which equals Python in a UTC container.
- The frontend **always sends `as_of`**: `asOf = ref(new Date())`, `asOfStr = asOf.value.toISOString().slice(0, 10)` (FE/ReportsView.vue:101-102), i.e. the **browser's UTC date**, not its local date. The server default only matters to direct API callers.
- Base currency: `Settings.base_currency`, default `"IDR"`, `min_length=3, max_length=3` (CFG:48).
- Decimal arithmetic: Python default context (28 significant digits, ROUND_HALF_EVEN); no `getcontext`/`localcontext` anywhere in app/. Every `quantize(Decimal("0.0001"))` in scope is **half-even to 4 places**: SFX:72 (convert), RA:494 (revaluation), SS:133 (MRR), SS:266 and :273 (monthly revenue). Everything else is unrounded sums/differences of `numeric(19,4)` values, which are exact at 4 places.
- Rendering: Pydantic v2 (`pydantic[email]>=2.7`, pyproject.toml:11) prints a `Decimal` as `str(Decimal)`. So a DB sum prints with 4 places (`"1000.0000"`), an untouched `Decimal("0")` start value prints `"0"`, a rate prints with 8 places (`"16000.00000000"`, `fx_rates.rate numeric(19,8)`, DDL:185). Spec: computed values at 4 places; Go `platform.Dec4` prints `"0.0000"` where Python prints `"0"` (allowed difference, record a port note). `avg_days_to_pay` is a Python `float` (JSON number).
- No `business_id` in any query in scope (single tenant).
- Reversed journal entries: **no report filters `reversed_by_entry_id`**. An original and its reversal (same accounts, debit/credit swapped, SL:222-251 in the plan 7 inventory) are both summed and net to zero. That is correct for these balance reports; keep.
- `journal_entries.posted_at` is `timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL` (DDL:296-303). The cutoff is `datetime.combine(as_of, time.max)` = `as_of 23:59:59.999999` naive (RA:236-238). Go: `posted_at < as_of + 1 day` is the same at Postgres's microsecond precision.

---

## 0. Storage and response shapes

### Tables read
- chart_of_accounts (DDL:89-95): account_id integer, code varchar(10), name varchar(100), type varchar(50) (no CHECK; the API only accepts `ASSET, LIABILITY, EQUITY, INCOME, EXPENSE, COGS`, app/schemas/item.py:117), active bool.
- journal_entries (DDL:296-303), journal_lines (DDL:309-320): debit, credit, base_amount_debit, base_amount_credit numeric(19,4) NOT NULL default 0; currency varchar(3); fx_rate numeric(19,8).
- fx_rates (DDL:181-189): rate numeric(19,8); `UNIQUE (from_currency, to_currency, as_of_date)` (DDL:708-709), so "newest on or before" has no tie.
- invoices (DDL:228-262): customer_id NULL, balance_due numeric(19,4) NOT NULL, status, billing_cycle_ref jsonb, issue_date/due_date date NULL, is_template bool NOT NULL default false.
- bills (DDL:27-45): **vendor_id NOT NULL**, no is_template column, issue_date/due_date NULL.
- quotations (DDL:436-450): amount numeric(19,4), status.
- payments, reconciliation_log, users (display_name NULL, email NOT NULL, DDL:477-490), customers (has `active`, DDL:140-160).

### TrialBalanceReport (SCH:21-28), fields in order
as_of date, base_currency str, rows list[TrialBalanceRow], total_debit_base Decimal, total_credit_base Decimal.
TrialBalanceRow (SCH:7-18): account_id int, code str, name str, type str, currency str, debit Decimal, credit Decimal, net Decimal, base_debit Decimal, base_credit Decimal, base_net Decimal.
Scale: row values 4 places (DB sums); totals 4 places, or `"0"` when there are no rows (start value `Decimal("0")`, RA:273-274).

### BalanceSheetReport (SCH:44-52), fields in order
as_of date, base_currency str, groups list[BalanceSheetGroup], assets_total_base, liabilities_total_base, equity_total_base, income_total_base, expense_total_base (all Decimal).
BalanceSheetGroup (SCH:38-41): type str, total_base Decimal, lines list[BalanceSheetLine]. BalanceSheetLine (SCH:31-35): account_id int, code str, name str, base_balance Decimal.
Scale: lines and group totals 4 places; a missing type's total prints `"0"` (`type_totals.get(t, Decimal("0"))`, RA:373-378).

### FxRevaluationReport (SCH:70-76), fields in order
as_of date, base_currency str, lines list[FxRevaluationLine], total_unrealised_gain Decimal, total_unrealised_loss Decimal, missing_rates list[str].
FxRevaluationLine (SCH:55-67): account_id int, code str, name str, type str, currency str, face_balance Decimal, original_base Decimal, revalued_base Decimal|null, rate Decimal|null, rate_as_of date|null, unrealised_gain_loss Decimal|null, rate_missing bool (default False).
Scale: face/original 4 places; revalued 4 (quantized); rate 8 (DB); unrealised 4, **`"-0.0000"` for a LIABILITY line with no change** (§5.6); totals `"0"` when nothing was added.

### AgingReport (SCH:98-102), fields in order
as_of date, kind str (`"AR"`/`"AP"`), rows list[AgingRow], totals_by_currency dict[str, AgingBuckets].
AgingRow (SCH:91-95): party_id str, party_name str, currency str, buckets AgingBuckets. AgingBuckets (SCH:82-88): current, d_1_30, d_31_60, d_61_90, d_90_plus, total (Decimal).
Scale: every bucket starts at `_ZERO = Decimal("0")` (RA:532-539) and prints `"0"` unless something was added, then 4 places (`"0" + "500.0000"` = `"500.0000"`).

### DashboardStats (RS:101-115), fields in order
1. awaiting_review_count int
2. draft_count int
3. sent_count int
4. customer_count int
5. auto_cleared_last_30d int
6. open_ar_by_currency list[CurrencyAmount]
7. open_quotation_count int
8. open_quotation_pipeline list[CurrencyAmount]
9. recent_activity list[ActivityItem]
10. mrr_by_currency list[CurrencyAmount]
11. base_currency str
12. open_ar_base_by_currency list[CurrencyBaseAmount]
13. open_ar_unconverted list[str]
14. upcoming_invoices list[UpcomingInvoiceItem]

CurrencyAmount (RS:29-31): currency str, amount Decimal. CurrencyBaseAmount (RS:34-36): currency str, base_amount Decimal.
ActivityItem (RS:50-68): log_id UUID, payment_id UUID, action str, reasons list[str], actor_user_id UUID|null, created_at datetime (naive), payer_name str|null, amount Decimal|null, currency str|null, payment_date date|null, payer_reference str|null, wht_amount Decimal|null, confidence_score Decimal|null, intake_source str|null, invoice_id UUID|null, invoice_number str|null, customer_name str|null, actor_name str|null. Scales from DB: amount/wht_amount 4 places, confidence_score **2 places** (`"97.50"`, Go `Dec2`).
UpcomingInvoiceItem (RS:39-47): invoice_id UUID, customer_id UUID|null, customer_name str|null, mode str (`"RECURRING"`/`"USAGE"`), next_date date, amount Decimal (DB, 4 places), currency str, amount_is_estimate bool.

### MonthlyRevenueResponse (RS:86-92), fields in order
base_currency str, currencies list[str], unconverted list[str], months list[MonthlyRevenueBucket].
MonthlyRevenueBucket (RS:71-83): month str `"YYYY-MM"`, totals dict[str, Decimal] (base), native_totals dict[str, Decimal], unpaid_totals dict[str, Decimal] (base), native_unpaid_totals dict[str, Decimal]. Note the response keys `totals`/`unpaid_totals` are the service's `base_totals`/`base_unpaid_totals` (RS:258-264).

### InvoicesSummaryResponse (RS:95-98), fields in order
overdue list[CurrencyAmount], due_30d list[CurrencyAmount], avg_days_to_pay float|null.

---

## 1. Routes (8)

### GET /api/v1/accounting/reports/trial-balance -> 200 TrialBalanceReport (RA:241-303)
Query: `as_of` date, default server-local `date.today()` (RA:243).
SQL (RA:248-271): chart_of_accounts **JOIN** journal_lines ON account_id **JOIN** journal_entries ON entry_id WHERE `posted_at <= as_of 23:59:59.999999`; GROUP BY account_id, code, name, type, line currency; `ORDER BY code, currency` (code is unique: 409 `"account code already exists"`, RA:69-71). SELECT `coalesce(sum(debit),0)`, credit, base_amount_debit, base_amount_credit.
- Inactive accounts included (no `active` filter). Accounts with no lines up to the cutoff are absent (inner join). Reversed and reversal entries both counted.
- Per row: `net = debit - credit`, `base_net = base_debit - base_credit` (RA:289, :292), whatever the account type (SCH:15 comment).
- `total_debit_base`/`total_credit_base` = sum of row base_debit/base_credit (RA:295-296). No per-currency totals.
- Empty: `rows: []`, totals `"0"`.
- FE reads code, name, type, currency, debit, credit, base_debit, base_credit, total_debit_base, total_credit_base (FE/ReportsView.vue:197-240); **not** net or base_net. All money read as strings (`formatAmount` takes number|string, MONEY:20-31).

### GET /api/v1/accounting/reports/balance-sheet -> 200 BalanceSheetReport (RA:312-379)
Query: `as_of` date, default `date.today()` (RA:314).
SQL (RA:319-342): chart_of_accounts **LEFT JOIN** journal_lines ON account_id **LEFT JOIN** journal_entries ON `entry_id = line.entry_id AND posted_at <= cutoff`; SELECT `coalesce(sum(line.base_amount_debit),0)`, `coalesce(sum(line.base_amount_credit),0)`; GROUP BY account_id, code, name, type; ORDER BY code.
- **The cutoff does not filter anything** (§5.1): it sits in the ON of the second outer join, and the sums read journal_lines columns, so lines of entries posted after `as_of` are still summed. The report is "all time" whatever `as_of` is.
- Sign (RA:306-309, :349-352): type in `{"ASSET", "EXPENSE", "COGS"}` -> `balance = base_debit - base_credit`; any other type (LIABILITY, EQUITY, INCOME, and any unknown type) -> `base_credit - base_debit`.
- Skip when `balance == 0 and base_debit == 0 and base_credit == 0` (RA:354-355): an account with offsetting activity (e.g. 100 debit, 100 credit) is **kept** with balance `"0.0000"`.
- groups in fixed order `ASSET, LIABILITY, EQUITY, INCOME, EXPENSE, COGS`, only types that have a line (RA:363-367); a type outside the six is summed into `type_totals` but never shown. Lines in code order within a group.
- `expense_total_base = EXPENSE + COGS` (RA:377-378). No retained earnings or current-year result line, so assets != liabilities + equity whenever there is P&L activity (§5.2).
- Empty: `groups: []`, five totals `"0"`. FE shows an empty state when `!groups.length` (FE/ReportsView.vue:257).
- FE reads assets/liabilities/equity totals (:250-254), group type/total_base/lines, line account_id/code/name/base_balance (:262-274). **Does not show** income_total_base or expense_total_base (declared at :53-54 only).

### GET /api/v1/accounting/reports/fx-revaluation -> 200 FxRevaluationReport (RA:387-526)
Query: `as_of` date, default `date.today()` (RA:389). Report only, posts nothing (RA:399).
SQL (RA:404-429): as trial balance (inner joins, cutoff in WHERE, so the cutoff **works** here) plus `type IN ('ASSET','LIABILITY')` (RA:384, :419) and `line.currency != base_currency` (RA:420, exact case); GROUP BY account, currency; ORDER BY code, currency.
Per bucket (RA:454-517):
- ASSET: `face = debit - credit`, `original_base = base_debit - base_credit`, sign +1; LIABILITY: `face = credit - debit`, `original_base = base_credit - base_debit`, sign -1 (RA:459-466).
- skip when `face == 0 and original_base == 0` (RA:469). A bucket with face 0 but a base residue is kept and revalued to 0 (§5.12).
- rate: own query (RA:434-447), **not** `fx.get_rate`: `fx_rates WHERE from_currency = currency AND to_currency = base AND as_of_date <= as_of ORDER BY as_of_date DESC LIMIT 1`, no upper-casing, cached per currency for the request.
- no rate: line with revalued_base, rate, rate_as_of, unrealised_gain_loss **null**, `rate_missing: true`; currency added to `missing_rates` (RA:473-491).
- else `revalued_base = (face * rate).quantize(0.0001)` half-even (RA:494); `unrealised = (revalued_base - original_base) * sign` (RA:496); `> 0` adds to total_unrealised_gain, `< 0` adds `-unrealised` to total_unrealised_loss (RA:497-500); `rate` = the DB rate (8 places), `rate_as_of` = that row's as_of_date.
- `missing_rates = sorted(set)` (RA:525).
- Empty: `lines: []`, totals `"0"`, `missing_rates: []`.
- FE reads totals (:288-292), missing_rates (:296-300), code, name, currency, face_balance, original_base, rate (via `formatRate`, `Number(v)`, 2-8 decimals, :144-150), rate_as_of, revalued_base (`!== null`), unrealised_gain_loss (`gainLossSeverity`, `Number(v)`, :152-158), as_of (:284).

### GET /api/v1/accounting/reports/ar-aging -> 200 AgingReport (RA:556-602)
Query: `as_of` date, default `date.today()` (RA:558).
SQL (RA:567-579): `SELECT customer_id, currency, due_date, balance_due, customers.name FROM invoices LEFT JOIN customers WHERE status IN ('OPEN','SENT','PARTIAL') AND is_template = false AND balance_due > 0` — **no ORDER BY**, no `as_of` filter on issue_date (an invoice issued after `as_of` is aged too).
Per invoice (RA:583-595):
- key `(str(customer_id) if customer_id else "-", currency)`; new row: party_id = key[0], party_name = `name or "(no customer)"`, currency, empty buckets.
- `days = (as_of - due_date).days if due_date else 0`: a NULL due_date is **current**.
- bucket (RA:542-553): `days <= 0` current; `<= 30` d_1_30; `<= 60` d_31_60; `<= 90` d_61_90; else d_90_plus; always `total += amount`. Same bucket added to `totals[currency]`.
- rows `sorted(..., key=(party_name.lower(), currency))` (RA:600): stable, so ties keep first-seen order of an unordered query. totals_by_currency keys in first-seen order (a JSON object; the FE iterates it, FE/ReportsView.vue:364).
- kind `"AR"`. Empty: `rows: []`, `totals_by_currency: {}`.

### GET /api/v1/accounting/reports/ap-aging -> 200 AgingReport (RA:605-650)
As AR with: bills LEFT JOIN vendors; `status IN ('OPEN','PARTIAL')` (no SENT) and `balance_due > 0`; no template filter (bills have none); party_name `name or "(no vendor)"`; kind `"AP"` (RA:616-648). vendor_id is NOT NULL with an FK (DDL:29), so `"-"` and `"(no vendor)"` are unreachable.
FE (both agings): party_name, currency, every bucket and total via `formatAmount` (strings), totals_by_currency `b.total` (FE/ReportsView.vue:364-436).

### GET /api/v1/stats/dashboard -> 200 DashboardStats (RS:118-232)
No parameters. Calls `compute_dashboard_stats(db)` (SS:451-554, §2.1) for fields 1-8 and 10-13, then adds:
- recent_activity (RS:134-189): reconciliation_log LEFT JOIN payments ON payment_id LEFT JOIN invoices ON `payments.invoice_id` LEFT JOIN customers ON `customer_id = coalesce(payments.customer_id, invoices.customer_id)` LEFT JOIN users ON `actor_user_id`; `ORDER BY reconciliation_log.created_at DESC LIMIT 10`, no tie-break. `reasons or []`; `actor_name = coalesce(users.display_name, users.email)` (an empty display_name stays `""`).
- upcoming_invoices (RS:191-213): `compute_upcoming_invoices(db, today=date.today())` (§2.5), then one `SELECT customer_id, name FROM customers WHERE customer_id IN (...)`; customer_name = that name, or null when customer_id is null.
FE (FE/PaymentIntakeWidget.vue): types at :12-58 (every Decimal typed `string`), polls every 30 s (:76-80). Reads the five counts (:355-391, :442), open_quotation_count and pipeline (`[top, ...rest]` = server order, :84-91), open_ar_by_currency as a currency -> amount map for the pie tooltip (:247-251), open_ar_base_by_currency (`Number(r.base_amount)`, order = pie order, :229-245), base_currency (:255), open_ar_unconverted (:458-459), mrr_by_currency (:400-404), upcoming_invoices (:473-485: invoice_id key, mode, customer_name `?? '—'`, amount, currency, amount_is_estimate, next_date), recent_activity (:499-524: every ActivityItem field).

### GET /api/v1/stats/revenue-monthly -> 200 MonthlyRevenueResponse (RS:235-272)
No parameters. `compute_monthly_revenue_base(db, today=date.today())` (§2.4); `currencies = sorted(union of base_totals keys)` (RS:253-256, :269) — only currencies that converted and had revenue; `unconverted` passed through (sorted).
FE (FE/PaymentIntakeWidget.vue:94-197, :227, :430-431): months in order, `m.totals[ccy]`, `m.unpaid_totals[ccy]`, `native_totals`, `native_unpaid_totals` via `Number(... ?? 0)` (a missing key is 0), currencies (one dataset per currency), base_currency, unconverted. Polls every 60 s.

### GET /api/v1/stats/invoices-summary -> 200 InvoicesSummaryResponse (RS:275-291)
No parameters. `compute_invoices_summary(db, today=date.today())` (§2.6).
FE (FE/InvoicesView.vue:40-49, :91-111): amount typed string, re-sorted client-side by `Number(b.amount) - Number(a.amount)` (:97-102, so server order is irrelevant); `avg_days_to_pay` number|null, shown `v.toFixed(1) + " days"` or `"—"`.

---

## 2. Computation rules

### 2.1 compute_dashboard_stats (SS:451-554)
`today = date.today()` (SS:464). Shared with MCP `get_stats` (MCPM:60-89).
- awaiting_review_count: `count(payments) WHERE status = 'PENDING_MANUAL_REVIEW'` (SS:466-473).
- draft_count: `count(invoices) WHERE status = 'DRAFT' AND is_template = false` (SS:478-486).
- sent_count: `count(invoices) WHERE status IN ('OPEN','SENT')` — no template filter (SS:490-495).
- customer_count: `count(customers)` — inactive included (SS:497).
- auto_cleared_last_30d: `count(payments) WHERE status = 'CLEARED' AND created_at >= (Python UTC now, naive) - 30 days` (SS:501-510). A full timestamp, not a date. Counts **every** CLEARED payment (auto-cleared intake, manual record-payment `intake_source='MANUAL'`, manual-review approvals); FLAGGED (reversed) ones drop out.
- open_ar_by_currency: `SELECT currency, sum(balance_due) FROM invoices WHERE status IN ('OPEN','SENT','PARTIAL') GROUP BY currency`, **no ORDER BY, no is_template filter, no `balance_due > 0`** (SS:512-517). `a or Decimal("0")` (sum is never NULL here).
- open_quotation_count: `count(quotations) WHERE status IN ('SENT','ACCEPTED')` (SS:67, :519-526).
- open_quotation_pipeline: `sum(amount) GROUP BY currency` same statuses, then `sort(key=amount, reverse=True)` on Decimals (SS:527-534). Stable: equal totals keep query order (unordered).
- mrr_by_currency: §2.2. open_ar_base_by_currency, open_ar_unconverted: §2.3 (both with `today`).
- base_currency: CFG:48.
- Empty DB: counts 0, every list `[]` (test_empty_db_returns_zero_counts_and_no_currency_rows).

### 2.2 MRR (SS:73-137)
- Templates: `invoices WHERE invoice_type = 'RECURRING' AND is_template = true`, no status filter, no ORDER BY (SS:89-93). Skip `is_paused(billing_cycle_ref)` (RSC:211-215: falsy cfg -> not paused; `bool(cfg.get("paused", False))`), skip `ScheduleError` from `parse_schedule` (RSC:65+; an empty/NULL cfg raises `"schedule config is empty"`), skip unless `current_cycle(s, today) is not None or next_cycle_after(s, today) is not None` (SS:73-78) (SS:94-105).
- monthly equivalent, **unquantized** (SS:108-119): MONTHLY `amount / interval`; YEARLY `amount / (12 * interval)`; WEEKLY `amount * 52 / (12 * interval)`; DAILY `amount * 365 / (12 * interval)`. Decimal division in the 28-digit half-even context.
- sum per currency, then `quantize(0.0001)` half-even per currency total (SS:128-135), sort by amount desc (stable; ties in dict insertion order from the unordered query) (SS:136).
- Pinned values: 1200 monthly -> `1200.0000`; yearly 1200 -> `100.0000`; weekly 100 -> `433.3333`; 900 every 3 months -> `300.0000` (tests/integration/test_mrr_stats.py:56-79).

### 2.3 Open AR in base (SS:140-168)
Same query as open_ar_by_currency (SS:150-154: open statuses, no template or `> 0` filter, no ORDER BY). Per currency total: `convert(db, amount, currency, today).base_amount` = `(amount * rate).quantize(0.0001)` half-even, rate = newest `as_of_date <= today`, base currency -> rate 1 without a query (SFX:36-73). `FxRateMissing` -> currency appended to unconverted, skipped. Rows `sort(key=base_amount, reverse=True)` (stable), `unconverted.sort()`. Conversion is of the **per-currency sum**, not per invoice.
Pinned: USD 100 at 16000 -> `"1600000.0000"`; IDR 5000000 -> `5000000.0000` (tests/integration/test_open_ar_base.py:45-60; RS output `"1600000.0000"`, test_dashboard_stats.py:291).

### 2.4 Monthly revenue (SS:171-284)
- labels: 12 `"YYYY-MM"` ending with today's month, oldest first (SS:174-185). window_start = first day of the oldest month (SS:213-215).
- SQL (SS:217-232): `SELECT to_char(issue_date,'YYYY-MM') AS month, currency, sum(amount), sum(balance_due) FROM invoices WHERE status IN ('OPEN','SENT','PARTIAL','PAID') AND issue_date IS NOT NULL AND issue_date >= window_start AND is_template = false GROUP BY month, currency` — no ORDER BY, **no upper bound** (future months are dropped by `if month in native`, SS:237).
- native_totals[month][ccy] = `Decimal("0") + sum(amount)` (4 places; a zero-amount invoice gives an entry `"0.0000"`) (SS:238-240). native_unpaid[month][ccy] = sum(balance_due) **only if truthy** (non-zero) (SS:241-242).
- Rate: `get_rate(db, ccy, today)` once per currency, **today's rate for every month** (SS:244-256); missing -> None and the currency goes to `unconverted`, discovered lazily (only currencies that appear).
- base_totals[ccy] = `(native * rate).quantize(0.0001)` half-even; base_unpaid likewise; unconverted currencies omitted from both base dicts but kept in both native dicts (SS:258-282).
- Dict key order inside each month follows the unordered SQL rows (JSON object order, harmless: FE indexes by key).
- Pinned: July 2026 TODAY -> labels `2025-08`..`2026-07`; IDR -> `5000000.0000`; USD 150 -> `2400000.0000`; unpaid USD 40 -> `640000.0000`; a fully paid month has `{}` unpaid (tests/integration/test_revenue_monthly_base.py:49-159).

### 2.5 Upcoming invoices (SS:287-359), horizon 90 days
- horizon = today + 90 (inclusive) (SS:310).
- Recurring: each active template of §2.2; `nxt = next_cycle_after(schedule, today)` — the first cycle starting **strictly after** today (RSC:194-204); skip None or `> horizon`; row amount = template.amount (DB), currency, customer_id, `amount_is_estimate=False` (SS:314-328). One row per template.
- Usage: `invoices WHERE invoice_type = 'USAGE' AND status = 'DRAFT'` (no is_template filter, no ORDER BY) (SS:331-335); `cfg = billing_cycle_ref or {}`; skip truthy `cfg.get("locked")`; `dom = cfg.get("cut_off_day")` must satisfy `isinstance(dom, int) and 1 <= dom <= 31` (Python bool is an int: JSON `true` passes as 1; `"20"` and `20.0` are skipped — stricter than the usage-lock worker's `int()`); `nxt = _next_day_of_month(today, dom)`: smallest date **>= today** whose day is dom, clamped to month length (SS:287-300); skip `> horizon`; amount = inv.amount, `amount_is_estimate=True` (SS:336-356).
- sort by `(next_date, str(invoice_id))` (SS:358) — deterministic. Go: compare the canonical lowercase UUID string, which equals byte order of the UUID.
- Pinned (TODAY 2026-07-24): monthly from Jan 5 -> 2026-08-05; usage day 20 -> 2026-08-20; day 28 -> 2026-07-28 (tests/integration/test_upcoming_invoices.py:60-118).

### 2.6 Invoices summary (SS:362-448)
- `_bucket` (SS:362-374): `SELECT currency, sum(balance_due) FROM invoices WHERE status IN ('OPEN','SENT','PARTIAL') AND is_template = false AND balance_due > 0 AND due_date IS NOT NULL AND <clause> GROUP BY currency`, no ORDER BY.
- overdue: `due_date < today`; due_30d: `due_date >= today AND due_date <= today + 30` (SS:438-443). Due today -> due_30d; +30 included; +31 excluded.
- avg_days_to_pay (SS:377-398): `SELECT payments.payment_date, invoices.issue_date FROM payments JOIN invoices ON invoice_id WHERE invoices.status = 'PAID' AND issue_date IS NOT NULL AND payments.status = 'CLEARED' AND payment_date >= today - 90`; mean of `(payment_date - issue_date).days` **per payment row** (an invoice paid in two payments counts twice); no upper bound on payment_date; negative deltas allowed; `None` when no rows. Python float true division (`sum / len`), e.g. `15.0`, `13.333333333333334`.

### 2.7 Python call sites outside the routes (Go functions must suit them)
- MCP `get_stats` (MCPM:60-89): calls `compute_dashboard_stats`, returns keys in order awaiting_review_count, draft_count, sent_count, customer_count, auto_cleared_last_30d, open_ar_by_currency, open_quotation_count, open_quotation_pipeline, mrr_by_currency, base_currency, open_ar_base_by_currency, open_ar_unconverted — no recent_activity, no upcoming_invoices. Decimals via `coerce_value` = `str(Decimal)` (SER:10-20): `"1234.5600"`, `"1200.0000"`. The Go service must return the 12 fields as a struct the MCP plan can reuse without the HTTP layer.
- No MCP tool and no worker calls the five accounting reports, `compute_monthly_revenue_base`, `compute_upcoming_invoices`, `compute_invoices_summary` or `compute_mrr_by_currency` directly (grep of app/mcp and app/workers).
- `compute_customer_balances` (SS:401-434) is called by `GET /customers/balances` (app/api/v1/routers/customers.py:65) and is **already ported** (`invoices.CustomerBalances`, backend/internal/invoices/customers.go:56; ROUTES.md:44). Out of scope.

Suggested Go shape: a `reports` package (5 routes) and a `stats` package (3 routes) under backend/internal, both taking `today` from one clock helper; `stats.Dashboard(ctx, q, today, now) (DashboardCore, error)` for fields 1-8, 10-13 (MCP reuse), `stats.MRR`, `stats.OpenARBase`, `stats.MonthlyRevenue`, `stats.Upcoming`, `stats.InvoicesSummary`, each with `today time.Time`.

---

## 3. Tests in scope

[IN] = this plan. [OUT] = later plan or already ported.

tests/integration/test_dashboard_stats.py (service + 4 route tests):
- [IN] test_empty_db_returns_zero_counts_and_no_currency_rows
- [IN] test_counts_reflect_invoice_and_customer_state — draft 1, sent 2, customers 1.
- [IN] test_open_ar_by_currency_sums_sent_and_partial_only — USD 800, SGD 800, DRAFT excluded.
- [IN] test_open_quotation_pipeline_sums_sent_and_accepted_only_sorted_desc — SGD 2000 before USD 500.
- [IN] test_auto_cleared_last_30d_counts_only_cleared_payments
- [IN] test_awaiting_review_count_counts_pending_manual_review_payments
- [IN] test_dashboard_endpoint_matches_service_function — route equals service.
- [IN] test_recent_activity_includes_payment_invoice_and_actor_details — customer via coalesce, actor_name `"Dana"`, confidence `97.50`, payment_date `"2026-07-01"`.
- [IN] test_recent_activity_details_empty_for_unmatched_system_payment — invoice/customer/actor null.
- [IN] test_draft_count_excludes_recurring_templates
- [IN] test_dashboard_stats_includes_mrr_and_base_ar — mrr `1200.0000`, base AR `1600000.0000`, base_currency `IDR`.
- [IN] test_dashboard_endpoint_exposes_new_fields — `"1600000.0000"` string in JSON, upcoming modes.

tests/integration/test_invoices_summary.py (TODAY 2026-04-22, service level):
- [IN] test_empty_db_returns_zero_buckets_and_null_avg; test_sent_invoice_past_due_appears_in_overdue; test_partial_invoice_past_due_uses_balance_due_not_amount; test_overdue_sums_multiple_invoices_same_currency; test_overdue_separates_by_currency; test_sent_invoice_due_today_is_in_due_30d_not_overdue; test_sent_invoice_due_in_30_days_inclusive; test_sent_invoice_due_in_31_days_excluded; test_draft_invoice_excluded_from_buckets; test_void_invoice_excluded_from_buckets; test_template_invoice_excluded; test_paid_invoice_excluded_from_overdue_and_due_30d; test_avg_days_to_pay_single_invoice (10.0); test_avg_days_to_pay_averages_across_invoices (15.0); test_avg_days_to_pay_ignores_payments_older_than_90_days; test_avg_days_to_pay_ignores_non_cleared_payments. (16)

tests/integration/test_mrr_stats.py (TODAY 2026-07-24):
- [IN] test_empty_db_returns_no_rows; test_monthly_interval_1_is_full_amount; test_yearly_divided_by_twelve; test_weekly_times_52_over_12; test_monthly_interval_3_is_third; test_paused_template_excluded; test_ended_template_excluded; test_non_template_recurring_excluded; test_grouped_and_sorted_desc. (9)

tests/integration/test_open_ar_base.py (TODAY 2026-07-24):
- [IN] test_empty_db_returns_empty; test_base_currency_ar_uses_rate_one; test_foreign_currency_converted_via_seeded_rate (also order USD before SGD); test_missing_rate_currency_lands_in_unconverted; test_draft_excluded. (5)

tests/integration/test_revenue_monthly_base.py (TODAY 2026-07-24):
- [IN] test_labels_span_12_months_oldest_first; test_base_currency_uses_rate_one; test_foreign_currency_converted_via_seeded_rate; test_missing_rate_currency_reported_unconverted; test_draft_template_and_out_of_window_excluded; test_same_currency_summed_across_invoices_in_a_month; test_unpaid_split_tracks_remaining_balance; test_fully_paid_month_has_no_unpaid_entry; test_unconverted_currency_has_no_base_unpaid. (9)

tests/integration/test_upcoming_invoices.py (TODAY 2026-07-24):
- [IN] test_empty_db_returns_empty; test_recurring_next_occurrence_within_window; test_recurring_reported_once_even_if_multiple_cycles_in_window; test_recurring_beyond_window_excluded; test_paused_recurring_excluded; test_usage_next_cutoff_is_estimate; test_usage_locked_or_no_cutoff_skipped; test_usage_non_draft_skipped; test_mixed_sorted_by_date. (9)

Out of scope but touching these services:
- [OUT] tests/integration/test_mcp_read_misc.py::test_get_stats_returns_dict_with_expected_keys, ::test_get_stats_renders_money_as_exact_decimal_strings (MCP plan; TESTS.md:167-168 already say "stats plan" — the MCP plan should port them on top of this plan's `stats.Dashboard`).
- [OUT] tests/integration/test_customer_balances.py, 10 tests (already `ported`, TESTS.md:17-26).

Counts: **60 [IN]**, 2 [OUT] (plus 10 already ported). All 60 are `todo` rows in backend/TESTS.md (lines 39-50, 78-93, 259-272, 339-347, 354-362). Every seeded rate they rely on (USD 16000, SGD 12000 to IDR as of 2026-01-01) is carried by `apitest.New` (TESTS.md:7; tests/conftest.py:524-539).
**No Python test covers any of the five accounting report routes**, nor the revenue-monthly and invoices-summary routes (only their service functions). Go must write route tests for all five reports from §1-§2 (at least: cutoff, sign per type, skip rules, missing rate, bucket edges 0/1/30/31/60/61/90/91, null due date, ordering, empty case) and one route test each for revenue-monthly and invoices-summary.

---

## 4. Existing Go code

- **fx** (backend/internal/fx/fx.go): `func GetRate(ctx context.Context, q *fxdb.Queries, from string, on time.Time, to string) (decimal.Decimal, error)` (:39, upper-cases both, same currency -> 1), `func Convert(ctx context.Context, q *fxdb.Queries, amount decimal.Decimal, from string, on time.Time, to string) (Conversion, error)` (:56, `RoundHalfEven4(amount.Mul(rate))`), `Conversion{BaseAmount, Rate}` (:33), `ErrRateMissing`. Query `GetRate` (fx/queries.sql:1-7) returns only `rate`. **Gap**: the revaluation needs `as_of_date` too — add a query returning `rate, as_of_date` (and keep RA's no-upper-case behaviour, or upper-case and note it; journal currencies are upper case in practice).
- **platform**: `RoundHalfEven4(d) = d.RoundBank(4)` (round.go:7); `Dec4` (types.go:233; marshals `d.StringFixed(4)`, types.go:173-178, so any value with more than 4 places would be rounded **half away from zero** at print time — quantize first), `Dec8` for `rate` (types.go:266), `Dec2` for `confidence_score` (types.go:299), `Date` (types.go:331), `Time` (types.go:361, UTC with Z), `OptionalParam[T]` for an `as_of` query with a default (types.go:526; pattern `query:"on_date" timeFormat:"2006-01-02"`, fx/routes.go:138), `Clock` (clock.go:6). shopspring has no negative zero (§5.6).
- **today**: `todayUTC(c platform.Clock)` and `utcDate` are unexported in invoices (core.go:393-399). **Gap**: export one helper (platform) so reports, stats and invoices share it (and later switch to Asia/Jakarta per PN 5 in one place).
- **schedule** (backend/internal/invoices/schedule/schedule.go): `Parse(cfg map[string]any) (Schedule, error)` (:62), `IsPaused(cfg map[string]any) bool` (:285), `(Schedule) Current(today) (Cycle, bool)` (:243), `(Schedule) NextAfter(after) (Cycle, bool)` (:268, strictly after, `Cycle.Start` is the date), `Schedule{Frequency, Interval, ...}` (:42-49). Enough for §2.2 and §2.5.
- **invoices**: `CurrencyAmount{Currency, Amount platform.Dec4}` (customers.go:33-36) — reuse for the stats shapes. `ListScanTemplates` (queries.sql:181-186) and `ListUsageDrafts` (:196-201) return only `invoice_id, billing_cycle_ref`. **Gap**: new queries with `customer_id, amount, currency` (keep Python's filters: no status filter on templates; usage = DRAFT, no template filter). `cutOffDay` (usage.go:139-144) is `int()`-like and must **not** be reused for upcoming (stricter rule, §2.5).
- **ledger** (backend/internal/ledger/queries.sql): write queries only (`AccountIDsByCode`, `InsertEntry`, `InsertLine`, `LockEntry`, `EntryLines`, `SetReversedBy`). **Gap**: all three ledger report queries are new (trial balance, balance sheet, revaluation buckets).
- **accounts**: chart-of-accounts CRUD (accounts/queries.sql:1-26); nothing to reuse beyond the table.
- **payments** (plan 7, in progress): recent_activity reads payments, reconciliation_log, invoices, customers, users directly; no dependency on the payments package needed.
- **bills, quotations, customers, vendors**: no aggregate queries to reuse; AP aging, pipeline and counts are new SQL.
- **Ordering**: the spec says `ORDER BY` "ends with the primary key". Python has none for open_ar_by_currency, pipeline ties, MRR ties, open-AR-base ties, invoices-summary buckets, aging rows with equal names, recent_activity ties. Choose: currency order for per-currency lists before the Python sort (Go `slices.SortStableFunc` after an ORDER BY currency), party_id after (lower name, currency) for aging, `log_id` after created_at for activity. Record in PN.

---

## 5. Oddities

1. **Balance sheet ignores `as_of`.** The cutoff is in the ON of the outer join to journal_entries while the sums read journal_lines (RA:325-334), so every line ever posted is counted; the FE always sends `as_of` (FE/ReportsView.vue:113-118), so a past date shows today's balances. Change: inner-join lines to entries with `posted_at <= cutoff` inside a subquery, then left-join accounts. This changes numbers; port note with its own test.
2. **The "balance sheet" mixes P&L and does not balance.** It lists INCOME, EXPENSE and COGS groups (RA:363-367) and has no retained earnings / current result line, so assets != liabilities + equity once there is income. Keep; note.
3. **Open AR differs by screen.** Dashboard open AR and the AR pie take OPEN/SENT/PARTIAL with **no** `is_template = false` and **no** `balance_due > 0` (SS:150-154, :512-517); AR aging (RA:576-578), invoices-summary (SS:366-369) and customer balances (SS:419-422) filter both. A negative or template balance moves the dashboard only. Change: same filter everywhere.
4. **`auto_cleared_last_30d` counts every CLEARED payment** (SS:501-510), including manual record-payments and manual-review approvals, and compares the DB `CURRENT_TIMESTAMP` column with a Python-UTC cutoff (correct only when the DB `TimeZone` is UTC, spec "Timestamps"). Keep the count for parity, note the name; or filter `intake_source <> 'MANUAL'` and no OVERRIDDEN log later.
5. **Server-local "today" and a browser-UTC `as_of`** (RA:243, :314, :389, :558, :607; RS:191, :251, :286; SS:464; FE/ReportsView.vue:102). In WIB before 07:00 the FE asks for yesterday and the server defaults to yesterday (UTC container). PN 5. Keep UTC for parity; fix both ends together after the switch (FE: local date, not `toISOString`).
6. **Negative zero.** `unrealised = (revalued - original) * Decimal("-1")` gives `Decimal("-0.0000")` for an unchanged LIABILITY bucket (RA:466, :496), printed `"-0.0000"`; shopspring prints `"0.0000"`. Change: print `"0.0000"`; port note.
7. **Scale of zero values.** Untouched starts print `"0"` (aging buckets RA:532-539, trial-balance totals RA:273-274, balance-sheet missing types RA:373-378, revaluation totals RA:450-451); DB sums print 4 places. Go prints 4 places always (spec). Port note.
8. **Unordered output.** No ORDER BY on open_ar_by_currency (SS:512-516), the pipeline / MRR / open-AR-base ties (stable sorts over unordered queries, SS:136, :166, :534), invoices-summary buckets (SS:363-373), aging input (RA:567-579, :616-627; ties on `party_name.lower()` and `totals_by_currency` key order), monthly dict key order (SS:218-242), recent_activity ties (RS:163). Change: deterministic order (§4 "Ordering").
9. **Revenue converts every month at today's rate** (SS:244-256; documented RS:240-248), so past months move with the rate and a currency without a current rate vanishes from all months. Keep (documented), note.
10. **`avg_days_to_pay` is per payment, not per invoice**, with no upper bound on payment_date and negative deltas allowed (SS:377-398). Keep; note.
11. **Upcoming: recurring is strictly after today, usage is today or later** (RSC:194-204 vs SS:292-300), so a recurring cycle starting today is not listed while a usage cut-off today is. `isinstance(dom, int)` accepts JSON `true` as day 1 and rejects `"20"`, unlike the usage-lock worker (SS:340-342). Templates of any status are listed (SS:89-93). Keep; note.
12. **FX revaluation keeps residue buckets.** A bucket with face 0 but base ≠ 0 (rounding residue, or a realised FX line in a foreign currency) is kept and its whole base shown as unrealised gain/loss (RA:469, :494-500). Its own rate query skips `fx.get_rate`'s upper-casing (RA:437-444). Keep; note.
13. **AR aging treats a NULL due_date as current** (RA:592, :640) and ages invoices issued after `as_of` (no issue_date filter, RA:576-578); AP has no SENT status and its `"(no vendor)"`/`"-"` branches are unreachable (vendor_id NOT NULL, DDL:29). Keep.
14. **Trial balance includes inactive accounts and drops accounts without lines** (RA:260-262); `net`/`base_net` are sent but unused by the FE (FE/ReportsView.vue:210-240). Keep.
15. **Counts include more than they say.** `sent_count` has no template filter (SS:490-495), `customer_count` includes inactive customers (SS:497). Keep; note.
16. **28-digit context.** Products `amount * rate` above 28 significant digits are rounded before `quantize` and a quantized value above 10^24 raises InvalidOperation (500) (SFX:72, RA:494, SS:266). shopspring multiplies exactly. Unreachable at `numeric(19,4)` x `numeric(19,8)` sizes in practice; note only.
17. **MRR division precision.** Python divides in the 28-significant-digit context (SS:108-119); shopspring `Div` uses 16 decimal places rounded half away from zero. Only a sum landing exactly on a 4-place tie could differ. Use `DivRound(x, 28)` (or more) then `RoundHalfEven4` after the per-currency sum, and pin 433.3333 plus a tie case.
18. **`actor_name` keeps an empty display_name** (`coalesce`, RS:153), so a user with `display_name = ""` shows as blank, not as their email. Keep; note.
19. **No tests for the five reports** (§3). Go writes them from this inventory; every rule above that changes in Go (1, 3, 6, 7, 8) gets a port note and a test.
