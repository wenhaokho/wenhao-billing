"""Item and Vendor write tools — thin wrappers over app.services.items and
app.services.vendors.

Same pattern as write_customers.py / write_invoices.py. `create_item`
covers plain (non-hosting) items only — hosting items also require a
CloudflareTarget and drive the suspension pipeline (see
app/services/hosting.py), which is a deliberately separate, higher-stakes
flow the app UI already owns; keeping it out of the autonomous tool
surface avoids an agent silently provisioning a hosting/DNS target.

Both `update_*` tools vet their `changes` dict with
`reject_unknown_changes` before opening a session — see app/mcp/validate.py
for why unknown keys must not be silently ignored.

Single-record returns use the `_*_DETAIL_FIELDS` sets, not the narrow
`_*_FIELDS` sets the `list_*` tools use, so a caller can see whether the
values it supplied actually landed.
"""
from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from sqlalchemy.exc import IntegrityError

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_catalog import _ITEM_DETAIL_FIELDS, _VENDOR_DETAIL_FIELDS
from app.mcp.validate import reject_unknown_changes
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
    revenue_account_id: int | None = None,
    expense_account_id: int | None = None,
    is_sold: bool = True,
    is_purchased: bool = False,
    active: bool = True,
) -> dict:
    """Create a catalog item (non-hosting: item_type SERVICE/USAGE/FIXED_FEE).
    Autonomous.

    Every writable non-hosting ItemCreate field is an explicit arg, including
    the revenue/expense account overrides — without them a caller had no way
    to post an item to a specific chart-of-accounts line. Hosting fields
    (is_hosting, hosting_*, cloudflare_target) are deliberately absent; see
    the module docstring.
    """
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
        revenue_account_id=revenue_account_id,
        expense_account_id=expense_account_id,
        is_sold=is_sold,
        is_purchased=is_purchased,
        active=active,
    )
    # A DB constraint violation (e.g. duplicate SKU) is returned as
    # {"error": ...} rather than propagating — the tool_session already rolled
    # back on the raised exception, so returning here is safe (same pattern as
    # the invoice write tools).
    try:
        with tool_session() as db:
            item = item_service.create_item(db, payload)
            db.flush()
            return to_dict(item, _ITEM_DETAIL_FIELDS)
    except IntegrityError as e:
        return {"error": str(e.orig) if e.orig is not None else str(e)}


@mcp.tool
def update_item(item_id: str, changes: dict) -> dict:
    """Edit an item. `changes` keys are ItemUpdate field names — the same names
    `create_item` takes (e.g. name, default_unit_price, active). Autonomous.

    Unknown keys are rejected rather than applied: `unit_price` is a near-miss
    for `default_unit_price` that Pydantic's default extra="ignore" would
    otherwise drop while still returning success.
    """
    if (err := reject_unknown_changes(changes, ItemUpdate)) is not None:
        return err
    try:
        with tool_session() as db:
            item = item_service.update_item(db, UUID(item_id), ItemUpdate(**changes))
            db.flush()
            return to_dict(item, _ITEM_DETAIL_FIELDS)
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
    active: bool = True,
) -> dict:
    """Create a vendor. Autonomous.

    These args are every writable Vendor column. Unlike Customer, Vendor has
    no address or phone column at all (see backend/app/models/vendor.py) —
    there is nothing further to expose here, and a caller asking to record a
    vendor address should be told the field doesn't exist rather than have it
    folded into `notes`.
    """
    payload = VendorCreate(
        name=name,
        contact_email=contact_email,
        contact_name=contact_name,
        default_currency=default_currency,
        payment_terms_days=payment_terms_days,
        tax_id=tax_id,
        notes=notes,
        active=active,
    )
    try:
        with tool_session() as db:
            vendor = vendor_service.create_vendor(db, payload)
            db.flush()
            return to_dict(vendor, _VENDOR_DETAIL_FIELDS)
    except IntegrityError as e:
        return {"error": str(e.orig) if e.orig is not None else str(e)}


@mcp.tool
def update_vendor(vendor_id: str, changes: dict) -> dict:
    """Edit a vendor. `changes` keys are VendorUpdate field names — the same
    names `create_vendor` takes (e.g. name, contact_email,
    payment_terms_days, tax_id, notes, active). Autonomous.

    Unknown keys are rejected rather than applied. This matters most for the
    columns Vendor doesn't have: a `changes={"address": ...}` used to return
    success while storing nothing.
    """
    if (err := reject_unknown_changes(changes, VendorUpdate)) is not None:
        return err
    try:
        with tool_session() as db:
            vendor = vendor_service.update_vendor(db, UUID(vendor_id), VendorUpdate(**changes))
            db.flush()
            return to_dict(vendor, _VENDOR_DETAIL_FIELDS)
    except vendor_service.VendorError as e:
        return {"error": str(e)}
