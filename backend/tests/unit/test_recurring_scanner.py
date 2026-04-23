"""Unit tests for the beat-task cycle-key helper.

`_cycle_key_for` is the bridge between the recurring_schedule module (pure)
and the Celery beat loop. These tests pin that bridge without touching the
database — we construct Invoice objects in memory.
"""

from __future__ import annotations

from datetime import date

from app.models.invoice import Invoice
from app.workers.tasks.recurring import _cycle_key_for


def _template(cfg: dict | None) -> Invoice:
    # Non-persisted in-memory Invoice with just the fields the helper reads.
    inv = Invoice(
        invoice_type="RECURRING",
        currency="USD",
        amount=0,
        balance_due=0,
        status="DRAFT",
        is_template=True,
    )
    inv.billing_cycle_ref = cfg
    return inv


def test_none_config_returns_none():
    assert _cycle_key_for(date(2026, 5, 1), _template(None)) is None


def test_malformed_config_returns_none():
    assert _cycle_key_for(date(2026, 5, 1), _template({"frequency": "NOPE"})) is None


def test_before_start_returns_none():
    t = _template({"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"})
    assert _cycle_key_for(date(2026, 4, 30), t) is None


def test_monthly_returns_iso_cycle_start():
    t = _template({"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"})
    assert _cycle_key_for(date(2026, 5, 10), t) == "2026-05-01"
    assert _cycle_key_for(date(2026, 6, 15), t) == "2026-06-01"


def test_weekly_interval_two():
    t = _template({"frequency": "WEEKLY", "interval": 2, "start_date": "2026-05-04"})
    assert _cycle_key_for(date(2026, 5, 17), t) == "2026-05-04"
    assert _cycle_key_for(date(2026, 5, 18), t) == "2026-05-18"


def test_past_end_date_returns_none():
    t = _template({
        "frequency": "MONTHLY", "interval": 1, "start_date": "2026-01-01",
        "end_mode": "ON_DATE", "end_date": "2026-03-31",
    })
    assert _cycle_key_for(date(2026, 3, 31), t) == "2026-03-01"
    assert _cycle_key_for(date(2026, 4, 1), t) is None


def test_paused_template_returns_none_even_when_cycle_is_due():
    t = _template({
        "frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01",
        "paused": True,
    })
    # Without `paused`, this would return "2026-05-01" — paused short-circuits.
    assert _cycle_key_for(date(2026, 5, 15), t) is None


def test_past_after_n_returns_none():
    t = _template({
        "frequency": "MONTHLY", "interval": 1, "start_date": "2026-01-01",
        "end_mode": "AFTER_N", "end_after_cycles": 3,
    })
    assert _cycle_key_for(date(2026, 3, 15), t) == "2026-03-01"
    assert _cycle_key_for(date(2026, 4, 1), t) is None
