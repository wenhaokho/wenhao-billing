"""Hosting suspension lives on Item: a hosting item carries domain + DNS +
operational state. The recurring invoice template drives billing; the daily
worker scans hosting items for overdue invoices and flips the Cloudflare
record. Reconciliation restores when the overdue invoice clears."""

from __future__ import annotations

from calendar import monthrange
from datetime import UTC, date, datetime, timedelta
from decimal import Decimal
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models.cloudflare_target import CloudflareTarget
from app.models.invoice import Invoice
from app.models.invoice_line_item import InvoiceLineItem
from app.models.item import Item
from app.services import cloudflare

HOSTING_STATUSES = {"ACTIVE", "SUSPEND_PENDING", "SUSPENDED", "CANCELLED"}
OPEN_INVOICE_STATUSES = ("SENT", "PARTIAL")


class HostingError(RuntimeError):
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


def normalize_hosting_status(status: str) -> str:
    normalized = status.strip().upper()
    if normalized not in HOSTING_STATUSES:
        raise HostingError(f"invalid hosting status: {status}")
    return normalized


def hosting_items_in_invoice(invoice: Invoice) -> list[Item]:
    return [
        ln.item for ln in invoice.line_items
        if ln.item is not None and ln.item.is_hosting
    ]


def apply_generated_invoice_hosting_fields(
    db: Session,
    *,
    template: Invoice,
    invoice: Invoice,
    cycle_key: str,
) -> None:
    """When a recurring template generates a child invoice, stamp coverage
    window onto the child if any of the line items is a hosting item."""
    hosting_lines = hosting_items_in_invoice(invoice)
    if not hosting_lines:
        return

    bundle_months = (template.billing_cycle_ref or {}).get("interval") or 1
    cycle_start = date.fromisoformat(cycle_key)
    coverage_start, coverage_end = compute_coverage_window(cycle_start, bundle_months)
    invoice.coverage_start = coverage_start
    invoice.coverage_end = coverage_end

    coverage_label = f"Coverage: {coverage_start.isoformat()} to {coverage_end.isoformat()}"
    for line in invoice.line_items:
        if coverage_label not in line.description:
            line.description = f"{line.description}\n{coverage_label}"


def _open_invoices_for_item_stmt(item: Item):
    return (
        select(Invoice)
        .join(InvoiceLineItem, InvoiceLineItem.invoice_id == Invoice.invoice_id)
        .where(InvoiceLineItem.item_id == item.item_id)
        .where(Invoice.is_template.is_(False))
        .where(Invoice.status.in_(OPEN_INVOICE_STATUSES))
        .where(Invoice.balance_due > 0)
    )


def resolve_overdue_invoice(
    db: Session, item: Item, today: date
) -> Invoice | None:
    grace_days = item.hosting_grace_days or 0
    rows = list(
        db.scalars(
            _open_invoices_for_item_stmt(item).order_by(
                Invoice.due_date.asc().nullslast(), Invoice.created_at.asc()
            )
        )
    )
    for invoice in rows:
        if invoice.due_date is None:
            continue
        if invoice.due_date + timedelta(days=grace_days) < today:
            return invoice
    return None


def _has_open_balance(db: Session, item: Item) -> bool:
    return db.scalar(
        _open_invoices_for_item_stmt(item)
        .with_only_columns(Invoice.invoice_id)
        .limit(1)
    ) is not None


def suspend_hosting(db: Session, item: Item, invoice: Invoice) -> bool:
    if not item.hosting_suspension_enabled or item.hosting_status == "CANCELLED":
        return False
    target = item.cloudflare_target
    if target is None:
        raise HostingError("hosting item is missing a Cloudflare target")
    if item.hosting_status == "SUSPENDED" and target.provider_status == "SUSPENDED":
        return False

    item.hosting_status = "SUSPEND_PENDING"
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
    item.hosting_status = "SUSPENDED"
    target.provider_status = "SUSPENDED"
    target.last_action_at = now
    target.last_error = None
    db.flush()
    return True


def restore_hosting_if_eligible(db: Session, item: Item) -> bool:
    target = item.cloudflare_target
    if target is None:
        return False
    if _has_open_balance(db, item):
        return False
    if item.hosting_status == "ACTIVE" and target.provider_status == "ACTIVE":
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
    item.hosting_status = "ACTIVE"
    target.provider_status = "ACTIVE"
    target.last_action_at = now
    target.last_error = None
    db.flush()
    return True


def ensure_hosting_restored(db: Session, invoice: Invoice) -> bool:
    """Called from reconciliation when an invoice clears. Walks the invoice's
    hosting line items and restores any that are eligible (no other open
    invoices on the same hosting item)."""
    if Decimal(invoice.balance_due) > Decimal("0"):
        return False
    restored_any = False
    for item in hosting_items_in_invoice(invoice):
        item.hosting_last_paid_at = _utcnow()
        if restore_hosting_if_eligible(db, item):
            restored_any = True
    db.flush()
    return restored_any


# Back-compat shim — reconciliation/invoices router still call this name.
ensure_subscription_restored = ensure_hosting_restored
