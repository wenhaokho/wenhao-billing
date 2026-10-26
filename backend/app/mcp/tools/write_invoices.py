"""Invoice write tools — thin wrappers over app.services.invoicing.

Never re-implement invoice/ledger logic here: every tool below just builds
the matching Pydantic payload from tool args, calls the corresponding
`app.services.invoicing` function, and serializes the result via
`_INVOICE_DETAIL_FIELDS` (imported from read_invoices.py). These are
single-record returns, so they use the detail set rather than the narrow
`_INVOICE_FIELDS` that `list_invoices` uses — a caller that supplied
notes/terms/discount needs to see whether the values landed.

`update_invoice` vets its `changes` dict with `reject_unknown_changes`
before opening a session — see app/mcp/validate.py for why unknown keys
must not be silently ignored.

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
from app.mcp.server import mcp
from app.mcp.tools.read_invoices import _INVOICE_DETAIL_FIELDS, invoice_to_dict
from app.mcp.validate import reject_unknown_changes
from app.schemas.invoice import (
    InvoiceCreate,
    InvoiceLineItemIn,
    InvoiceUpdate,
    RecurringTemplateCreate,
)
from app.services import draft_notifications, invoicing


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
    wht_type: str | None = None,
    wht_rate: str | None = None,
    wht_gross_up: bool | None = None,
) -> dict:
    """Create a DRAFT invoice (lands in Awaiting Finalization). Autonomous.

    Optional withholding: `wht_type` NONE|PPH21|PPH23, `wht_rate` percent
    (defaults 2.5 for PPH21, 2 for PPH23), `wht_gross_up`; gross-up raises the
    invoice so that after the client withholds PPh you still receive the full
    base amount. Omitted values fall back to the customer's defaults."""
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
            wht_type=wht_type,
            wht_rate=Decimal(wht_rate) if wht_rate else None,
            wht_gross_up=wht_gross_up,
        )
        with tool_session() as db:
            inv = invoicing.create_invoice(db, payload)
            db.flush()
            return invoice_to_dict(inv, _INVOICE_DETAIL_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}


@mcp.tool
def update_invoice(invoice_id: str, changes: dict) -> dict:
    """Edit a DRAFT invoice. `changes` keys are InvoiceUpdate field names (e.g.
    notes, payment_terms, discount_type/discount_value, line_items,
    wht_type/wht_rate/wht_gross_up). Gross-up raises the invoice so that after
    the client withholds PPh you still receive the full base amount. Autonomous.

    Unknown keys are rejected rather than applied, so a near-miss like "note"
    or "terms" errors instead of being dropped by Pydantic's default
    extra="ignore" while the tool still reports success.
    """
    if (err := reject_unknown_changes(changes, InvoiceUpdate)) is not None:
        return err
    try:
        with tool_session() as db:
            inv = invoicing.update_invoice(db, UUID(invoice_id), InvoiceUpdate(**changes))
            db.flush()
            return invoice_to_dict(inv, _INVOICE_DETAIL_FIELDS)
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
            return invoice_to_dict(inv, _INVOICE_DETAIL_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}


@mcp.tool
def void_invoice(invoice_id: str) -> dict:
    """Void an invoice (reversible state, not a delete). Autonomous."""
    try:
        with tool_session() as db:
            inv = invoicing.void_invoice(db, UUID(invoice_id))
            db.flush()
            return invoice_to_dict(inv, _INVOICE_DETAIL_FIELDS)
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
    wht_type: str | None = None,
    wht_rate: str | None = None,
    wht_gross_up: bool | None = None,
) -> dict:
    """Create a recurring invoice template (is_template=True, DRAFT).
    `schedule` matches RecurringSchedule fields (frequency, interval,
    start_date, end_mode, end_date?, end_after_cycles?). Optional withholding
    via `wht_type` NONE|PPH21|PPH23, `wht_rate`, `wht_gross_up`; gross-up
    raises the invoice so that after the client withholds PPh you still
    receive the full base amount. Autonomous."""
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
            wht_type=wht_type,
            wht_rate=Decimal(wht_rate) if wht_rate else None,
            wht_gross_up=wht_gross_up,
        )
        with tool_session() as db:
            inv = invoicing.create_recurring_template(db, payload)
            db.flush()
            return invoice_to_dict(inv, _INVOICE_DETAIL_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}


@mcp.tool
def trigger_recurring(template_id: str, cycle_key: str) -> dict:
    """Manually trigger one recurring cycle for a template — idempotent per
    (template_id, cycle_key). Autonomous."""
    try:
        with tool_session() as db:
            existing = invoicing.find_cycle_invoice(
                db, template_invoice_id=UUID(template_id), cycle_key=cycle_key
            )
            inv = invoicing.trigger_recurring_cycle(
                db, template_invoice_id=UUID(template_id), cycle_key=cycle_key
            )
            db.flush()
            result = invoice_to_dict(inv, _INVOICE_DETAIL_FIELDS)
            # Collect notification data before tool_session commits/closes;
            # send only after the draft is committed.
            new_drafts = (
                [draft_notifications.draft_summary(db, inv, cycle_key)]
                if existing is None
                else []
            )
            emails = draft_notifications.user_emails(db) if new_drafts else []
    except invoicing.InvoicingError as e:
        return {"error": str(e)}
    draft_notifications.send_draft_notifications(emails, new_drafts)
    return result
