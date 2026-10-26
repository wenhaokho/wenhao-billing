"""Withholding tax (PPh 21/23) on invoices, templates, conversions."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

from app.models.customer import Customer
from app.schemas.invoice import (
    InvoiceLineItemIn,
    RecurringSchedule,
    RecurringTemplateCreate,
)
from app.schemas.quotation import QuotationCreate, QuotationLineItemIn
from app.services import invoicing, quoting


def _customer(db, **kw) -> Customer:
    c = Customer(name="Wht Customer", **kw)
    db.add(c)
    db.flush()
    return c


def _line(price: str, qty: str = "1") -> dict:
    return {"description": "Work", "quantity": qty, "unit_price": price}


def _post(client, customer_id, price="10000000", currency="IDR", **extra):
    r = client.post(
        "/api/v1/invoices",
        json={
            "customer_id": str(customer_id),
            "currency": currency,
            "line_items": [_line(price)],
            **extra,
        },
    )
    assert r.status_code == 201, r.text
    return r.json()


def test_create_invoice_gross_up_pph23(client, admin_session, db):
    c = _customer(db)
    body = _post(client, c.customer_id, wht_type="PPH23", wht_gross_up=True)
    assert Decimal(body["amount"]) == Decimal(10204082)
    assert Decimal(body["balance_due"]) == Decimal(10204082)
    assert Decimal(body["wht_amount"]) == Decimal(204082)
    assert Decimal(body["wht_base"]) == Decimal(10000000)
    assert Decimal(body["wht_rate"]) == Decimal(2)
    assert Decimal(body["expected_cash"]) == Decimal(10000000)


def test_create_invoice_inherits_customer_default(client, admin_session, db):
    c = _customer(db, default_wht_type="PPH21", default_wht_gross_up=True)
    body = _post(client, c.customer_id)
    assert body["wht_type"] == "PPH21"
    assert Decimal(body["wht_rate"]) == Decimal("2.5")
    assert body["wht_gross_up"] is True


def test_explicit_none_overrides_customer_default(client, admin_session, db):
    c = _customer(db, default_wht_type="PPH23", default_wht_gross_up=True)
    body = _post(client, c.customer_id, wht_type="NONE")
    assert body["wht_type"] == "NONE"
    assert Decimal(body["wht_amount"]) == 0
    assert Decimal(body["amount"]) == Decimal(10000000)


def test_discount_then_withholding(client, admin_session, db):
    c = _customer(db)
    body = _post(
        client,
        c.customer_id,
        price="1000000",
        discount_type="PERCENT",
        discount_value=10,
        wht_type="PPH23",
    )
    assert Decimal(body["amount"]) == Decimal(900000)
    assert Decimal(body["wht_amount"]) == Decimal(18000)


def test_update_draft_recomputes_withholding(client, admin_session, db):
    c = _customer(db)
    body = _post(client, c.customer_id, wht_type="PPH23")
    assert Decimal(body["amount"]) == Decimal(10000000)
    r = client.patch(
        f"/api/v1/invoices/{body['invoice_id']}", json={"wht_gross_up": True}
    )
    assert r.status_code == 200, r.text
    out = r.json()
    assert out["wht_type"] == "PPH23"
    assert Decimal(out["amount"]) == Decimal(10204082)
    assert Decimal(out["balance_due"]) == Decimal(10204082)
    assert Decimal(out["wht_amount"]) == Decimal(204082)


def test_update_type_switch_resets_rate(client, admin_session, db):
    c = _customer(db)
    body = _post(client, c.customer_id, wht_type="PPH23", wht_rate=3)
    r = client.patch(
        f"/api/v1/invoices/{body['invoice_id']}", json={"wht_type": "PPH21"}
    )
    assert r.status_code == 200, r.text
    assert Decimal(r.json()["wht_rate"]) == Decimal("2.5")


def test_recurring_cycle_uses_template_snapshot(db):
    c = _customer(db, default_wht_type="PPH23", default_wht_gross_up=True)
    template = invoicing.create_recurring_template(
        db,
        RecurringTemplateCreate(
            customer_id=c.customer_id,
            currency="IDR",
            payment_terms="Net 14",
            line_items=[
                InvoiceLineItemIn(
                    description="Retainer",
                    quantity=Decimal(1),
                    unit_price=Decimal(10000000),
                )
            ],
            schedule=RecurringSchedule(
                frequency="MONTHLY", interval=1, start_date=date(2026, 5, 1)
            ),
        ),
    )
    assert template.wht_type == "PPH23"
    assert template.amount == Decimal(10204082)

    c.default_wht_type = "NONE"
    c.default_wht_rate = None
    c.default_wht_gross_up = False
    db.flush()

    inv = invoicing.trigger_recurring_cycle(
        db, template_invoice_id=template.invoice_id, cycle_key="2026-05-01"
    )
    assert inv.wht_type == "PPH23"
    assert inv.wht_gross_up is True
    assert inv.amount == template.amount
    assert inv.balance_due == template.amount
    assert inv.wht_amount == Decimal(204082)


def test_quote_conversion_applies_customer_default(db):
    c = _customer(db, default_wht_type="PPH23", default_wht_gross_up=True)
    q = quoting.create_quotation(
        db,
        QuotationCreate(
            customer_id=c.customer_id,
            currency="IDR",
            line_items=[
                QuotationLineItemIn(
                    description="Work",
                    quantity=Decimal(1),
                    unit_price=Decimal(10000000),
                )
            ],
        ),
    )
    inv = quoting.convert_to_invoice(db, q.quotation_id)
    assert inv.wht_type == "PPH23"
    assert inv.amount == Decimal(10204082)
    assert inv.balance_due == Decimal(10204082)
    assert inv.wht_amount == Decimal(204082)
    assert inv.wht_base == Decimal(10000000)
