"""Manual record-payment accepts a withheld PPh amount (no ledger posting)."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.schemas.invoice import InvoiceCreate, InvoiceLineItemIn
from app.services import invoicing

NET = Decimal(10000000)
WHT = Decimal(204082)
GROSS = Decimal(10204082)


@pytest.fixture()
def wht_invoice(db) -> Invoice:
    customer = Customer(name="Acme Corp", matching_aliases=["Acme"])
    db.add(customer)
    db.flush()
    invoice = invoicing.create_invoice(
        db,
        InvoiceCreate(
            customer_id=customer.customer_id,
            currency="IDR",
            issue_date=date(2026, 4, 10),
            due_date=date(2026, 4, 24),
            wht_type="PPH23",
            wht_rate=Decimal(2),
            wht_gross_up=True,
            line_items=[
                InvoiceLineItemIn(description="Work", quantity=Decimal(1), unit_price=NET)
            ],
        ),
    )
    invoicing.finalize_invoice(db, invoice.invoice_id)
    assert invoice.balance_due == GROSS
    return invoice


def _post(client, invoice_id, **body):
    return client.post(
        f"/api/v1/invoices/{invoice_id}/record-payment",
        json={"payment_date": "2026-04-20", "payer_name": "Acme", **body},
    )


def test_record_net_plus_withheld_marks_paid(client, admin_session, db, wht_invoice):
    r = _post(client, wht_invoice.invoice_id, amount="10000000", wht_amount="204082")
    assert r.status_code == 201, r.text
    assert Decimal(r.json()["wht_amount"]) == WHT
    db.refresh(wht_invoice)
    assert wht_invoice.status == "PAID"
    assert wht_invoice.balance_due == 0
    assert wht_invoice.wht_booked == WHT


def test_record_exceeding_balance_400(client, admin_session, db, wht_invoice):
    r = _post(client, wht_invoice.invoice_id, amount="10000000", wht_amount="204083")
    assert r.status_code == 400, r.text
    db.refresh(wht_invoice)
    assert wht_invoice.balance_due == GROSS
    assert wht_invoice.wht_booked == 0


def test_record_without_wht_unchanged(client, admin_session, db, wht_invoice):
    r = _post(client, wht_invoice.invoice_id, amount="1000000")
    assert r.status_code == 201, r.text
    assert Decimal(r.json()["wht_amount"]) == 0
    db.refresh(wht_invoice)
    assert wht_invoice.status == "PARTIAL"
    assert wht_invoice.balance_due == GROSS - Decimal(1000000)
    assert wht_invoice.wht_booked == 0


def test_record_partial_with_wht_reduces_balance_by_both(client, admin_session, db, wht_invoice):
    r = _post(client, wht_invoice.invoice_id, amount="5000000", wht_amount="100000")
    assert r.status_code == 201, r.text
    db.refresh(wht_invoice)
    assert wht_invoice.status == "PARTIAL"
    assert wht_invoice.balance_due == GROSS - Decimal(5100000)
    assert wht_invoice.wht_booked == Decimal(100000)


def test_exceeding_error_mentions_withheld(client, admin_session, wht_invoice):
    r = _post(client, wht_invoice.invoice_id, amount="10000000", wht_amount="204083")
    assert r.status_code == 400
    assert "amount + withheld" in r.json()["detail"]


def test_withheld_above_outstanding_400(client, admin_session, db, wht_invoice):
    # Fits the balance due, but withholds more PPh than the invoice carries.
    r = _post(client, wht_invoice.invoice_id, amount="1000000", wht_amount="204083")
    assert r.status_code == 400, r.text
    assert "exceeds outstanding withholding" in r.json()["detail"]
    db.refresh(wht_invoice)
    assert wht_invoice.balance_due == GROSS
    assert wht_invoice.wht_booked == 0


def test_withheld_above_remaining_after_partial_400(client, admin_session, db, wht_invoice):
    r = _post(client, wht_invoice.invoice_id, amount="5000000", wht_amount="100000")
    assert r.status_code == 201, r.text
    r = _post(client, wht_invoice.invoice_id, amount="1000000", wht_amount="104083")
    assert r.status_code == 400, r.text
    assert "outstanding withholding 104082" in r.json()["detail"]
    r = _post(client, wht_invoice.invoice_id, amount="1000000", wht_amount="104082")
    assert r.status_code == 201, r.text
