"""Pure helpers for recurring-invoice scheduling.

A recurring template stores its schedule in `Invoice.billing_cycle_ref` as a
plain JSON dict. This module is the single source of truth for parsing that
dict and answering two runtime questions:

  * "Is a cycle due today?"  → `current_cycle(schedule, today)`
  * "When is the next cycle?" → `next_cycle_after(schedule, after)`

Everything here is side-effect free so it can be exercised by unit tests
without a database.

Cycle-key convention: the ISO date of the cycle's *start* day. That string is
what `invoicing.trigger_recurring_cycle` uses as the per-template idempotency
key, so generating the same cycle twice on the same day is a no-op.
"""

from __future__ import annotations

import calendar
from dataclasses import dataclass
from datetime import date
from typing import Any, Mapping

VALID_FREQUENCIES = ("DAILY", "WEEKLY", "MONTHLY", "YEARLY")
VALID_END_MODES = ("NEVER", "ON_DATE", "AFTER_N")


class ScheduleError(ValueError):
    """Malformed schedule config on a recurring template."""


@dataclass(frozen=True)
class Schedule:
    frequency: str
    interval: int
    start_date: date
    end_mode: str = "NEVER"
    end_date: date | None = None
    end_after_cycles: int | None = None


@dataclass(frozen=True)
class Cycle:
    start: date
    index: int  # 0-based count of cycles elapsed since start_date


# ---------------------------------------------------------------------------
# parse
# ---------------------------------------------------------------------------


def _parse_date(value: Any, field: str) -> date:
    if isinstance(value, date):
        return value
    if isinstance(value, str):
        try:
            return date.fromisoformat(value)
        except ValueError as e:
            raise ScheduleError(f"invalid ISO date for {field}: {value!r}") from e
    raise ScheduleError(f"missing or invalid {field}")


def parse_schedule(cfg: Mapping[str, Any] | None) -> Schedule:
    if not cfg:
        raise ScheduleError("schedule config is empty")
    frequency = cfg.get("frequency")
    if frequency not in VALID_FREQUENCIES:
        raise ScheduleError(f"invalid frequency: {frequency!r}")
    interval = cfg.get("interval", 1)
    if not isinstance(interval, int) or interval < 1:
        raise ScheduleError(f"interval must be a positive int, got {interval!r}")
    start_date = _parse_date(cfg.get("start_date"), "start_date")

    end_mode = cfg.get("end_mode", "NEVER")
    if end_mode not in VALID_END_MODES:
        raise ScheduleError(f"invalid end_mode: {end_mode!r}")

    end_date: date | None = None
    end_after_cycles: int | None = None
    if end_mode == "ON_DATE":
        if "end_date" not in cfg or cfg["end_date"] is None:
            raise ScheduleError("end_mode=ON_DATE requires end_date")
        end_date = _parse_date(cfg["end_date"], "end_date")
    elif end_mode == "AFTER_N":
        n = cfg.get("end_after_cycles")
        if not isinstance(n, int) or n < 1:
            raise ScheduleError(
                "end_mode=AFTER_N requires positive integer end_after_cycles"
            )
        end_after_cycles = n

    return Schedule(
        frequency=frequency,
        interval=interval,
        start_date=start_date,
        end_mode=end_mode,
        end_date=end_date,
        end_after_cycles=end_after_cycles,
    )


# ---------------------------------------------------------------------------
# cycle math
# ---------------------------------------------------------------------------


def _add_months(d: date, months: int) -> date:
    total = d.month - 1 + months
    year = d.year + total // 12
    month = total % 12 + 1
    day = min(d.day, calendar.monthrange(year, month)[1])
    return date(year, month, day)


def _cycle_start_for_index(schedule: Schedule, index: int) -> date:
    if index < 0:
        raise ValueError("index must be non-negative")
    f = schedule.frequency
    step = schedule.interval * index
    if f == "DAILY":
        return date.fromordinal(schedule.start_date.toordinal() + step)
    if f == "WEEKLY":
        return date.fromordinal(schedule.start_date.toordinal() + step * 7)
    if f == "MONTHLY":
        return _add_months(schedule.start_date, step)
    if f == "YEARLY":
        return _add_months(schedule.start_date, step * 12)
    raise ScheduleError(f"unreachable frequency {f!r}")


def _index_for_day(schedule: Schedule, day: date) -> int:
    """Largest index such that cycle_start(index) <= day. Assumes day >= start."""
    if day < schedule.start_date:
        raise ValueError("day before start")
    f = schedule.frequency
    if f == "DAILY":
        return (day - schedule.start_date).days // schedule.interval
    if f == "WEEKLY":
        return (day - schedule.start_date).days // (schedule.interval * 7)
    if f in ("MONTHLY", "YEARLY"):
        unit_months = 1 if f == "MONTHLY" else 12
        # Estimate from month delta, then correct by at most 1 for day-of-month clamp.
        months = (day.year - schedule.start_date.year) * 12 + (
            day.month - schedule.start_date.month
        )
        est = months // (schedule.interval * unit_months)
        # Walk down while the estimated start is in the future.
        while est > 0 and _cycle_start_for_index(schedule, est) > day:
            est -= 1
        # Walk up while the next cycle is still ≤ day.
        while _cycle_start_for_index(schedule, est + 1) <= day:
            est += 1
        return est
    raise ScheduleError(f"unreachable frequency {f!r}")


def _index_past_end(schedule: Schedule, index: int, cycle_start: date) -> bool:
    if schedule.end_mode == "ON_DATE":
        assert schedule.end_date is not None
        return cycle_start > schedule.end_date
    if schedule.end_mode == "AFTER_N":
        assert schedule.end_after_cycles is not None
        return index >= schedule.end_after_cycles
    return False


def current_cycle(schedule: Schedule, today: date) -> Cycle | None:
    """The cycle that `today` falls inside, or None if not yet started / ended."""
    if today < schedule.start_date:
        return None
    index = _index_for_day(schedule, today)
    start = _cycle_start_for_index(schedule, index)
    if _index_past_end(schedule, index, start):
        return None
    return Cycle(start=start, index=index)


def next_cycle_after(schedule: Schedule, after: date) -> date | None:
    """Start of the next cycle strictly after `after`. None if past end."""
    if after < schedule.start_date:
        next_index = 0
    else:
        current_index = _index_for_day(schedule, after)
        next_index = current_index + 1
    next_start = _cycle_start_for_index(schedule, next_index)
    if _index_past_end(schedule, next_index, next_start):
        return None
    return next_start


def cycle_key(cycle: Cycle) -> str:
    return cycle.start.isoformat()


def is_paused(cfg: Mapping[str, Any] | None) -> bool:
    """Paused templates are skipped by the scanner until un-paused."""
    if not cfg:
        return False
    return bool(cfg.get("paused", False))


# ---------------------------------------------------------------------------
# describe (human-readable)
# ---------------------------------------------------------------------------


_ORDINAL_SUFFIX = {1: "st", 2: "nd", 3: "rd"}
_WEEKDAY_NAMES = [
    "Monday", "Tuesday", "Wednesday", "Thursday",
    "Friday", "Saturday", "Sunday",
]
_MONTH_NAMES = [
    "January", "February", "March", "April", "May", "June",
    "July", "August", "September", "October", "November", "December",
]


def _ordinal(n: int) -> str:
    if 10 <= n % 100 <= 20:
        suffix = "th"
    else:
        suffix = _ORDINAL_SUFFIX.get(n % 10, "th")
    return f"{n}{suffix}"


def describe(schedule: Schedule) -> str:
    i = schedule.interval
    f = schedule.frequency
    if f == "DAILY":
        return "Daily" if i == 1 else f"Every {i} days"
    if f == "WEEKLY":
        weekday = _WEEKDAY_NAMES[schedule.start_date.weekday()]
        if i == 1:
            return f"Weekly on {weekday}"
        return f"Every {i} weeks on {weekday}"
    if f == "MONTHLY":
        day = _ordinal(schedule.start_date.day)
        if i == 1:
            return f"Monthly on the {day}"
        return f"Every {i} months on the {day}"
    if f == "YEARLY":
        month = _MONTH_NAMES[schedule.start_date.month - 1]
        if i == 1:
            return f"Yearly on {month} {schedule.start_date.day}"
        return f"Every {i} years on {month} {schedule.start_date.day}"
    return "Custom"
