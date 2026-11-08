"""TDD: invoice-list summary stats (Overdue / Due-30d / Avg days to pay).

Service under test: `app.services.stats.compute_invoices_summary(db, today)`.
Pins the contract consumed by the /stats/invoices-summary endpoint and by the
InvoicesView summary tiles.
"""

from __future__ import annotations

import uuid
from datetime import date, timedelta
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.services.stats import compute_invoices_summary


TODAY = date(2026, 4, 22)


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


def _pay(db, invoice, *, amount=None, payment_date, status="CLEARED") -> Payment:
    p = Payment(
        invoice_id=invoice.invoice_id,
        customer_id=invoice.customer_id,
        amount=Decimal(amount) if amount is not None else invoice.amount,
        currency=invoice.currency,
        payer_name="Acme Corp",
        payment_date=payment_date,
        intake_source="TEST",
        external_ref=f"EXT-{uuid.uuid4()}",
        status=status,
    )
    db.add(p)
    db.flush()
    return p


# ---------------------------------------------------------------------------
# Empty / baseline
# ---------------------------------------------------------------------------


def test_empty_db_returns_zero_buckets_and_null_avg(db):
    result = compute_invoices_summary(db, today=TODAY)
    assert result.overdue == []
    assert result.due_30d == []
    assert result.avg_days_to_pay is None


# ---------------------------------------------------------------------------
# Overdue bucket
# ---------------------------------------------------------------------------


def test_sent_invoice_past_due_appears_in_overdue(db, customer):
    _inv(db, customer, status="SENT", due_date=TODAY - timedelta(days=5), balance_due="1000")
    result = compute_invoices_summary(db, today=TODAY)

    assert len(result.overdue) == 1
    assert result.overdue[0].currency == "USD"
    assert result.overdue[0].amount == Decimal("1000")
    assert result.due_30d == []


def test_partial_invoice_past_due_uses_balance_due_not_amount(db, customer):
    _inv(
        db, customer,
        status="PARTIAL",
        due_date=TODAY - timedelta(days=2),
        amount="1000",
        balance_due="400",
    )
    result = compute_invoices_summary(db, today=TODAY)
    assert result.overdue[0].amount == Decimal("400")


def test_overdue_sums_multiple_invoices_same_currency(db, customer):
    _inv(db, customer, status="SENT", due_date=TODAY - timedelta(days=1), balance_due="300")
    _inv(db, customer, status="SENT", due_date=TODAY - timedelta(days=10), balance_due="700")
    result = compute_invoices_summary(db, today=TODAY)
    assert len(result.overdue) == 1
    assert result.overdue[0].amount == Decimal("1000")


def test_overdue_separates_by_currency(db, customer):
    _inv(db, customer, currency="USD", status="SENT",
         due_date=TODAY - timedelta(days=1), balance_due="500")
    _inv(db, customer, currency="SGD", status="SENT",
         due_date=TODAY - timedelta(days=1), balance_due="800")
    result = compute_invoices_summary(db, today=TODAY)
    by_ccy = {row.currency: row.amount for row in result.overdue}
    assert by_ccy == {"USD": Decimal("500"), "SGD": Decimal("800")}


# ---------------------------------------------------------------------------
# Due-30d bucket
# ---------------------------------------------------------------------------


def test_sent_invoice_due_today_is_in_due_30d_not_overdue(db, customer):
    _inv(db, customer, status="SENT", due_date=TODAY, balance_due="500")
    result = compute_invoices_summary(db, today=TODAY)
    assert result.overdue == []
    assert result.due_30d[0].amount == Decimal("500")


def test_sent_invoice_due_in_30_days_inclusive(db, customer):
    _inv(db, customer, status="SENT", due_date=TODAY + timedelta(days=30), balance_due="600")
    result = compute_invoices_summary(db, today=TODAY)
    assert result.due_30d[0].amount == Decimal("600")


def test_sent_invoice_due_in_31_days_excluded(db, customer):
    _inv(db, customer, status="SENT", due_date=TODAY + timedelta(days=31), balance_due="999")
    result = compute_invoices_summary(db, today=TODAY)
    assert result.overdue == []
    assert result.due_30d == []


# ---------------------------------------------------------------------------
# Exclusions
# ---------------------------------------------------------------------------


def test_draft_invoice_excluded_from_buckets(db, customer):
    _inv(db, customer, status="DRAFT", due_date=TODAY - timedelta(days=5), balance_due="500")
    _inv(db, customer, status="DRAFT", due_date=TODAY + timedelta(days=5), balance_due="500")
    result = compute_invoices_summary(db, today=TODAY)
    assert result.overdue == []
    assert result.due_30d == []


def test_void_invoice_excluded_from_buckets(db, customer):
    _inv(db, customer, status="VOID", due_date=TODAY - timedelta(days=5), balance_due="500")
    result = compute_invoices_summary(db, today=TODAY)
    assert result.overdue == []


def test_template_invoice_excluded(db, customer):
    _inv(db, customer, status="SENT", due_date=TODAY - timedelta(days=5),
         balance_due="500", is_template=True)
    result = compute_invoices_summary(db, today=TODAY)
    assert result.overdue == []


def test_paid_invoice_excluded_from_overdue_and_due_30d(db, customer):
    inv = _inv(db, customer, status="PAID", due_date=TODAY - timedelta(days=5),
               balance_due="0")
    _pay(db, inv, payment_date=TODAY - timedelta(days=3))
    result = compute_invoices_summary(db, today=TODAY)
    assert result.overdue == []
    assert result.due_30d == []


# ---------------------------------------------------------------------------
# Avg days to pay
# ---------------------------------------------------------------------------


def test_avg_days_to_pay_single_invoice(db, customer):
    inv = _inv(
        db, customer,
        status="PAID",
        issue_date=TODAY - timedelta(days=20),
        due_date=TODAY,
        balance_due="0",
    )
    _pay(db, inv, payment_date=TODAY - timedelta(days=10))
    result = compute_invoices_summary(db, today=TODAY)
    # issue → payment = 10 days
    assert result.avg_days_to_pay == pytest.approx(10.0)


def test_avg_days_to_pay_averages_across_invoices(db, customer):
    inv_a = _inv(db, customer, status="PAID",
                 issue_date=TODAY - timedelta(days=30),
                 due_date=TODAY - timedelta(days=15), balance_due="0")
    _pay(db, inv_a, payment_date=TODAY - timedelta(days=20))  # 10 days
    inv_b = _inv(db, customer, status="PAID",
                 issue_date=TODAY - timedelta(days=30),
                 due_date=TODAY - timedelta(days=10), balance_due="0")
    _pay(db, inv_b, payment_date=TODAY - timedelta(days=10))  # 20 days
    result = compute_invoices_summary(db, today=TODAY)
    assert result.avg_days_to_pay == pytest.approx(15.0)


def test_avg_days_to_pay_ignores_payments_older_than_90_days(db, customer):
    inv = _inv(db, customer, status="PAID",
               issue_date=TODAY - timedelta(days=200),
               due_date=TODAY - timedelta(days=185), balance_due="0")
    _pay(db, inv, payment_date=TODAY - timedelta(days=180))
    result = compute_invoices_summary(db, today=TODAY)
    assert result.avg_days_to_pay is None


def test_avg_days_to_pay_ignores_non_cleared_payments(db, customer):
    inv = _inv(db, customer, status="PAID",
               issue_date=TODAY - timedelta(days=20),
               due_date=TODAY, balance_due="0")
    _pay(db, inv, payment_date=TODAY - timedelta(days=10), status="PENDING_MANUAL_REVIEW")
    result = compute_invoices_summary(db, today=TODAY)
    assert result.avg_days_to_pay is None
