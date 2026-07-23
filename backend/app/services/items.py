"""Item create/update — extracted from routers/items.py so the MCP write
tools (write_catalog.py) can call the exact same write logic without
duplicating it in the tool layer.

Includes the hosting-domain normalization and CloudflareTarget upsert
helpers moved verbatim from the router (not re-implemented).
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy.orm import Session

from app.models.cloudflare_target import CloudflareTarget
from app.models.item import Item
from app.schemas.item import ItemCreate, ItemUpdate


class ItemError(Exception):
    """Raised on domain errors in the item layer (e.g. not found)."""


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


def create_item(db: Session, payload: ItemCreate) -> Item:
    data = payload.model_dump(exclude={"cloudflare_target"})
    _normalize_hosting_fields(data)
    item = Item(**data)
    if payload.is_hosting:
        item.hosting_status = "ACTIVE"
    db.add(item)
    db.flush()
    if payload.cloudflare_target is not None:
        _apply_cloudflare_target(item, payload.cloudflare_target.model_dump())
    db.flush()
    return item


def update_item(db: Session, item_id: UUID, payload: ItemUpdate) -> Item:
    item = db.get(Item, item_id)
    if item is None:
        raise ItemError(f"item {item_id} not found")
    data = payload.model_dump(exclude_unset=True)
    target_data = data.pop("cloudflare_target", None)
    _normalize_hosting_fields(data)
    for k, v in data.items():
        setattr(item, k, v)
    if item.is_hosting and item.hosting_status is None:
        item.hosting_status = "ACTIVE"
    if target_data is not None:
        _apply_cloudflare_target(item, target_data)
    db.flush()
    return item
