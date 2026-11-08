"""Team-only internal note on invoices.

``internal_note`` is free text for the agency's own team. It must round-trip
through create / update / read, be copied from a recurring template onto each
generated child, and never appear in the customer-facing PDF.
"""
from __future__ import annotations

from datetime import date
from decimal import Decimal

from app.models.customer import Customer
from app.schemas.invoice import (
    InvoiceCreate,
    InvoiceLineItemIn,
    InvoiceUpdate,
    RecurringSchedule,
    RecurringTemplateCreate,
)
from app.services import invoicing
from app.services.pdf.render import render_invoice_html

NOTE = "Chase Sam on the 5th; PO usually lags a week."


def _line():
    return InvoiceLineItemIn(
        description="Retainer", quantity=Decimal("1"), unit_price=Decimal("100")
    )


def test_internal_note_round_trips_through_api(admin_session, seed_customer):
    resp = admin_session.post(
        "/api/v1/invoices",
        json={
            "customer_id": str(seed_customer),
            "currency": "USD",
            "internal_note": NOTE,
            "line_items": [
                {"description": "Retainer", "quantity": 1, "unit_price": 100}
            ],
        },
    )
    assert resp.status_code == 201, resp.text
    invoice_id = resp.json()["invoice_id"]
    assert resp.json()["internal_note"] == NOTE

    resp = admin_session.patch(
        f"/api/v1/invoices/{invoice_id}", json={"internal_note": "updated"}
    )
    assert resp.status_code == 200, resp.text
    assert resp.json()["internal_note"] == "updated"

    resp = admin_session.get(f"/api/v1/invoices/{invoice_id}")
    assert resp.status_code == 200, resp.text
    assert resp.json()["internal_note"] == "updated"


def test_internal_note_can_be_cleared(db, seed_customer):
    invoice = invoicing.create_invoice(
        db,
        InvoiceCreate(
            customer_id=seed_customer,
            currency="USD",
            internal_note=NOTE,
            line_items=[_line()],
        ),
    )
    db.flush()
    invoicing.update_invoice(db, invoice.invoice_id, InvoiceUpdate(internal_note=None))
    db.flush()
    assert invoice.internal_note is None


def test_internal_note_never_reaches_the_pdf(db, seed_customer):
    invoice = invoicing.create_invoice(
        db,
        InvoiceCreate(
            customer_id=seed_customer,
            currency="USD",
            notes="Customer-facing note",
            internal_note=NOTE,
            line_items=[_line()],
        ),
    )
    db.flush()
    html = render_invoice_html(invoice, db.get(Customer, seed_customer), None)
    assert "Customer-facing note" in html
    assert NOTE not in html


def test_template_internal_note_copies_to_generated_child(db, seed_customer):
    template = invoicing.create_recurring_template(
        db,
        RecurringTemplateCreate(
            customer_id=seed_customer,
            currency="USD",
            payment_terms="Net 14",
            internal_note=NOTE,
            line_items=[_line()],
            schedule=RecurringSchedule(
                frequency="MONTHLY", interval=1, start_date=date(2026, 1, 1)
            ),
        ),
    )
    db.flush()
    child = invoicing.trigger_recurring_cycle(
        db, template_invoice_id=template.invoice_id, cycle_key="2026-06-01"
    )
    assert child.internal_note == NOTE
