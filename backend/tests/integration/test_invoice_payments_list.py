"""GET /invoices/{invoice_id}/payments — payments list for the receipt dialog."""
from __future__ import annotations

from uuid import uuid4


def _record(client, invoice_id, amount, day):
    r = client.post(
        f"/api/v1/invoices/{invoice_id}/record-payment",
        json={"amount": amount, "payment_date": day, "payer_name": "Acme"},
    )
    assert r.status_code == 201, r.text
    return r.json()


def test_list_payments_newest_first(client, admin_session, seed_invoice):
    p_old = _record(client, seed_invoice, "100", "2026-08-01")
    p_new = _record(client, seed_invoice, "200", "2026-08-05")

    r = client.get(f"/api/v1/invoices/{seed_invoice}/payments")
    assert r.status_code == 200, r.text
    ids = [p["payment_id"] for p in r.json()]
    assert ids == [p_new["payment_id"], p_old["payment_id"]]


def test_list_payments_empty(client, admin_session, seed_invoice):
    r = client.get(f"/api/v1/invoices/{seed_invoice}/payments")
    assert r.status_code == 200, r.text
    assert r.json() == []


def test_list_payments_unknown_invoice_404(client, admin_session):
    r = client.get(f"/api/v1/invoices/{uuid4()}/payments")
    assert r.status_code == 404, r.text
