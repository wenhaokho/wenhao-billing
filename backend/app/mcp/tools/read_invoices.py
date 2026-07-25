"""Invoice read tools.

Mirrors the invoice router's list/get queries
(backend/app/api/v1/routers/invoices.py: list_invoices ~L321-344,
get_invoice ~L377-385).

Real columns on Invoice (backend/app/models/invoice.py): invoice_id,
invoice_number, customer_id, project_id, source_quote_id, invoice_type,
po_so_number, currency, subtotal, discount_type, discount_value, amount,
balance_due, status, billing_cycle_ref, issue_date, due_date,
coverage_start, coverage_end, payment_terms, notes, footer, is_template,
created_at (plus a line_items relationship not surfaced here).

Two field sets, deliberately (the read_customers.py pattern):
`_INVOICE_FIELDS` is the narrow subset for `list_invoices`;
`_INVOICE_DETAIL_FIELDS` adds the editable terms/notes/footer/discount
columns and is used by single-record returns (`get_invoice` and the
write_invoices.py tools) so a caller can see that supplied data landed.
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

_INVOICE_DETAIL_FIELDS = [
    *_INVOICE_FIELDS,
    "project_id",
    "po_so_number",
    "subtotal",
    "discount_type",
    "discount_value",
    "payment_terms",
    "notes",
    "footer",
    "is_template",
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
    """Fetch one invoice by id, including terms/notes/footer/discount.
    Returns {} if not found."""
    with tool_session() as db:
        inv = db.get(Invoice, UUID(invoice_id))
        return to_dict(inv, _INVOICE_DETAIL_FIELDS) if inv else {}
