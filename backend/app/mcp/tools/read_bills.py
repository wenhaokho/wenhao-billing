"""Bill (AP) read tools.

Mirrors the bills router's list/get queries
(backend/app/api/v1/routers/bills.py: list_bills, get_bill).

Real columns on Bill (backend/app/models/bill.py): bill_id, vendor_id,
project_id, bill_number, po_number, currency, subtotal, discount_type,
discount_value, amount, balance_due, status, issue_date, due_date,
payment_terms, notes, created_at (plus a line_items relationship not
surfaced here).

Two field sets, deliberately (the read_customers.py pattern):
`_BILL_FIELDS` is the narrow subset for `list_bills`;
`_BILL_DETAIL_FIELDS` adds the editable terms/notes/discount columns and is
used by single-record returns (`get_bill` and the write_bills.py tools) so a
caller can see that supplied data landed.
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.bill import Bill

_BILL_FIELDS = [
    "bill_id",
    "vendor_id",
    "project_id",
    "bill_number",
    "po_number",
    "currency",
    "amount",
    "balance_due",
    "status",
    "issue_date",
    "due_date",
]

_BILL_DETAIL_FIELDS = [
    *_BILL_FIELDS,
    "subtotal",
    "discount_type",
    "discount_value",
    "payment_terms",
    "notes",
]


@mcp.tool
def list_bills(
    status: list[str] | None = None,
    vendor_id: str | None = None,
    limit: int = 200,
) -> list[dict]:
    """List bills with optional status/vendor filters."""
    with tool_session() as db:
        stmt = select(Bill).order_by(Bill.created_at.desc()).limit(min(limit, 500))
        if status:
            stmt = stmt.where(Bill.status.in_(status))
        if vendor_id:
            stmt = stmt.where(Bill.vendor_id == UUID(vendor_id))
        return [to_dict(b, _BILL_FIELDS) for b in db.scalars(stmt)]


@mcp.tool
def get_bill(bill_id: str) -> dict:
    """Fetch one bill by id, including terms/notes/discount. Returns {} if not
    found."""
    with tool_session() as db:
        b = db.get(Bill, UUID(bill_id))
        return to_dict(b, _BILL_DETAIL_FIELDS) if b else {}
