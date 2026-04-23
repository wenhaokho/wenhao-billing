from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.coa import ChartOfAccount
from app.models.item import Item
from app.models.user import User
from app.schemas.item import AccountOut, ItemCreate, ItemOut, ItemUpdate

router = APIRouter(prefix="/items", tags=["items"])


@router.get("", response_model=list[ItemOut])
def list_items(
    active: bool | None = None,
    item_type: str | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Item]:
    stmt = select(Item).order_by(Item.name.asc())
    if active is not None:
        stmt = stmt.where(Item.active == active)
    if item_type:
        stmt = stmt.where(Item.item_type == item_type)
    return list(db.scalars(stmt))


@router.post("", response_model=ItemOut, status_code=201)
def create_item(
    payload: ItemCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Item:
    item = Item(**payload.model_dump())
    db.add(item)
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
    for k, v in payload.model_dump(exclude_unset=True).items():
        setattr(item, k, v)
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
