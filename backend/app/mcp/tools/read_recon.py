"""Reconciliation queue read tool.

Mirrors the recon router's awaiting-review query
(backend/app/api/v1/routers/recon.py: awaiting_review) — Payment rows
with status == "PENDING_MANUAL_REVIEW" (the exact status value the
router filters on; confirmed in that router, not guessed), ordered by
Payment.created_at.desc(). The router itself has no limit param; `limit`
is an MCP-only addition, matching the read_bills.py/read_projects.py
pattern of clamping to <=500.

Real columns on Payment (backend/app/models/payment.py): payment_id,
invoice_id, customer_id, amount, currency, payer_name, payer_reference,
payment_date, intake_source, external_ref, status, adjustment_type,
confidence_score, created_at (_PAYMENT_FIELDS is a deliberate subset).
"""
from __future__ import annotations

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.payment import Payment

_PAYMENT_FIELDS = [
    "payment_id",
    "invoice_id",
    "customer_id",
    "amount",
    "currency",
    "payer_name",
    "payer_reference",
    "payment_date",
    "intake_source",
    "status",
    "confidence_score",
]


@mcp.tool
def get_reconciliation_queue(limit: int = 200) -> list[dict]:
    """List payments awaiting manual review (status == PENDING_MANUAL_REVIEW)."""
    with tool_session() as db:
        stmt = (
            select(Payment)
            .where(Payment.status == "PENDING_MANUAL_REVIEW")
            .order_by(Payment.created_at.desc())
            .limit(min(limit, 500))
        )
        return [to_dict(p, _PAYMENT_FIELDS) for p in db.scalars(stmt)]
