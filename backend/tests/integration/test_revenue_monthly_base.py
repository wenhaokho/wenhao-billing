"""compute_monthly_revenue_base — 12-month invoiced revenue converted to IDR."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services.stats import compute_monthly_revenue_base

TODAY = date(2026, 7, 24)  # seeded USD/SGD->IDR rates are as_of 2026-01-01


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Acme Corp", matching_aliases=["Acme"])
    db.add(c)
    db.flush()
    return c


def _inv(
    db,
    customer,
    *,
    currency,
    amount,
    issue_date,
    status="SENT",
    is_template=False,
):
    inv = Invoice(
        customer_id=customer.customer_id,
        invoice_type="MILESTONE",
        currency=currency,
        amount=Decimal(amount),
        balance_due=Decimal(amount),
        status=status,
        issue_date=issue_date,
        is_template=is_template,
    )
    db.add(inv)
    db.flush()
    return inv


def test_labels_span_12_months_oldest_first(db):
    labels, months, unconverted = compute_monthly_revenue_base(db, today=TODAY)
    assert len(labels) == 12
    assert labels[0] == "2025-08"
    assert labels[-1] == "2026-07"
    assert [m["month"] for m in months] == labels
    assert unconverted == []


def test_base_currency_uses_rate_one(db, customer):
    _inv(db, customer, currency="IDR", amount="5000000", issue_date=date(2026, 7, 10))
    _, months, unconverted = compute_monthly_revenue_base(db, today=TODAY)
    jul = next(m for m in months if m["month"] == "2026-07")
    assert jul["native_totals"] == {"IDR": Decimal("5000000")}
    assert jul["base_totals"] == {"IDR": Decimal("5000000.0000")}
    assert unconverted == []


def test_foreign_currency_converted_via_seeded_rate(db, customer):
    _inv(db, customer, currency="USD", amount="100", issue_date=date(2026, 6, 15))  # *16000
    _inv(db, customer, currency="SGD", amount="100", issue_date=date(2026, 6, 20))  # *12000
    _, months, unconverted = compute_monthly_revenue_base(db, today=TODAY)
    jun = next(m for m in months if m["month"] == "2026-06")
    assert jun["base_totals"] == {
        "USD": Decimal("1600000.0000"),
        "SGD": Decimal("1200000.0000"),
    }
    assert jun["native_totals"] == {"USD": Decimal("100"), "SGD": Decimal("100")}
    assert unconverted == []


def test_missing_rate_currency_reported_unconverted(db, customer):
    _inv(db, customer, currency="GBP", amount="100", issue_date=date(2026, 5, 5))
    _, months, unconverted = compute_monthly_revenue_base(db, today=TODAY)
    may = next(m for m in months if m["month"] == "2026-05")
    # Native revenue is still visible; the base total omits the unconvertible ccy.
    assert may["native_totals"] == {"GBP": Decimal("100")}
    assert may["base_totals"] == {}
    assert unconverted == ["GBP"]


def test_draft_template_and_out_of_window_excluded(db, customer):
    # DRAFT is not revenue.
    _inv(db, customer, currency="IDR", amount="111", issue_date=date(2026, 7, 1), status="DRAFT")
    # A recurring template must never count as invoiced revenue.
    _inv(
        db,
        customer,
        currency="IDR",
        amount="222",
        issue_date=date(2026, 7, 1),
        status="SENT",
        is_template=True,
    )
    # 12 months back from 2026-07 starts at 2025-08; 2025-07 is out of window.
    _inv(db, customer, currency="IDR", amount="333", issue_date=date(2025, 7, 1))
    _, months, unconverted = compute_monthly_revenue_base(db, today=TODAY)
    assert all(m["base_totals"] == {} for m in months)
    assert all(m["native_totals"] == {} for m in months)
    assert unconverted == []


def test_same_currency_summed_across_invoices_in_a_month(db, customer):
    _inv(db, customer, currency="USD", amount="100", issue_date=date(2026, 3, 3))
    _inv(db, customer, currency="USD", amount="50", issue_date=date(2026, 3, 28))
    _, months, _unconverted = compute_monthly_revenue_base(db, today=TODAY)
    mar = next(m for m in months if m["month"] == "2026-03")
    assert mar["native_totals"] == {"USD": Decimal("150")}
    assert mar["base_totals"] == {"USD": Decimal("2400000.0000")}  # 150 * 16000
