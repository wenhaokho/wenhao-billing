"""TDD: wire recurring_schedule into template creation + scanner.

Covers:
  * `create_recurring_template` persists the schedule block into
    `billing_cycle_ref` (validated via `parse_schedule`).
  * `_cycle_key_for(today, template)` in workers.tasks.recurring delegates
    to `current_cycle` and returns the ISO cycle-start date, or None when
    no cycle is due (not started, ended, wrong interval day).
"""

from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.schemas.invoice import (
    InvoiceLineItemIn,
    RecurringSchedule,
    RecurringTemplateCreate,
)
from app.services import invoicing
from app.workers.tasks.recurring import _cycle_key_for


def _mk_template(
    db,
    *,
    frequency="MONTHLY",
    interval=1,
    start_date=date(2026, 5, 1),
    end_mode="NEVER",
    end_date=None,
    end_after_cycles=None,
) -> Invoice:
    customer = Customer(name="Retainer Corp")
    db.add(customer)
    db.flush()
    payload = RecurringTemplateCreate(
        customer_id=customer.customer_id,
        currency="USD",
        payment_terms="Net 14",
        line_items=[
            InvoiceLineItemIn(description="Monthly retainer", quantity=Decimal("1"),
                              unit_price=Decimal("1000"))
        ],
        schedule=RecurringSchedule(
            frequency=frequency,
            interval=interval,
            start_date=start_date,
            end_mode=end_mode,
            end_date=end_date,
            end_after_cycles=end_after_cycles,
        ),
    )
    invoice = invoicing.create_recurring_template(db, payload)
    db.flush()
    return invoice


# ---------------------------------------------------------------------------
# create_recurring_template persists schedule
# ---------------------------------------------------------------------------


def test_create_template_stores_schedule_in_billing_cycle_ref(db):
    t = _mk_template(db, frequency="MONTHLY", interval=3, start_date=date(2026, 1, 15))
    assert t.is_template is True
    assert t.invoice_type == "RECURRING"
    assert t.billing_cycle_ref is not None
    assert t.billing_cycle_ref["frequency"] == "MONTHLY"
    assert t.billing_cycle_ref["interval"] == 3
    assert t.billing_cycle_ref["start_date"] == "2026-01-15"
    assert t.billing_cycle_ref["end_mode"] == "NEVER"


def test_create_template_persists_end_condition(db):
    t = _mk_template(
        db, frequency="WEEKLY", start_date=date(2026, 1, 5),
        end_mode="ON_DATE", end_date=date(2026, 6, 30),
    )
    assert t.billing_cycle_ref["end_mode"] == "ON_DATE"
    assert t.billing_cycle_ref["end_date"] == "2026-06-30"


def test_create_template_rejects_inconsistent_end_condition(db):
    with pytest.raises(invoicing.InvoicingError):
        _mk_template(
            db, frequency="MONTHLY", end_mode="AFTER_N",
            end_after_cycles=None,  # missing
        )


# ---------------------------------------------------------------------------
# _cycle_key_for — scanner delegates to current_cycle
# ---------------------------------------------------------------------------


def test_cycle_key_none_before_start(db):
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 5, 1))
    assert _cycle_key_for(date(2026, 4, 30), t) is None


def test_cycle_key_is_iso_cycle_start_monthly(db):
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 5, 1))
    # Any day in May → cycle 0 starts May 1
    assert _cycle_key_for(date(2026, 5, 10), t) == "2026-05-01"
    # Any day in June → cycle 1 starts June 1
    assert _cycle_key_for(date(2026, 6, 15), t) == "2026-06-01"


def test_cycle_key_weekly_interval_two(db):
    t = _mk_template(db, frequency="WEEKLY", interval=2, start_date=date(2026, 5, 4))
    assert _cycle_key_for(date(2026, 5, 17), t) == "2026-05-04"  # still cycle 0
    assert _cycle_key_for(date(2026, 5, 18), t) == "2026-05-18"  # cycle 1


def test_cycle_key_none_past_end_date(db):
    t = _mk_template(
        db, frequency="MONTHLY", start_date=date(2026, 1, 1),
        end_mode="ON_DATE", end_date=date(2026, 3, 31),
    )
    assert _cycle_key_for(date(2026, 3, 31), t) == "2026-03-01"
    assert _cycle_key_for(date(2026, 4, 1), t) is None


def test_cycle_key_none_past_after_n(db):
    t = _mk_template(
        db, frequency="MONTHLY", start_date=date(2026, 1, 1),
        end_mode="AFTER_N", end_after_cycles=3,
    )
    assert _cycle_key_for(date(2026, 3, 15), t) == "2026-03-01"
    assert _cycle_key_for(date(2026, 4, 1), t) is None


def test_cycle_key_returns_none_if_config_missing(db):
    """Legacy / malformed templates shouldn't crash the beat loop."""
    t = _mk_template(db)
    t.billing_cycle_ref = None
    db.flush()
    assert _cycle_key_for(date(2026, 5, 1), t) is None
