"""Invoice read tools.

Mirrors the invoice router's list/get queries
(backend/app/api/v1/routers/invoices.py: list_invoices ~L321-344,
get_invoice ~L377-385).

Real columns on Invoice (backend/app/models/invoice.py): invoice_id,
invoice_number, customer_id, invoice_type, currency, amount, balance_due,
status, issue_date, due_date (plus other fields not surfaced here —
_INVOICE_FIELDS is a deliberate subset; Task 7 imports this list).
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.invoice import Invoice

_INVOICE_FIELDS = [
    "invoice_id",
    "invoice_number",
    "customer_id",
    "invoice_type",
    "currency",
    "amount",
    "balance_due",
    "status",
    "issue_date",
    "due_date",
]


@mcp.tool
def list_invoices(
    status: list[str] | None = None,
    customer_id: str | None = None,
    invoice_type: str | None = None,
    limit: int = 200,
) -> list[dict]:
    """List non-template invoices with optional status/customer/type filters."""
    with tool_session() as db:
        stmt = (
            select(Invoice)
            .where(Invoice.is_template.is_(False))
            .order_by(Invoice.created_at.desc())
            .limit(min(limit, 500))
        )
        if status:
            stmt = stmt.where(Invoice.status.in_(status))
        if customer_id:
            stmt = stmt.where(Invoice.customer_id == UUID(customer_id))
        if invoice_type:
            stmt = stmt.where(Invoice.invoice_type == invoice_type)
        return [to_dict(i, _INVOICE_FIELDS) for i in db.scalars(stmt)]


@mcp.tool
def get_invoice(invoice_id: str) -> dict:
    """Fetch one invoice by id. Returns {} if not found."""
    with tool_session() as db:
        inv = db.get(Invoice, UUID(invoice_id))
        return to_dict(inv, _INVOICE_FIELDS) if inv else {}
