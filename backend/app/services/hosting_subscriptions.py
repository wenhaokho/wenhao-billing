from __future__ import annotations

from calendar import monthrange
from datetime import UTC, date, datetime, timedelta
from decimal import Decimal
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models.cloudflare_target import CloudflareTarget
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.item import Item
from app.schemas.hosting_subscription import HostingSubscriptionCreate, HostingSubscriptionUpdate
from app.schemas.invoice import InvoiceLineItemIn, RecurringSchedule, RecurringTemplateCreate
from app.services import cloudflare

HOSTING_STATUSES = {"ACTIVE", "SUSPEND_PENDING", "SUSPENDED", "CANCELLED"}
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
    if normalized not in HOSTING_STATUSES:
        raise HostingSubscriptionError(f"invalid subscription status: {status}")
    return normalized


def _require_hosting_template(db: Session, template_invoice_id: UUID) -> Invoice:
    template = db.get(Invoice, template_invoice_id)
    if template is None or not template.is_template or not template.is_hosting:
        raise HostingSubscriptionError(
            f"hosting subscription {template_invoice_id} not found"
        )
    return template


def _line_item_description(service_name: str, domain_name: str, item: Item) -> str:
    label = service_name.strip() or item.name.strip()
    return f"{label} - {domain_name.strip()}"


def _template_payload(
    *,
    customer_id: UUID,
    project_id: UUID | None,
    currency: str,
    payment_terms: str,
    bundle_months: int,
    billing_anchor_date: date,
    service_name: str,
    domain_name: str,
    item: Item,
) -> RecurringTemplateCreate:
    item_currency = (item.default_currency or "").upper()
    if item_currency and item_currency != currency:
        raise HostingSubscriptionError(
            f"item currency {item_currency} does not match subscription currency {currency}"
        )
    return RecurringTemplateCreate(
        customer_id=customer_id,
        project_id=project_id,
        currency=currency,
        payment_terms=payment_terms,
        notes=f"Prepaid hosting bundle for {domain_name}",
        line_items=[
            InvoiceLineItemIn(
                item_id=item.item_id,
                description=_line_item_description(service_name, domain_name, item),
                quantity=Decimal("1.0000"),
                unit_price=Decimal(item.default_unit_price),
                position=0,
            )
        ],
        schedule=RecurringSchedule(
            frequency="MONTHLY",
            interval=bundle_months,
            start_date=billing_anchor_date,
            end_mode="NEVER",
        ),
    )


def create_hosting_subscription(
    db: Session, payload: HostingSubscriptionCreate
) -> Invoice:
    from app.services import invoicing

    _require_customer(db, payload.customer_id)
    item = _require_item(db, payload.item_id)

    domain = payload.domain_name.strip().lower()
    template_payload = _template_payload(
        customer_id=payload.customer_id,
        project_id=payload.project_id,
        currency=payload.currency.upper(),
        payment_terms=payload.payment_terms.strip(),
        bundle_months=payload.bundle_months,
        billing_anchor_date=payload.billing_anchor_date,
        service_name=payload.service_name,
        domain_name=domain,
        item=item,
    )
    template = invoicing.create_recurring_template(db, template_payload)
    template.is_hosting = True
    template.hosting_service_name = payload.service_name.strip()
    template.hosting_domain = domain
    template.hosting_grace_days = payload.grace_days
    template.hosting_suspension_enabled = payload.suspension_enabled
    template.hosting_status = "ACTIVE"
    template.cloudflare_target = CloudflareTarget(
        zone_id=payload.cloudflare_target.zone_id.strip(),
        record_id=payload.cloudflare_target.record_id.strip(),
        record_name=payload.cloudflare_target.record_name.strip().lower(),
        record_type=payload.cloudflare_target.record_type.strip().upper(),
        live_content=payload.cloudflare_target.live_content.strip(),
        maintenance_content=payload.cloudflare_target.maintenance_content.strip(),
        proxied=payload.cloudflare_target.proxied,
    )
    db.flush()
    return template


def update_hosting_subscription(
    db: Session, template_invoice_id: UUID, payload: HostingSubscriptionUpdate
) -> Invoice:
    from app.services import invoicing

    template = _require_hosting_template(db, template_invoice_id)
    data = payload.model_dump(exclude_unset=True)
    target_data = data.pop("cloudflare_target", None)

    if "customer_id" in data:
        _require_customer(db, data["customer_id"])
    if "item_id" in data and data["item_id"] is not None:
        _require_item(db, data["item_id"])

    item = _require_item(db, data.get("item_id") or template.line_items[0].item_id)
    billing_ref = dict(template.billing_cycle_ref or {})

    customer_id = data.get("customer_id") or template.customer_id
    project_id = data.get("project_id", template.project_id)
    currency = (data.get("currency") or template.currency).upper()
    payment_terms = (data.get("payment_terms") or template.payment_terms or "").strip()
    bundle_months = data.get("bundle_months") or billing_ref.get("interval") or 1
    billing_anchor_date = data.get("billing_anchor_date") or date.fromisoformat(
        billing_ref.get("start_date")
    )
    service_name = data.get("service_name") or template.hosting_service_name or item.name
    domain = (data.get("domain_name") or template.hosting_domain or "").strip().lower()

    new_payload = _template_payload(
        customer_id=customer_id,
        project_id=project_id,
        currency=currency,
        payment_terms=payment_terms,
        bundle_months=bundle_months,
        billing_anchor_date=billing_anchor_date,
        service_name=service_name,
        domain_name=domain,
        item=item,
    )
    invoicing.update_recurring_template(db, template.invoice_id, new_payload)
    template.is_hosting = True
    template.hosting_service_name = service_name.strip()
    template.hosting_domain = domain
    if "grace_days" in data and data["grace_days"] is not None:
        template.hosting_grace_days = data["grace_days"]
    if "suspension_enabled" in data and data["suspension_enabled"] is not None:
        template.hosting_suspension_enabled = data["suspension_enabled"]
    if "status" in data and data["status"] is not None:
        template.hosting_status = _normalize_status(data["status"])

    if target_data is not None:
        target = template.cloudflare_target
        if target is None:
            target = CloudflareTarget(invoice_id=template.invoice_id)
            template.cloudflare_target = target
        for field, value in target_data.items():
            if isinstance(value, str):
                value = value.strip()
                if field == "record_name":
                    value = value.lower()
                if field == "record_type":
                    value = value.upper()
            setattr(target, field, value)

    db.flush()
    return template


def apply_generated_invoice_hosting_fields(
    db: Session,
    *,
    template: Invoice,
    invoice: Invoice,
    cycle_key: str,
) -> None:
    """Stamp coverage window onto a generated invoice when the template is hosting."""
    if not template.is_hosting:
        return

    bundle_months = (template.billing_cycle_ref or {}).get("interval") or 1
    cycle_start = date.fromisoformat(cycle_key)
    coverage_start, coverage_end = compute_coverage_window(cycle_start, bundle_months)
    invoice.coverage_start = coverage_start
    invoice.coverage_end = coverage_end
    invoice.project_id = template.project_id

    coverage_label = f"Coverage: {coverage_start.isoformat()} to {coverage_end.isoformat()}"
    for line in invoice.line_items:
        if coverage_label not in line.description:
            line.description = f"{line.description}\n{coverage_label}"


def _children_invoices_stmt(template: Invoice):
    return (
        select(Invoice)
        .where(Invoice.is_template.is_(False))
        .where(
            Invoice.billing_cycle_ref["template_invoice_id"].astext
            == str(template.invoice_id)
        )
    )


def latest_child_invoice(db: Session, template: Invoice) -> Invoice | None:
    return db.scalar(
        _children_invoices_stmt(template).order_by(Invoice.created_at.desc()).limit(1)
    )


def resolve_overdue_invoice(
    db: Session, template: Invoice, today: date
) -> Invoice | None:
    grace_days = template.hosting_grace_days or 0
    rows = list(
        db.scalars(
            _children_invoices_stmt(template)
            .where(Invoice.status.in_(OPEN_INVOICE_STATUSES))
            .where(Invoice.balance_due > 0)
            .order_by(Invoice.due_date.asc().nullslast(), Invoice.created_at.asc())
        )
    )
    for invoice in rows:
        if invoice.due_date is None:
            continue
        if invoice.due_date + timedelta(days=grace_days) < today:
            return invoice
    return None


def _has_open_subscription_balance(db: Session, template: Invoice) -> bool:
    return db.scalar(
        _children_invoices_stmt(template)
        .where(Invoice.status.in_(OPEN_INVOICE_STATUSES))
        .where(Invoice.balance_due > 0)
        .with_only_columns(Invoice.invoice_id)
        .limit(1)
    ) is not None


def suspend_subscription(
    db: Session, template: Invoice, invoice: Invoice
) -> bool:
    if not template.hosting_suspension_enabled or template.hosting_status == "CANCELLED":
        return False
    target = template.cloudflare_target
    if target is None:
        raise HostingSubscriptionError("subscription is missing a Cloudflare target")
    if template.hosting_status == "SUSPENDED" and target.provider_status == "SUSPENDED":
        return False

    template.hosting_status = "SUSPEND_PENDING"
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
    template.hosting_status = "SUSPENDED"
    target.provider_status = "SUSPENDED"
    target.last_action_at = now
    target.last_error = None
    db.flush()
    return True


def restore_subscription_if_eligible(db: Session, template: Invoice) -> bool:
    target = template.cloudflare_target
    if target is None:
        return False
    if _has_open_subscription_balance(db, template):
        return False
    if template.hosting_status == "ACTIVE" and target.provider_status == "ACTIVE":
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
    template.hosting_status = "ACTIVE"
    target.provider_status = "ACTIVE"
    target.last_action_at = now
    target.last_error = None
    db.flush()
    return True


def ensure_subscription_restored(db: Session, invoice: Invoice) -> bool:
    """Called from reconciliation when a child invoice clears. Walks back to
    its hosting template and restores if eligible."""
    ref = invoice.billing_cycle_ref or {}
    template_id = ref.get("template_invoice_id")
    if not template_id:
        return False
    template = db.get(Invoice, UUID(template_id))
    if template is None or not template.is_hosting:
        return False
    if Decimal(invoice.balance_due) > Decimal("0"):
        return False
    template.hosting_last_paid_at = _utcnow()
    db.flush()
    return restore_subscription_if_eligible(db, template)


def list_hosting_templates(
    db: Session,
    *,
    customer_id: UUID | None = None,
    status: str | None = None,
) -> list[Invoice]:
    stmt = (
        select(Invoice)
        .where(Invoice.is_hosting.is_(True))
        .where(Invoice.is_template.is_(True))
        .order_by(Invoice.created_at.desc())
    )
    if customer_id is not None:
        stmt = stmt.where(Invoice.customer_id == customer_id)
    if status is not None:
        stmt = stmt.where(Invoice.hosting_status == status.upper())
    return list(db.scalars(stmt))


def project_hosting_subscription(db: Session, template: Invoice) -> dict:
    """Build the API-facing HostingSubscriptionOut payload from a hosting template."""
    billing_ref = template.billing_cycle_ref or {}
    latest = latest_child_invoice(db, template)
    item_id = template.line_items[0].item_id if template.line_items else None
    return {
        "subscription_id": template.invoice_id,
        "template_invoice_id": template.invoice_id,
        "customer_id": template.customer_id,
        "project_id": template.project_id,
        "item_id": item_id,
        "service_name": template.hosting_service_name or "",
        "domain_name": template.hosting_domain or "",
        "currency": template.currency,
        "bundle_months": billing_ref.get("interval") or 1,
        "payment_terms": template.payment_terms or "",
        "billing_anchor_date": date.fromisoformat(billing_ref["start_date"]),
        "grace_days": template.hosting_grace_days or 0,
        "suspension_enabled": bool(template.hosting_suspension_enabled),
        "status": template.hosting_status or "ACTIVE",
        "last_invoice_id": latest.invoice_id if latest is not None else None,
        "last_paid_at": template.hosting_last_paid_at,
        "created_at": template.created_at,
        "cloudflare_target": template.cloudflare_target,
    }
