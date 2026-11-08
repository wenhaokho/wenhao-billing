"""FX rate lookup and amount conversion to base currency.

Rules:
- All rates are stored as `from_currency -> to_currency = rate` on a given
  `as_of_date`. v1 only converts into the base currency; `to_currency` is
  expected to equal `Settings.base_currency`.
- `get_rate` picks the newest rate with `as_of_date <= on_date`. A request
  for the base currency always returns Decimal("1") without touching the DB.
- Missing rates raise `FxRateMissing` — callers must decide whether that
  blocks posting or degrades to manual review.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from decimal import Decimal

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.config import get_settings
from app.models.fx import FxRate


class FxRateMissing(RuntimeError):
    pass


@dataclass(frozen=True)
class Conversion:
    base_amount: Decimal
    rate: Decimal


def get_rate(
    db: Session,
    from_currency: str,
    on_date: date,
    to_currency: str | None = None,
) -> Decimal:
    base = (to_currency or get_settings().base_currency).upper()
    src = from_currency.upper()
    if src == base:
        return Decimal("1")

    stmt = (
        select(FxRate.rate)
        .where(FxRate.from_currency == src)
        .where(FxRate.to_currency == base)
        .where(FxRate.as_of_date <= on_date)
        .order_by(FxRate.as_of_date.desc())
        .limit(1)
    )
    rate = db.scalar(stmt)
    if rate is None:
        raise FxRateMissing(
            f"no fx rate for {src}->{base} on or before {on_date.isoformat()}"
        )
    return rate


def convert(
    db: Session,
    amount: Decimal,
    from_currency: str,
    on_date: date,
    to_currency: str | None = None,
) -> Conversion:
    rate = get_rate(db, from_currency, on_date, to_currency)
    # Numeric(19,4) matches journal_lines.base_amount_* precision.
    base_amount = (amount * rate).quantize(Decimal("0.0001"))
    return Conversion(base_amount=base_amount, rate=rate)
