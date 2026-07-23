"""Unit tests for app.services.vendors — extracted from routers/vendors.py.

TDD sub-task A for Task 8's vendors extraction.
"""
from __future__ import annotations

import uuid

import pytest

from app.schemas.vendor import VendorCreate, VendorUpdate
from app.services.vendors import VendorError, create_vendor, update_vendor


def test_create_vendor_sets_fields(db):
    payload = VendorCreate(name="Acme Supplies", default_currency="USD")
    vendor = create_vendor(db, payload)
    assert vendor.vendor_id is not None
    assert vendor.name == "Acme Supplies"


def test_update_vendor_edits_fields(db, seed_vendor):
    vendor = update_vendor(db, seed_vendor, VendorUpdate(notes="Preferred supplier"))
    assert vendor.notes == "Preferred supplier"


def test_update_vendor_not_found_raises(db):
    with pytest.raises(VendorError):
        update_vendor(db, uuid.uuid4(), VendorUpdate(notes="x"))
