"""Quotation read tools.

Mirrors the quotations router's list/get queries
(backend/app/api/v1/routers/quotations.py: list_quotations, get_one).

Real columns on Quotation (backend/app/models/quotation.py): quotation_id,
customer_id, project_id, quotation_number, po_so_number, currency,
subtotal, discount_type, discount_value, amount, status, issue_date,
valid_until, payment_terms, notes, footer, last_sent_at, accepted_at,
accepted_by, converted_invoice_id, created_at (plus line_items
relationship not surfaced here — _QUOTATION_FIELDS is a deliberate
subset, matching the read_invoices.py pattern).
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.quotation import Quotation

_QUOTATION_FIELDS = [
    "quotation_id",
    "quotation_number",
    "customer_id",
    "project_id",
    "currency",
    "amount",
    "status",
    "issue_date",
    "valid_until",
]


@mcp.tool
def list_quotations(
    status: list[str] | None = None,
    customer_id: str | None = None,
    limit: int = 200,
) -> list[dict]:
    """List quotations with optional status/customer filters."""
    with tool_session() as db:
        stmt = (
            select(Quotation)
            .order_by(Quotation.created_at.desc())
            .limit(min(limit, 500))
        )
        if status:
            stmt = stmt.where(Quotation.status.in_(status))
        if customer_id:
            stmt = stmt.where(Quotation.customer_id == UUID(customer_id))
        return [to_dict(q, _QUOTATION_FIELDS) for q in db.scalars(stmt)]


@mcp.tool
def get_quotation(quotation_id: str) -> dict:
    """Fetch one quotation by id. Returns {} if not found."""
    with tool_session() as db:
        q = db.get(Quotation, UUID(quotation_id))
        return to_dict(q, _QUOTATION_FIELDS) if q else {}
