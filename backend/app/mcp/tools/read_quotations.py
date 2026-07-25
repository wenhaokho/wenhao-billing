"""Quotation read tools.

Mirrors the quotations router's list/get queries
(backend/app/api/v1/routers/quotations.py: list_quotations, get_one).

Real columns on Quotation (backend/app/models/quotation.py): quotation_id,
customer_id, project_id, quotation_number, po_so_number, currency,
subtotal, discount_type, discount_value, amount, status, issue_date,
valid_until, payment_terms, notes, footer, last_sent_at, accepted_at,
accepted_by, converted_invoice_id, created_at (plus a line_items
relationship not surfaced here).

Two field sets, deliberately (the read_customers.py pattern):
`_QUOTATION_FIELDS` is the narrow subset for `list_quotations`;
`_QUOTATION_DETAIL_FIELDS` adds the editable terms/notes/footer/discount
columns and is used by single-record returns (`get_quotation` and the
write_quotations.py tools) so a caller can see that supplied data landed.
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

_QUOTATION_DETAIL_FIELDS = [
    *_QUOTATION_FIELDS,
    "po_so_number",
    "subtotal",
    "discount_type",
    "discount_value",
    "payment_terms",
    "notes",
    "footer",
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
    """Fetch one quotation by id, including terms/notes/footer/discount.
    Returns {} if not found."""
    with tool_session() as db:
        q = db.get(Quotation, UUID(quotation_id))
        return to_dict(q, _QUOTATION_DETAIL_FIELDS) if q else {}
