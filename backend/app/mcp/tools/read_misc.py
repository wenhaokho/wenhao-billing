"""Misc read tools: business profile, dashboard stats."""
from __future__ import annotations

from datetime import datetime, timedelta, timezone
from decimal import Decimal

from sqlalchemy import func, select

from app.mcp.db import tool_session
from app.mcp.serialize import _coerce, to_dict
from app.mcp.server import mcp
from app.models.business_profile import BusinessProfile
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.models.quotation import Quotation

# Real columns on BusinessProfile (backend/app/models/business_profile.py):
# id, name, address, contact_email, contact_phone, invoice_title,
# invoice_summary, logo_url, default_notes, updated_at. There is no
# "legal_name" / "base_currency" column on this model (base_currency is a
# global app setting, not per-business-profile data).
_PROFILE_FIELDS = [
    "id",
    "name",
    "address",
    "contact_email",
    "contact_phone",
    "invoice_title",
    "invoice_summary",
    "logo_url",
    "default_notes",
    "updated_at",
]

_OPEN_AR_STATUSES = ("SENT", "PARTIAL")
_OPEN_QUOTE_STATUSES = ("SENT", "ACCEPTED")


@mcp.tool
def get_business_profile() -> dict:
    """Return the agency's business profile (name, contact, address, etc.)."""
    with tool_session() as db:
        row = db.scalar(select(BusinessProfile))
        if row is None:
            return {}
        return to_dict(row, _PROFILE_FIELDS)


@mcp.tool
def get_stats() -> dict:
    """Return dashboard/summary figures (counts + open AR/quotation totals
    per currency), mirroring GET /api/v1/stats/dashboard.

    There is no `app/services/stats.get_dashboard_stats` service function —
    that logic lives inline in `app/api/v1/routers/stats.py:dashboard()`.
    This tool reuses the same queries directly against `tool_session()`
    rather than calling the HTTP endpoint. `recent_activity` (a
    reconciliation-log feed, not a summary figure) is intentionally omitted
    to keep this tool's scope to countable/summable stats; see task report
    for the rationale.
    """
    with tool_session() as db:
        awaiting_review_count = (
            db.scalar(
                select(func.count(Payment.payment_id)).where(
                    Payment.status == "PENDING_MANUAL_REVIEW"
                )
            )
            or 0
        )

        draft_count = (
            db.scalar(select(func.count(Invoice.invoice_id)).where(Invoice.status == "DRAFT"))
            or 0
        )

        sent_count = (
            db.scalar(select(func.count(Invoice.invoice_id)).where(Invoice.status == "SENT"))
            or 0
        )

        customer_count = db.scalar(select(func.count(Customer.customer_id))) or 0

        # Payment.created_at is a naive (timezone=False) UTC column, so build
        # a naive UTC datetime here too — avoids the deprecated
        # datetime.utcnow() the router uses, without changing comparison
        # semantics.
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
            .where(Invoice.status.in_(_OPEN_AR_STATUSES))
            .group_by(Invoice.currency)
        ).all()
        open_ar_by_currency = [
            {"currency": c, "amount": _coerce(a or Decimal("0"))} for c, a in ar_rows
        ]

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
        open_quotation_pipeline = [
            {"currency": c, "amount": a or Decimal("0")} for c, a in quote_rows
        ]
        # Largest total first, matching the router's sort — then coerce to str.
        open_quotation_pipeline.sort(key=lambda x: x["amount"], reverse=True)
        open_quotation_pipeline = [
            {"currency": row["currency"], "amount": _coerce(row["amount"])}
            for row in open_quotation_pipeline
        ]

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
