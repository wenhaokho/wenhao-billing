"""POST /invoices/{invoice_id}/payments/{payment_id}/send-receipt.

Sends a payment-receipt email (PDF attached) for a manually recorded payment.
Email dispatch is monkeypatched — no real Resend/SMTP calls.
"""
from __future__ import annotations

from decimal import Decimal
from uuid import uuid4

from app.models.customer import Customer
from app.models.invoice import Invoice


def _record_payment(client, invoice_id, amount="400"):
    r = client.post(
        f"/api/v1/invoices/{invoice_id}/record-payment",
        json={"amount": amount, "payment_date": "2026-08-07", "payer_name": "Acme"},
    )
    assert r.status_code == 201, r.text
    return r.json()


def _patch_send_email(monkeypatch, calls):
    def fake_send_email(**kwargs):
        calls.append(kwargs)

    monkeypatch.setattr("app.api.v1.routers.invoices.send_email", fake_send_email)


def test_send_receipt_happy_path(client, admin_session, db, seed_invoice, monkeypatch):
    calls = []
    _patch_send_email(monkeypatch, calls)
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={"to_email": "acme@example.com", "cc_email": "me@example.com"},
    )
    assert r.status_code == 200, r.text
    assert r.json() == {"sent_to": "acme@example.com"}

    assert len(calls) == 1
    kw = calls[0]
    assert kw["to_email"] == "acme@example.com"
    assert kw["cc_email"] == "me@example.com"
    filename, content, mime = kw["attachments"][0]
    assert filename.startswith("receipt-RCT-") and filename.endswith(".pdf")
    assert content[:5] == b"%PDF-"
    assert mime == "application/pdf"
    # 1000 - 400 leaves 600 remaining; both figures appear in the body.
    assert "400" in kw["body_text"]
    assert "600" in kw["body_text"]


def test_send_receipt_defaults_to_customer_contact_email(
    client, admin_session, db, seed_customer, seed_invoice, monkeypatch
):
    calls = []
    _patch_send_email(monkeypatch, calls)
    db.get(Customer, seed_customer).contact_email = "billing@acme.example"
    db.flush()
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={},
    )
    assert r.status_code == 200, r.text
    assert r.json() == {"sent_to": "billing@acme.example"}
    assert calls[0]["to_email"] == "billing@acme.example"


def test_send_receipt_no_recipient_400(client, admin_session, db, seed_invoice, monkeypatch):
    # seed_customer has no contact_email and no to_email is provided.
    calls = []
    _patch_send_email(monkeypatch, calls)
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={},
    )
    assert r.status_code == 400, r.text
    assert calls == []


def test_send_receipt_payment_not_on_invoice_404(
    client, admin_session, db, seed_customer, seed_invoice, monkeypatch
):
    calls = []
    _patch_send_email(monkeypatch, calls)
    payment = _record_payment(client, seed_invoice)

    other = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-OTHER",
        currency="USD",
        amount=Decimal("500.0000"),
        balance_due=Decimal("500.0000"),
        status="SENT",
    )
    db.add(other)
    db.flush()

    r = client.post(
        f"/api/v1/invoices/{other.invoice_id}/payments/{payment['payment_id']}/send-receipt",
        json={"to_email": "acme@example.com"},
    )
    assert r.status_code == 404, r.text
    assert calls == []


def test_send_receipt_unknown_payment_404(client, admin_session, seed_invoice, monkeypatch):
    calls = []
    _patch_send_email(monkeypatch, calls)
    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{uuid4()}/send-receipt",
        json={"to_email": "acme@example.com"},
    )
    assert r.status_code == 404, r.text


def test_send_receipt_email_failure_502(client, admin_session, db, seed_invoice, monkeypatch):
    def boom(**kwargs):
        raise RuntimeError("resend down")

    monkeypatch.setattr("app.api.v1.routers.invoices.send_email", boom)
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={"to_email": "acme@example.com"},
    )
    assert r.status_code == 502, r.text
