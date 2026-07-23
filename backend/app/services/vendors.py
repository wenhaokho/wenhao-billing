"""Vendor create/update — extracted from routers/vendors.py so the MCP
write tools (write_catalog.py) can call the exact same write logic without
duplicating it in the tool layer.
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy.orm import Session

from app.models.vendor import Vendor
from app.schemas.vendor import VendorCreate, VendorUpdate


class VendorError(Exception):
    """Raised on domain errors in the vendor layer (e.g. not found)."""


def create_vendor(db: Session, payload: VendorCreate) -> Vendor:
    vendor = Vendor(**payload.model_dump())
    db.add(vendor)
    db.flush()
    return vendor


def update_vendor(db: Session, vendor_id: UUID, payload: VendorUpdate) -> Vendor:
    vendor = db.get(Vendor, vendor_id)
    if vendor is None:
        raise VendorError(f"vendor {vendor_id} not found")
    for k, v in payload.model_dump(exclude_unset=True).items():
        setattr(vendor, k, v)
    db.flush()
    return vendor
