# Dashboard: MRR & Upcoming Invoices

**Date:** 2026-07-24
**Status:** Approved design, pending implementation plan

## Goal

Add two forward-looking stats to the dashboard (`PaymentIntakeWidget.vue`, routed at `/dashboard`):

1. **MRR (Monthly Recurring Revenue)** — normalized monthly value of active recurring templates, shown **per currency**.
2. **Upcoming invoices (next 90 days)** — a list of invoices scheduled to generate soon, covering **recurring + usage-based** billing modes.

No schema changes → **no migration**.

## Background (existing building blocks)

- Recurring templates: `Invoice` rows with `invoice_type == "RECURRING"` and `is_template == True`. Schedule lives in `billing_cycle_ref` (JSONB). Per-cycle money is `amount`; `currency` is fixed.
- `app/services/recurring_schedule.py` already provides the schedule algebra: `parse_schedule`, `current_cycle(schedule, today)`, `next_cycle_after(schedule, after)`, `is_paused(cfg)`, `describe(schedule)`. These are pure and side-effect free.
- Usage-based invoices: `Invoice` rows with `invoice_type == "USAGE"`, held as `DRAFT` until locked. `billing_cycle_ref.cut_off_day` is the day-of-month the usage-lock fires; `billing_cycle_ref.locked` marks it done. Accrued amount is computed externally (currently a stub) and only finalized at lock time — so before lock the amount is **accruing / indicative**.
- `app/services/stats.py` holds the pure aggregation helpers; `compute_dashboard_stats(db)` is **shared** by `GET /api/v1/stats/dashboard` and the MCP `get_stats` tool, so anything added there stays in parity across both. `recent_activity` is assembled router-side (not in that shared function) because it needs a join + shaping.
- `Customer.name` (non-null) is the display label for a customer.
- Frontend money formatting: `formatAmount(amount, currency)` from `@/utils/money`.

## Component 1 — MRR by currency

### Computation (`services/stats.py`)

New pure helper:

```python
def compute_mrr_by_currency(db: Session, *, today: date) -> list[CurrencyAmount]:
    ...
```

For each active recurring template, normalize its per-cycle `amount` to a **monthly** figure and sum grouped by currency.

**Active** = `invoice_type == "RECURRING"` AND `is_template` AND `not is_paused(billing_cycle_ref)` AND schedule parses AND schedule not ended (`current_cycle(schedule, today) is not None or next_cycle_after(schedule, today) is not None`). Templates with malformed `billing_cycle_ref` are skipped (never raise — same defensive posture as the beat scanner).

**Monthly normalization** (Decimal math, `interval` = schedule interval ≥ 1):

| frequency | monthly-equivalent |
|-----------|--------------------|
| MONTHLY   | `amount / interval` |
| YEARLY    | `amount / (12 * interval)` |
| WEEKLY    | `amount * 52 / (12 * interval)` |
| DAILY     | `amount * 365 / (12 * interval)` |

Sum per currency, quantize to `DECIMAL(19,4)`, sort largest-amount-first (sort on Decimal before any string coercion, mirroring the existing `open_quotation_pipeline` note). Return `[CurrencyAmount(currency, amount)]`.

### Wiring

- Add `mrr_by_currency` to the dict returned by `compute_dashboard_stats(db)` (needs `today`; pass `date.today()` inside, consistent with how the caller works). This flows to both the REST response and the MCP `get_stats` tool automatically.
- Add `mrr_by_currency: list[CurrencyAmount]` to the `DashboardStats` pydantic model in `routers/stats.py`, and build it from the shared dict in the `dashboard` handler.
- Update MCP `get_stats` tool schema/output shape (`mcp/tools/read_misc.py`) if it declares an explicit shape, so the new key is surfaced.

## Component 2 — Upcoming invoices (next 90 days)

### Computation (`services/stats.py`)

New helper returning raw rows (customer name resolved in the router, like `recent_activity`):

```python
@dataclass(frozen=True)
class UpcomingInvoice:
    invoice_id: uuid.UUID
    customer_id: uuid.UUID | None
    mode: str          # "RECURRING" | "USAGE"
    next_date: date
    amount: Decimal
    currency: str
    amount_is_estimate: bool   # True for USAGE (accruing)

def compute_upcoming_invoices(
    db: Session, *, today: date, horizon_days: int = 90
) -> list[UpcomingInvoice]:
    ...
```

Window = `(today, today + horizon_days]`.

- **Recurring:** for each active recurring template (same active test as MRR), `d = next_cycle_after(schedule, today)`. Include one row if `d is not None and d <= today + 90d`. `amount = template.amount`, `currency = template.currency`, `amount_is_estimate = False`.
- **Usage:** for each `USAGE` invoice with `status == "DRAFT"` and `not billing_cycle_ref.locked` and a valid `cut_off_day`, compute the **next occurrence** of that day-of-month strictly after `today` (this month if the day is still ahead, else next month; clamp to month length). Include if within window. `amount = invoice.amount`, `currency = invoice.currency`, `amount_is_estimate = True`.

**One row per source = its next occurrence only** (a daily/weekly template contributes a single row). Sort ascending by `next_date`, tie-break stably. No artificial row cap in the service; the card may show a "+N more" tail if the list is long (see frontend).

### Wiring

- In the `dashboard` router handler, call `compute_upcoming_invoices`, resolve `Customer.name` per `customer_id` (single `IN` query, or a join), and build response items.
- New pydantic model `UpcomingInvoiceItem` with fields: `invoice_id`, `customer_id`, `customer_name: str | None`, `mode`, `next_date`, `amount`, `currency`, `amount_is_estimate`.
- Add `upcoming_invoices: list[UpcomingInvoiceItem]` to `DashboardStats`.
- Assembled router-side (kept out of the shared `compute_dashboard_stats` so the MCP `get_stats` payload doesn't balloon with name-resolved rows — matches the `recent_activity` precedent).

## Component 3 — Frontend (`PaymentIntakeWidget.vue`)

Extend the `DashboardStats` TS interface with `mrr_by_currency: CurrencyAmount[]` and `upcoming_invoices: UpcomingInvoiceItem[]`.

Insert a new `two-col` row **above** the revenue chart:

- **MRR by currency** card — reuses the Open-AR list style (`ar-list`): one row per currency, `CCY` chip + `formatAmount`. Header subtitle: "Normalized monthly value of active recurring templates." Empty-state ("No active recurring revenue.") when the list is empty.
- **Upcoming invoices (next 90 days)** card — a list; each row: `next_date` (formatted), `customer_name` (fallback "—"), a mode `Tag` (`Recurring` = info, `Usage` = warning), and `amount` via `formatAmount` with a small "est." marker when `amount_is_estimate`. Empty-state ("Nothing scheduled in the next 90 days.") when empty. Optional "+N more" tail if rendering is capped client-side (cap e.g. 12 rows).

The existing 5 stat tiles, revenue chart, and Open-AR / Recent-activity row are unchanged. Reuse existing scoped CSS classes; add minimal new rules only for the mode tag / est. marker.

## Edge cases

- Malformed / unparseable `billing_cycle_ref` → template silently skipped (both MRR and upcoming).
- Paused template → excluded from MRR and upcoming.
- Ended schedule (`next_cycle_after` and `current_cycle` both None) → excluded from MRR; contributes no upcoming row.
- USAGE invoice with missing/`null` `cut_off_day` → skipped.
- USAGE `cut_off_day` > days in target month → clamp to last day of that month.
- Empty results → cards render their empty-states; MRR list may be `[]`.
- Multi-currency: never converted or summed across currencies (consistent with the never-auto-convert rule and the existing Open-AR / revenue-monthly display convention).

## Testing

New unit tests under `tests/unit/` (real Postgres per `conftest.py`; skip if `TEST_DATABASE_URL` unset):

- `compute_mrr_by_currency`: each frequency's normalization (MONTHLY/YEARLY/WEEKLY/DAILY, interval > 1), paused exclusion, ended-schedule exclusion, malformed-config skip, multi-currency grouping + sort order.
- `compute_upcoming_invoices`: recurring next-occurrence within window, boundary at exactly `today + 90d`, template past end excluded, paused excluded, usage next-cutoff computation incl. month-length clamp, locked/no-cut_off_day usage skipped, sort order, mixed recurring+usage.

No migration; no changes to the ledger or reconciliation paths.

## Out of scope

- FX-converted single-number MRR (explicitly rejected — per-currency chosen).
- Milestone invoices in the upcoming list (manual, no deterministic date).
- Enumerating every future occurrence per template (next-occurrence-only chosen).
- Wiring the real usage accrual source (remains a separate concern; amounts stay indicative).
