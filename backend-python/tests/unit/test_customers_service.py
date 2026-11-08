"""Unit tests for app.services.customers — extracted from routers/customers.py.

TDD sub-task A for Task 8's customers extraction: pins the create/update
contract before the router is refactored to call this service.
"""
from __future__ import annotations

import uuid

import pytest

from app.schemas.customer import CustomerCreate, CustomerUpdate
from app.services.customers import CustomerError, create_customer, update_customer


def test_create_customer_sets_fields(db):
    payload = CustomerCreate(name="Beta Pte Ltd", contact_email="ap@beta.com")
    customer = create_customer(db, payload)
    assert customer.customer_id is not None
    assert customer.name == "Beta Pte Ltd"
    assert customer.contact_email == "ap@beta.com"


def test_update_customer_edits_fields(db, seed_customer):
    payload = CustomerUpdate(notes="VIP account", contact_phone="+65 1234 5678")
    customer = update_customer(db, seed_customer, payload)
    assert customer.notes == "VIP account"
    assert customer.contact_phone == "+65 1234 5678"


def test_update_customer_not_found_raises(db):
    with pytest.raises(CustomerError):
        update_customer(db, uuid.uuid4(), CustomerUpdate(notes="x"))
