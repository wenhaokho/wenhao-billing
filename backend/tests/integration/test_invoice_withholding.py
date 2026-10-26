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


def test_usage_lock_recomputes_from_snapshot(db):
    from app.models.invoice import Invoice

    c = _customer(db)
    inv = Invoice(
        customer_id=c.customer_id,
        invoice_type="USAGE",
        currency="IDR",
        amount=0,
        balance_due=0,
        status="DRAFT",
        wht_type="PPH23",
        wht_rate=Decimal(2),
        wht_gross_up=True,
        billing_cycle_ref={"cut_off_day": 25},
        is_template=False,
    )
    db.add(inv)
    db.flush()
    out = invoicing.lock_usage_invoice(
        db,
        invoice_id=inv.invoice_id,
        accrued_amount=Decimal(10000000),
        cutoff_date=date(2026, 5, 25),
    )
    assert out.amount == Decimal(10204082)
    assert out.balance_due == Decimal(10204082)
    assert out.wht_amount == Decimal(204082)
    assert out.wht_base == Decimal(10000000)


def test_milestone_invoice_inherits_customer_default(db):
    c = _customer(db, default_wht_type="PPH21", default_wht_gross_up=False)
    inv = invoicing.create_milestone_invoice(
        db,
        customer_id=c.customer_id,
        currency="IDR",
        amount=Decimal(1000000),
        milestone_ref="M1",
    )
    assert inv.wht_type == "PPH21"
    assert inv.wht_rate == Decimal("2.5")
    assert inv.wht_amount == Decimal(25000)
    assert inv.amount == Decimal(1000000)


def test_update_template_gross_up_only_keeps_type_and_rate(db):
    c = _customer(db)
    base = {
        "customer_id": c.customer_id,
        "currency": "IDR",
        "payment_terms": "Net 14",
        "line_items": [
            InvoiceLineItemIn(
                description="Retainer",
                quantity=Decimal(1),
                unit_price=Decimal(10000000),
            )
        ],
        "schedule": RecurringSchedule(
            frequency="MONTHLY", interval=1, start_date=date(2026, 5, 1)
        ),
    }
    t = invoicing.create_recurring_template(
        db,
        RecurringTemplateCreate(**base, wht_type="PPH23", wht_rate=Decimal(3)),
    )
    assert t.amount == Decimal(10000000)
    t = invoicing.update_recurring_template(
        db, t.invoice_id, RecurringTemplateCreate(**base, wht_gross_up=True)
    )
    assert t.wht_type == "PPH23"
    assert t.wht_rate == Decimal(3)
    assert t.wht_gross_up is True
    assert t.amount == Decimal(10309278)
    assert t.wht_amount == Decimal(309278)


def test_create_invoice_rate_100_returns_400(client, admin_session, db):
    c = _customer(db)
    r = client.post(
        "/api/v1/invoices",
        json={
            "customer_id": str(c.customer_id),
            "currency": "IDR",
            "wht_type": "PPH23",
            "wht_rate": 100,
            "line_items": [_line("1000000")],
        },
    )
    assert r.status_code == 400, r.text
