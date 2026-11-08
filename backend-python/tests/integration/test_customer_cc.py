"""Customer default CC lists and multi-address CC on send requests.

Email dispatch is monkeypatched — no real Resend/SMTP calls.
"""
from __future__ import annotations

from tests.integration.test_send_receipt import _patch_send_email, _record_payment


def test_customer_cc_lists_roundtrip(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={
            "name": "CC Co",
            "quotation_cc_emails": ["sales@cc.example", "boss@cc.example"],
            "billing_cc_emails": ["finance@cc.example"],
        },
    )
    assert r.status_code == 201, r.text
    body = r.json()
    assert body["quotation_cc_emails"] == ["sales@cc.example", "boss@cc.example"]
    assert body["billing_cc_emails"] == ["finance@cc.example"]

    r = client.patch(
        f"/api/v1/customers/{body['customer_id']}",
        json={"billing_cc_emails": []},
    )
    assert r.status_code == 200, r.text
    assert r.json()["billing_cc_emails"] == []
    assert r.json()["quotation_cc_emails"] == ["sales@cc.example", "boss@cc.example"]


def test_customer_cc_rejects_invalid_address(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={"name": "Bad CC", "billing_cc_emails": ["not-an-email"]},
    )
    assert r.status_code == 422


def test_send_receipt_multiple_cc_normalised(client, admin_session, db, seed_invoice, monkeypatch):
    calls = []
    _patch_send_email(monkeypatch, calls)
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={
            "to_email": "acme@example.com",
            "cc_email": " a@example.com;b@example.com,, A@example.com ",
        },
    )
    assert r.status_code == 200, r.text
    assert calls[0]["cc_email"] == "a@example.com, b@example.com"


def test_send_receipt_invalid_cc_422(client, admin_session, db, seed_invoice, monkeypatch):
    calls = []
    _patch_send_email(monkeypatch, calls)
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={"to_email": "acme@example.com", "cc_email": "a@example.com, nope"},
    )
    assert r.status_code == 422
    assert calls == []
