from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.user import User
from app.models.vendor import Vendor
from app.schemas.vendor import VendorCreate, VendorOut, VendorUpdate
from app.services import vendors as vendor_service

router = APIRouter(prefix="/vendors", tags=["vendors"])


@router.get("", response_model=list[VendorOut])
def list_vendors(
    active: bool | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Vendor]:
    stmt = select(Vendor).order_by(Vendor.name.asc())
    if active is not None:
        stmt = stmt.where(Vendor.active == active)
    return list(db.scalars(stmt))


@router.post("", response_model=VendorOut, status_code=201)
def create_vendor(
    payload: VendorCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Vendor:
    vendor = vendor_service.create_vendor(db, payload)
    db.commit()
    db.refresh(vendor)
    return vendor


@router.get("/{vendor_id}", response_model=VendorOut)
def get_vendor(
    vendor_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Vendor:
    vendor = db.get(Vendor, vendor_id)
    if vendor is None:
        raise HTTPException(status_code=404, detail="vendor not found")
    return vendor


@router.patch("/{vendor_id}", response_model=VendorOut)
def update_vendor(
    vendor_id: UUID,
    payload: VendorUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Vendor:
    try:
        vendor = vendor_service.update_vendor(db, vendor_id, payload)
    except vendor_service.VendorError:
        raise HTTPException(status_code=404, detail="vendor not found")
    db.commit()
    db.refresh(vendor)
    return vendor


@router.delete("/{vendor_id}", response_model=VendorOut)
def deactivate_vendor(
    vendor_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Vendor:
    vendor = db.get(Vendor, vendor_id)
    if vendor is None:
        raise HTTPException(status_code=404, detail="vendor not found")
    vendor.active = False
    db.commit()
    db.refresh(vendor)
    return vendor
