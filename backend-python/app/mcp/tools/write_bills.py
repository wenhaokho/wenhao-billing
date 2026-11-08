"""Bill (AP) write tools — thin wrappers over app.services.billing_ap.

Never re-implement bill/line-item logic here. `billing_ap.update_bill`
takes an already-loaded `Bill` (not an id) — mirroring
routers/bills.py's own `update_bill`, this tool loads the row and returns
a not-found error itself before delegating, exactly like the router does.
Only `billing_ap.BillError` is caught for the service-raised failures
(e.g. editing a non-DRAFT/OPEN bill).

`update_bill` vets its `changes` dict with `reject_unknown_changes` before
loading the row — see app/mcp/validate.py. That ordering is deliberate: a
bad key is reported as a bad key even when the bill id is also wrong, so
the caller fixes the name rather than chasing the not-found. Returns use
`_BILL_DETAIL_FIELDS` so supplied notes/terms/discount are echoed back.
"""
from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_bills import _BILL_DETAIL_FIELDS
from app.mcp.validate import reject_unknown_changes
from app.models.bill import Bill
from app.schemas.bill import BillCreate, BillLineItemIn, BillUpdate
from app.services import billing_ap


def _lines(raw: list[dict]) -> list[BillLineItemIn]:
    """Build BillLineItemIn list from tool-arg dicts. Money fields arrive
    as strings over MCP — convert to Decimal explicitly rather than
    relying on implicit coercion."""
    return [
        BillLineItemIn(
            description=ln["description"],
            quantity=Decimal(str(ln["quantity"])),
            unit_price=Decimal(str(ln["unit_price"])),
            item_id=UUID(ln["item_id"]) if ln.get("item_id") else None,
            expense_account_id=ln.get("expense_account_id"),
            position=ln.get("position", 0),
        )
        for ln in raw
    ]


@mcp.tool
def create_bill(
    vendor_id: str,
    currency: str,
    line_items: list[dict],
    project_id: str | None = None,
    bill_number: str | None = None,
    po_number: str | None = None,
    issue_date: str | None = None,
    due_date: str | None = None,
    payment_terms: str | None = None,
    notes: str | None = None,
    discount_type: str | None = None,
    discount_value: str | None = None,
) -> dict:
    """Create a DRAFT bill. Autonomous."""
    try:
        payload = BillCreate(
            vendor_id=UUID(vendor_id),
            project_id=UUID(project_id) if project_id else None,
            bill_number=bill_number,
            po_number=po_number,
            currency=currency,
            issue_date=issue_date,
            due_date=due_date,
            payment_terms=payment_terms,
            notes=notes,
            discount_type=discount_type,
            discount_value=Decimal(discount_value) if discount_value else None,
            line_items=_lines(line_items),
        )
        with tool_session() as db:
            bill = billing_ap.create_bill(db, payload)
            db.flush()
            return to_dict(bill, _BILL_DETAIL_FIELDS)
    except billing_ap.BillError as e:
        return {"error": str(e)}


@mcp.tool
def update_bill(bill_id: str, changes: dict) -> dict:
    """Edit a DRAFT/OPEN bill. `changes` keys are BillUpdate field names (e.g.
    notes, payment_terms, discount_type/discount_value, line_items).
    Autonomous.

    Unknown keys are rejected rather than applied, so a near-miss like "note"
    or "terms" errors instead of being dropped by Pydantic's default
    extra="ignore" while the tool still reports success.
    """
    if (err := reject_unknown_changes(changes, BillUpdate)) is not None:
        return err
    try:
        with tool_session() as db:
            bill = db.get(Bill, UUID(bill_id))
            if bill is None:
                return {"error": f"bill {bill_id} not found"}
            bill = billing_ap.update_bill(db, bill, BillUpdate(**changes))
            db.flush()
            return to_dict(bill, _BILL_DETAIL_FIELDS)
    except billing_ap.BillError as e:
        return {"error": str(e)}
