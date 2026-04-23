"""TDD: customer balance / overdue aggregation.

Service under test: `app.services.stats.compute_customer_balances(db, today)`.
Pins the contract consumed by the /customers/balances endpoint and the
CustomersView Balance/Overdue column.
"""

from __future__ import annotations

from datetime import date, timedelta
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services.stats import compute_customer_balances


TODAY = date(2026, 4, 22)


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Acme Corp")
    db.add(c)
    db.flush()
    return c


@pytest.fixture()
def other_customer(db) -> Customer:
    c = Customer(name="Globex")
    db.add(c)
    db.flush()
    return c


def _inv(
    db,
    customer,
    *,
    currency="USD",
    amount="1000",
    balance_due=None,
    status="SENT",
    issue_date=date(2026, 4, 1),
    due_date=date(2026, 4, 15),
    is_template=False,
) -> Invoice:
    bal = Decimal(balance_due) if balance_due is not None else Decimal(amount)
    inv = Invoice(
        customer_id=customer.customer_id,
        invoice_type="MILESTONE",
        currency=currency,
        amount=Decimal(amount),
        balance_due=bal,
        status=status,
        issue_date=issue_date,
        due_date=due_date,
        is_template=is_template,
    )
    db.add(inv)
    db.flush()
    return inv


def _as_dict(rows):
    """Normalize rows to {(customer_id, currency): (balance, overdue)}."""
    return {(r.customer_id, r.currency): (r.balance, r.overdue) for r in rows}


# ---------------------------------------------------------------------------
# Baseline
# ---------------------------------------------------------------------------


def test_empty_db_returns_empty_list(db):
    result = compute_customer_balances(db, today=TODAY)
    assert result == []


def test_customer_with_no_open_invoices_excluded(db, customer):
    _inv(db, customer, status="DRAFT")
    _inv(db, customer, status="VOID")
    _inv(db, customer, status="PAID", balance_due="0")
    result = compute_customer_balances(db, today=TODAY)
    assert result == []


# ---------------------------------------------------------------------------
# Open balance aggregation
# ---------------------------------------------------------------------------


def test_sent_invoice_not_yet_due_contributes_to_balance_only(db, customer):
    _inv(db, customer, status="SENT", due_date=TODAY + timedelta(days=10),
         balance_due="500")
    result = compute_customer_balances(db, today=TODAY)
    assert len(result) == 1
    row = result[0]
    assert row.customer_id == customer.customer_id
    assert row.currency == "USD"
    assert row.balance == Decimal("500")
    assert row.overdue == Decimal("0")


def test_sent_invoice_past_due_contributes_to_both_balance_and_overdue(db, customer):
    _inv(db, customer, status="SENT", due_date=TODAY - timedelta(days=3),
         balance_due="700")
    result = compute_customer_balances(db, today=TODAY)
    row = result[0]
    assert row.balance == Decimal("700")
    assert row.overdue == Decimal("700")


def test_partial_uses_balance_due_not_amount(db, customer):
    _inv(db, customer, status="PARTIAL",
         due_date=TODAY - timedelta(days=1),
         amount="1000", balance_due="250")
    result = compute_customer_balances(db, today=TODAY)
    row = result[0]
    assert row.balance == Decimal("250")
    assert row.overdue == Decimal("250")


def test_sums_across_multiple_open_invoices_same_currency(db, customer):
    _inv(db, customer, status="SENT",
         due_date=TODAY - timedelta(days=1), balance_due="100")
    _inv(db, customer, status="SENT",
         due_date=TODAY + timedelta(days=5), balance_due="400")
    result = compute_customer_balances(db, today=TODAY)
    row = result[0]
    assert row.balance == Decimal("500")
    assert row.overdue == Decimal("100")


# ---------------------------------------------------------------------------
# Exclusions
# ---------------------------------------------------------------------------


def test_draft_void_paid_template_all_excluded(db, customer):
    _inv(db, customer, status="DRAFT", balance_due="1")
    _inv(db, customer, status="VOID", balance_due="1")
    _inv(db, customer, status="PAID", balance_due="0")
    _inv(db, customer, status="SENT", balance_due="1", is_template=True)
    result = compute_customer_balances(db, today=TODAY)
    assert result == []


def test_zero_balance_rows_excluded(db, customer):
    _inv(db, customer, status="SENT",
         due_date=TODAY - timedelta(days=1),
         balance_due="0")
    result = compute_customer_balances(db, today=TODAY)
    assert result == []


# ---------------------------------------------------------------------------
# Multi-currency + multi-customer
# ---------------------------------------------------------------------------


def test_multiple_currencies_produce_separate_rows(db, customer):
    _inv(db, customer, currency="USD", status="SENT",
         due_date=TODAY + timedelta(days=5), balance_due="100")
    _inv(db, customer, currency="SGD", status="SENT",
         due_date=TODAY - timedelta(days=1), balance_due="800")
    result = compute_customer_balances(db, today=TODAY)
    idx = _as_dict(result)
    assert idx[(customer.customer_id, "USD")] == (Decimal("100"), Decimal("0"))
    assert idx[(customer.customer_id, "SGD")] == (Decimal("800"), Decimal("800"))


def test_multiple_customers_produce_separate_rows(db, customer, other_customer):
    _inv(db, customer, status="SENT",
         due_date=TODAY + timedelta(days=5), balance_due="100")
    _inv(db, other_customer, status="SENT",
         due_date=TODAY - timedelta(days=1), balance_due="300")
    result = compute_customer_balances(db, today=TODAY)
    idx = _as_dict(result)
    assert idx[(customer.customer_id, "USD")] == (Decimal("100"), Decimal("0"))
    assert idx[(other_customer.customer_id, "USD")] == (Decimal("300"), Decimal("300"))
