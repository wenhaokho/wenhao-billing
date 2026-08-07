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


# ---------------------------------------------------------------------------
# Recurring rows endpoint — "Previous" must be the scheduled cycle date,
# not the day the child invoice happened to be generated.
# ---------------------------------------------------------------------------


def test_rows_previous_is_cycle_date_not_generation_date(admin_session, db):
    """The 'Previous' column should show the scheduled cycle-start date
    (billing_cycle_ref['cycle_key']), not the child's issue_date, which is
    stamped with date.today() at generation time."""
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 1, 1))
    # Generate a child for the June cycle. Its issue_date is date.today()
    # (the real run date), but it belongs to the 2026-06-01 cycle.
    invoicing.trigger_recurring_cycle(
        db, template_invoice_id=t.invoice_id, cycle_key="2026-06-01"
    )
    db.flush()

    resp = admin_session.get("/api/v1/invoices/recurring-templates/rows")
    assert resp.status_code == 200, resp.text
    row = next(r for r in resp.json() if r["template_id"] == str(t.invoice_id))

    assert row["previous_issue_date"] == "2026-06-01"


def test_rows_next_is_previous_cycle_plus_one_interval(admin_session, db):
    """Rule: after a cycle generates, Next = previous cycle + one interval.
    Next must be anchored on the cycle date, not the child's issue_date
    (which is date.today() at generation and can fall in a later cycle)."""
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 1, 1))
    invoicing.trigger_recurring_cycle(
        db, template_invoice_id=t.invoice_id, cycle_key="2026-06-01"
    )
    db.flush()

    resp = admin_session.get("/api/v1/invoices/recurring-templates/rows")
    assert resp.status_code == 200, resp.text
    row = next(r for r in resp.json() if r["template_id"] == str(t.invoice_id))

    assert row["previous_issue_date"] == "2026-06-01"
    assert row["next_run_date"] == "2026-07-01"  # previous cycle + 1 month


# ---------------------------------------------------------------------------
# trigger_recurring_cycle rejects malformed / off-cycle cycle_keys so it can't
# create duplicate or mis-dated children (regression: "2026-03" year-month keys
# bypassed idempotency and double-billed a cycle).
# ---------------------------------------------------------------------------


def test_trigger_rejects_year_month_cycle_key(db):
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 1, 1))
    with pytest.raises(invoicing.InvoicingError, match="ISO date"):
        invoicing.trigger_recurring_cycle(
            db, template_invoice_id=t.invoice_id, cycle_key="2026-03"
        )


def test_trigger_rejects_off_cycle_date(db):
    # Monthly on the 1st: the 15th is not a cycle start.
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 1, 1))
    with pytest.raises(invoicing.InvoicingError, match="not a valid cycle start"):
        invoicing.trigger_recurring_cycle(
            db, template_invoice_id=t.invoice_id, cycle_key="2026-03-15"
        )


def test_trigger_accepts_valid_cycle_start(db):
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 1, 1))
    inv = invoicing.trigger_recurring_cycle(
        db, template_invoice_id=t.invoice_id, cycle_key="2026-03-01"
    )
    assert inv.billing_cycle_ref["cycle_key"] == "2026-03-01"


def test_manual_trigger_notifies_users_once(admin_session, db, monkeypatch):
    """Manual trigger emails the approval digest for a NEW draft only —
    an idempotent re-trigger of the same cycle stays silent."""
    sent = []
    monkeypatch.setattr(
        "app.services.draft_notifications.send_recurring_drafts_email",
        lambda *, to_email, drafts: sent.append((to_email, drafts)),
    )
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 1, 1))

    resp = admin_session.post(
        f"/api/v1/invoices/recurring/{t.invoice_id}/trigger",
        json={"cycle_key": "2026-03-01"},
    )
    assert resp.status_code == 200, resp.text
    assert len(sent) >= 1
    _, drafts = sent[0]
    assert drafts[0]["cycle_date"] == "2026-03-01"
    assert "/edit" in drafts[0]["link"]

    sent.clear()
    resp = admin_session.post(
        f"/api/v1/invoices/recurring/{t.invoice_id}/trigger",
        json={"cycle_key": "2026-03-01"},
    )
    assert resp.status_code == 200, resp.text
    assert sent == []


def test_find_cycle_invoice_none_before_trigger_then_found(db):
    """The beat scanner uses find_cycle_invoice to tell newly created drafts
    apart from idempotent re-returns (drives the approval email)."""
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 1, 1))
    assert (
        invoicing.find_cycle_invoice(
            db, template_invoice_id=t.invoice_id, cycle_key="2026-03-01"
        )
        is None
    )
    inv = invoicing.trigger_recurring_cycle(
        db, template_invoice_id=t.invoice_id, cycle_key="2026-03-01"
    )
    found = invoicing.find_cycle_invoice(
        db, template_invoice_id=t.invoice_id, cycle_key="2026-03-01"
    )
    assert found is not None
    assert found.invoice_id == inv.invoice_id


# ---------------------------------------------------------------------------
# Cleanup script: fix malformed cycle_keys + de-duplicate cycles.
# ---------------------------------------------------------------------------


def _mk_child(db, template, *, issue_date: date, cycle_key: str) -> Invoice:
    """Insert a generated recurring child directly, bypassing trigger
    validation, to reproduce legacy malformed-key rows."""
    inv = Invoice(
        customer_id=template.customer_id,
        invoice_type="RECURRING",
        is_template=False,
        invoice_number=invoicing.generate_invoice_number(db),
        currency=template.currency,
        subtotal=template.subtotal,
        amount=template.amount,
        balance_due=template.amount,
        status="DRAFT",
        billing_cycle_ref={
            "template_invoice_id": str(template.invoice_id),
            "cycle_key": cycle_key,
        },
        issue_date=issue_date,
        due_date=issue_date,
    )
    db.add(inv)
    db.flush()
    return inv


def test_fix_recurring_cycle_keys_plan_and_apply(db):
    from sqlalchemy import select

    from scripts.fix_recurring_cycle_keys import (
        DELETE,
        FIX,
        KEEP,
        apply_plan,
        build_plan,
    )

    # 6-monthly from 2026-01-01: cycles 2026-01-01, 2026-07-01, ...
    t = _mk_template(db, frequency="MONTHLY", interval=6, start_date=date(2026, 1, 1))
    jan = _mk_child(db, t, issue_date=date(2026, 1, 1), cycle_key="2026-01")  # unique
    jul_bad = _mk_child(db, t, issue_date=date(2026, 7, 1), cycle_key="2026-07")  # dup
    jul_ok = _mk_child(db, t, issue_date=date(2026, 7, 25), cycle_key="2026-07-01")

    plan = {a.invoice_id: a for a in build_plan(db)}
    assert plan[jan.invoice_id].kind == FIX
    assert plan[jan.invoice_id].new_key == "2026-01-01"
    assert plan[jul_bad.invoice_id].kind == DELETE
    assert plan[jul_ok.invoice_id].kind == KEEP

    apply_plan(db, list(plan.values()))
    db.flush()

    remaining = list(
        db.scalars(
            select(Invoice)
            .where(Invoice.is_template.is_(False))
            .where(Invoice.invoice_type == "RECURRING")
        )
    )
    keys = sorted(r.billing_cycle_ref["cycle_key"] for r in remaining)
    assert keys == ["2026-01-01", "2026-07-01"]  # Jan fixed, July deduped


def test_rows_excludes_void_children(admin_session, db):
    """A VOID child must not count toward Generated nor drive Previous."""
    t = _mk_template(db, frequency="MONTHLY", start_date=date(2026, 1, 1))
    _mk_child(db, t, issue_date=date(2026, 6, 1), cycle_key="2026-06-01")  # live
    voided = _mk_child(db, t, issue_date=date(2026, 7, 25), cycle_key="2026-07-01")
    voided.status = "VOID"  # later + would otherwise be "latest"
    db.flush()

    resp = admin_session.get("/api/v1/invoices/recurring-templates/rows")
    row = next(r for r in resp.json() if r["template_id"] == str(t.invoice_id))
    assert row["generated_count"] == 1  # VOID excluded
    assert row["previous_issue_date"] == "2026-06-01"  # from live, not VOID


def test_fix_script_ignores_void_and_fixes_live_key(db):
    """Reproduces the real Yayasan-July case: a live SENT invoice with a
    malformed key alongside a VOID invoice with the valid key. The live one
    must be fixed; the VOID one left untouched."""
    from scripts.fix_recurring_cycle_keys import (
        FIX,
        SKIP,
        apply_plan,
        build_plan,
    )

    t = _mk_template(db, frequency="MONTHLY", interval=6, start_date=date(2026, 1, 1))
    live = _mk_child(db, t, issue_date=date(2026, 7, 1), cycle_key="2026-07")
    live.status = "SENT"
    voided = _mk_child(db, t, issue_date=date(2026, 7, 25), cycle_key="2026-07-01")
    voided.status = "VOID"
    db.flush()

    plan = {a.invoice_id: a for a in build_plan(db)}
    assert plan[voided.invoice_id].kind == SKIP
    assert plan[live.invoice_id].kind == FIX
    assert plan[live.invoice_id].new_key == "2026-07-01"

    apply_plan(db, list(plan.values()))
    db.flush()
    db.refresh(live)
    db.refresh(voided)
    assert live.billing_cycle_ref["cycle_key"] == "2026-07-01"
    assert voided.status == "VOID"  # untouched
