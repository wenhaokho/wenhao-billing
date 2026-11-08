"""TDD: compute_mrr_by_currency — active recurring templates normalized to monthly."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services.stats import compute_mrr_by_currency

TODAY = date(2026, 7, 24)


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Retainer Corp", matching_aliases=["Retainer"])
    db.add(c)
    db.flush()
    return c


def _tmpl(db, customer, *, currency="USD", amount="1200", frequency="MONTHLY",
          interval=1, start_date=date(2026, 1, 1), end_mode="NEVER",
          end_date=None, end_after_cycles=None, paused=False) -> Invoice:
    cfg = {
        "frequency": frequency, "interval": interval,
        "start_date": start_date.isoformat(), "end_mode": end_mode,
    }
    if end_date is not None:
        cfg["end_date"] = end_date.isoformat()
    if end_after_cycles is not None:
        cfg["end_after_cycles"] = end_after_cycles
    if paused:
        cfg["paused"] = True
    inv = Invoice(
        customer_id=customer.customer_id,
        invoice_type="RECURRING",
        currency=currency,
        amount=Decimal(amount),
        balance_due=Decimal("0"),
        status="DRAFT",
        is_template=True,
        billing_cycle_ref=cfg,
    )
    db.add(inv)
    db.flush()
    return inv


def test_empty_db_returns_no_rows(db):
    assert compute_mrr_by_currency(db, today=TODAY) == []


def test_monthly_interval_1_is_full_amount(db, customer):
    _tmpl(db, customer, amount="1200", frequency="MONTHLY", interval=1)
    rows = compute_mrr_by_currency(db, today=TODAY)
    assert rows == [{"currency": "USD", "amount": Decimal("1200.0000")}]


def test_yearly_divided_by_twelve(db, customer):
    _tmpl(db, customer, amount="1200", frequency="YEARLY", interval=1)
    rows = compute_mrr_by_currency(db, today=TODAY)
    assert rows[0]["amount"] == Decimal("100.0000")


def test_weekly_times_52_over_12(db, customer):
    _tmpl(db, customer, amount="100", frequency="WEEKLY", interval=1)
    rows = compute_mrr_by_currency(db, today=TODAY)
    # 100 * 52 / 12 = 433.3333
    assert rows[0]["amount"] == Decimal("433.3333")


def test_monthly_interval_3_is_third(db, customer):
    _tmpl(db, customer, amount="900", frequency="MONTHLY", interval=3)
    rows = compute_mrr_by_currency(db, today=TODAY)
    assert rows[0]["amount"] == Decimal("300.0000")


def test_paused_template_excluded(db, customer):
    _tmpl(db, customer, amount="1200", paused=True)
    assert compute_mrr_by_currency(db, today=TODAY) == []


def test_ended_template_excluded(db, customer):
    _tmpl(db, customer, amount="1200", frequency="MONTHLY",
          start_date=date(2026, 1, 1), end_mode="ON_DATE",
          end_date=date(2026, 3, 1))
    assert compute_mrr_by_currency(db, today=TODAY) == []


def test_non_template_recurring_excluded(db, customer):
    t = _tmpl(db, customer, amount="1200")
    t.is_template = False
    db.flush()
    assert compute_mrr_by_currency(db, today=TODAY) == []


def test_grouped_and_sorted_desc(db, customer):
    _tmpl(db, customer, currency="USD", amount="1000", frequency="MONTHLY")
    _tmpl(db, customer, currency="SGD", amount="6000", frequency="MONTHLY")
    rows = compute_mrr_by_currency(db, today=TODAY)
    assert [r["currency"] for r in rows] == ["SGD", "USD"]
    assert rows[0]["amount"] == Decimal("6000.0000")
