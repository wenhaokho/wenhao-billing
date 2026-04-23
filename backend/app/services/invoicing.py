"""Invoice lifecycle: milestone / recurring / usage.

All generation produces DRAFT invoices that land in the "Awaiting Finalization"
queue (PRD §5). Only `finalize_invoice` moves DRAFT -> SENT.
"""

from __future__ import annotations

import re
from datetime import date, timedelta
from decimal import ROUND_HALF_UP, Decimal
from typing import Any
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models.invoice import Invoice
from app.models.invoice_line_item import InvoiceLineItem
from app.schemas.invoice import (
    InvoiceCreate,
    InvoiceLineItemIn,
    InvoiceUpdate,
    RecurringTemplateCreate,
)
from app.services.recurring_schedule import ScheduleError, parse_schedule


class InvoicingError(RuntimeError):
    pass


Q4 = Decimal("0.0001")


def _round4(v: Decimal) -> Decimal:
    return v.quantize(Q4, rounding=ROUND_HALF_UP)


def _compute_totals(
    lines: list[InvoiceLineItemIn],
    discount_type: str | None,
    discount_value: Decimal | None,
) -> tuple[Decimal, Decimal]:
    subtotal = Decimal("0")
    for ln in lines:
        subtotal += Decimal(ln.quantity) * Decimal(ln.unit_price)
    subtotal = _round4(subtotal)

    if discount_type == "PERCENT" and discount_value is not None:
        pct = Decimal(discount_value) / Decimal("100")
        amount = _round4(subtotal * (Decimal("1") - pct))
    elif discount_type == "AMOUNT" and discount_value is not None:
        amount = _round4(subtotal - Decimal(discount_value))
    else:
        amount = subtotal

    if amount < 0:
        amount = Decimal("0")
    return subtotal, amount


def _parse_payment_terms_days(terms: str | None) -> int:
    if not terms:
        return 14
    t = terms.strip()
    if t.lower() in ("on receipt", "on-receipt", "due on receipt"):
        return 0
    m = re.match(r"^\s*net\s+(\d+)\s*$", t, flags=re.IGNORECASE)
    if m:
        return int(m.group(1))
    return 14


def generate_invoice_number(db: Session) -> str:
    year = date.today().year
    prefix = f"WH{year}"
    pattern = f"{prefix}%"
    rows = db.scalars(
        select(Invoice.invoice_number).where(Invoice.invoice_number.like(pattern))
    ).all()
    max_n = 0
    for n in rows:
        if n is None:
            continue
        tail = n[len(prefix):]
        if tail.isdigit():
            max_n = max(max_n, int(tail))
    return f"{prefix}{max_n + 1:04d}"


def _build_line_items(inputs: list[InvoiceLineItemIn]) -> list[InvoiceLineItem]:
    out: list[InvoiceLineItem] = []
    for idx, ln in enumerate(inputs):
        qty = Decimal(ln.quantity)
        price = Decimal(ln.unit_price)
        out.append(
            InvoiceLineItem(
                item_id=ln.item_id,
                position=ln.position if ln.position is not None else idx,
                description=ln.description,
                quantity=qty,
                unit_price=price,
                amount=_round4(qty * price),
            )
        )
    return out


def create_milestone_invoice(
    db: Session,
    *,
    customer_id: UUID,
    currency: str,
    amount: Decimal,
    milestone_ref: str,
    issue_date: date | None = None,
    due_in_days: int = 14,
    project_id: UUID | None = None,
) -> Invoice:
    today = issue_date or date.today()
    invoice = Invoice(
        customer_id=customer_id,
        project_id=project_id,
        invoice_type="MILESTONE",
        currency=currency,
        amount=amount,
        balance_due=amount,
        status="DRAFT",
        billing_cycle_ref={"milestone_ref": milestone_ref},
        issue_date=today,
        due_date=today + timedelta(days=due_in_days),
    )
    db.add(invoice)
    db.flush()
    return invoice


def create_invoice(db: Session, payload: InvoiceCreate) -> Invoice:
    if not payload.line_items:
        raise InvoicingError("invoice must have at least one line item")

    subtotal, amount = _compute_totals(
        payload.line_items, payload.discount_type, payload.discount_value
    )

    invoice_number = payload.invoice_number or generate_invoice_number(db)
    today = payload.issue_date or date.today()
    due = payload.due_date
    if due is None:
        due = today + timedelta(days=_parse_payment_terms_days(payload.payment_terms))

    invoice = Invoice(
        customer_id=payload.customer_id,
        project_id=payload.project_id,
        invoice_type=payload.invoice_type or "MILESTONE",
        invoice_number=invoice_number,
        po_so_number=payload.po_so_number,
        currency=payload.currency,
        subtotal=subtotal,
        discount_type=payload.discount_type,
        discount_value=payload.discount_value,
        amount=amount,
        balance_due=amount,
        status="DRAFT",
        issue_date=today,
        due_date=due,
        payment_terms=payload.payment_terms,
        notes=payload.notes,
        footer=payload.footer,
        is_template=False,
    )
    invoice.line_items = _build_line_items(payload.line_items)
    db.add(invoice)
    db.flush()
    return invoice


def update_invoice(db: Session, invoice_id: UUID, payload: InvoiceUpdate) -> Invoice:
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise InvoicingError(f"invoice {invoice_id} not found")
    if invoice.status != "DRAFT":
        raise InvoicingError(f"only DRAFT invoices can be edited (got {invoice.status})")

    data = payload.model_dump(exclude_unset=True)
    new_lines = data.pop("line_items", None)

    for field in (
        "customer_id",
        "project_id",
        "invoice_type",
        "currency",
        "invoice_number",
        "po_so_number",
        "issue_date",
        "due_date",
        "payment_terms",
        "notes",
        "footer",
        "discount_type",
        "discount_value",
    ):
        if field in data:
            setattr(invoice, field, data[field])

    if new_lines is not None:
        for existing_line in list(invoice.line_items):
            invoice.line_items.remove(existing_line)
        db.flush()
        for built in _build_line_items([InvoiceLineItemIn(**ln) for ln in new_lines]):
            invoice.line_items.append(built)

    # recompute totals from current lines
    current_inputs = [
        InvoiceLineItemIn(
            item_id=ln.item_id,
            description=ln.description,
            quantity=ln.quantity,
            unit_price=ln.unit_price,
            position=ln.position,
        )
        for ln in invoice.line_items
    ]
    subtotal, amount = _compute_totals(
        current_inputs, invoice.discount_type, invoice.discount_value
    )
    invoice.subtotal = subtotal
    invoice.amount = amount
    invoice.balance_due = amount

    if invoice.due_date is None and invoice.issue_date is not None:
        invoice.due_date = invoice.issue_date + timedelta(
            days=_parse_payment_terms_days(invoice.payment_terms)
        )

    db.flush()
    return invoice


def _schedule_to_json(payload: RecurringTemplateCreate) -> dict[str, Any]:
    """Pydantic schedule → JSON-safe dict, validated through the pure module
    so malformed combos fail at save time, not at 02:00 in the beat loop."""
    out: dict[str, Any] = {
        "frequency": payload.schedule.frequency,
        "interval": payload.schedule.interval,
        "start_date": payload.schedule.start_date.isoformat(),
        "end_mode": payload.schedule.end_mode,
    }
    if payload.schedule.end_date is not None:
        out["end_date"] = payload.schedule.end_date.isoformat()
    if payload.schedule.end_after_cycles is not None:
        out["end_after_cycles"] = payload.schedule.end_after_cycles
    try:
        parse_schedule(out)
    except ScheduleError as e:
        raise InvoicingError(f"invalid schedule: {e}") from e
    return out


def create_recurring_template(db: Session, payload: RecurringTemplateCreate) -> Invoice:
    if not payload.line_items:
        raise InvoicingError("recurring template must have at least one line item")

    schedule_dict = _schedule_to_json(payload)

    subtotal, amount = _compute_totals(
        payload.line_items, payload.discount_type, payload.discount_value
    )
    invoice = Invoice(
        customer_id=payload.customer_id,
        invoice_type="RECURRING",
        invoice_number=None,
        po_so_number=payload.po_so_number,
        currency=payload.currency,
        subtotal=subtotal,
        discount_type=payload.discount_type,
        discount_value=payload.discount_value,
        amount=amount,
        balance_due=amount,
        status="DRAFT",
        billing_cycle_ref=schedule_dict,
        issue_date=None,
        due_date=None,
        payment_terms=payload.payment_terms,
        notes=payload.notes,
        footer=payload.footer,
        is_template=True,
    )
    invoice.line_items = _build_line_items(payload.line_items)
    db.add(invoice)
    db.flush()
    return invoice


def update_recurring_template(
    db: Session, template_id: UUID, payload: RecurringTemplateCreate
) -> Invoice:
    template = db.get(Invoice, template_id)
    if template is None or not template.is_template:
        raise InvoicingError(f"recurring template {template_id} not found")
    if not payload.line_items:
        raise InvoicingError("recurring template must have at least one line item")

    schedule_dict = _schedule_to_json(payload)
    # Preserve pause flag across edits — user's pause state shouldn't be lost
    # just because they updated an unrelated field.
    if (template.billing_cycle_ref or {}).get("paused"):
        schedule_dict["paused"] = True

    subtotal, amount = _compute_totals(
        payload.line_items, payload.discount_type, payload.discount_value
    )
    template.customer_id = payload.customer_id
    template.currency = payload.currency
    template.po_so_number = payload.po_so_number
    template.payment_terms = payload.payment_terms
    template.notes = payload.notes
    template.footer = payload.footer
    template.discount_type = payload.discount_type
    template.discount_value = payload.discount_value
    template.subtotal = subtotal
    template.amount = amount
    template.balance_due = amount
    template.billing_cycle_ref = schedule_dict

    # Replace line items wholesale — templates aren't referenced by ledger.
    for existing in list(template.line_items):
        db.delete(existing)
    db.flush()
    template.line_items = _build_line_items(payload.line_items)
    db.flush()
    return template


def delete_invoice(db: Session, invoice_id: UUID) -> None:
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise InvoicingError(f"invoice {invoice_id} not found")
    if invoice.status != "DRAFT":
        raise InvoicingError(f"only DRAFT invoices can be deleted (got {invoice.status})")
    db.delete(invoice)
    db.flush()


def trigger_recurring_cycle(
    db: Session,
    *,
    template_invoice_id: UUID,
    cycle_key: str,
) -> Invoice:
    """Idempotent: refuses to create a second invoice for the same cycle_key+template."""
    template = db.get(Invoice, template_invoice_id)
    if template is None:
        raise InvoicingError(f"template invoice {template_invoice_id} not found")
    if template.invoice_type != "RECURRING":
        raise InvoicingError(f"invoice {template_invoice_id} is not RECURRING")

    existing = db.scalar(
        select(Invoice)
        .where(Invoice.customer_id == template.customer_id)
        .where(Invoice.invoice_type == "RECURRING")
        .where(Invoice.is_template.is_(False))
        .where(Invoice.billing_cycle_ref["cycle_key"].astext == cycle_key)
    )
    if existing is not None:
        return existing

    today = date.today()
    due_days = _parse_payment_terms_days(template.payment_terms)
    invoice = Invoice(
        customer_id=template.customer_id,
        invoice_type="RECURRING",
        invoice_number=generate_invoice_number(db),
        po_so_number=template.po_so_number,
        currency=template.currency,
        subtotal=template.subtotal,
        discount_type=template.discount_type,
        discount_value=template.discount_value,
        amount=template.amount,
        balance_due=template.amount,
        status="DRAFT",
        billing_cycle_ref={
            "template_invoice_id": str(template.invoice_id),
            "cycle_key": cycle_key,
        },
        issue_date=today,
        due_date=today + timedelta(days=due_days),
        payment_terms=template.payment_terms,
        notes=template.notes,
        footer=template.footer,
        is_template=False,
    )
    invoice.line_items = [
        InvoiceLineItem(
            item_id=ln.item_id,
            position=ln.position,
            description=ln.description,
            quantity=ln.quantity,
            unit_price=ln.unit_price,
            amount=ln.amount,
        )
        for ln in template.line_items
    ]
    db.add(invoice)
    db.flush()
    return invoice


def lock_usage_invoice(
    db: Session,
    *,
    invoice_id: UUID,
    accrued_amount: Decimal,
    cutoff_date: date,
) -> Invoice:
    """Usage Lock: freeze accrued usage, generate billable amount.

    Caller has already summed accrued usage for the period. This flips the
    invoice from an open accrual to a billable DRAFT with a fixed amount.
    """
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise InvoicingError(f"invoice {invoice_id} not found")
    if invoice.invoice_type != "USAGE":
        raise InvoicingError(f"invoice {invoice_id} is not USAGE")
    if invoice.status != "DRAFT":
        raise InvoicingError(f"usage invoice already locked (status={invoice.status})")

    invoice.amount = accrued_amount
    invoice.balance_due = accrued_amount
    billing_ref: dict[str, Any] = dict(invoice.billing_cycle_ref or {})
    billing_ref["cutoff_date"] = cutoff_date.isoformat()
    billing_ref["locked"] = True
    invoice.billing_cycle_ref = billing_ref
    db.flush()
    return invoice


def finalize_invoice(db: Session, invoice_id: UUID) -> Invoice:
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise InvoicingError(f"invoice {invoice_id} not found")
    if invoice.status != "DRAFT":
        raise InvoicingError(f"only DRAFT invoices can be finalized (got {invoice.status})")
    if invoice.is_template:
        raise InvoicingError("cannot finalize a recurring template")
    invoice.status = "SENT"
    db.flush()
    return invoice


def void_invoice(db: Session, invoice_id: UUID) -> Invoice:
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise InvoicingError(f"invoice {invoice_id} not found")
    if invoice.status == "PAID":
        raise InvoicingError("cannot void a PAID invoice — reverse payments first")
    invoice.status = "VOID"
    invoice.balance_due = Decimal("0")
    db.flush()
    return invoice
