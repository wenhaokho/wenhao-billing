from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.cloudflare_target import CloudflareTarget
from app.models.item import Item
from app.models.user import User
from app.schemas.item import ItemCreate, ItemOut, ItemUpdate

router = APIRouter(prefix="/items", tags=["items"])


def _normalize_hosting_fields(data: dict) -> None:
    """Lowercase domain, lowercase record_name, uppercase record_type."""
    if data.get("hosting_domain"):
        data["hosting_domain"] = data["hosting_domain"].strip().lower()


def _apply_cloudflare_target(item: Item, target_data: dict) -> None:
    target = item.cloudflare_target
    if target is None:
        target = CloudflareTarget(item_id=item.item_id)
        item.cloudflare_target = target
    for field, value in target_data.items():
        if isinstance(value, str):
            value = value.strip()
            if field == "record_name":
                value = value.lower()
            if field == "record_type":
                value = value.upper()
        setattr(target, field, value)


@router.get("", response_model=list[ItemOut])
def list_items(
    active: bool | None = None,
    item_type: str | None = None,
    is_hosting: bool | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Item]:
    stmt = select(Item).order_by(Item.name.asc())
    if active is not None:
        stmt = stmt.where(Item.active == active)
    if item_type:
        stmt = stmt.where(Item.item_type == item_type)
    if is_hosting is not None:
        stmt = stmt.where(Item.is_hosting == is_hosting)
    return list(db.scalars(stmt))


@router.post("", response_model=ItemOut, status_code=201)
def create_item(
    payload: ItemCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Item:
    data = payload.model_dump(exclude={"cloudflare_target"})
    _normalize_hosting_fields(data)
    item = Item(**data)
    if payload.is_hosting:
        item.hosting_status = "ACTIVE"
    db.add(item)
    db.flush()
    if payload.cloudflare_target is not None:
        _apply_cloudflare_target(item, payload.cloudflare_target.model_dump())
    db.commit()
    db.refresh(item)
    return item


@router.get("/{item_id}", response_model=ItemOut)
def get_item(
    item_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Item:
    item = db.get(Item, item_id)
    if item is None:
        raise HTTPException(status_code=404, detail="item not found")
    return item


@router.patch("/{item_id}", response_model=ItemOut)
def update_item(
    item_id: UUID,
    payload: ItemUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Item:
    item = db.get(Item, item_id)
    if item is None:
        raise HTTPException(status_code=404, detail="item not found")
    data = payload.model_dump(exclude_unset=True)
    target_data = data.pop("cloudflare_target", None)
    _normalize_hosting_fields(data)
    for k, v in data.items():
        setattr(item, k, v)
    if item.is_hosting and item.hosting_status is None:
        item.hosting_status = "ACTIVE"
    if target_data is not None:
        _apply_cloudflare_target(item, target_data)
    db.commit()
    db.refresh(item)
    return item


@router.delete("/{item_id}", response_model=ItemOut)
def deactivate_item(
    item_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Item:
    item = db.get(Item, item_id)
    if item is None:
        raise HTTPException(status_code=404, detail="item not found")
    item.active = False
    db.commit()
    db.refresh(item)
    return item
