"""Customer read tools + the ambiguity-stop resolver.

Real columns on Customer (backend/app/models/customer.py): customer_id,
name, contact_email (plus many address/contact fields not surfaced here —
_CUSTOMER_FIELDS is a deliberate subset, matching the read_misc.py pattern).
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.customer import Customer

_CUSTOMER_FIELDS = ["customer_id", "name", "contact_email"]


@mcp.tool
def list_customers(query: str | None = None, limit: int = 200) -> list[dict]:
    """List customers, optionally filtered by a name substring."""
    with tool_session() as db:
        stmt = select(Customer).order_by(Customer.name).limit(limit)
        if query:
            stmt = stmt.where(Customer.name.ilike(f"%{query}%"))
        return [to_dict(c, _CUSTOMER_FIELDS) for c in db.scalars(stmt)]


@mcp.tool
def get_customer(customer_id: str) -> dict:
    """Fetch one customer by id. Returns {} if not found."""
    with tool_session() as db:
        c = db.get(Customer, UUID(customer_id))
        return to_dict(c, _CUSTOMER_FIELDS) if c else {}


@mcp.tool
def resolve_customer(query: str) -> dict:
    """Resolve a customer name substring to a single customer id.

    Ambiguity-stop primitive: on exactly one match, returns
    {"customer_id": "<uuid>"}. On zero or more than one match, returns
    {"candidates": [{"customer_id", "name"}, ...]} and takes NO other
    action — later write tools depend on this exact shape to decide
    whether they may proceed automatically or must ask for disambiguation.
    """
    with tool_session() as db:
        rows = list(
            db.scalars(
                select(Customer).where(Customer.name.ilike(f"%{query}%")).limit(10)
            )
        )
        if len(rows) == 1:
            return {"customer_id": str(rows[0].customer_id)}
        return {
            "candidates": [
                {"customer_id": str(c.customer_id), "name": c.name} for c in rows
            ]
        }
