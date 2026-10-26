from decimal import Decimal
from types import SimpleNamespace

import pytest

from app.services.withholding import (
    compute_withholding,
    expected_cash,
    normalize,
    round_ccy,
)


def test_pph23_no_gross_up():
    r = compute_withholding(Decimal(10000000), "IDR", "PPH23", Decimal(2), False)
    assert r.total == Decimal(10000000)
    assert r.wht_amount == Decimal(200000)
    assert r.base == Decimal(10000000)


def test_pph23_gross_up():
    r = compute_withholding(Decimal(10000000), "IDR", "PPH23", Decimal(2), True)
    assert r.total == Decimal(10204082)
    assert r.wht_amount == Decimal(204082)
    assert r.total - r.wht_amount == Decimal(10000000)


def test_pph21_usd_gross_up_two_decimals():
    r = compute_withholding(Decimal(1000), "USD", "PPH21", Decimal("2.5"), True)
    assert r.total == Decimal("1025.64")
    assert r.wht_amount == Decimal("25.64")


def test_none_type():
    r = compute_withholding(Decimal(500), "USD", "NONE", None, True)
    assert r.total == Decimal(500)
    assert r.wht_amount == 0


def test_zero_base_gross_up():
    r = compute_withholding(Decimal(0), "IDR", "PPH23", Decimal(2), True)
    assert r.total == 0
    assert r.wht_amount == 0


def test_round_half_up():
    assert round_ccy(Decimal("0.5"), "IDR") == Decimal(1)
    assert round_ccy(Decimal("1.005"), "USD") == Decimal("1.01")


def test_normalize_defaults_rate():
    assert normalize("PPH21", None, True) == ("PPH21", Decimal("2.5"), True)


def test_normalize_none_clears():
    assert normalize("NONE", Decimal(2), True) == ("NONE", None, False)


@pytest.mark.parametrize(
    "args",
    [("PPH23", Decimal(100), False), ("PPH23", Decimal(0), False), ("PPN", None, False)],
)
def test_normalize_rejects_invalid(args):
    with pytest.raises(ValueError):
        normalize(*args)


def test_expected_cash():
    inv = SimpleNamespace(
        balance_due=Decimal(1000), wht_amount=Decimal(20), wht_booked=Decimal(5)
    )
    assert expected_cash(inv) == Decimal(985)


def test_expected_cash_never_negative():
    inv = SimpleNamespace(
        balance_due=Decimal(10), wht_amount=Decimal(50), wht_booked=Decimal(0)
    )
    assert expected_cash(inv) == Decimal(0)
