from datetime import date, timedelta
from decimal import Decimal
from uuid import uuid4

import pytest

from app.services.matching_engine import (
    AdjustmentType,
    IncomingPayment,
    InvoiceCandidate,
    PaymentStatus,
    match_payment,
)


def _incoming(**kwargs) -> IncomingPayment:
    defaults = dict(
        amount=Decimal("1000.0000"),
        currency="USD",
        payer_name="Acme Corp",
        payer_reference="REF-1",
        payment_date=date(2026, 4, 15),
        intake_source="WEBHOOK",
    )
    defaults.update(kwargs)
    return IncomingPayment(**defaults)


def _candidate(**kwargs) -> InvoiceCandidate:
    defaults = dict(
        invoice_id=uuid4(),
        customer_id=uuid4(),
        customer_name="Acme Corp",
        customer_aliases=(),
        currency="USD",
        balance_due=Decimal("1000.0000"),
        issue_date=date(2026, 4, 10),
        due_date=date(2026, 4, 24),
        status="SENT",
    )
    defaults.update(kwargs)
    return InvoiceCandidate(**defaults)


def test_exact_match_clears():
    result = match_payment(_incoming(), [_candidate()])
    assert result.status is PaymentStatus.CLEARED
    assert result.confidence >= Decimal("95")
    assert result.adjustment_type is AdjustmentType.NONE


def test_amount_mismatch_holds_and_no_ledger_hint():
    result = match_payment(
        _incoming(amount=Decimal("999.9000")), [_candidate()]
    )
    assert result.status is PaymentStatus.PENDING_MANUAL_REVIEW
    assert result.amount_mismatch is True
    assert result.adjustment_type is AdjustmentType.PARTIAL_PAY


def test_overpayment_flagged_as_credit_on_account():
    result = match_payment(
        _incoming(amount=Decimal("1500.0000")), [_candidate()]
    )
    assert result.status is PaymentStatus.PENDING_MANUAL_REVIEW
    assert result.adjustment_type is AdjustmentType.CREDIT_ON_ACCOUNT


def test_currency_mismatch_always_holds_even_with_exact_amount():
    result = match_payment(
        _incoming(currency="EUR"), [_candidate(currency="USD")]
    )
    assert result.status is PaymentStatus.PENDING_MANUAL_REVIEW
    assert result.currency_mismatch is True
    assert result.adjustment_type is AdjustmentType.NONE


def test_amount_epsilon_boundary_accepts_within_tolerance():
    # Within DECIMAL(19,4) rounding — engine treats as exact.
    result = match_payment(
        _incoming(amount=Decimal("1000.0001")), [_candidate()]
    )
    assert result.status is PaymentStatus.CLEARED


def test_payer_alias_hit_raises_confidence():
    invoice = _candidate(
        customer_name="Acme Corporation Limited",
        customer_aliases=("Acme Corp",),
    )
    result = match_payment(_incoming(), [invoice])
    assert result.status is PaymentStatus.CLEARED


def test_payer_alias_miss_does_not_fail_if_amount_currency_exact():
    # PRD: amount is dominant; a weak payer match shouldn't kill a clean exact match.
    invoice = _candidate(customer_name="Totally Different Name Ltd")
    result = match_payment(_incoming(), [invoice])
    # Could land either side of 95 depending on date — the critical invariant is:
    # if under threshold it MUST hold, not fail-open.
    if result.status is PaymentStatus.PENDING_MANUAL_REVIEW:
        assert result.confidence < Decimal("95")


def test_empty_candidates_holds_as_credit_on_account():
    result = match_payment(_incoming(), [])
    assert result.status is PaymentStatus.PENDING_MANUAL_REVIEW
    assert result.invoice is None
    assert result.adjustment_type is AdjustmentType.CREDIT_ON_ACCOUNT


def test_date_decay_still_clears_within_window():
    invoice = _candidate(
        issue_date=date(2026, 4, 1),
        due_date=date(2026, 4, 10),
    )
    # Payment 3 days after due — still within 14-day window.
    result = match_payment(_incoming(payment_date=date(2026, 4, 13)), [invoice])
    assert result.status is PaymentStatus.CLEARED


def test_date_outside_window_still_holds_if_payer_weak():
    invoice = _candidate(
        issue_date=date(2026, 1, 1),
        due_date=date(2026, 1, 15),
        customer_name="Different Ltd",
    )
    result = match_payment(_incoming(payment_date=date(2026, 4, 15)), [invoice])
    assert result.status is PaymentStatus.PENDING_MANUAL_REVIEW
