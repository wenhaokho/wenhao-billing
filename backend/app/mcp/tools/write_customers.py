"""Customer write tools — thin wrappers over app.services.customers.

Never re-implement customer write logic here: each tool builds the
matching Pydantic payload from tool args, calls the corresponding
`app.services.customers` function (the exact logic extracted out of
routers/customers.py — see the refactor commit that precedes this one),
and serializes the result via `_CUSTOMER_FIELDS` (imported from
read_customers.py, the deliberate field subset shared by read and write
tools).

Error handling: `customers.CustomerError` is caught at the tool boundary
and returned as `{"error": str(e)}` — see write_invoices.py's module
docstring for why this is safe (the `with tool_session():` rollback fires
before this except catches it).
"""
from __future__ import annotations

from uuid import UUID

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_customers import _CUSTOMER_FIELDS
from app.schemas.customer import CustomerCreate, CustomerUpdate
from app.services import customers as customer_service


@mcp.tool
def create_customer(
    name: str,
    contact_email: str | None = None,
    contact_name: str | None = None,
    default_currency: str | None = None,
    notes: str | None = None,
) -> dict:
    """Create a customer. Autonomous."""
    try:
        payload = CustomerCreate(
            name=name,
            contact_email=contact_email,
            contact_name=contact_name,
            default_currency=default_currency,
            notes=notes,
        )
        with tool_session() as db:
            customer = customer_service.create_customer(db, payload)
            db.flush()
            return to_dict(customer, _CUSTOMER_FIELDS)
    except customer_service.CustomerError as e:
        return {"error": str(e)}


@mcp.tool
def update_customer(customer_id: str, changes: dict) -> dict:
    """Edit a customer. `changes` matches CustomerUpdate fields (e.g.
    name, contact_email, notes, default_currency, active). Autonomous."""
    try:
        with tool_session() as db:
            customer = customer_service.update_customer(
                db, UUID(customer_id), CustomerUpdate(**changes)
            )
            db.flush()
            return to_dict(customer, _CUSTOMER_FIELDS)
    except customer_service.CustomerError as e:
        return {"error": str(e)}
