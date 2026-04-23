"""TDD: pure helpers for recurring-invoice scheduling.

Module under test: `app.services.recurring_schedule`.

Contract pinned here:
  * `parse_schedule(cfg)` → `Schedule` frozen dataclass, raising
    `ScheduleError` on malformed config.
  * `current_cycle(schedule, today)` → `Cycle(start, index)` or `None`
    if no cycle is due (before start, or past end condition).
  * `cycle_key(cycle)` → ISO date string for the cycle start; used as
    the idempotency key for `trigger_recurring_cycle`.
  * `describe(schedule)` → short human-readable string for list UIs.
  * `next_cycle_after(schedule, after)` → `date | None`, the start of
    the next cycle strictly after `after` (used to show "Next invoice").
"""

from __future__ import annotations

from datetime import date

import pytest

from app.services.recurring_schedule import (
    Schedule,
    ScheduleError,
    current_cycle,
    cycle_key,
    describe,
    next_cycle_after,
    parse_schedule,
)


# ---------------------------------------------------------------------------
# parse_schedule
# ---------------------------------------------------------------------------


def test_parse_minimal_monthly_schedule():
    sch = parse_schedule(
        {"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"}
    )
    assert sch.frequency == "MONTHLY"
    assert sch.interval == 1
    assert sch.start_date == date(2026, 5, 1)
    assert sch.end_mode == "NEVER"
    assert sch.end_date is None
    assert sch.end_after_cycles is None


def test_parse_end_on_date():
    sch = parse_schedule(
        {
            "frequency": "WEEKLY",
            "interval": 2,
            "start_date": "2026-01-05",
            "end_mode": "ON_DATE",
            "end_date": "2026-06-30",
        }
    )
    assert sch.end_mode == "ON_DATE"
    assert sch.end_date == date(2026, 6, 30)


def test_parse_end_after_n():
    sch = parse_schedule(
        {
            "frequency": "MONTHLY",
            "interval": 1,
            "start_date": "2026-05-01",
            "end_mode": "AFTER_N",
            "end_after_cycles": 12,
        }
    )
    assert sch.end_mode == "AFTER_N"
    assert sch.end_after_cycles == 12


def test_parse_rejects_bad_frequency():
    with pytest.raises(ScheduleError):
        parse_schedule({"frequency": "HOURLY", "interval": 1, "start_date": "2026-05-01"})


def test_parse_rejects_non_positive_interval():
    with pytest.raises(ScheduleError):
        parse_schedule({"frequency": "MONTHLY", "interval": 0, "start_date": "2026-05-01"})


def test_parse_rejects_missing_start_date():
    with pytest.raises(ScheduleError):
        parse_schedule({"frequency": "MONTHLY", "interval": 1})


def test_parse_rejects_end_on_date_without_end_date():
    with pytest.raises(ScheduleError):
        parse_schedule(
            {"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01",
             "end_mode": "ON_DATE"}
        )


def test_parse_rejects_end_after_n_without_count():
    with pytest.raises(ScheduleError):
        parse_schedule(
            {"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01",
             "end_mode": "AFTER_N"}
        )


def test_parse_rejects_none_config():
    with pytest.raises(ScheduleError):
        parse_schedule(None)


# ---------------------------------------------------------------------------
# current_cycle — basic frequency behaviour
# ---------------------------------------------------------------------------


def _sch(**kw) -> Schedule:
    base = {"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"}
    base.update({k: v for k, v in kw.items() if v is not None})
    return parse_schedule(base)


def test_returns_none_before_start_date():
    sch = _sch(start_date="2026-05-01")
    assert current_cycle(sch, date(2026, 4, 30)) is None


def test_start_date_itself_is_cycle_zero():
    sch = _sch(frequency="DAILY", start_date="2026-05-01")
    c = current_cycle(sch, date(2026, 5, 1))
    assert c is not None
    assert c.start == date(2026, 5, 1)
    assert c.index == 0


def test_daily_every_day_advances_index():
    sch = _sch(frequency="DAILY", interval=1, start_date="2026-05-01")
    assert current_cycle(sch, date(2026, 5, 3)).index == 2
    assert current_cycle(sch, date(2026, 5, 3)).start == date(2026, 5, 3)


def test_daily_interval_three_buckets_by_three_days():
    sch = _sch(frequency="DAILY", interval=3, start_date="2026-05-01")
    # day 0: May 1 → cycle 0 (May 1)
    # days 1,2: still cycle 0
    assert current_cycle(sch, date(2026, 5, 2)).start == date(2026, 5, 1)
    assert current_cycle(sch, date(2026, 5, 3)).start == date(2026, 5, 1)
    # day 3: cycle 1 (May 4)
    assert current_cycle(sch, date(2026, 5, 4)).start == date(2026, 5, 4)
    assert current_cycle(sch, date(2026, 5, 4)).index == 1


def test_weekly_interval_one_advances_by_seven_days():
    sch = _sch(frequency="WEEKLY", interval=1, start_date="2026-05-04")  # a Monday
    assert current_cycle(sch, date(2026, 5, 4)).start == date(2026, 5, 4)
    assert current_cycle(sch, date(2026, 5, 10)).start == date(2026, 5, 4)  # same week
    assert current_cycle(sch, date(2026, 5, 11)).start == date(2026, 5, 11)  # next cycle


def test_weekly_interval_two_advances_every_fortnight():
    sch = _sch(frequency="WEEKLY", interval=2, start_date="2026-05-04")
    assert current_cycle(sch, date(2026, 5, 17)).start == date(2026, 5, 4)  # day 13 still cycle 0
    assert current_cycle(sch, date(2026, 5, 18)).start == date(2026, 5, 18)


def test_monthly_interval_one_basic():
    sch = _sch(frequency="MONTHLY", interval=1, start_date="2026-05-01")
    assert current_cycle(sch, date(2026, 5, 15)).start == date(2026, 5, 1)
    assert current_cycle(sch, date(2026, 6, 1)).start == date(2026, 6, 1)
    assert current_cycle(sch, date(2026, 6, 1)).index == 1
    assert current_cycle(sch, date(2027, 3, 1)).index == 10


def test_monthly_interval_three_quarterly():
    sch = _sch(frequency="MONTHLY", interval=3, start_date="2026-01-15")
    assert current_cycle(sch, date(2026, 4, 14)).start == date(2026, 1, 15)
    assert current_cycle(sch, date(2026, 4, 15)).start == date(2026, 4, 15)
    assert current_cycle(sch, date(2026, 7, 15)).start == date(2026, 7, 15)


def test_monthly_end_of_month_start_clamps_short_months():
    # Start Jan 31 → next cycle clamps to Feb 28 (2026 is not a leap year).
    sch = _sch(frequency="MONTHLY", interval=1, start_date="2026-01-31")
    assert current_cycle(sch, date(2026, 2, 28)).start == date(2026, 2, 28)
    assert current_cycle(sch, date(2026, 3, 31)).start == date(2026, 3, 31)


def test_yearly_interval_one():
    sch = _sch(frequency="YEARLY", interval=1, start_date="2026-05-01")
    assert current_cycle(sch, date(2027, 4, 30)).start == date(2026, 5, 1)
    assert current_cycle(sch, date(2027, 5, 1)).start == date(2027, 5, 1)
    assert current_cycle(sch, date(2027, 5, 1)).index == 1


# ---------------------------------------------------------------------------
# end conditions
# ---------------------------------------------------------------------------


def test_end_on_date_inclusive_on_the_day():
    sch = _sch(frequency="MONTHLY", start_date="2026-01-01",
               end_mode="ON_DATE", end_date="2026-06-30")
    assert current_cycle(sch, date(2026, 6, 30)) is not None
    assert current_cycle(sch, date(2026, 7, 1)) is None


def test_end_after_n_caps_by_index():
    # 3 cycles: index 0, 1, 2 valid; index 3 rejected.
    sch = _sch(frequency="MONTHLY", start_date="2026-01-01",
               end_mode="AFTER_N", end_after_cycles=3)
    assert current_cycle(sch, date(2026, 1, 1)).index == 0
    assert current_cycle(sch, date(2026, 3, 31)).index == 2
    assert current_cycle(sch, date(2026, 4, 1)) is None


# ---------------------------------------------------------------------------
# cycle_key format
# ---------------------------------------------------------------------------


def test_cycle_key_is_iso_date_of_cycle_start():
    sch = _sch(frequency="MONTHLY", start_date="2026-05-01")
    c = current_cycle(sch, date(2026, 7, 10))
    assert cycle_key(c) == "2026-07-01"


# ---------------------------------------------------------------------------
# describe
# ---------------------------------------------------------------------------


def test_describe_monthly_basic():
    sch = _sch(frequency="MONTHLY", interval=1, start_date="2026-05-01")
    assert describe(sch) == "Monthly on the 1st"


def test_describe_monthly_interval():
    sch = _sch(frequency="MONTHLY", interval=3, start_date="2026-05-15")
    assert describe(sch) == "Every 3 months on the 15th"


def test_describe_weekly():
    sch = _sch(frequency="WEEKLY", interval=1, start_date="2026-05-04")  # Mon
    assert describe(sch) == "Weekly on Monday"


def test_describe_weekly_interval():
    sch = _sch(frequency="WEEKLY", interval=2, start_date="2026-05-04")
    assert describe(sch) == "Every 2 weeks on Monday"


def test_describe_daily():
    sch = _sch(frequency="DAILY", interval=1, start_date="2026-05-01")
    assert describe(sch) == "Daily"


def test_describe_yearly():
    sch = _sch(frequency="YEARLY", interval=1, start_date="2026-05-01")
    assert describe(sch) == "Yearly on May 1"


# ---------------------------------------------------------------------------
# next_cycle_after — used for "Next invoice" column on the list view
# ---------------------------------------------------------------------------


def test_next_cycle_after_before_start_returns_start():
    sch = _sch(frequency="MONTHLY", start_date="2026-05-01")
    assert next_cycle_after(sch, date(2026, 4, 15)) == date(2026, 5, 1)


def test_next_cycle_after_on_current_returns_next():
    sch = _sch(frequency="MONTHLY", start_date="2026-05-01")
    assert next_cycle_after(sch, date(2026, 5, 1)) == date(2026, 6, 1)


def test_next_cycle_after_returns_none_past_end_date():
    sch = _sch(frequency="MONTHLY", start_date="2026-01-01",
               end_mode="ON_DATE", end_date="2026-03-31")
    assert next_cycle_after(sch, date(2026, 3, 31)) is None


def test_next_cycle_after_returns_none_past_count_cap():
    sch = _sch(frequency="MONTHLY", start_date="2026-01-01",
               end_mode="AFTER_N", end_after_cycles=2)
    # Cycle 0 = Jan 1, cycle 1 = Feb 1. After Feb 1 → no more.
    assert next_cycle_after(sch, date(2026, 2, 1)) is None


# ---------------------------------------------------------------------------
# is_paused
# ---------------------------------------------------------------------------


def test_paused_flag_roundtrips():
    from app.services.recurring_schedule import is_paused

    assert is_paused({"paused": True}) is True
    assert is_paused({"paused": False}) is False
    assert is_paused({}) is False
    assert is_paused(None) is False
