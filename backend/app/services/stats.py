"""Dashboard / list-view aggregation stats.

Pure functions over the DB. Keep SQL here so routers stay thin and tests can
assert on shape directly.
"""

from __future__ import annotations

import calendar
import uuid
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from decimal import Decimal

from sqlalchemy import case, func, select
from sqlalchemy.orm import Session

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.models.quotation import Quotation
from app.services.fx import FxRateMissing, convert
from app.services.recurring_schedule import (
    Schedule,
    ScheduleError,
    current_cycle,
    is_paused,
    next_cycle_after,
    parse_schedule,
)


@dataclass(frozen=True)
class CurrencyAmount:
    currency: str
    amount: Decimal


@dataclass(frozen=True)
class CustomerBalance:
    customer_id: uuid.UUID
    currency: str
    balance: Decimal
    overdue: Decimal


@dataclass(frozen=True)
class InvoicesSummary:
    overdue: list[CurrencyAmount]
    due_30d: list[CurrencyAmount]
    avg_days_to_pay: float | None


@dataclass(frozen=True)
class UpcomingInvoice:
    invoice_id: uuid.UUID
    customer_id: uuid.UUID | None
    mode: str  # "RECURRING" | "USAGE"
    next_date: date
    amount: Decimal
    currency: str
    amount_is_estimate: bool


_OPEN_STATUSES = ("SENT", "PARTIAL")
_OPEN_QUOTE_STATUSES = ("SENT", "ACCEPTED")


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


def _bucket(db: Session, *, where_clause) -> list[CurrencyAmount]:
    rows = db.execute(
        select(Invoice.currency, func.sum(Invoice.balance_due))
        .where(
            Invoice.status.in_(_OPEN_STATUSES),
            Invoice.is_template.is_(False),
            Invoice.balance_due > 0,
            Invoice.due_date.is_not(None),
            where_clause,
        )
        .group_by(Invoice.currency)
    ).all()
    return [CurrencyAmount(currency=c, amount=a or Decimal("0")) for c, a in rows]


def _avg_days_to_pay(db: Session, *, today: date, window_days: int = 90) -> float | None:
    """Average (payment_date - issue_date) for CLEARED payments in the last
    `window_days` against PAID invoices. Returns None if no data."""
    cutoff = today - timedelta(days=window_days)
    rows = db.execute(
        select(Payment.payment_date, Invoice.issue_date)
        .join(Invoice, Invoice.invoice_id == Payment.invoice_id)
        .where(
            Invoice.status == "PAID",
            Invoice.issue_date.is_not(None),
            Payment.status == "CLEARED",
            Payment.payment_date >= cutoff,
        )
    ).all()
    deltas = [
        (pay_date - issue_date).days
        for pay_date, issue_date in rows
        if pay_date is not None and issue_date is not None
    ]
    if not deltas:
        return None
    return sum(deltas) / len(deltas)


def compute_customer_balances(db: Session, *, today: date) -> list[CustomerBalance]:
    """Open AR per (customer, currency) with overdue split out.

    Only SENT and PARTIAL invoices contribute. Rows with balance_due == 0 are
    filtered out. Customers with no open invoices do not appear.
    """
    overdue_expr = case(
        (Invoice.due_date < today, Invoice.balance_due),
        else_=Decimal("0"),
    )
    rows = db.execute(
        select(
            Invoice.customer_id,
            Invoice.currency,
            func.sum(Invoice.balance_due).label("balance"),
            func.sum(overdue_expr).label("overdue"),
        )
        .where(
            Invoice.status.in_(_OPEN_STATUSES),
            Invoice.is_template.is_(False),
            Invoice.balance_due > 0,
            Invoice.customer_id.is_not(None),
        )
        .group_by(Invoice.customer_id, Invoice.currency)
    ).all()
    return [
        CustomerBalance(
            customer_id=cust_id,
            currency=ccy,
            balance=bal or Decimal("0"),
            overdue=od or Decimal("0"),
        )
        for cust_id, ccy, bal, od in rows
    ]


def compute_invoices_summary(db: Session, *, today: date) -> InvoicesSummary:
    overdue = _bucket(db, where_clause=Invoice.due_date < today)
    due_30d = _bucket(
        db,
        where_clause=(Invoice.due_date >= today)
        & (Invoice.due_date <= today + timedelta(days=30)),
    )
    return InvoicesSummary(
        overdue=overdue,
        due_30d=due_30d,
        avg_days_to_pay=_avg_days_to_pay(db, today=today),
    )


def compute_dashboard_stats(db: Session) -> dict:
    """Counts + open AR/quotation totals per currency for the dashboard.

    Shared by GET /api/v1/stats/dashboard and the MCP `get_stats` tool so
    the two never drift. Deliberately excludes `recent_activity` (a
    ReconciliationLog x Payment join / activity feed, not a summable stat)
    — the router appends that itself on top of this dict.

    `open_quotation_pipeline` is sorted largest-total-first by the raw
    Decimal amount *before* any string coercion happens in a caller — do
    that sort here, on Decimals, not after converting to strings (string
    sort order does not match numeric order, e.g. "900" > "1000").
    """
    awaiting_review_count = (
        db.scalar(
            select(func.count(Payment.payment_id)).where(
                Payment.status == "PENDING_MANUAL_REVIEW"
            )
        )
        or 0
    )

    draft_count = (
        db.scalar(select(func.count(Invoice.invoice_id)).where(Invoice.status == "DRAFT")) or 0
    )

    sent_count = (
        db.scalar(select(func.count(Invoice.invoice_id)).where(Invoice.status == "SENT")) or 0
    )

    customer_count = db.scalar(select(func.count(Customer.customer_id))) or 0

    # Payment.created_at is a naive (timezone=False) UTC column, so build a
    # naive UTC datetime here — avoids the deprecated datetime.utcnow().
    thirty_days_ago = datetime.now(timezone.utc).replace(tzinfo=None) - timedelta(days=30)
    auto_cleared_last_30d = (
        db.scalar(
            select(func.count(Payment.payment_id)).where(
                Payment.status == "CLEARED",
                Payment.created_at >= thirty_days_ago,
            )
        )
        or 0
    )

    ar_rows = db.execute(
        select(Invoice.currency, func.sum(Invoice.balance_due))
        .where(Invoice.status.in_(_OPEN_STATUSES))
        .group_by(Invoice.currency)
    ).all()
    open_ar_by_currency = [{"currency": c, "amount": a or Decimal("0")} for c, a in ar_rows]

    open_quotation_count = (
        db.scalar(
            select(func.count(Quotation.quotation_id)).where(
                Quotation.status.in_(_OPEN_QUOTE_STATUSES)
            )
        )
        or 0
    )
    quote_rows = db.execute(
        select(Quotation.currency, func.sum(Quotation.amount))
        .where(Quotation.status.in_(_OPEN_QUOTE_STATUSES))
        .group_by(Quotation.currency)
    ).all()
    open_quotation_pipeline = [{"currency": c, "amount": a or Decimal("0")} for c, a in quote_rows]
    # Largest total first — sort on Decimal amounts (see docstring).
    open_quotation_pipeline.sort(key=lambda row: row["amount"], reverse=True)

    return {
        "awaiting_review_count": awaiting_review_count,
        "draft_count": draft_count,
        "sent_count": sent_count,
        "customer_count": customer_count,
        "auto_cleared_last_30d": auto_cleared_last_30d,
        "open_ar_by_currency": open_ar_by_currency,
        "open_quotation_count": open_quotation_count,
        "open_quotation_pipeline": open_quotation_pipeline,
    }
