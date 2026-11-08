"""TDD: compute_open_ar_base_by_currency — SENT/PARTIAL AR converted to IDR."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.fx import FxRate
from app.models.invoice import Invoice
from app.services.stats import compute_open_ar_base_by_currency

TODAY = date(2026, 7, 24)  # seeded USD/SGD->IDR rates are as_of 2026-01-01


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Acme Corp", matching_aliases=["Acme"])
    db.add(c)
    db.flush()
    return c


def _inv(db, customer, *, currency, balance_due, status="SENT"):
    inv = Invoice(
        customer_id=customer.customer_id,
        invoice_type="MILESTONE",
        currency=currency,
        amount=Decimal(balance_due),
        balance_due=Decimal(balance_due),
        status=status,
    )
    db.add(inv)
    db.flush()
    return inv


def test_empty_db_returns_empty(db):
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    assert rows == []
    assert unconverted == []


def test_base_currency_ar_uses_rate_one(db, customer):
    _inv(db, customer, currency="IDR", balance_due="5000000")
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    assert rows == [{"currency": "IDR", "base_amount": Decimal("5000000.0000")}]
    assert unconverted == []


def test_foreign_currency_converted_via_seeded_rate(db, customer):
    _inv(db, customer, currency="USD", balance_due="100")   # * 16000
    _inv(db, customer, currency="SGD", balance_due="100")   # * 12000
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    by = {r["currency"]: r["base_amount"] for r in rows}
    assert by == {"USD": Decimal("1600000.0000"), "SGD": Decimal("1200000.0000")}
    # sorted desc by base_amount
    assert [r["currency"] for r in rows] == ["USD", "SGD"]
    assert unconverted == []


def test_missing_rate_currency_lands_in_unconverted(db, customer):
    _inv(db, customer, currency="GBP", balance_due="100")  # no seeded GBP->IDR
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    assert rows == []
    assert unconverted == ["GBP"]


def test_draft_excluded(db, customer):
    _inv(db, customer, currency="USD", balance_due="100", status="DRAFT")
    rows, unconverted = compute_open_ar_base_by_currency(db, today=TODAY)
    assert rows == []
