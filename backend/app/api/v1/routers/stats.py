from datetime import date, datetime, timedelta
from decimal import Decimal
from uuid import UUID

from fastapi import APIRouter, Depends
from pydantic import BaseModel
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from app.config import get_settings

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.models.quotation import Quotation
from app.models.recon_log import ReconciliationLog
from app.models.user import User
from app.services.stats import compute_invoices_summary

router = APIRouter(prefix="/stats", tags=["stats"])


class CurrencyAmount(BaseModel):
    currency: str
    amount: Decimal


class ActivityItem(BaseModel):
    log_id: UUID
    payment_id: UUID
    action: str
    reasons: list[str]
    actor_user_id: UUID | None
    created_at: datetime
    payer_name: str | None = None
    amount: Decimal | None = None
    currency: str | None = None


class MonthlyRevenueBucket(BaseModel):
    # "YYYY-MM" in calendar order, oldest first
    month: str
    # One entry per currency that had revenue in at least one month in the window.
    totals: dict[str, Decimal]


class MonthlyRevenueResponse(BaseModel):
    base_currency: str
    currencies: list[str]
    months: list[MonthlyRevenueBucket]


class InvoicesSummaryResponse(BaseModel):
    overdue: list[CurrencyAmount]
    due_30d: list[CurrencyAmount]
    avg_days_to_pay: float | None


class DashboardStats(BaseModel):
    awaiting_review_count: int
    draft_count: int
    sent_count: int
    customer_count: int
    auto_cleared_last_30d: int
    open_ar_by_currency: list[CurrencyAmount]
    open_quotation_count: int
    open_quotation_pipeline: list[CurrencyAmount]
    recent_activity: list[ActivityItem]


@router.get("/dashboard", response_model=DashboardStats)
def dashboard(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> DashboardStats:
    awaiting_review_count = db.scalar(
        select(func.count(Payment.payment_id)).where(
            Payment.status == "PENDING_MANUAL_REVIEW"
        )
    ) or 0

    draft_count = db.scalar(
        select(func.count(Invoice.invoice_id)).where(Invoice.status == "DRAFT")
    ) or 0

    sent_count = db.scalar(
        select(func.count(Invoice.invoice_id)).where(Invoice.status == "SENT")
    ) or 0

    customer_count = db.scalar(select(func.count(Customer.customer_id))) or 0

    # Cleared payments in last 30 days
    thirty_days_ago = datetime.utcnow() - timedelta(days=30)
    auto_cleared_last_30d = db.scalar(
        select(func.count(Payment.payment_id)).where(
            Payment.status == "CLEARED",
            Payment.created_at >= thirty_days_ago,
        )
    ) or 0

    # Open AR = sum of balance_due where status in SENT/PARTIAL, grouped by currency
    ar_rows = db.execute(
        select(Invoice.currency, func.sum(Invoice.balance_due))
        .where(Invoice.status.in_(("SENT", "PARTIAL")))
        .group_by(Invoice.currency)
    ).all()
    open_ar = [CurrencyAmount(currency=c, amount=a or Decimal("0")) for c, a in ar_rows]

    # Open quotations = SENT + ACCEPTED (not yet INVOICED / DECLINED / EXPIRED / VOID)
    open_quote_statuses = ("SENT", "ACCEPTED")
    open_quotation_count = db.scalar(
        select(func.count(Quotation.quotation_id)).where(
            Quotation.status.in_(open_quote_statuses)
        )
    ) or 0
    quote_rows = db.execute(
        select(Quotation.currency, func.sum(Quotation.amount))
        .where(Quotation.status.in_(open_quote_statuses))
        .group_by(Quotation.currency)
    ).all()
    open_quotation_pipeline = [
        CurrencyAmount(currency=c, amount=a or Decimal("0")) for c, a in quote_rows
    ]
    # Sort largest total first so the dashboard hint shows the top currency
    open_quotation_pipeline.sort(key=lambda x: x.amount, reverse=True)

    # Recent activity: last 10 reconciliation_log rows, joined to payment for context
    activity_rows = db.execute(
        select(
            ReconciliationLog.log_id,
            ReconciliationLog.payment_id,
            ReconciliationLog.action,
            ReconciliationLog.reasons,
            ReconciliationLog.actor_user_id,
            ReconciliationLog.created_at,
            Payment.payer_name,
            Payment.amount,
            Payment.currency,
        )
        .join(Payment, Payment.payment_id == ReconciliationLog.payment_id, isouter=True)
        .order_by(ReconciliationLog.created_at.desc())
        .limit(10)
    ).all()

    recent_activity = [
        ActivityItem(
            log_id=r.log_id,
            payment_id=r.payment_id,
            action=r.action,
            reasons=r.reasons or [],
            actor_user_id=r.actor_user_id,
            created_at=r.created_at,
            payer_name=r.payer_name,
            amount=r.amount,
            currency=r.currency,
        )
        for r in activity_rows
    ]

    return DashboardStats(
        awaiting_review_count=awaiting_review_count,
        draft_count=draft_count,
        sent_count=sent_count,
        customer_count=customer_count,
        auto_cleared_last_30d=auto_cleared_last_30d,
        open_ar_by_currency=open_ar,
        open_quotation_count=open_quotation_count,
        open_quotation_pipeline=open_quotation_pipeline,
        recent_activity=recent_activity,
    )


def _last_12_month_labels(today: date) -> list[str]:
    """Return YYYY-MM labels for the 12 months ending with `today`'s month, oldest first."""
    labels: list[str] = []
    y, m = today.year, today.month
    # Walk backwards 11 times then reverse
    points: list[tuple[int, int]] = [(y, m)]
    for _ in range(11):
        m -= 1
        if m == 0:
            m = 12
            y -= 1
        points.append((y, m))
    points.reverse()
    return [f"{yy:04d}-{mm:02d}" for yy, mm in points]


@router.get("/revenue-monthly", response_model=MonthlyRevenueResponse)
def revenue_monthly(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> MonthlyRevenueResponse:
    """Invoiced revenue by calendar month for the last 12 months.

    Revenue = invoice.amount for invoices with status in SENT/PARTIAL/PAID,
    bucketed by issue_date's YYYY-MM. DRAFT and VOID are excluded. Bills (AP)
    are not revenue and are not counted. Grouped per currency so the chart can
    show a stacked bar or single series for the base currency.
    """
    settings = get_settings()
    today = date.today()
    labels = _last_12_month_labels(today)
    # Lower bound: first day of the oldest bucket
    oldest_y, oldest_m = int(labels[0][:4]), int(labels[0][5:])
    window_start = date(oldest_y, oldest_m, 1)

    month_expr = func.to_char(Invoice.issue_date, "YYYY-MM")
    rows = db.execute(
        select(month_expr.label("month"), Invoice.currency, func.sum(Invoice.amount))
        .where(
            Invoice.status.in_(("SENT", "PARTIAL", "PAID")),
            Invoice.issue_date.is_not(None),
            Invoice.issue_date >= window_start,
            Invoice.is_template.is_(False),
        )
        .group_by("month", Invoice.currency)
    ).all()

    # Seed empty buckets so the chart always has 12 points.
    buckets: dict[str, dict[str, Decimal]] = {lbl: {} for lbl in labels}
    currencies: set[str] = set()
    for month, ccy, total in rows:
        if month in buckets:
            buckets[month][ccy] = (buckets[month].get(ccy, Decimal("0")) + (total or Decimal("0")))
            currencies.add(ccy)

    return MonthlyRevenueResponse(
        base_currency=settings.base_currency,
        currencies=sorted(currencies),
        months=[MonthlyRevenueBucket(month=lbl, totals=buckets[lbl]) for lbl in labels],
    )


@router.get("/invoices-summary", response_model=InvoicesSummaryResponse)
def invoices_summary(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> InvoicesSummaryResponse:
    """Summary tiles for the Invoices list (Wave parity).

    Returns Overdue + Due-within-30-days totals per currency, and the average
    number of days between issue and payment for invoices paid in the last 90
    days.
    """
    summary = compute_invoices_summary(db, today=date.today())
    return InvoicesSummaryResponse(
        overdue=[CurrencyAmount(currency=r.currency, amount=r.amount) for r in summary.overdue],
        due_30d=[CurrencyAmount(currency=r.currency, amount=r.amount) for r in summary.due_30d],
        avg_days_to_pay=summary.avg_days_to_pay,
    )
