"""Customer read tools + the ambiguity-stop resolver.

Two field sets, deliberately:

- `_CUSTOMER_FIELDS` — the narrow identity subset for `list_customers`,
  which returns up to 500 rows and would bloat badly if it carried the full
  profile (the read_misc.py pattern).
- `_CUSTOMER_DETAIL_FIELDS` — the full profile, used by single-record
  returns (`get_customer` and the write tools). Single-record returns MUST
  echo phone/address back: when they returned only the identity subset, a
  caller had no way to see that contact/address data it supplied had gone
  nowhere.
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.customer import Customer

_CUSTOMER_FIELDS = ["customer_id", "name", "contact_email"]

_CUSTOMER_DETAIL_FIELDS = [
    "customer_id",
    "name",
    "active",
    "matching_aliases",
    # Primary contact
    "contact_first_name",
    "contact_last_name",
    "contact_email",
    "contact_phone",
    "contact_phone_2",
    # Misc profile
    "account_number",
    "website",
    "notes",
    # Billing
    "default_currency",
    "billing_address",  # legacy unstructured column: readable, never written here
    "billing_address1",
    "billing_address2",
    "billing_city",
    "billing_state",
    "billing_postal_code",
    "billing_country",
    # Shipping
    "ship_to_name",
    "shipping_address1",
    "shipping_address2",
    "shipping_city",
    "shipping_state",
    "shipping_postal_code",
    "shipping_country",
    "shipping_phone",
    "shipping_delivery_instructions",
]


@mcp.tool
def list_customers(query: str | None = None, limit: int = 200) -> list[dict]:
    """List customers, optionally filtered by a name substring."""
    with tool_session() as db:
        stmt = select(Customer).order_by(Customer.name).limit(min(limit, 500))
        if query:
            stmt = stmt.where(Customer.name.ilike(f"%{query}%"))
        return [to_dict(c, _CUSTOMER_FIELDS) for c in db.scalars(stmt)]


@mcp.tool
def get_customer(customer_id: str) -> dict:
    """Fetch one customer by id, with its full contact/billing/shipping
    profile. Returns {} if not found."""
    with tool_session() as db:
        c = db.get(Customer, UUID(customer_id))
        return to_dict(c, _CUSTOMER_DETAIL_FIELDS) if c else {}


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
