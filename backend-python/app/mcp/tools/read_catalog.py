"""Catalog read tools: items, vendors, and the vendor ambiguity-stop resolver.

Base ordering mirrors the items/vendors routers
(backend/app/api/v1/routers/items.py: list_items order_by Item.name.asc();
backend/app/api/v1/routers/vendors.py: list_vendors order_by Vendor.name.asc()).
Neither router exposes a free-text name search, so — matching the
established read_customers.py precedent (list_customers(query=...) is an
MCP-only addition on top of the customers router's own `active` filter) —
`query` here is an MCP-only ilike-on-name filter layered on the router's
base query, giving the assistant a name-search primitive for the catalog.

Real columns on Item (backend/app/models/item.py): item_id, sku, name,
item_type, description, default_currency, default_unit_price,
default_purchase_price, revenue_account_id, expense_account_id, is_sold,
is_purchased, active, is_hosting, hosting_domain, hosting_grace_days,
hosting_suspension_enabled, hosting_status, hosting_last_paid_at,
hosting_last_action_at, hosting_last_error, created_at.

Real columns on Vendor (backend/app/models/vendor.py): vendor_id, name,
contact_email, contact_name, default_currency, payment_terms_days,
tax_id, notes, active, created_at. Note there is deliberately no
address or phone column on Vendor — unlike Customer, a vendor's postal
address has never been modelled, so `update_vendor` rejecting an
"address"/"phone" key is the honest answer rather than an omission.

Two field sets per entity, deliberately (the read_customers.py pattern):

- `_ITEM_FIELDS` / `_VENDOR_FIELDS` — the narrow subsets for the `list_*`
  tools, which return up to 500 rows and would bloat if they carried every
  column.
- `_ITEM_DETAIL_FIELDS` / `_VENDOR_DETAIL_FIELDS` — every writable column,
  used by single-record returns (the write tools in write_catalog.py).
  Single-record returns MUST echo back what was supplied: when create_vendor
  returned only `_VENDOR_FIELDS`, a caller that set tax_id/notes had no way
  to see whether either landed.
"""
from __future__ import annotations

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.item import Item
from app.models.vendor import Vendor

_ITEM_FIELDS = [
    "item_id",
    "sku",
    "name",
    "item_type",
    "default_currency",
    "default_unit_price",
    "default_purchase_price",
    "active",
    "is_hosting",
]

_ITEM_DETAIL_FIELDS = [
    "item_id",
    "sku",
    "name",
    "item_type",
    "description",
    "default_currency",
    "default_unit_price",
    "default_purchase_price",
    "revenue_account_id",
    "expense_account_id",
    "is_sold",
    "is_purchased",
    "active",
    "is_hosting",
    "hosting_domain",
    "hosting_grace_days",
    "hosting_suspension_enabled",
    "hosting_status",
]

_VENDOR_FIELDS = [
    "vendor_id",
    "name",
    "contact_email",
    "contact_name",
    "default_currency",
    "payment_terms_days",
    "active",
]

_VENDOR_DETAIL_FIELDS = [
    "vendor_id",
    "name",
    "contact_email",
    "contact_name",
    "default_currency",
    "payment_terms_days",
    "tax_id",
    "notes",
    "active",
]


@mcp.tool
def list_items(query: str | None = None, limit: int = 200) -> list[dict]:
    """List items, optionally filtered by a name substring."""
    with tool_session() as db:
        stmt = select(Item).order_by(Item.name.asc()).limit(min(limit, 500))
        if query:
            stmt = stmt.where(Item.name.ilike(f"%{query}%"))
        return [to_dict(i, _ITEM_FIELDS) for i in db.scalars(stmt)]


@mcp.tool
def list_vendors(query: str | None = None, limit: int = 200) -> list[dict]:
    """List vendors, optionally filtered by a name substring."""
    with tool_session() as db:
        stmt = select(Vendor).order_by(Vendor.name.asc()).limit(min(limit, 500))
        if query:
            stmt = stmt.where(Vendor.name.ilike(f"%{query}%"))
        return [to_dict(v, _VENDOR_FIELDS) for v in db.scalars(stmt)]


@mcp.tool
def resolve_vendor(query: str) -> dict:
    """Resolve a vendor name substring to a single vendor id.

    Ambiguity-stop primitive: on exactly one match, returns
    {"vendor_id": "<uuid>"}. On zero or more than one match, returns
    {"candidates": [{"vendor_id", "name"}, ...]} and takes NO other
    action — later write tools depend on this exact shape to decide
    whether they may proceed automatically or must ask for disambiguation.
    """
    with tool_session() as db:
        rows = list(
            db.scalars(
                select(Vendor).where(Vendor.name.ilike(f"%{query}%")).limit(10)
            )
        )
        if len(rows) == 1:
            return {"vendor_id": str(rows[0].vendor_id)}
        return {
            "candidates": [
                {"vendor_id": str(v.vendor_id), "name": v.name} for v in rows
            ]
        }
