"""Editing a draft must not change its ``invoice_type``.

The type decides which scheduled job picks an invoice up, so a RECURRING or
USAGE draft that silently became MILESTONE would stop being processed. PATCH
only touches the fields it receives; the edit form relies on this by leaving
``invoice_type`` out of its payload.
"""
from __future__ import annotations

import pytest


@pytest.mark.parametrize("invoice_type", ["RECURRING", "USAGE"])
def test_patch_without_invoice_type_keeps_it(admin_session, seed_customer, invoice_type):
    resp = admin_session.post(
        "/api/v1/invoices",
        json={
            "customer_id": str(seed_customer),
            "currency": "USD",
            "invoice_type": invoice_type,
            "line_items": [
                {"description": "Retainer", "quantity": 1, "unit_price": 100}
            ],
        },
    )
    assert resp.status_code == 201, resp.text
    invoice_id = resp.json()["invoice_id"]

    resp = admin_session.patch(
        f"/api/v1/invoices/{invoice_id}",
        json={
            "notes": "edited",
            "line_items": [
                {"description": "Retainer", "quantity": 2, "unit_price": 100}
            ],
        },
    )
    assert resp.status_code == 200, resp.text
    assert resp.json()["invoice_type"] == invoice_type
    assert resp.json()["notes"] == "edited"
