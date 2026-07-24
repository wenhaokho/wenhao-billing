"""Misc read tools: business profile, dashboard stats."""
from __future__ import annotations

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import coerce_value, to_dict
from app.mcp.server import mcp
from app.models.business_profile import BusinessProfile
from app.services.stats import compute_dashboard_stats

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


@mcp.tool
def get_business_profile() -> dict:
    """Return the agency's business profile (name, contact, address, etc.)."""
    with tool_session() as db:
        row = db.scalar(select(BusinessProfile))
        if row is None:
            return {}
        return to_dict(row, _PROFILE_FIELDS)


def _coerce_currency_rows(rows: list[dict]) -> list[dict]:
    """[{"currency": str, "amount": Decimal}, ...] -> JSON-safe dicts.
    Order is preserved as-is — compute_dashboard_stats already sorts
    open_quotation_pipeline by the raw Decimal amount before this runs."""
    return [{"currency": r["currency"], "amount": coerce_value(r["amount"])} for r in rows]


@mcp.tool
def get_stats() -> dict:
    """Return dashboard/summary figures (counts + open AR/quotation totals
    per currency), mirroring GET /api/v1/stats/dashboard (minus
    `recent_activity`, which is a reconciliation-log feed, not a summary
    figure).

    Delegates all query logic to
    `app.services.stats.compute_dashboard_stats`, the same function the
    router calls — no duplicated SQL here.
    """
    with tool_session() as db:
        stats = compute_dashboard_stats(db)
        return {
            "awaiting_review_count": stats["awaiting_review_count"],
            "draft_count": stats["draft_count"],
            "sent_count": stats["sent_count"],
            "customer_count": stats["customer_count"],
            "auto_cleared_last_30d": stats["auto_cleared_last_30d"],
            "open_ar_by_currency": _coerce_currency_rows(stats["open_ar_by_currency"]),
            "open_quotation_count": stats["open_quotation_count"],
            "open_quotation_pipeline": _coerce_currency_rows(stats["open_quotation_pipeline"]),
            "mrr_by_currency": _coerce_currency_rows(stats["mrr_by_currency"]),
            "base_currency": stats["base_currency"],
            "open_ar_base_by_currency": [
                {"currency": r["currency"], "base_amount": coerce_value(r["base_amount"])}
                for r in stats["open_ar_base_by_currency"]
            ],
            "open_ar_unconverted": stats["open_ar_unconverted"],
        }
