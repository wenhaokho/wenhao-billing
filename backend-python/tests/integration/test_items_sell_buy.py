"""TDD: Wave-parity sell/buy distinction on Items catalog.

Catalog items now carry `is_sold` / `is_purchased` flags plus optional
`expense_account_id` and `default_purchase_price` so the same table can
back both invoice line-items (sold) and bill line-items (purchased).

Defaults: existing items are `is_sold=True, is_purchased=False` — matches
today's behaviour where the catalog is only used on invoices.
"""

from __future__ import annotations

from decimal import Decimal

import pytest
from fastapi.testclient import TestClient

from app.models.item import Item
from app.schemas.item import ItemCreate, ItemOut, ItemUpdate


def _minimal_kwargs(**extra):
    base = dict(name="Support retainer", item_type="SERVICE", default_currency="USD")
    base.update(extra)
    return base


def test_item_defaults_sold_true_purchased_false(db):
    item = Item(**_minimal_kwargs())
    db.add(item)
    db.flush()
    assert item.is_sold is True
    assert item.is_purchased is False
    assert item.expense_account_id is None
    assert item.default_purchase_price is None


def test_item_can_be_purchased_only(db):
    item = Item(
        **_minimal_kwargs(name="Cloud hosting"),
        is_sold=False,
        is_purchased=True,
        default_purchase_price=Decimal("120.00"),
    )
    db.add(item)
    db.flush()
    assert item.is_sold is False
    assert item.is_purchased is True
    assert item.default_purchase_price == Decimal("120.0000")


def test_item_can_be_both_sold_and_purchased(db):
    item = Item(
        **_minimal_kwargs(name="Reseller SaaS seat"),
        is_sold=True,
        is_purchased=True,
        default_unit_price=Decimal("50.00"),
        default_purchase_price=Decimal("30.00"),
    )
    db.add(item)
    db.flush()
    assert item.is_sold and item.is_purchased


# ---------------------------------------------------------------------------
# Pydantic schema round-trips
# ---------------------------------------------------------------------------


def test_item_create_schema_accepts_purchase_fields():
    payload = ItemCreate(
        name="AWS",
        item_type="USAGE",
        is_sold=False,
        is_purchased=True,
        default_purchase_price=Decimal("42"),
        expense_account_id=5001,
    )
    assert payload.is_sold is False
    assert payload.is_purchased is True
    assert payload.default_purchase_price == Decimal("42")
    assert payload.expense_account_id == 5001


def test_item_out_serializes_new_fields(db):
    item = Item(
        **_minimal_kwargs(name="Reseller"),
        is_sold=True, is_purchased=True,
        default_unit_price=Decimal("100"),
        default_purchase_price=Decimal("60"),
    )
    db.add(item)
    db.flush()
    out = ItemOut.model_validate(item)
    assert out.is_sold and out.is_purchased
    assert out.default_purchase_price == Decimal("60.0000")


def test_item_update_can_toggle_purchased(db):
    item = Item(**_minimal_kwargs())
    db.add(item)
    db.flush()
    patch = ItemUpdate(is_purchased=True, default_purchase_price=Decimal("10"))
    data = patch.model_dump(exclude_unset=True)
    for k, v in data.items():
        setattr(item, k, v)
    db.flush()
    assert item.is_purchased is True
    assert item.default_purchase_price == Decimal("10.0000")
