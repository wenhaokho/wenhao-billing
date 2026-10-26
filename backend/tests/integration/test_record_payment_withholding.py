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


def test_pph_only_payment_settles_stuck_partial(client, admin_session, db, wht_invoice):
    # Customer paid the full net in cash without a PPh slip; the remaining
    # balance equals the outstanding PPh, so the cash-to-receive is 0.
    r = _post(client, wht_invoice.invoice_id, amount="10000000")
    assert r.status_code == 201, r.text
    db.refresh(wht_invoice)
    assert wht_invoice.balance_due == WHT
    r = _post(client, wht_invoice.invoice_id, amount="0", wht_amount="204082")
    assert r.status_code == 201, r.text
    db.refresh(wht_invoice)
    assert wht_invoice.status == "PAID"
    assert wht_invoice.balance_due == 0
    assert wht_invoice.wht_booked == WHT


def test_zero_amount_and_zero_withheld_400(client, admin_session, db, wht_invoice):
    r = _post(client, wht_invoice.invoice_id, amount="0", wht_amount="0")
    assert r.status_code == 400, r.text
    db.refresh(wht_invoice)
    assert wht_invoice.balance_due == GROSS


def test_send_invoice_body_quotes_amount_to_transfer(
    client, admin_session, wht_invoice, monkeypatch
):
    calls = []
    monkeypatch.setattr(
        "app.api.v1.routers.invoices.send_email", lambda **kw: calls.append(kw)
    )
    monkeypatch.setattr(
        "app.api.v1.routers.invoices.render_invoice_pdf", lambda *a, **k: b"%PDF"
    )
    r = client.post(
        f"/api/v1/invoices/{wht_invoice.invoice_id}/send",
        json={"to_email": "ap@acme.example"},
    )
    assert r.status_code == 200, r.text
    body = calls[0]["body_text"]
    assert "for 10,204,082 IDR." in body
    assert "Amount to transfer (after withholding tax): 10,000,000 IDR." in body


def test_send_invoice_body_without_wht_unchanged(
    client, admin_session, seed_invoice, monkeypatch
):
    calls = []
    monkeypatch.setattr(
        "app.api.v1.routers.invoices.send_email", lambda **kw: calls.append(kw)
    )
    monkeypatch.setattr(
        "app.api.v1.routers.invoices.render_invoice_pdf", lambda *a, **k: b"%PDF"
    )
    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/send", json={"to_email": "ap@acme.example"}
    )
    assert r.status_code == 200, r.text
    assert "Amount to transfer" not in calls[0]["body_text"]


def test_payment_reminder_quotes_expected_cash(mcp_tool_db, wht_invoice):
    from unittest.mock import patch

    from app.mcp.tools.gated_email import send_payment_reminder

    preview = send_payment_reminder(
        invoice_id=str(wht_invoice.invoice_id), to_email="ap@acme.example"
    )
    assert Decimal(preview["amount"]) == NET
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_payment_reminder(
            invoice_id=str(wht_invoice.invoice_id),
            to_email="ap@acme.example",
            confirm_token=preview["confirm_token"],
        )
    assert out["sent"] is True
    assert "outstanding balance of 10000000" in mock_send.call_args.kwargs["body_text"]


def test_bill_payment_schema_still_requires_positive_amount():
    from pydantic import ValidationError

    from app.schemas.payment import InvoiceRecordPaymentRequest, RecordPaymentRequest

    with pytest.raises(ValidationError):
        RecordPaymentRequest(amount=Decimal(0), payment_date=date(2026, 4, 20))
    req = InvoiceRecordPaymentRequest(
        amount=Decimal(0), wht_amount=Decimal(1), payment_date=date(2026, 4, 20)
    )
    assert req.amount == 0
