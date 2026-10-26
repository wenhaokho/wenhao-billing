"""Reconciliation of PPh 21/23 withholding invoices.

The customer pays net cash (balance minus withheld tax); clearing books the
withheld part to Prepaid Income Tax (1300) and relieves AR for the gross amount.
"""

from __future__ import annotations

import uuid
from datetime import date
from decimal import Decimal

import pytest
from sqlalchemy import select

from app.models.coa import ChartOfAccount
from app.models.customer import Customer
from app.models.fx import FxRate
from app.models.invoice import Invoice
from app.models.journal import JournalEntry
from app.models.user import User
from app.schemas.invoice import InvoiceCreate, InvoiceLineItemIn
from app.services import invoicing, reconciliation
from app.services.reconciliation import IntakePayload

GROSS = Decimal(10204082)
WHT = Decimal(204082)
NET = Decimal(10000000)


@pytest.fixture()
def admin_user(db) -> User:
    u = User(email="admin@example.com", password_hash="$2b$12$placeholder", role="admin")
    db.add(u)
    db.flush()
    return u


def _make_wht_invoice(db, currency: str, base: Decimal) -> Invoice:
    customer = Customer(name="Acme Corp", matching_aliases=["Acme"])
    db.add(customer)
    db.flush()
    invoice = invoicing.create_invoice(
        db,
        InvoiceCreate(
            customer_id=customer.customer_id,
            currency=currency,
            issue_date=date(2026, 4, 10),
            due_date=date(2026, 4, 24),
            wht_type="PPH23",
            wht_rate=Decimal(2),
            wht_gross_up=True,
            line_items=[
                InvoiceLineItemIn(description="Work", quantity=Decimal(1), unit_price=base)
            ],
        ),
    )
    invoicing.finalize_invoice(db, invoice.invoice_id)
    return invoice


@pytest.fixture()
def wht_invoice(db) -> Invoice:
    invoice = _make_wht_invoice(db, "IDR", NET)
    assert invoice.balance_due == GROSS
    assert invoice.wht_amount == WHT
    return invoice


def _payload(amount: Decimal, **kwargs) -> IntakePayload:
    fields = {
        "amount": amount,
        "currency": "IDR",
        "payer_name": "Acme Corp",
        "payer_reference": "REF-WHT",
        "payment_date": date(2026, 4, 15),
        "intake_source": "WEBHOOK",
        "external_ref": f"EXT-{uuid.uuid4()}",
    }
    return IntakePayload(**(fields | kwargs))


def _codes(db) -> dict[str, int]:
    return dict(db.execute(select(ChartOfAccount.code, ChartOfAccount.account_id)).all())


def _active_entry(db, payment_id) -> JournalEntry:
    return db.scalar(
        select(JournalEntry)
        .where(JournalEntry.source_id == payment_id)
        .where(JournalEntry.reversed_by_entry_id.is_(None))
    )


def test_auto_clear_books_prepaid_tax(db, wht_invoice):
    payment = reconciliation.process_incoming_payment(db, _payload(NET))
    db.flush()

    assert payment.status == "CLEARED"
    assert payment.wht_amount == WHT
    db.refresh(wht_invoice)
    assert wht_invoice.status == "PAID"
    assert wht_invoice.balance_due == 0
    assert wht_invoice.wht_booked == WHT

    entry = _active_entry(db, payment.payment_id)
    assert entry is not None
    assert sum(l.debit - l.credit for l in entry.lines) == 0
    assert sum(l.base_amount_debit - l.base_amount_credit for l in entry.lines) == 0

    codes = _codes(db)
    by_account = {l.account_id: l for l in entry.lines}
    assert len(entry.lines) == 3
    assert by_account[codes["1000"]].debit == NET
    assert by_account[codes["1300"]].debit == WHT
    assert by_account[codes["1100"]].credit == GROSS


def test_reverse_then_repay(db, wht_invoice, admin_user):
    first = reconciliation.process_incoming_payment(db, _payload(NET))
    db.flush()
    assert first.status == "CLEARED"

    reconciliation.reverse_payment(db, first.payment_id, admin_user.user_id, reason="bounced")
    db.flush()
    db.refresh(wht_invoice)
    assert wht_invoice.balance_due == GROSS
    assert wht_invoice.wht_booked == 0
    assert wht_invoice.status == "OPEN"

    second = reconciliation.process_incoming_payment(db, _payload(NET))
    db.flush()
    assert second.status == "CLEARED"
    assert second.wht_amount == WHT
    db.refresh(wht_invoice)
    assert wht_invoice.status == "PAID"
    assert wht_invoice.balance_due == 0
    assert wht_invoice.wht_booked == WHT


def test_manual_approve_non_matching_amount_books_no_withholding(db, wht_invoice, admin_user):
    payment = reconciliation.process_incoming_payment(db, _payload(Decimal(9600000)))
    db.flush()
    assert payment.status == "PENDING_MANUAL_REVIEW"

    reconciliation.approve_manual_match(
        db, payment.payment_id, wht_invoice.invoice_id, admin_user.user_id
    )
    db.flush()

    db.refresh(payment)
    db.refresh(wht_invoice)
    assert payment.status == "CLEARED"
    assert payment.wht_amount == 0
    assert wht_invoice.balance_due == Decimal(604082)
    assert wht_invoice.wht_booked == 0
    assert wht_invoice.status == "PARTIAL"

    entry = _active_entry(db, payment.payment_id)
    codes = _codes(db)
    assert {l.account_id for l in entry.lines} == {codes["1000"], codes["1100"]}
    assert sum(l.debit - l.credit for l in entry.lines) == 0


def test_foreign_currency_withholding_entry_balances_in_base(db):
    # At this rate base(1000.00) + base(20.41) != base(1020.41) after 4dp
    # rounding, so converting the three lines independently would leave the
    # entry 0.0001 IDR out of balance.
    db.add(
        FxRate(
            from_currency="USD",
            to_currency="IDR",
            rate=Decimal("15204.60256196"),
            as_of_date=date(2026, 4, 15),
            source="TEST",
        )
    )
    invoice = _make_wht_invoice(db, "USD", Decimal(1000))
    assert invoice.balance_due == Decimal("1020.41")
    assert invoice.wht_amount == Decimal("20.41")

    payment = reconciliation.process_incoming_payment(
        db, _payload(Decimal("1000.00"), currency="USD")
    )
    db.flush()
    assert payment.status == "CLEARED"
    assert payment.wht_amount == Decimal("20.41")

    entry = _active_entry(db, payment.payment_id)
    assert sum(l.debit - l.credit for l in entry.lines) == 0
    assert sum(l.base_amount_debit - l.base_amount_credit for l in entry.lines) == 0
    assert {l.currency for l in entry.lines} == {"USD"}


def test_gross_payment_is_held_without_side_effects(db, wht_invoice):
    # Paying the gross total means the customer did not withhold — ambiguous,
    # so it must stop for a human: no ledger, no invoice change.
    payment = reconciliation.process_incoming_payment(db, _payload(GROSS))
    db.flush()

    assert payment.status == "PENDING_MANUAL_REVIEW"
    assert payment.wht_amount == 0
    assert db.scalar(
        select(JournalEntry).where(JournalEntry.source_id == payment.payment_id)
    ) is None
    db.refresh(wht_invoice)
    assert wht_invoice.balance_due == GROSS
    assert wht_invoice.wht_booked == 0
    assert wht_invoice.status == "OPEN"
