"""Dashboard / list-view aggregation stats.

Pure functions over the DB. Keep SQL here so routers stay thin and tests can
assert on shape directly.
"""

from __future__ import annotations

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


_OPEN_STATUSES = ("SENT", "PARTIAL")
_OPEN_QUOTE_STATUSES = ("SENT", "ACCEPTED")


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
