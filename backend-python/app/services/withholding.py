"""Indonesian withholding tax (PPh 21 / PPh 23) calculation helpers.

The customer withholds a percentage of the invoice base and remits it to the
tax office, so the cash received is lower than the invoice balance. With
gross-up the invoice total is increased so that ``total - wht == base``.
"""
from __future__ import annotations

from dataclasses import dataclass
from decimal import ROUND_HALF_UP, Decimal

WHT_TYPES: tuple[str, ...] = ("NONE", "PPH21", "PPH23")
DEFAULT_RATES: dict[str, Decimal] = {"PPH21": Decimal("2.5"), "PPH23": Decimal(2)}
WHT_LABELS = {"PPH21": "PPh 21", "PPH23": "PPh 23"}

_ZERO = Decimal(0)
_HUNDRED = Decimal(100)


@dataclass(frozen=True)
class WhtResult:
    total: Decimal
    wht_amount: Decimal
    base: Decimal


def round_ccy(value: Decimal, currency: str) -> Decimal:
    """IDR rounds to 0 decimals, every other currency to 2 (ROUND_HALF_UP)."""
    exp = Decimal(1) if currency == "IDR" else Decimal("0.01")
    return value.quantize(exp, rounding=ROUND_HALF_UP)


def compute_withholding(
    base: Decimal,
    currency: str,
    wht_type: str,
    rate: Decimal | None,
    gross_up: bool,
) -> WhtResult:
    """``base`` is the subtotal after discount."""
    if wht_type == "NONE" or rate is None or base == _ZERO:
        return WhtResult(total=base, wht_amount=_ZERO, base=base)
    if gross_up:
        total = round_ccy(base / (Decimal(1) - rate / _HUNDRED), currency)
        return WhtResult(total=total, wht_amount=total - base, base=base)
    return WhtResult(
        total=base,
        wht_amount=round_ccy(base * rate / _HUNDRED, currency),
        base=base,
    )


def expected_cash(invoice) -> Decimal:
    """Cash still expected from the customer: balance minus unbooked withholding."""
    value = invoice.balance_due - (invoice.wht_amount - invoice.wht_booked)
    return max(_ZERO, value)


def normalize(
    wht_type: str, rate: Decimal | None, gross_up: bool
) -> tuple[str, Decimal | None, bool]:
    if wht_type not in WHT_TYPES:
        raise ValueError(f"Unknown withholding tax type: {wht_type}")
    if wht_type == "NONE":
        return "NONE", None, False
    if rate is None:
        rate = DEFAULT_RATES[wht_type]
    if not (_ZERO < rate < _HUNDRED):
        raise ValueError("Withholding tax rate must be greater than 0 and less than 100")
    return wht_type, rate, gross_up
