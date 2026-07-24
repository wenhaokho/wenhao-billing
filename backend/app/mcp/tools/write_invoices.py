"""Invoice write tools — thin wrappers over app.services.invoicing.

Never re-implement invoice/ledger logic here: every tool below just builds
the matching Pydantic payload from tool args, calls the corresponding
`app.services.invoicing` function, and serializes the result via
`_INVOICE_FIELDS` (imported from read_invoices.py — the deliberate field
subset shared by read and write tools).

Error handling: `invoicing.InvoicingError` is caught at the tool boundary
and returned as `{"error": str(e)}` instead of propagating — this lets the
model see and relay the failure instead of the MCP call blowing up. Because
the raise happens *inside* the `with tool_session():` block, the context
manager's own `except Exception: db.rollback(); raise` fires first (rolling
back anything written this call), and only then does our `except` catch it
here and turn it into a dict.
"""
from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_invoices import _INVOICE_FIELDS
from app.schemas.invoice import InvoiceCreate, InvoiceLineItemIn, InvoiceUpdate, RecurringTemplateCreate
from app.services import invoicing


def _lines(raw: list[dict]) -> list[InvoiceLineItemIn]:
    """Build InvoiceLineItemIn list from tool-arg dicts. Money fields arrive
    as strings over MCP — convert to Decimal explicitly rather than relying
    on implicit coercion."""
    return [
        InvoiceLineItemIn(
            description=ln["description"],
            quantity=Decimal(str(ln["quantity"])),
            unit_price=Decimal(str(ln["unit_price"])),
            item_id=UUID(ln["item_id"]) if ln.get("item_id") else None,
            position=ln.get("position", 0),
        )
        for ln in raw
    ]


@mcp.tool
def create_invoice(
    customer_id: str,
    currency: str,
    line_items: list[dict],
    invoice_type: str = "MILESTONE",
    discount_type: str | None = None,
    discount_value: str | None = None,
    payment_terms: str | None = None,
    notes: str | None = None,
) -> dict:
    """Create a DRAFT invoice (lands in Awaiting Finalization). Autonomous."""
    try:
        payload = InvoiceCreate(
            customer_id=UUID(customer_id),
            currency=currency,
            invoice_type=invoice_type,
            line_items=_lines(line_items),
            discount_type=discount_type,
            discount_value=Decimal(discount_value) if discount_value else None,
            payment_terms=payment_terms,
            notes=notes,
        )
        with tool_session() as db:
            inv = invoicing.create_invoice(db, payload)
            db.flush()
            return to_dict(inv, _INVOICE_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}


@mcp.tool
def update_invoice(invoice_id: str, changes: dict) -> dict:
    """Edit a DRAFT invoice. `changes` matches InvoiceUpdate fields (e.g.
    notes, payment_terms, discount_type/discount_value, line_items).
    Autonomous."""
    try:
        with tool_session() as db:
            inv = invoicing.update_invoice(db, UUID(invoice_id), InvoiceUpdate(**changes))
            db.flush()
            return to_dict(inv, _INVOICE_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}


@mcp.tool
def finalize_invoice(invoice_id: str) -> dict:
    """Issue a DRAFT invoice (-> OPEN; becomes SENT only when emailed).

    Autonomous (internal, reversible via void). A zero-value invoice settles
    straight to PAID.
    """
    try:
        with tool_session() as db:
            inv = invoicing.finalize_invoice(db, UUID(invoice_id))
            db.flush()
            return to_dict(inv, _INVOICE_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}


@mcp.tool
def void_invoice(invoice_id: str) -> dict:
    """Void an invoice (reversible state, not a delete). Autonomous."""
    try:
        with tool_session() as db:
            inv = invoicing.void_invoice(db, UUID(invoice_id))
            db.flush()
            return to_dict(inv, _INVOICE_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}


@mcp.tool
def create_recurring_template(
    customer_id: str,
    currency: str,
    line_items: list[dict],
    schedule: dict,
    payment_terms: str,
    project_id: str | None = None,
    po_so_number: str | None = None,
    notes: str | None = None,
    footer: str | None = None,
    discount_type: str | None = None,
    discount_value: str | None = None,
) -> dict:
    """Create a recurring invoice template (is_template=True, DRAFT).
    `schedule` matches RecurringSchedule fields (frequency, interval,
    start_date, end_mode, end_date?, end_after_cycles?). Autonomous."""
    try:
        payload = RecurringTemplateCreate(
            customer_id=UUID(customer_id) if customer_id else None,
            project_id=UUID(project_id) if project_id else None,
            currency=currency,
            po_so_number=po_so_number,
            payment_terms=payment_terms,
            notes=notes,
            footer=footer,
            discount_type=discount_type,
            discount_value=Decimal(discount_value) if discount_value else None,
            line_items=_lines(line_items),
            schedule=schedule,
        )
        with tool_session() as db:
            inv = invoicing.create_recurring_template(db, payload)
            db.flush()
            return to_dict(inv, _INVOICE_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}


@mcp.tool
def trigger_recurring(template_id: str, cycle_key: str) -> dict:
    """Manually trigger one recurring cycle for a template — idempotent per
    (template_id, cycle_key). Autonomous."""
    try:
        with tool_session() as db:
            inv = invoicing.trigger_recurring_cycle(
                db, template_invoice_id=UUID(template_id), cycle_key=cycle_key
            )
            db.flush()
            return to_dict(inv, _INVOICE_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}
