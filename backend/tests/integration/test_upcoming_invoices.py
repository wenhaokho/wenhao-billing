"""TDD: compute_upcoming_invoices — next recurring + usage generation dates."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services.stats import compute_upcoming_invoices

TODAY = date(2026, 7, 24)


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Acme Corp", matching_aliases=["Acme"])
    db.add(c)
    db.flush()
    return c


def _recurring(db, customer, *, currency="USD", amount="1000",
               frequency="MONTHLY", interval=1, start_date=date(2026, 1, 5),
               paused=False) -> Invoice:
    cfg = {"frequency": frequency, "interval": interval,
           "start_date": start_date.isoformat(), "end_mode": "NEVER"}
    if paused:
        cfg["paused"] = True
    inv = Invoice(
        customer_id=customer.customer_id, invoice_type="RECURRING",
        currency=currency, amount=Decimal(amount), balance_due=Decimal("0"),
        status="DRAFT", is_template=True, billing_cycle_ref=cfg,
    )
    db.add(inv)
    db.flush()
    return inv


def _usage(db, customer, *, currency="USD", amount="0", cut_off_day=20,
           status="DRAFT", locked=False) -> Invoice:
    cfg = {"cut_off_day": cut_off_day}
    if locked:
        cfg["locked"] = True
    inv = Invoice(
        customer_id=customer.customer_id, invoice_type="USAGE",
        currency=currency, amount=Decimal(amount), balance_due=Decimal("0"),
        status=status, is_template=False, billing_cycle_ref=cfg,
    )
    db.add(inv)
    db.flush()
    return inv


def test_empty_db_returns_empty(db):
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_recurring_next_occurrence_within_window(db, customer):
    # MONTHLY on the 5th, start Jan 5 -> next after Jul 24 is Aug 5.
    _recurring(db, customer, amount="1000", start_date=date(2026, 1, 5))
    rows = compute_upcoming_invoices(db, today=TODAY)
    assert len(rows) == 1
    assert rows[0].mode == "RECURRING"
    assert rows[0].next_date == date(2026, 8, 5)
    assert rows[0].amount == Decimal("1000")
    assert rows[0].amount_is_estimate is False


def test_recurring_reported_once_even_if_multiple_cycles_in_window(db, customer):
    # WEEKLY would fire ~13 times in 90 days; we want a single row.
    _recurring(db, customer, frequency="WEEKLY", start_date=date(2026, 1, 5))
    rows = compute_upcoming_invoices(db, today=TODAY)
    assert len(rows) == 1


def test_recurring_beyond_window_excluded(db, customer):
    # YEARLY on Jan 5 -> next is 2027-01-05, well beyond 90 days.
    _recurring(db, customer, frequency="YEARLY", start_date=date(2026, 1, 5))
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_paused_recurring_excluded(db, customer):
    _recurring(db, customer, start_date=date(2026, 1, 5), paused=True)
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_usage_next_cutoff_is_estimate(db, customer):
    # cut_off_day 20, today Jul 24 -> next is Aug 20.
    _usage(db, customer, cut_off_day=20, amount="0")
    rows = compute_upcoming_invoices(db, today=TODAY)
    assert len(rows) == 1
    assert rows[0].mode == "USAGE"
    assert rows[0].next_date == date(2026, 8, 20)
    assert rows[0].amount_is_estimate is True


def test_usage_locked_or_no_cutoff_skipped(db, customer):
    _usage(db, customer, cut_off_day=20, locked=True)
    u = _usage(db, customer, cut_off_day=20)
    u.billing_cycle_ref = {}  # no cut_off_day
    db.flush()
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_usage_non_draft_skipped(db, customer):
    _usage(db, customer, cut_off_day=25, status="SENT")
    assert compute_upcoming_invoices(db, today=TODAY) == []


def test_mixed_sorted_by_date(db, customer):
    _usage(db, customer, cut_off_day=28)          # Jul 28
    _recurring(db, customer, start_date=date(2026, 1, 5))  # Aug 5
    rows = compute_upcoming_invoices(db, today=TODAY)
    assert [r.next_date for r in rows] == [date(2026, 7, 28), date(2026, 8, 5)]
