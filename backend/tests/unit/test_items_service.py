"""Unit tests for app.services.items — extracted from routers/items.py.

TDD sub-task A for Task 8's items extraction.
"""
from __future__ import annotations

import uuid

import pytest

from app.schemas.item import CloudflareTargetIn, ItemCreate, ItemUpdate
from app.services.items import ItemError, create_item, update_item


def test_create_item_basic(db):
    payload = ItemCreate(name="Consulting", item_type="SERVICE", default_currency="USD")
    item = create_item(db, payload)
    assert item.item_id is not None
    assert item.name == "Consulting"
    assert item.is_hosting is False


def test_create_hosting_item_sets_active_status_and_normalizes(db):
    payload = ItemCreate(
        name="Website hosting",
        item_type="SERVICE",
        is_hosting=True,
        hosting_domain="Example.COM",
        hosting_grace_days=7,
        hosting_suspension_enabled=True,
        cloudflare_target=CloudflareTargetIn(
            zone_id="zone1",
            record_id="rec1",
            record_name="WWW",
            record_type="a",
            live_content="1.2.3.4",
            maintenance_content="5.6.7.8",
        ),
    )
    item = create_item(db, payload)
    assert item.hosting_status == "ACTIVE"
    assert item.hosting_domain == "example.com"
    assert item.cloudflare_target is not None
    assert item.cloudflare_target.record_name == "www"
    assert item.cloudflare_target.record_type == "A"


def test_update_item_not_found_raises(db):
    with pytest.raises(ItemError):
        update_item(db, uuid.uuid4(), ItemUpdate(name="x"))


def test_update_item_edits_fields(db, seed_item):
    item = update_item(db, seed_item, ItemUpdate(name="Web Hosting Renamed"))
    assert item.name == "Web Hosting Renamed"
