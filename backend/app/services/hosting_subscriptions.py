from __future__ import annotations

from calendar import monthrange
from datetime import UTC, date, datetime, timedelta
from decimal import Decimal
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models.cloudflare_target import CloudflareTarget
from app.models.customer import Customer
from app.models.hosting_subscription import HostingSubscription
from app.models.invoice import Invoice
from app.models.item import Item
from app.schemas.hosting_subscription import HostingSubscriptionCreate, HostingSubscriptionUpdate
from app.schemas.invoice import InvoiceLineItemIn, RecurringSchedule, RecurringTemplateCreate
from app.services import cloudflare

ACTIVE_SUBSCRIPTION_STATUSES = {"ACTIVE", "SUSPEND_PENDING", "SUSPENDED", "CANCELLED"}
OPEN_INVOICE_STATUSES = ("SENT", "PARTIAL")


class HostingSubscriptionError(RuntimeError):
    pass


def _utcnow() -> datetime:
    return datetime.now(UTC).replace(tzinfo=None, microsecond=0)


def _add_months(anchor: date, months: int) -> date:
    total = anchor.month - 1 + months
    year = anchor.year + total // 12
    month = total % 12 + 1
    day = min(anchor.day, monthrange(year, month)[1])
    return date(year, month, day)


def compute_coverage_window(cycle_start: date, bundle_months: int) -> tuple[date, date]:
    cycle_end = _add_months(cycle_start, bundle_months) - timedelta(days=1)
    return cycle_start, cycle_end


def _require_customer(db: Session, customer_id: UUID) -> Customer:
    customer = db.get(Customer, customer_id)
    if customer is None:
        raise HostingSubscriptionError(f"customer {customer_id} not found")
    return customer


def _require_item(db: Session, item_id: UUID | None) -> Item:
    if item_id is None:
        raise HostingSubscriptionError("item_id is required")
    item = db.get(Item, item_id)
    if item is None:
        raise HostingSubscriptionError(f"item {item_id} not found")
    if item.default_unit_price is None:
        raise HostingSubscriptionError("selected item must have a default unit price")
    return item


def _normalize_status(status: str) -> str:
    normalized = status.strip().upper()
    if normalized not in ACTIVE_SUBSCRIPTION_STATUSES:
        raise HostingSubscriptionError(f"invalid subscription status: {status}")
    return normalized


def _line_item_description(subscription: HostingSubscription, item: Item) -> str:
    label = subscription.service_name.strip() or item.name.strip()
    return f"{label} - {subscription.domain_name.strip()}"


def _template_payload(subscription: HostingSubscription, item: Item) -> RecurringTemplateCreate:
    item_currency = (item.default_currency or "").upper()
    if item_currency and item_currency != subscription.currency:
        raise HostingSubscriptionError(
            f"item currency {item_currency} does not match subscription currency {subscription.currency}"
        )
    return RecurringTemplateCreate(
        customer_id=subscription.customer_id,
        project_id=subscription.project_id,
        currency=subscription.currency,
        payment_terms=subscription.payment_terms,
        notes=f"Prepaid hosting bundle for {subscription.domain_name}",
        line_items=[
            InvoiceLineItemIn(
                item_id=item.item_id,
                description=_line_item_description(subscription, item),
                quantity=Decimal("1.0000"),
                unit_price=Decimal(item.default_unit_price),
                position=0,
            )
        ],
        schedule=RecurringSchedule(
            frequency="MONTHLY",
            interval=subscription.bundle_months,
            start_date=subscription.billing_anchor_date,
            end_mode="NEVER",
        ),
    )


def _sync_template(db: Session, subscription: HostingSubscription) -> None:
    from app.services import invoicing

    item = _require_item(db, subscription.item_id)
    payload = _template_payload(subscription, item)
    if subscription.template_invoice_id is None:
        template = invoicing.create_recurring_template(db, payload)
        subscription.template_invoice_id = template.invoice_id
    else:
        template = invoicing.update_recurring_template(
            db, subscription.template_invoice_id, payload
        )
    template.project_id = subscription.project_id


def create_hosting_subscription(
    db: Session, payload: HostingSubscriptionCreate
) -> HostingSubscription:
    _require_customer(db, payload.customer_id)
    _require_item(db, payload.item_id)

    subscription = HostingSubscription(
        customer_id=payload.customer_id,
        project_id=payload.project_id,
        item_id=payload.item_id,
        service_name=payload.service_name.strip(),
        domain_name=payload.domain_name.strip().lower(),
        currency=payload.currency.upper(),
        bundle_months=payload.bundle_months,
        payment_terms=payload.payment_terms.strip(),
        billing_anchor_date=payload.billing_anchor_date,
        grace_days=payload.grace_days,
        suspension_enabled=payload.suspension_enabled,
        status="ACTIVE",
    )
    subscription.cloudflare_target = CloudflareTarget(
        zone_id=payload.cloudflare_target.zone_id.strip(),
        record_id=payload.cloudflare_target.record_id.strip(),
        record_name=payload.cloudflare_target.record_name.strip().lower(),
        record_type=payload.cloudflare_target.record_type.strip().upper(),
        live_content=payload.cloudflare_target.live_content.strip(),
        maintenance_content=payload.cloudflare_target.maintenance_content.strip(),
        proxied=payload.cloudflare_target.proxied,
    )
    db.add(subscription)
    db.flush()
    _sync_template(db, subscription)
    db.flush()
    return subscription


def update_hosting_subscription(
    db: Session, subscription_id: UUID, payload: HostingSubscriptionUpdate
) -> HostingSubscription:
    subscription = db.get(HostingSubscription, subscription_id)
    if subscription is None:
        raise HostingSubscriptionError(f"subscription {subscription_id} not found")

    data = payload.model_dump(exclude_unset=True)
    target_data = data.pop("cloudflare_target", None)

    if "customer_id" in data:
        _require_customer(db, data["customer_id"])
    if "item_id" in data:
        _require_item(db, data["item_id"])
    if "status" in data and data["status"] is not None:
        data["status"] = _normalize_status(data["status"])
    if "currency" in data and data["currency"] is not None:
        data["currency"] = data["currency"].upper()
    if "service_name" in data and data["service_name"] is not None:
        data["service_name"] = data["service_name"].strip()
    if "domain_name" in data and data["domain_name"] is not None:
        data["domain_name"] = data["domain_name"].strip().lower()
    if "payment_terms" in data and data["payment_terms"] is not None:
        data["payment_terms"] = data["payment_terms"].strip()

    for field, value in data.items():
        setattr(subscription, field, value)

    if target_data is not None:
        target = subscription.cloudflare_target
        if target is None:
            target = CloudflareTarget(subscription_id=subscription.subscription_id)
            subscription.cloudflare_target = target
        for field, value in target_data.items():
            if isinstance(value, str):
                value = value.strip()
                if field == "record_name":
                    value = value.lower()
                if field == "record_type":
                    value = value.upper()
            setattr(target, field, value)

    _sync_template(db, subscription)
    db.flush()
    return subscription


def apply_generated_invoice_subscription_fields(
    db: Session,
    *,
    template: Invoice,
    invoice: Invoice,
    cycle_key: str,
) -> None:
    subscription = db.scalar(
        select(HostingSubscription).where(
            HostingSubscription.template_invoice_id == template.invoice_id
        )
    )
    if subscription is None:
        return

    cycle_start = date.fromisoformat(cycle_key)
    coverage_start, coverage_end = compute_coverage_window(
        cycle_start, subscription.bundle_months
    )
    invoice.subscription_id = subscription.subscription_id
    invoice.project_id = subscription.project_id
    invoice.coverage_start = coverage_start
    invoice.coverage_end = coverage_end
    billing_ref = dict(invoice.billing_cycle_ref or {})
    billing_ref["subscription_id"] = str(subscription.subscription_id)
    invoice.billing_cycle_ref = billing_ref

    coverage_label = f"Coverage: {coverage_start.isoformat()} to {coverage_end.isoformat()}"
    for line in invoice.line_items:
        if coverage_label not in line.description:
            line.description = f"{line.description}\n{coverage_label}"

    subscription.last_invoice_id = invoice.invoice_id


def resolve_overdue_invoice(
    db: Session, subscription: HostingSubscription, today: date
) -> Invoice | None:
    rows = list(
        db.scalars(
            select(Invoice)
            .where(Invoice.subscription_id == subscription.subscription_id)
            .where(Invoice.status.in_(OPEN_INVOICE_STATUSES))
            .where(Invoice.balance_due > 0)
            .order_by(Invoice.due_date.asc().nullslast(), Invoice.created_at.asc())
        )
    )
    for invoice in rows:
        if invoice.due_date is None:
            continue
        if invoice.due_date + timedelta(days=subscription.grace_days) < today:
            return invoice
    return None


def _has_open_subscription_balance(db: Session, subscription_id: UUID) -> bool:
    return db.scalar(
        select(Invoice.invoice_id)
        .where(Invoice.subscription_id == subscription_id)
        .where(Invoice.status.in_(OPEN_INVOICE_STATUSES))
        .where(Invoice.balance_due > 0)
        .limit(1)
    ) is not None


def suspend_subscription(
    db: Session, subscription: HostingSubscription, invoice: Invoice
) -> bool:
    if not subscription.suspension_enabled or subscription.status == "CANCELLED":
        return False
    target = subscription.cloudflare_target
    if target is None:
        raise HostingSubscriptionError("subscription is missing a Cloudflare target")
    if subscription.status == "SUSPENDED" and target.provider_status == "SUSPENDED":
        return False

    subscription.status = "SUSPEND_PENDING"
    target.last_error = None
    db.flush()
    try:
        cloudflare.suspend_target(target)
    except Exception as exc:
        target.provider_status = "ERROR"
        target.last_error = str(exc)
        target.last_action_at = _utcnow()
        db.flush()
        raise

    now = _utcnow()
    subscription.status = "SUSPENDED"
    subscription.last_invoice_id = invoice.invoice_id
    target.provider_status = "SUSPENDED"
    target.last_action_at = now
    target.last_error = None
    db.flush()
    return True


def restore_subscription_if_eligible(
    db: Session, subscription: HostingSubscription
) -> bool:
    target = subscription.cloudflare_target
    if target is None:
        return False
    if _has_open_subscription_balance(db, subscription.subscription_id):
        return False
    if subscription.status == "ACTIVE" and target.provider_status == "ACTIVE":
        return False

    try:
        cloudflare.restore_target(target)
    except Exception as exc:
        target.provider_status = "ERROR"
        target.last_error = str(exc)
        target.last_action_at = _utcnow()
        db.flush()
        raise

    now = _utcnow()
    subscription.status = "ACTIVE"
    target.provider_status = "ACTIVE"
    target.last_action_at = now
    target.last_error = None
    db.flush()
    return True


def ensure_subscription_restored(db: Session, invoice: Invoice) -> bool:
    if invoice.subscription_id is None:
        return False
    subscription = db.get(HostingSubscription, invoice.subscription_id)
    if subscription is None:
        return False
    if Decimal(invoice.balance_due) > Decimal("0"):
        return False
    subscription.last_paid_at = _utcnow()
    db.flush()
    return restore_subscription_if_eligible(db, subscription)
