from __future__ import annotations

from datetime import date
from decimal import Decimal

from app.models.cloudflare_target import CloudflareTarget
from app.models.customer import Customer
from app.models.item import Item
from app.schemas.invoice import (
    InvoiceLineItemIn,
    RecurringSchedule,
    RecurringTemplateCreate,
)
from app.services import invoicing, reconciliation
from app.services.hosting import ensure_hosting_restored
from app.workers.tasks.hosting_enforcement import run_hosting_enforcement


def _mk_customer(db: object) -> Customer:
    customer = Customer(name="Hosted Client", matching_aliases=["Hosted Client"])
    db.add(customer)
    db.flush()
    return customer


def _mk_hosting_item(db: object) -> Item:
    item = Item(
        name="Acme hosting",
        item_type="SERVICE",
        default_currency="USD",
        default_unit_price=Decimal("240.0000"),
        is_sold=True,
        is_purchased=False,
        active=True,
        is_hosting=True,
        hosting_domain="example.com",
        hosting_grace_days=3,
        hosting_suspension_enabled=True,
        hosting_status="ACTIVE",
    )
    item.cloudflare_target = CloudflareTarget(
        zone_id="zone_123",
        record_id="record_123",
        record_name="example.com",
        record_type="CNAME",
        live_content="live.example.net",
        maintenance_content="maintenance.example.net",
        proxied=True,
    )
    db.add(item)
    db.flush()
    return item


def _mk_template(db: object, customer: Customer, item: Item) -> object:
    payload = RecurringTemplateCreate(
        customer_id=customer.customer_id,
        currency="USD",
        payment_terms="Net 7",
        line_items=[
            InvoiceLineItemIn(
                item_id=item.item_id,
                description=f"{item.name} - {item.hosting_domain}",
                quantity=Decimal("1.0000"),
                unit_price=Decimal("240.0000"),
                position=0,
            )
        ],
        schedule=RecurringSchedule(
            frequency="MONTHLY",
            interval=3,
            start_date=date(2026, 6, 1),
            end_mode="NEVER",
        ),
    )
    template = invoicing.create_recurring_template(db, payload)
    db.flush()
    return template


def test_hosting_item_creates_with_cloudflare_target(db):
    item = _mk_hosting_item(db)
    db.flush()

    assert item.is_hosting is True
    assert item.hosting_status == "ACTIVE"
    assert item.hosting_domain == "example.com"
    assert item.cloudflare_target is not None
    assert item.cloudflare_target.record_name == "example.com"
    assert item.cloudflare_target.provider_status == "ACTIVE"


def test_trigger_recurring_cycle_stamps_coverage_on_hosting_invoice(db):
    customer = _mk_customer(db)
    item = _mk_hosting_item(db)
    template = _mk_template(db, customer, item)

    invoice = invoicing.trigger_recurring_cycle(
        db,
        template_invoice_id=template.invoice_id,
        cycle_key="2026-06-01",
    )
    db.flush()

    assert invoice.coverage_start == date(2026, 6, 1)
    assert invoice.coverage_end == date(2026, 8, 31)
    assert "Coverage: 2026-06-01 to 2026-08-31" in invoice.line_items[0].description


def test_hosting_enforcement_suspends_and_reconciliation_restore_reactivates(
    db, monkeypatch
):
    customer = _mk_customer(db)
    item = _mk_hosting_item(db)
    template = _mk_template(db, customer, item)
    invoice = invoicing.trigger_recurring_cycle(
        db,
        template_invoice_id=template.invoice_id,
        cycle_key="2026-06-01",
    )
    invoice.status = "SENT"
    invoice.due_date = date(2026, 6, 8)
    db.flush()

    calls: list[tuple[str, str]] = []

    def _fake_suspend(target):
        calls.append(("suspend", target.record_id))

    def _fake_restore(target):
        calls.append(("restore", target.record_id))

    monkeypatch.setattr("app.services.cloudflare.suspend_target", _fake_suspend)
    monkeypatch.setattr("app.services.cloudflare.restore_target", _fake_restore)

    result = run_hosting_enforcement(db, today=date(2026, 6, 12))
    assert result["suspended"] == 1

    db.refresh(item)
    db.refresh(invoice)
    assert item.hosting_status == "SUSPENDED"
    assert item.cloudflare_target.provider_status == "SUSPENDED"
    assert calls == [("suspend", "record_123")]

    payment = reconciliation.process_incoming_payment(
        db,
        reconciliation.IntakePayload(
            amount=Decimal("240.0000"),
            currency="USD",
            payer_name="Hosted Client",
            payer_reference="HOST-240",
            payment_date=date(2026, 6, 12),
            intake_source="WEBHOOK",
            external_ref="host-240",
        ),
    )
    ensure_hosting_restored(db, invoice)
    db.flush()

    db.refresh(payment)
    db.refresh(invoice)
    db.refresh(item)
    assert payment.status == "CLEARED"
    assert invoice.status == "PAID"
    assert item.hosting_status == "ACTIVE"
    assert item.cloudflare_target.provider_status == "ACTIVE"
    assert calls == [("suspend", "record_123"), ("restore", "record_123")]


def test_enforcement_suspend_failure_marks_provider_error(db, monkeypatch):
    customer = _mk_customer(db)
    item = _mk_hosting_item(db)
    template = _mk_template(db, customer, item)
    invoice = invoicing.trigger_recurring_cycle(
        db,
        template_invoice_id=template.invoice_id,
        cycle_key="2026-06-01",
    )
    invoice.status = "SENT"
    invoice.due_date = date(2026, 6, 8)
    db.flush()

    def _boom(target):
        raise RuntimeError("cloudflare 500")

    monkeypatch.setattr("app.services.cloudflare.suspend_target", _boom)

    result = run_hosting_enforcement(db, today=date(2026, 6, 12))
    assert result == {"suspended": 0, "restored": 0, "errors": 1}

    db.refresh(item)
    assert item.hosting_status == "SUSPEND_PENDING"
    assert item.cloudflare_target.provider_status == "ERROR"
    assert item.cloudflare_target.last_error == "cloudflare 500"
    assert item.cloudflare_target.last_action_at is not None


def test_enforcement_is_idempotent_for_already_suspended_hosting_item(db, monkeypatch):
    customer = _mk_customer(db)
    item = _mk_hosting_item(db)
    template = _mk_template(db, customer, item)
    invoice = invoicing.trigger_recurring_cycle(
        db,
        template_invoice_id=template.invoice_id,
        cycle_key="2026-06-01",
    )
    invoice.status = "SENT"
    invoice.due_date = date(2026, 6, 8)
    db.flush()

    calls: list[str] = []
    monkeypatch.setattr(
        "app.services.cloudflare.suspend_target",
        lambda target: calls.append(target.record_id),
    )
    monkeypatch.setattr(
        "app.services.cloudflare.restore_target",
        lambda target: (_ for _ in ()).throw(AssertionError("should not restore")),
    )

    first = run_hosting_enforcement(db, today=date(2026, 6, 12))
    assert first["suspended"] == 1
    assert calls == ["record_123"]
    db.refresh(item)
    first_action_at = item.cloudflare_target.last_action_at

    second = run_hosting_enforcement(db, today=date(2026, 6, 13))
    assert second == {"suspended": 0, "restored": 0, "errors": 0}
    assert calls == ["record_123"]

    db.refresh(item)
    assert item.hosting_status == "SUSPENDED"
    assert item.cloudflare_target.provider_status == "SUSPENDED"
    assert item.cloudflare_target.last_action_at == first_action_at


def test_enforcement_ignores_non_overdue_invoice(db, monkeypatch):
    customer = _mk_customer(db)
    item = _mk_hosting_item(db)
    template = _mk_template(db, customer, item)
    invoice = invoicing.trigger_recurring_cycle(
        db,
        template_invoice_id=template.invoice_id,
        cycle_key="2026-06-01",
    )
    invoice.status = "SENT"
    invoice.due_date = date(2026, 6, 15)
    db.flush()

    monkeypatch.setattr(
        "app.services.cloudflare.suspend_target",
        lambda target: (_ for _ in ()).throw(AssertionError("should not suspend")),
    )

    result = run_hosting_enforcement(db, today=date(2026, 6, 12))
    assert result["suspended"] == 0

    db.refresh(item)
    assert item.hosting_status == "ACTIVE"
