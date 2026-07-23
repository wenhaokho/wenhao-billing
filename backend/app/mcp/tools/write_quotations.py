"""Quotation write tools — thin wrappers over app.services.quoting.

Same pattern as write_invoices.py — `quoting.create_quotation` and
`quoting.update_quotation` both take an id and self-raise
`quoting.QuotingError` (not-found, wrong status), so this tool just
builds args and delegates, catching only that error.
"""
from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_quotations import _QUOTATION_FIELDS
from app.schemas.quotation import QuotationCreate, QuotationLineItemIn, QuotationUpdate
from app.services import quoting


def _lines(raw: list[dict]) -> list[QuotationLineItemIn]:
    """Build QuotationLineItemIn list from tool-arg dicts. Money fields
    arrive as strings over MCP — convert to Decimal explicitly rather
    than relying on implicit coercion."""
    return [
        QuotationLineItemIn(
            description=ln["description"],
            quantity=Decimal(str(ln["quantity"])),
            unit_price=Decimal(str(ln["unit_price"])),
            item_id=UUID(ln["item_id"]) if ln.get("item_id") else None,
            position=ln.get("position", 0),
        )
        for ln in raw
    ]


@mcp.tool
def create_quotation(
    customer_id: str,
    currency: str,
    line_items: list[dict],
    project_id: str | None = None,
    po_so_number: str | None = None,
    issue_date: str | None = None,
    valid_until: str | None = None,
    payment_terms: str | None = None,
    notes: str | None = None,
    footer: str | None = None,
    discount_type: str | None = None,
    discount_value: str | None = None,
) -> dict:
    """Create a DRAFT quotation. Autonomous."""
    try:
        payload = QuotationCreate(
            customer_id=UUID(customer_id),
            project_id=UUID(project_id) if project_id else None,
            currency=currency,
            po_so_number=po_so_number,
            issue_date=issue_date,
            valid_until=valid_until,
            payment_terms=payment_terms,
            notes=notes,
            footer=footer,
            discount_type=discount_type,
            discount_value=Decimal(discount_value) if discount_value else None,
            line_items=_lines(line_items),
        )
        with tool_session() as db:
            q = quoting.create_quotation(db, payload)
            db.flush()
            return to_dict(q, _QUOTATION_FIELDS)
    except quoting.QuotingError as e:
        return {"error": str(e)}


@mcp.tool
def update_quotation(quotation_id: str, changes: dict) -> dict:
    """Edit a DRAFT/SENT quotation. `changes` matches QuotationUpdate
    fields (e.g. notes, payment_terms, discount_type/discount_value,
    line_items). Autonomous."""
    try:
        with tool_session() as db:
            q = quoting.update_quotation(db, UUID(quotation_id), QuotationUpdate(**changes))
            db.flush()
            return to_dict(q, _QUOTATION_FIELDS)
    except quoting.QuotingError as e:
        return {"error": str(e)}
