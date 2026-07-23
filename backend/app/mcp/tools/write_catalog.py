"""Item and Vendor write tools — thin wrappers over app.services.items and
app.services.vendors.

Same pattern as write_customers.py / write_invoices.py. `create_item`
covers plain (non-hosting) items only — hosting items also require a
CloudflareTarget and drive the suspension pipeline (see
app/services/hosting.py), which is a deliberately separate, higher-stakes
flow the app UI already owns; keeping it out of the autonomous tool
surface avoids an agent silently provisioning a hosting/DNS target.
"""
from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_catalog import _ITEM_FIELDS, _VENDOR_FIELDS
from app.schemas.item import ItemCreate, ItemUpdate
from app.schemas.vendor import VendorCreate, VendorUpdate
from app.services import items as item_service
from app.services import vendors as vendor_service


@mcp.tool
def create_item(
    name: str,
    item_type: str,
    sku: str | None = None,
    description: str | None = None,
    default_currency: str = "IDR",
    default_unit_price: str | None = None,
    default_purchase_price: str | None = None,
    is_sold: bool = True,
    is_purchased: bool = False,
    active: bool = True,
) -> dict:
    """Create a catalog item (non-hosting: item_type SERVICE/USAGE/FIXED_FEE).
    Autonomous."""
    payload = ItemCreate(
        sku=sku,
        name=name,
        item_type=item_type,
        description=description,
        default_currency=default_currency,
        default_unit_price=Decimal(default_unit_price) if default_unit_price else None,
        default_purchase_price=(
            Decimal(default_purchase_price) if default_purchase_price else None
        ),
        is_sold=is_sold,
        is_purchased=is_purchased,
        active=active,
    )
    with tool_session() as db:
        item = item_service.create_item(db, payload)
        db.flush()
        return to_dict(item, _ITEM_FIELDS)


@mcp.tool
def update_item(item_id: str, changes: dict) -> dict:
    """Edit an item. `changes` matches ItemUpdate fields (e.g. name,
    default_unit_price, active) — not hosting/cloudflare_target fields;
    use the app UI for those. Autonomous."""
    try:
        with tool_session() as db:
            item = item_service.update_item(db, UUID(item_id), ItemUpdate(**changes))
            db.flush()
            return to_dict(item, _ITEM_FIELDS)
    except item_service.ItemError as e:
        return {"error": str(e)}


@mcp.tool
def create_vendor(
    name: str,
    contact_email: str | None = None,
    contact_name: str | None = None,
    default_currency: str = "IDR",
    payment_terms_days: int = 30,
    tax_id: str | None = None,
    notes: str | None = None,
) -> dict:
    """Create a vendor. Autonomous."""
    payload = VendorCreate(
        name=name,
        contact_email=contact_email,
        contact_name=contact_name,
        default_currency=default_currency,
        payment_terms_days=payment_terms_days,
        tax_id=tax_id,
        notes=notes,
    )
    with tool_session() as db:
        vendor = vendor_service.create_vendor(db, payload)
        db.flush()
        return to_dict(vendor, _VENDOR_FIELDS)


@mcp.tool
def update_vendor(vendor_id: str, changes: dict) -> dict:
    """Edit a vendor. `changes` matches VendorUpdate fields (e.g. name,
    contact_email, payment_terms_days, active). Autonomous."""
    try:
        with tool_session() as db:
            vendor = vendor_service.update_vendor(db, UUID(vendor_id), VendorUpdate(**changes))
            db.flush()
            return to_dict(vendor, _VENDOR_FIELDS)
    except vendor_service.VendorError as e:
        return {"error": str(e)}
