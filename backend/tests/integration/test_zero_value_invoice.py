"""Zero-value invoice settlement.

A genuinely zero-total invoice (all line items sum to 0, or a 100% discount)
has nothing to collect. It must never get stuck in an unsettleable SENT state:
- finalizing a zero-value DRAFT settles it straight to PAID, and
- an already-finalized zero-value invoice can be marked PAID through the
  ``/mark-paid`` endpoint (the payment dialog can't help — both layers require
  a positive amount <= balance_due, which is impossible when balance_due is 0).
"""
from __future__ import annotations

from decimal import Decimal

import pytest

from app.models.invoice import Invoice
from app.schemas.invoice import InvoiceCreate, InvoiceLineItemIn
from app.services import invoicing


def _make_zero_value_draft(db, customer_id) -> Invoice:
    payload = InvoiceCreate(
        customer_id=customer_id,
        currency="USD",
        line_items=[InvoiceLineItemIn(description="Complimentary", quantity=Decimal("1"), unit_price=Decimal("0"))],
    )
    invoice = invoicing.create_invoice(db, payload)
    db.flush()
    return invoice


def test_zero_value_draft_has_zero_totals(db, seed_customer):
    invoice = _make_zero_value_draft(db, seed_customer)
    assert invoice.amount == Decimal("0")
    assert invoice.balance_due == Decimal("0")
    assert invoice.status == "DRAFT"


def test_finalize_zero_value_invoice_becomes_paid(db, seed_customer):
    invoice = _make_zero_value_draft(db, seed_customer)
    out = invoicing.finalize_invoice(db, invoice.invoice_id)
    assert out.status == "PAID"
    assert out.balance_due == Decimal("0")


def test_finalize_nonzero_invoice_still_sent(db, seed_customer):
    payload = InvoiceCreate(
        customer_id=seed_customer,
        currency="USD",
        line_items=[InvoiceLineItemIn(description="Work", quantity=Decimal("1"), unit_price=Decimal("100"))],
    )
    invoice = invoicing.create_invoice(db, payload)
    db.flush()
    out = invoicing.finalize_invoice(db, invoice.invoice_id)
    assert out.status == "SENT"
    assert out.balance_due == Decimal("100")


def test_settle_zero_value_invoice_service(db, seed_customer):
    # Simulate a pre-existing zero-value invoice already finalized to SENT.
    invoice = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-ZERO",
        currency="USD",
        amount=Decimal("0"),
        balance_due=Decimal("0"),
        status="SENT",
    )
    db.add(invoice)
    db.flush()

    out = invoicing.settle_zero_value_invoice(db, invoice.invoice_id)
    assert out.status == "PAID"
    assert out.balance_due == Decimal("0")


def test_settle_rejects_nonzero_balance(db, seed_invoice):
    # seed_invoice is a SENT invoice with balance_due 1000 — not a zero-value
    # invoice, so settling it must fail rather than wipe out a real receivable.
    with pytest.raises(invoicing.InvoicingError):
        invoicing.settle_zero_value_invoice(db, seed_invoice)


def test_settle_rejects_draft(db, seed_customer):
    invoice = _make_zero_value_draft(db, seed_customer)  # still DRAFT
    with pytest.raises(invoicing.InvoicingError):
        invoicing.settle_zero_value_invoice(db, invoice.invoice_id)


def test_mark_paid_endpoint_settles_zero_value(client, admin_session, db, seed_customer):
    invoice = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-ZERO-API",
        currency="USD",
        amount=Decimal("0"),
        balance_due=Decimal("0"),
        status="SENT",
    )
    db.add(invoice)
    db.flush()

    r = client.post(f"/api/v1/invoices/{invoice.invoice_id}/mark-paid")
    assert r.status_code == 200, r.text
    assert r.json()["status"] == "PAID"


def test_mark_paid_endpoint_rejects_nonzero_balance(client, admin_session, seed_invoice):
    r = client.post(f"/api/v1/invoices/{seed_invoice}/mark-paid")
    assert r.status_code == 400, r.text
