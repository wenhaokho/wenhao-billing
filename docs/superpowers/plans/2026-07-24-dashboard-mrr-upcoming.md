# Dashboard MRR & Upcoming Invoices Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-currency MRR, a 90-day upcoming-invoices list, and an IDR-converted Open-AR pie (alongside the existing per-currency list) to the dashboard.

**Architecture:** Three new pure service helpers in `app/services/stats.py` (MRR, base-converted AR, upcoming invoices). MRR + base-AR fold into the shared `compute_dashboard_stats` (so REST + MCP `get_stats` stay in parity); upcoming is assembled router-side with customer-name resolution (mirroring `recent_activity`). Frontend adds two cards and a pie to `PaymentIntakeWidget.vue`.

**Tech Stack:** FastAPI + SQLAlchemy 2.0, pydantic v2, pytest against real Postgres; Vue 3 + TS + PrimeVue Chart + @tanstack/vue-query.

## Global Constraints

- All money is `DECIMAL(19,4)`; use `Decimal`, quantize to `Decimal("0.0001")`.
- Never sum/convert across currencies except the explicit base-currency (IDR) conversion in Task 2, which uses `app.services.fx.convert`.
- Business-id scoping: this app is single-tenant with no `business_id` column on these models — preserve existing query shapes; add no unscoped cross-entity reads beyond what's shown.
- Base currency = `get_settings().base_currency` (default `"IDR"`).
- db-backed service tests live in `backend/tests/integration/` (not `unit/`), matching `test_dashboard_stats.py`. Tests skip unless `TEST_DATABASE_URL` is set; FX rates USD→IDR=16000, SGD→IDR=12000 (as_of 2026-01-01) are auto-seeded by `conftest.py`.
- Run backend commands inside `backend/` (or `docker compose exec backend ...`). Lint: `ruff check app`.
- Malformed/paused/ended recurring templates must never raise — skip them (same posture as `workers/tasks/recurring._cycle_key_for`).

---

### Task 1: MRR per currency (`compute_mrr_by_currency`)

**Files:**
- Modify: `backend/app/services/stats.py`
- Test: `backend/tests/integration/test_mrr_stats.py` (create)

**Interfaces:**
- Consumes: `app.services.recurring_schedule.parse_schedule`, `current_cycle`, `next_cycle_after`, `is_paused`, `ScheduleError`.
- Produces:
  - `_active_recurring_templates(db, *, today) -> list[tuple[Invoice, Schedule]]` — parseable, non-paused, non-ended RECURRING templates (reused by Task 3).
  - `_monthly_equiv(amount: Decimal, frequency: str, interval: int) -> Decimal`
  - `compute_mrr_by_currency(db, *, today: date) -> list[dict]` → `[{"currency": str, "amount": Decimal}]`, sorted amount-desc.

- [ ] **Step 1: Write the failing test**

Create `backend/tests/integration/test_mrr_stats.py`:

```python
"""TDD: compute_mrr_by_currency — active recurring templates normalized to monthly."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services.stats import compute_mrr_by_currency

TODAY = date(2026, 7, 24)


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Retainer Corp", matching_aliases=["Retainer"])
    db.add(c)
    db.flush()
    return c


def _tmpl(db, customer, *, currency="USD", amount="1200", frequency="MONTHLY",
          interval=1, start_date=date(2026, 1, 1), end_mode="NEVER",
          end_date=None, end_after_cycles=None, paused=False) -> Invoice:
    cfg = {
        "frequency": frequency, "interval": interval,
        "start_date": start_date.isoformat(), "end_mode": end_mode,
    }
    if end_date is not None:
        cfg["end_date"] = end_date.isoformat()
    if end_after_cycles is not None:
        cfg["end_after_cycles"] = end_after_cycles
    if paused:
        cfg["paused"] = True
    inv = Invoice(
        customer_id=customer.customer_id,
        invoice_type="RECURRING",
        currency=currency,
        amount=Decimal(amount),
        balance_due=Decimal("0"),
        status="DRAFT",
        is_template=True,
        billing_cycle_ref=cfg,
    )
    db.add(inv)
    db.flush()
    return inv


def test_empty_db_returns_no_rows(db):
    assert compute_mrr_by_currency(db, today=TODAY) == []


def test_monthly_interval_1_is_full_amount(db, customer):
    _tmpl(db, customer, amount="1200", frequency="MONTHLY", interval=1)
    rows = compute_mrr_by_currency(db, today=TODAY)
    assert rows == [{"currency": "USD", "amount": Decimal("1200.0000")}]


def test_yearly_divided_by_twelve(db, customer):
    _tmpl(db, customer, amount="1200", frequency="YEARLY", interval=1)
    rows = compute_mrr_by_currency(db, today=TODAY)
    assert rows[0]["amount"] == Decimal("100.0000")


def test_weekly_times_52_over_12(db, customer):
    _tmpl(db, customer, amount="100", frequency="WEEKLY", interval=1)
    rows = compute_mrr_by_currency(db, today=TODAY)
    # 100 * 52 / 12 = 433.3333
    assert rows[0]["amount"] == Decimal("433.3333")


def test_monthly_interval_3_is_third(db, customer):
    _tmpl(db, customer, amount="900", frequency="MONTHLY", interval=3)
    rows = compute_mrr_by_currency(db, today=TODAY)
    assert rows[0]["amount"] == Decimal("300.0000")


def test_paused_template_excluded(db, customer):
    _tmpl(db, customer, amount="1200", paused=True)
    assert compute_mrr_by_currency(db, today=TODAY) == []


def test_ended_template_excluded(db, customer):
    _tmpl(db, customer, amount="1200", frequency="MONTHLY",
          start_date=date(2026, 1, 1), end_mode="ON_DATE",
          end_date=date(2026, 3, 1))
    assert compute_mrr_by_currency(db, today=TODAY) == []


def test_non_template_recurring_excluded(db, customer):
    t = _tmpl(db, customer, amount="1200")
    t.is_template = False
    db.flush()
    assert compute_mrr_by_currency(db, today=TODAY) == []


def test_grouped_and_sorted_desc(db, customer):
    _tmpl(db, customer, currency="USD", amount="1000", frequency="MONTHLY")
    _tmpl(db, customer, currency="SGD", amount="6000", frequency="MONTHLY")
    rows = compute_mrr_by_currency(db, today=TODAY)
    assert [r["currency"] for r in rows] == ["SGD", "USD"]
    assert rows[0]["amount"] == Decimal("6000.0000")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && ruff check app && pytest tests/integration/test_mrr_stats.py -v`
Expected: FAIL with `ImportError: cannot import name 'compute_mrr_by_currency'`.

- [ ] **Step 3: Write minimal implementation**

In `backend/app/services/stats.py`, add imports near the top (after existing imports):

```python
from app.config import get_settings
from app.services.recurring_schedule import (
    Schedule,
    ScheduleError,
    current_cycle,
    is_paused,
    next_cycle_after,
    parse_schedule,
)
```

Add the model import for the return type is not needed; add these functions (place after `_OPEN_QUOTE_STATUSES`):

```python
_MONTHS_PER_YEAR = Decimal(12)


def _is_schedule_active(schedule: Schedule, today: date) -> bool:
    """Active = has a current cycle or a future cycle (i.e. not past its end)."""
    return (
        current_cycle(schedule, today) is not None
        or next_cycle_after(schedule, today) is not None
    )


def _active_recurring_templates(
    db: Session, *, today: date
) -> list[tuple[Invoice, Schedule]]:
    """Parseable, non-paused, non-ended RECURRING templates with their Schedule.

    A single malformed/paused/ended template is skipped, never raises — same
    defensive posture as the recurring beat scanner.
    """
    templates = db.scalars(
        select(Invoice)
        .where(Invoice.invoice_type == "RECURRING")
        .where(Invoice.is_template.is_(True))
    )
    out: list[tuple[Invoice, Schedule]] = []
    for t in templates:
        if is_paused(t.billing_cycle_ref):
            continue
        try:
            schedule = parse_schedule(t.billing_cycle_ref)
        except ScheduleError:
            continue
        if not _is_schedule_active(schedule, today):
            continue
        out.append((t, schedule))
    return out


def _monthly_equiv(amount: Decimal, frequency: str, interval: int) -> Decimal:
    """Per-cycle amount normalized to a monthly figure (not yet quantized)."""
    ival = Decimal(interval)
    if frequency == "MONTHLY":
        return amount / ival
    if frequency == "YEARLY":
        return amount / (_MONTHS_PER_YEAR * ival)
    if frequency == "WEEKLY":
        return amount * Decimal(52) / (_MONTHS_PER_YEAR * ival)
    if frequency == "DAILY":
        return amount * Decimal(365) / (_MONTHS_PER_YEAR * ival)
    raise ScheduleError(f"unreachable frequency {frequency!r}")


def compute_mrr_by_currency(db: Session, *, today: date) -> list[dict]:
    """Monthly recurring revenue per currency from active recurring templates.

    Amounts are never converted across currencies. Sorted largest-first on the
    raw Decimal before any string coercion (same rule as open_quotation_pipeline).
    """
    totals: dict[str, Decimal] = {}
    for template, schedule in _active_recurring_templates(db, today=today):
        monthly = _monthly_equiv(template.amount, schedule.frequency, schedule.interval)
        totals[template.currency] = totals.get(template.currency, Decimal("0")) + monthly
    rows = [
        {"currency": ccy, "amount": amt.quantize(Decimal("0.0001"))}
        for ccy, amt in totals.items()
    ]
    rows.sort(key=lambda r: r["amount"], reverse=True)
    return rows
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && ruff check app && pytest tests/integration/test_mrr_stats.py -v`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/stats.py backend/tests/integration/test_mrr_stats.py
git commit -m "feat(stats): compute MRR per currency from active recurring templates"
```

---

### Task 2: Open AR converted to base currency (`compute_open_ar_base_by_currency`)

**Files:**
- Modify: `backend/app/services/stats.py`
- Test: `backend/tests/integration/test_open_ar_base.py` (create)

**Interfaces:**
- Consumes: `app.services.fx.convert`, `app.services.fx.FxRateMissing`; the existing `_OPEN_STATUSES`.
- Produces: `compute_open_ar_base_by_currency(db, *, today: date) -> tuple[list[dict], list[str]]` → `([{"currency": str, "base_amount": Decimal}], unconverted_currency_codes)`. Rows sorted base_amount-desc.

- [ ] **Step 1: Write the failing test**

Create `backend/tests/integration/test_open_ar_base.py`:

```python
"""TDD: compute_open_ar_base_by_currency — SENT/PARTIAL AR converted to IDR."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.fx import FxRate
from app.models.invoice import Invoice
from app.services.stats import compute_open_ar_base_by_currency

TODAY = date(2026, 7, 24)  # seeded USD/SGD->IDR rates are as_of 2026-01-01


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Acme Corp", matching_aliases=["Acme"])
    db.add(c)
    db.flush()
    return c


def _inv(db, customer, *, currency, balance_due, status="SENT"):
    inv = Invoice(
        customer_id=customer.customer_id,
        invoice_type="MILESTONE",
        currency=currency,
        amount=Decimal(balance_due),
        balance_due=Decimal(balance_due),
        status=status,
    )
    db.add(inv)
    db.flush()
    return inv


def test_empty_db_returns_empty(db):
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    assert rows == []
    assert unconverted == []


def test_base_currency_ar_uses_rate_one(db, customer):
    _inv(db, customer, currency="IDR", balance_due="5000000")
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    assert rows == [{"currency": "IDR", "base_amount": Decimal("5000000.0000")}]
    assert unconverted == []


def test_foreign_currency_converted_via_seeded_rate(db, customer):
    _inv(db, customer, currency="USD", balance_due="100")   # * 16000
    _inv(db, customer, currency="SGD", balance_due="100")   # * 12000
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    by = {r["currency"]: r["base_amount"] for r in rows}
    assert by == {"USD": Decimal("1600000.0000"), "SGD": Decimal("1200000.0000")}
    # sorted desc by base_amount
    assert [r["currency"] for r in rows] == ["USD", "SGD"]
    assert unconverted == []


def test_missing_rate_currency_lands_in_unconverted(db, customer):
    _inv(db, customer, currency="GBP", balance_due="100")  # no seeded GBP->IDR
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    assert rows == []
    assert unconverted == ["GBP"]


def test_draft_excluded(db, customer):
    _inv(db, customer, currency="USD", balance_due="100", status="DRAFT")
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    assert rows == []
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && pytest tests/integration/test_open_ar_base.py -v`
Expected: FAIL with `ImportError: cannot import name 'compute_open_ar_base_by_currency'`.

- [ ] **Step 3: Write minimal implementation**

In `backend/app/services/stats.py` add the import:

```python
from app.services.fx import FxRateMissing, convert
```

Add the function (after `compute_mrr_by_currency`):

```python
def compute_open_ar_base_by_currency(
    db: Session, *, today: date
) -> tuple[list[dict], list[str]]:
    """Open AR (SENT + PARTIAL balances) per currency, each converted to the
    base currency (IDR) as of `today`.

    Returns (rows, unconverted) where rows is
    [{"currency": str, "base_amount": Decimal}] sorted base_amount-desc, and
    unconverted lists currency codes that had no FX rate (skipped, never raised).
    """
    ar_rows = db.execute(
        select(Invoice.currency, func.sum(Invoice.balance_due))
        .where(Invoice.status.in_(_OPEN_STATUSES))
        .group_by(Invoice.currency)
    ).all()

    converted: list[dict] = []
    unconverted: list[str] = []
    for currency, total in ar_rows:
        amount = total or Decimal("0")
        try:
            base_amount = convert(db, amount, currency, today).base_amount
        except FxRateMissing:
            unconverted.append(currency)
            continue
        converted.append({"currency": currency, "base_amount": base_amount})
    converted.sort(key=lambda r: r["base_amount"], reverse=True)
    unconverted.sort()
    return converted, unconverted
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && ruff check app && pytest tests/integration/test_open_ar_base.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/stats.py backend/tests/integration/test_open_ar_base.py
git commit -m "feat(stats): convert open AR per currency to base currency (IDR)"
```

---

### Task 3: Upcoming invoices — recurring + usage (`compute_upcoming_invoices`)

**Files:**
- Modify: `backend/app/services/stats.py`
- Test: `backend/tests/integration/test_upcoming_invoices.py` (create)

**Interfaces:**
- Consumes: `_active_recurring_templates` (Task 1), `next_cycle_after`.
- Produces:
  - `@dataclass(frozen=True) class UpcomingInvoice` with fields `invoice_id: uuid.UUID`, `customer_id: uuid.UUID | None`, `mode: str` (`"RECURRING"`/`"USAGE"`), `next_date: date`, `amount: Decimal`, `currency: str`, `amount_is_estimate: bool`.
  - `compute_upcoming_invoices(db, *, today: date, horizon_days: int = 90) -> list[UpcomingInvoice]` — one row per source at its next occurrence within `[today, today+horizon_days]`, sorted by `next_date` asc then `invoice_id`.

- [ ] **Step 1: Write the failing test**

Create `backend/tests/integration/test_upcoming_invoices.py`:

```python
"""TDD: compute_upcoming_invoices — next recurring + usage generation dates."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services.stats import compute_upcoming_invoices

TODAY = date(2026, 7, 24)


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Acme Corp", matching_aliases=["Acme"])
    db.add(c)
    db.flush()
    return c


def _recurring(db, customer, *, currency="USD", amount="1000",
               frequency="MONTHLY", interval=1, start_date=date(2026, 1, 5),
               paused=False) -> Invoice:
    cfg = {"frequency": frequency, "interval": interval,
           "start_date": start_date.isoformat(), "end_mode": "NEVER"}
    if paused:
        cfg["paused"] = True
    inv = Invoice(
        customer_id=customer.customer_id, invoice_type="RECURRING",
        currency=currency, amount=Decimal(amount), balance_due=Decimal("0"),
        status="DRAFT", is_template=True, billing_cycle_ref=cfg,
    )
    db.add(inv)
    db.flush()
    return inv


def _usage(db, customer, *, currency="USD", amount="0", cut_off_day=20,
           status="DRAFT", locked=False) -> Invoice:
    cfg = {"cut_off_day": cut_off_day}
    if locked:
        cfg["locked"] = True
    inv = Invoice(
        customer_id=customer.customer_id, invoice_type="USAGE",
        currency=currency, amount=Decimal(amount), balance_due=Decimal("0"),
        status=status, is_template=False, billing_cycle_ref=cfg,
    )
    db.add(inv)
    db.flush()
    return inv


def test_empty_db_returns_empty(db):
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_recurring_next_occurrence_within_window(db, customer):
    # MONTHLY on the 5th, start Jan 5 -> next after Jul 24 is Aug 5.
    _recurring(db, customer, amount="1000", start_date=date(2026, 1, 5))
    rows = compute_upcoming_invoices(db, today=TODAY)
    assert len(rows) == 1
    assert rows[0].mode == "RECURRING"
    assert rows[0].next_date == date(2026, 8, 5)
    assert rows[0].amount == Decimal("1000")
    assert rows[0].amount_is_estimate is False


def test_recurring_reported_once_even_if_multiple_cycles_in_window(db, customer):
    # WEEKLY would fire ~13 times in 90 days; we want a single row.
    _recurring(db, customer, frequency="WEEKLY", start_date=date(2026, 1, 5))
    rows = compute_upcoming_invoices(db, today=TODAY)
    assert len(rows) == 1


def test_recurring_beyond_window_excluded(db, customer):
    # YEARLY on Jan 5 -> next is 2027-01-05, well beyond 90 days.
    _recurring(db, customer, frequency="YEARLY", start_date=date(2026, 1, 5))
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_paused_recurring_excluded(db, customer):
    _recurring(db, customer, start_date=date(2026, 1, 5), paused=True)
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_usage_next_cutoff_is_estimate(db, customer):
    # cut_off_day 20, today Jul 24 -> next is Aug 20.
    _usage(db, customer, cut_off_day=20, amount="0")
    rows = compute_upcoming_invoices(db, today=TODAY)
    assert len(rows) == 1
    assert rows[0].mode == "USAGE"
    assert rows[0].next_date == date(2026, 8, 20)
    assert rows[0].amount_is_estimate is True


def test_usage_locked_or_no_cutoff_skipped(db, customer):
    _usage(db, customer, cut_off_day=20, locked=True)
    u = _usage(db, customer, cut_off_day=20)
    u.billing_cycle_ref = {}  # no cut_off_day
    db.flush()
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_usage_non_draft_skipped(db, customer):
    _usage(db, customer, cut_off_day=25, status="SENT")
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_mixed_sorted_by_date(db, customer):
    _usage(db, customer, cut_off_day=28)          # Jul 28
    _recurring(db, customer, start_date=date(2026, 1, 5))  # Aug 5
    rows = compute_upcoming_invoices(db, today=TODAY)
    assert [r.next_date for r in rows] == [date(2026, 7, 28), date(2026, 8, 5)]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && pytest tests/integration/test_upcoming_invoices.py -v`
Expected: FAIL with `ImportError: cannot import name 'compute_upcoming_invoices'`.

- [ ] **Step 3: Write minimal implementation**

In `backend/app/services/stats.py`, add `calendar` to the imports at top:

```python
import calendar
```

Add the dataclass near the other dataclasses:

```python
@dataclass(frozen=True)
class UpcomingInvoice:
    invoice_id: uuid.UUID
    customer_id: uuid.UUID | None
    mode: str  # "RECURRING" | "USAGE"
    next_date: date
    amount: Decimal
    currency: str
    amount_is_estimate: bool
```

Add the helpers + function (after `compute_open_ar_base_by_currency`):

```python
def _clamp_dom(year: int, month: int, day: int) -> date:
    last = calendar.monthrange(year, month)[1]
    return date(year, month, min(day, last))


def _next_day_of_month(today: date, dom: int) -> date:
    """Smallest date >= today whose day-of-month is `dom` (clamped to month length)."""
    cand = _clamp_dom(today.year, today.month, dom)
    if cand >= today:
        return cand
    y, m = today.year, today.month + 1
    if m == 13:
        y, m = y + 1, 1
    return _clamp_dom(y, m, dom)


def compute_upcoming_invoices(
    db: Session, *, today: date, horizon_days: int = 90
) -> list[UpcomingInvoice]:
    """Next generation date per recurring template and per pending usage invoice,
    within [today, today + horizon_days]. One row per source (next occurrence
    only). Usage amounts are indicative (accruing), flagged amount_is_estimate.
    """
    horizon = today + timedelta(days=horizon_days)
    out: list[UpcomingInvoice] = []

    # Recurring: next cycle start per active template.
    for template, schedule in _active_recurring_templates(db, today=today):
        nxt = next_cycle_after(schedule, today)
        if nxt is None or nxt > horizon:
            continue
        out.append(
            UpcomingInvoice(
                invoice_id=template.invoice_id,
                customer_id=template.customer_id,
                mode="RECURRING",
                next_date=nxt,
                amount=template.amount,
                currency=template.currency,
                amount_is_estimate=False,
            )
        )

    # Usage: next cut_off_day occurrence per DRAFT, unlocked usage invoice.
    usage_rows = db.scalars(
        select(Invoice)
        .where(Invoice.invoice_type == "USAGE")
        .where(Invoice.status == "DRAFT")
    )
    for inv in usage_rows:
        cfg = inv.billing_cycle_ref or {}
        if cfg.get("locked"):
            continue
        dom = cfg.get("cut_off_day")
        if not isinstance(dom, int) or not (1 <= dom <= 31):
            continue
        nxt = _next_day_of_month(today, dom)
        if nxt > horizon:
            continue
        out.append(
            UpcomingInvoice(
                invoice_id=inv.invoice_id,
                customer_id=inv.customer_id,
                mode="USAGE",
                next_date=nxt,
                amount=inv.amount,
                currency=inv.currency,
                amount_is_estimate=True,
            )
        )

    out.sort(key=lambda u: (u.next_date, str(u.invoice_id)))
    return out
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && ruff check app && pytest tests/integration/test_upcoming_invoices.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/stats.py backend/tests/integration/test_upcoming_invoices.py
git commit -m "feat(stats): compute upcoming recurring + usage invoices (90d window)"
```

---

### Task 4: Wire into dashboard endpoint + MCP get_stats

**Files:**
- Modify: `backend/app/services/stats.py` (`compute_dashboard_stats`)
- Modify: `backend/app/api/v1/routers/stats.py` (models + `dashboard` handler)
- Modify: `backend/app/mcp/tools/read_misc.py` (`get_stats`)
- Test: `backend/tests/integration/test_dashboard_stats.py` (extend)

**Interfaces:**
- Consumes: `compute_mrr_by_currency`, `compute_open_ar_base_by_currency`, `compute_upcoming_invoices`, `get_settings`, `Customer`.
- Produces (on `DashboardStats` response): `mrr_by_currency: list[CurrencyAmount]`, `base_currency: str`, `open_ar_base_by_currency: list[CurrencyBaseAmount]`, `open_ar_unconverted: list[str]`, `upcoming_invoices: list[UpcomingInvoiceItem]`.

- [ ] **Step 1: Write the failing test**

Append to `backend/tests/integration/test_dashboard_stats.py` (reuse its existing `customer`, `_inv` helpers; add a local recurring/usage helper):

```python
def _recurring_tmpl(db, customer, *, currency="USD", amount="1200"):
    inv = Invoice(
        customer_id=customer.customer_id, invoice_type="RECURRING",
        currency=currency, amount=Decimal(amount), balance_due=Decimal("0"),
        status="DRAFT", is_template=True,
        billing_cycle_ref={"frequency": "MONTHLY", "interval": 1,
                           "start_date": "2026-01-05", "end_mode": "NEVER"},
    )
    db.add(inv)
    db.flush()
    return inv


def test_dashboard_stats_includes_mrr_and_base_ar(db, customer):
    _recurring_tmpl(db, customer, currency="USD", amount="1200")
    _inv(db, customer, currency="USD", status="SENT", balance_due="100")
    stats = compute_dashboard_stats(db)
    assert stats["mrr_by_currency"] == [{"currency": "USD", "amount": Decimal("1200.0000")}]
    assert stats["base_currency"] == "IDR"
    assert stats["open_ar_base_by_currency"] == [
        {"currency": "USD", "base_amount": Decimal("1600000.0000")}
    ]
    assert stats["open_ar_unconverted"] == []


def test_dashboard_endpoint_exposes_new_fields(admin_session, db, customer):
    _recurring_tmpl(db, customer, currency="USD", amount="1200")
    _inv(db, customer, currency="USD", status="SENT", balance_due="100")

    r = admin_session.get("/api/v1/stats/dashboard")
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["base_currency"] == "IDR"
    assert body["mrr_by_currency"][0]["currency"] == "USD"
    assert body["open_ar_base_by_currency"][0]["base_amount"] == "1600000.0000"
    assert "upcoming_invoices" in body
    # the recurring template's next cycle (Aug 5) is within 90 days of a mid-year run
    modes = {u["mode"] for u in body["upcoming_invoices"]}
    assert modes <= {"RECURRING", "USAGE"}
    if body["upcoming_invoices"]:
        assert "customer_name" in body["upcoming_invoices"][0]
```

> Note: `test_dashboard_endpoint_exposes_new_fields` asserts on shape, not on exact upcoming rows (the next-cycle date depends on `date.today()` at run time). The MRR/base-AR assertions are deterministic.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && pytest tests/integration/test_dashboard_stats.py -v -k "new_fields or base_ar"`
Expected: FAIL — `KeyError: 'mrr_by_currency'` / missing response fields.

- [ ] **Step 3: Write the implementation**

In `backend/app/services/stats.py`, extend `compute_dashboard_stats` — add near the top of the function body:

```python
    today = date.today()
```

and replace the final `return {...}` to include the new keys:

```python
    mrr_by_currency = compute_mrr_by_currency(db, today=today)
    open_ar_base_by_currency, open_ar_unconverted = compute_open_ar_base_by_currency(
        db, today=today
    )

    return {
        "awaiting_review_count": awaiting_review_count,
        "draft_count": draft_count,
        "sent_count": sent_count,
        "customer_count": customer_count,
        "auto_cleared_last_30d": auto_cleared_last_30d,
        "open_ar_by_currency": open_ar_by_currency,
        "open_quotation_count": open_quotation_count,
        "open_quotation_pipeline": open_quotation_pipeline,
        "mrr_by_currency": mrr_by_currency,
        "open_ar_base_by_currency": open_ar_base_by_currency,
        "open_ar_unconverted": open_ar_unconverted,
        "base_currency": get_settings().base_currency,
    }
```

In `backend/app/api/v1/routers/stats.py`:

Add imports:

```python
from app.models.customer import Customer
from app.services.stats import (
    compute_dashboard_stats,
    compute_invoices_summary,
    compute_upcoming_invoices,
)
```

Add pydantic models (near `CurrencyAmount`):

```python
class CurrencyBaseAmount(BaseModel):
    currency: str
    base_amount: Decimal


class UpcomingInvoiceItem(BaseModel):
    invoice_id: UUID
    customer_id: UUID | None
    customer_name: str | None
    mode: str
    next_date: date
    amount: Decimal
    currency: str
    amount_is_estimate: bool
```

Extend `DashboardStats`:

```python
    mrr_by_currency: list[CurrencyAmount]
    base_currency: str
    open_ar_base_by_currency: list[CurrencyBaseAmount]
    open_ar_unconverted: list[str]
    upcoming_invoices: list[UpcomingInvoiceItem]
```

In the `dashboard` handler, after building `recent_activity` and before the `return`, add:

```python
    upcoming = compute_upcoming_invoices(db, today=date.today())
    cust_ids = {u.customer_id for u in upcoming if u.customer_id is not None}
    names: dict[UUID, str] = {}
    if cust_ids:
        for cid, cname in db.execute(
            select(Customer.customer_id, Customer.name).where(
                Customer.customer_id.in_(cust_ids)
            )
        ).all():
            names[cid] = cname
    upcoming_invoices = [
        UpcomingInvoiceItem(
            invoice_id=u.invoice_id,
            customer_id=u.customer_id,
            customer_name=names.get(u.customer_id) if u.customer_id else None,
            mode=u.mode,
            next_date=u.next_date,
            amount=u.amount,
            currency=u.currency,
            amount_is_estimate=u.amount_is_estimate,
        )
        for u in upcoming
    ]
```

Extend the `return DashboardStats(...)` with:

```python
        mrr_by_currency=[CurrencyAmount(**row) for row in stats["mrr_by_currency"]],
        base_currency=stats["base_currency"],
        open_ar_base_by_currency=[
            CurrencyBaseAmount(**row) for row in stats["open_ar_base_by_currency"]
        ],
        open_ar_unconverted=stats["open_ar_unconverted"],
        upcoming_invoices=upcoming_invoices,
```

In `backend/app/mcp/tools/read_misc.py`, extend the `get_stats` return dict (parity — upcoming stays router-only, like recent_activity):

```python
            "mrr_by_currency": _coerce_currency_rows(stats["mrr_by_currency"]),
            "base_currency": stats["base_currency"],
            "open_ar_base_by_currency": [
                {"currency": r["currency"], "base_amount": coerce_value(r["base_amount"])}
                for r in stats["open_ar_base_by_currency"]
            ],
            "open_ar_unconverted": stats["open_ar_unconverted"],
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && ruff check app && pytest tests/integration/test_dashboard_stats.py tests/integration/test_mcp_read_misc.py -v`
Expected: PASS. (If `test_mcp_read_misc.py` asserts an exact key set for `get_stats`, update that assertion to include the four new keys.)

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/stats.py backend/app/api/v1/routers/stats.py backend/app/mcp/tools/read_misc.py backend/tests/integration/test_dashboard_stats.py
git commit -m "feat(stats): expose MRR, base-AR, and upcoming invoices on dashboard + MCP"
```

---

### Task 5: Frontend — MRR card, Upcoming card, Open-AR pie

**Files:**
- Modify: `frontend/src/views/PaymentIntakeWidget.vue`

**Interfaces:**
- Consumes: the extended `/stats/dashboard` payload from Task 4 (`mrr_by_currency`, `base_currency`, `open_ar_base_by_currency`, `open_ar_unconverted`, `upcoming_invoices`).

- [ ] **Step 1: Extend the TS interfaces**

In the `<script setup>` block, after the `CurrencyAmount` interface add:

```ts
interface CurrencyBaseAmount { currency: string; base_amount: string }
interface UpcomingInvoiceItem {
  invoice_id: string;
  customer_id: string | null;
  customer_name: string | null;
  mode: "RECURRING" | "USAGE";
  next_date: string;
  amount: string;
  currency: string;
  amount_is_estimate: boolean;
}
```

Extend the `DashboardStats` interface with:

```ts
  mrr_by_currency: CurrencyAmount[];
  base_currency: string;
  open_ar_base_by_currency: CurrencyBaseAmount[];
  open_ar_unconverted: string[];
  upcoming_invoices: UpcomingInvoiceItem[];
```

- [ ] **Step 2: Add pie chart data + a date formatter**

After the existing `chartOptions`/`hasRevenueData` computed block, add:

```ts
const arPieData = computed(() => {
  const rows = stats.value?.open_ar_base_by_currency ?? [];
  return {
    labels: rows.map((r) => r.currency),
    datasets: [
      {
        data: rows.map((r) => Number(r.base_amount)),
        backgroundColor: rows.map((_, i) => CHART_COLORS[i % CHART_COLORS.length]),
      },
    ],
  };
});

const hasArPie = computed(
  () => (stats.value?.open_ar_base_by_currency.length ?? 0) > 0,
);

const arPieOptions = computed(() => {
  const base = stats.value?.base_currency ?? "";
  return {
    maintainAspectRatio: false,
    responsive: true,
    plugins: {
      legend: { position: "bottom" as const, labels: { font: { size: 11 }, boxWidth: 12 } },
      tooltip: {
        callbacks: {
          label: (ctx: { label?: string; parsed: number }) =>
            `${ctx.label}: ${formatAmount(String(ctx.parsed), base)} ${base}`,
        },
      },
    },
  };
});

function formatDate(iso: string) {
  // next_date is a plain YYYY-MM-DD (no timezone) — format without Date() drift.
  const [y, m, d] = iso.split("-");
  const months = ["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"];
  return `${Number(d)} ${months[Number(m) - 1]} ${y}`;
}

function modeSeverity(mode: string) {
  return mode === "USAGE" ? "warning" : "info";
}
```

- [ ] **Step 3: Add the MRR + Upcoming two-col row above the revenue chart**

In the template, immediately **before** the `<!-- Revenue chart -->` block, insert:

```html
      <!-- Forward-looking: MRR + Upcoming -->
      <div class="two-col">
        <!-- MRR -->
        <div class="card">
          <div class="card-head">
            <div>
              <h3>Monthly Recurring Revenue</h3>
              <p class="subtitle">Normalized monthly value of active recurring templates, by currency.</p>
            </div>
          </div>
          <div v-if="!stats.mrr_by_currency.length" class="empty-state">
            <i class="pi pi-sync" />
            <div>No active recurring revenue.</div>
          </div>
          <ul v-else class="ar-list">
            <li v-for="row in stats.mrr_by_currency" :key="row.currency">
              <span class="ar-ccy">{{ row.currency }}</span>
              <span class="ar-amt num">{{ formatAmount(row.amount, row.currency) }}</span>
            </li>
          </ul>
        </div>

        <!-- Upcoming -->
        <div class="card">
          <div class="card-head">
            <div>
              <h3>Upcoming Invoices</h3>
              <p class="subtitle">Recurring &amp; usage invoices generating in the next 90 days.</p>
            </div>
          </div>
          <div v-if="!stats.upcoming_invoices.length" class="empty-state">
            <i class="pi pi-calendar" />
            <div>Nothing scheduled in the next 90 days.</div>
          </div>
          <ul v-else class="activity">
            <li v-for="u in stats.upcoming_invoices" :key="u.invoice_id">
              <div class="act-head">
                <Tag :value="u.mode === 'USAGE' ? 'Usage' : 'Recurring'" :severity="modeSeverity(u.mode)" />
                <span class="act-payer">{{ u.customer_name ?? '—' }}</span>
                <span class="act-amt num">
                  {{ formatAmount(u.amount, u.currency) }} {{ u.currency }}<template v-if="u.amount_is_estimate"> · est.</template>
                </span>
                <span class="act-time">{{ formatDate(u.next_date) }}</span>
              </div>
            </li>
          </ul>
        </div>
      </div>
```

- [ ] **Step 4: Replace the Open-AR card body — keep list, add pie**

In the existing Open AR card, keep the `card-head` and the `ar-list` / empty-state exactly as they are, and insert the pie **after** the `ar-list` (still inside the same `.card`), before the card closes:

```html
          <div v-if="hasArPie" class="chart-wrap ar-pie">
            <p class="pie-caption">Share of open AR, converted to {{ stats.base_currency }}.</p>
            <Chart type="pie" :data="arPieData" :options="arPieOptions" />
          </div>
          <p v-if="stats.open_ar_unconverted.length" class="pie-note">
            Not shown (no FX rate): {{ stats.open_ar_unconverted.join(', ') }}
          </p>
```

- [ ] **Step 5: Add minimal styles**

In the `<style scoped>` block, append:

```css
.ar-pie { height: 260px; }
.pie-caption { margin: 0 1.25rem 0.25rem; color: var(--color-text-muted); font-size: 0.8rem; }
.pie-note { margin: 0.25rem 1.25rem 0.75rem; color: var(--color-text-subtle); font-size: 0.75rem; }
```

- [ ] **Step 6: Typecheck + build**

Run: `cd frontend && npm run build`
Expected: `vue-tsc -b && vite build` completes with no type errors.

- [ ] **Step 7: Visual verification**

Start the stack (`docker compose up --build`), open `http://localhost:5173/dashboard`, and confirm: MRR card lists per-currency amounts; Upcoming card lists dated rows with Recurring/Usage tags; Open AR card shows the original-currency list **and** a pie beneath it with an IDR caption. Check the browser console for errors. (Seed data via `docker compose exec backend python -m scripts.seed_dummy` if the dashboard is empty.)

- [ ] **Step 8: Commit**

```bash
git add frontend/src/views/PaymentIntakeWidget.vue
git commit -m "feat(dashboard): add MRR + upcoming-invoices cards and IDR open-AR pie"
```

---

## Self-Review

**Spec coverage:**
- MRR per currency → Task 1 (+ wired in Task 4, rendered in Task 5). ✓
- Upcoming invoices, recurring + usage, next-occurrence-only, 90d → Task 3 (+ Task 4 name resolution, Task 5 card). ✓
- Open AR: keep original-currency list + add IDR-converted pie, missing-rate handling → Task 2 (+ Task 4 exposure, Task 5 list-retained + pie). ✓
- MCP `get_stats` parity for MRR + base-AR (upcoming router-only, like recent_activity) → Task 4. ✓
- No migration → confirmed (no model/schema changes). ✓
- Tests for each helper incl. edge cases → Tasks 1–3; endpoint/shape → Task 4. ✓

**Placeholder scan:** No TBD/TODO; every code step shows full code; commands have expected output. ✓

**Type consistency:** `compute_mrr_by_currency` → `[{"currency","amount"}]` used as `CurrencyAmount(**row)` (Task 4) and `CurrencyAmount[]` (Task 5). `compute_open_ar_base_by_currency` → `([{"currency","base_amount"}], list[str])` matched by `CurrencyBaseAmount` and `open_ar_unconverted` everywhere. `UpcomingInvoice` dataclass fields map 1:1 to `UpcomingInvoiceItem` pydantic fields and the `UpcomingInvoiceItem` TS interface. `_active_recurring_templates` returns `(Invoice, Schedule)` used consistently in Tasks 1 & 3. ✓
