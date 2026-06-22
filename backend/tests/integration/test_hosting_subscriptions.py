from __future__ import annotations

from datetime import date
from decimal import Decimal

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.item import Item
from app.schemas.hosting_subscription import (
    CloudflareTargetIn,
    HostingSubscriptionCreate,
)
from app.services import invoicing, reconciliation
from app.services.hosting_subscriptions import (
    create_hosting_subscription,
    ensure_subscription_restored,
    latest_child_invoice,
)
from app.workers.tasks.hosting_enforcement import run_hosting_enforcement


def _mk_customer(db: object) -> Customer:
    customer = Customer(name="Hosted Client", matching_aliases=["Hosted Client"])
    db.add(customer)
    db.flush()
    return customer


def _mk_item(db: object) -> Item:
    item = Item(
        name="Managed hosting",
        item_type="SERVICE",
        default_currency="USD",
        default_unit_price=Decimal("240.0000"),
        is_sold=True,
        is_purchased=False,
        active=True,
    )
    db.add(item)
    db.flush()
    return item


def _mk_payload(customer: Customer, item: Item) -> HostingSubscriptionCreate:
    return HostingSubscriptionCreate(
        customer_id=customer.customer_id,
        item_id=item.item_id,
        service_name="Primary hosting",
        domain_name="example.com",
        currency="USD",
        bundle_months=3,
        payment_terms="Net 7",
        billing_anchor_date=date(2026, 6, 1),
        grace_days=3,
        suspension_enabled=True,
        cloudflare_target=CloudflareTargetIn(
            zone_id="zone_123",
            record_id="record_123",
            record_name="example.com",
            record_type="CNAME",
            live_content="live.example.net",
            maintenance_content="maintenance.example.net",
            proxied=True,
        ),
    )


def test_create_hosting_subscription_creates_template_and_target(db):
    customer = _mk_customer(db)
    item = _mk_item(db)

    template = create_hosting_subscription(db, _mk_payload(customer, item))
    db.flush()

    assert template.is_template is True
    assert template.is_hosting is True
    assert template.hosting_status == "ACTIVE"
    assert template.hosting_domain == "example.com"
    assert template.hosting_service_name == "Primary hosting"
    assert template.hosting_grace_days == 3
    assert template.hosting_suspension_enabled is True
    assert template.invoice_type == "RECURRING"
    assert template.project_id is None
    assert template.billing_cycle_ref["frequency"] == "MONTHLY"
    assert template.billing_cycle_ref["interval"] == 3
    assert template.billing_cycle_ref["start_date"] == "2026-06-01"
    assert template.line_items[0].item_id == item.item_id
    assert "example.com" in template.line_items[0].description
    assert template.cloudflare_target is not None
    assert template.cloudflare_target.record_name == "example.com"
    assert template.cloudflare_target.provider_status == "ACTIVE"


def test_trigger_recurring_cycle_stamps_coverage(db):
    customer = _mk_customer(db)
    item = _mk_item(db)
    template = create_hosting_subscription(db, _mk_payload(customer, item))
    db.flush()

    invoice = invoicing.trigger_recurring_cycle(
        db,
        template_invoice_id=template.invoice_id,
        cycle_key="2026-06-01",
    )
    db.flush()

    assert invoice.coverage_start == date(2026, 6, 1)
    assert invoice.coverage_end == date(2026, 8, 31)
    assert invoice.project_id == template.project_id
    assert "Coverage: 2026-06-01 to 2026-08-31" in invoice.line_items[0].description
    assert invoice.billing_cycle_ref["template_invoice_id"] == str(template.invoice_id)

    assert latest_child_invoice(db, template).invoice_id == invoice.invoice_id


def test_hosting_enforcement_suspends_and_reconciliation_restore_reactivates(
    db, monkeypatch
):
    customer = _mk_customer(db)
    item = _mk_item(db)
    template = create_hosting_subscription(db, _mk_payload(customer, item))
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

    db.refresh(template)
    db.refresh(invoice)
    assert template.hosting_status == "SUSPENDED"
    assert template.cloudflare_target.provider_status == "SUSPENDED"
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
    ensure_subscription_restored(db, invoice)
    db.flush()

    db.refresh(payment)
    db.refresh(invoice)
    db.refresh(template)
    assert payment.status == "CLEARED"
    assert invoice.status == "PAID"
    assert template.hosting_status == "ACTIVE"
    assert template.cloudflare_target.provider_status == "ACTIVE"
    assert calls == [("suspend", "record_123"), ("restore", "record_123")]


def test_enforcement_suspend_failure_marks_provider_error(db, monkeypatch):
    customer = _mk_customer(db)
    item = _mk_item(db)
    template = create_hosting_subscription(db, _mk_payload(customer, item))
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

    db.refresh(template)
    assert template.hosting_status == "SUSPEND_PENDING"
    assert template.cloudflare_target.provider_status == "ERROR"
    assert template.cloudflare_target.last_error == "cloudflare 500"
    assert template.cloudflare_target.last_action_at is not None


def test_enforcement_is_idempotent_for_already_suspended_subscription(db, monkeypatch):
    customer = _mk_customer(db)
    item = _mk_item(db)
    template = create_hosting_subscription(db, _mk_payload(customer, item))
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
    db.refresh(template)
    first_action_at = template.cloudflare_target.last_action_at

    second = run_hosting_enforcement(db, today=date(2026, 6, 13))
    assert second == {"suspended": 0, "restored": 0, "errors": 0}
    assert calls == ["record_123"]

    db.refresh(template)
    assert template.hosting_status == "SUSPENDED"
    assert template.cloudflare_target.provider_status == "SUSPENDED"
    assert template.cloudflare_target.last_action_at == first_action_at


def test_enforcement_ignores_non_overdue_invoice(db, monkeypatch):
    customer = _mk_customer(db)
    item = _mk_item(db)
    template = create_hosting_subscription(db, _mk_payload(customer, item))
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

    db.refresh(template)
    assert template.hosting_status == "ACTIVE"
    # Template still exists and is still flagged as hosting.
    reloaded = db.get(Invoice, template.invoice_id)
    assert reloaded is not None
    assert reloaded.is_hosting is True
