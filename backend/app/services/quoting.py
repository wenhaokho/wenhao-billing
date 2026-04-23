"""Quotation (estimate) lifecycle.

Status machine:
  DRAFT -> SENT -> ACCEPTED | DECLINED | EXPIRED
                -> INVOICED (terminal, once converted)
                -> VOID (manual kill)

Convert-to-invoice copies header + line items into a standard (MILESTONE) invoice,
advances this quote to INVOICED, and links both ways.
"""

from __future__ import annotations

from datetime import date, datetime, timedelta
from decimal import ROUND_HALF_UP, Decimal
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models.invoice import Invoice
from app.models.invoice_line_item import InvoiceLineItem
from app.models.quotation import Quotation
from app.models.quotation_line_item import QuotationLineItem
from app.schemas.quotation import (
    QuotationCreate,
    QuotationLineItemIn,
    QuotationUpdate,
)
from app.services.invoicing import (
    _parse_payment_terms_days,
    generate_invoice_number,
)


class QuotingError(RuntimeError):
    pass


Q4 = Decimal("0.0001")


def _round4(v: Decimal) -> Decimal:
    return v.quantize(Q4, rounding=ROUND_HALF_UP)


def _compute_totals(
    lines: list[QuotationLineItemIn],
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


def generate_quotation_number(db: Session) -> str:
    year = date.today().year
    prefix = f"Q{year}"
    pattern = f"{prefix}%"
    rows = db.scalars(
        select(Quotation.quotation_number).where(
            Quotation.quotation_number.like(pattern)
        )
    ).all()
    max_n = 0
    for n in rows:
        if n is None:
            continue
        tail = n[len(prefix):]
        if tail.isdigit():
            max_n = max(max_n, int(tail))
    return f"{prefix}{max_n + 1:04d}"


def _build_line_items(inputs: list[QuotationLineItemIn]) -> list[QuotationLineItem]:
    out: list[QuotationLineItem] = []
    for idx, ln in enumerate(inputs):
        qty = Decimal(ln.quantity)
        price = Decimal(ln.unit_price)
        out.append(
            QuotationLineItem(
                item_id=ln.item_id,
                position=ln.position if ln.position is not None else idx,
                description=ln.description,
                quantity=qty,
                unit_price=price,
                amount=_round4(qty * price),
            )
        )
    return out


def create_quotation(db: Session, payload: QuotationCreate) -> Quotation:
    if not payload.line_items:
        raise QuotingError("quotation must have at least one line item")

    subtotal, amount = _compute_totals(
        payload.line_items, payload.discount_type, payload.discount_value
    )

    number = payload.quotation_number or generate_quotation_number(db)
    today = payload.issue_date or date.today()
    valid = payload.valid_until or today + timedelta(days=30)

    q = Quotation(
        customer_id=payload.customer_id,
        project_id=payload.project_id,
        quotation_number=number,
        po_so_number=payload.po_so_number,
        currency=payload.currency,
        subtotal=subtotal,
        discount_type=payload.discount_type,
        discount_value=payload.discount_value,
        amount=amount,
        status="DRAFT",
        issue_date=today,
        valid_until=valid,
        payment_terms=payload.payment_terms,
        notes=payload.notes,
        footer=payload.footer,
    )
    q.line_items = _build_line_items(payload.line_items)
    db.add(q)
    db.flush()
    return q


def update_quotation(
    db: Session, quotation_id: UUID, payload: QuotationUpdate
) -> Quotation:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise QuotingError(f"quotation {quotation_id} not found")
    if q.status not in ("DRAFT", "SENT"):
        raise QuotingError(
            f"only DRAFT or SENT quotations can be edited (got {q.status})"
        )

    data = payload.model_dump(exclude_unset=True)
    new_lines = data.pop("line_items", None)

    for field in (
        "customer_id",
        "project_id",
        "currency",
        "quotation_number",
        "po_so_number",
        "issue_date",
        "valid_until",
        "payment_terms",
        "notes",
        "footer",
        "discount_type",
        "discount_value",
    ):
        if field in data:
            setattr(q, field, data[field])

    if new_lines is not None:
        for existing_line in list(q.line_items):
            q.line_items.remove(existing_line)
        db.flush()
        for built in _build_line_items(
            [QuotationLineItemIn(**ln) for ln in new_lines]
        ):
            q.line_items.append(built)

    current_inputs = [
        QuotationLineItemIn(
            item_id=ln.item_id,
            description=ln.description,
            quantity=ln.quantity,
            unit_price=ln.unit_price,
            position=ln.position,
        )
        for ln in q.line_items
    ]
    subtotal, amount = _compute_totals(
        current_inputs, q.discount_type, q.discount_value
    )
    q.subtotal = subtotal
    q.amount = amount

    db.flush()
    return q


def mark_sent(db: Session, quotation_id: UUID) -> Quotation:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise QuotingError(f"quotation {quotation_id} not found")
    if q.status not in ("DRAFT", "SENT"):
        raise QuotingError(f"cannot mark-sent from status {q.status}")
    q.status = "SENT"
    q.last_sent_at = datetime.utcnow()
    db.flush()
    return q


def accept_quotation(
    db: Session, quotation_id: UUID, *, accepted_by: str | None = None
) -> Quotation:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise QuotingError(f"quotation {quotation_id} not found")
    if q.status not in ("DRAFT", "SENT"):
        raise QuotingError(f"cannot accept from status {q.status}")
    q.status = "ACCEPTED"
    q.accepted_at = datetime.utcnow()
    q.accepted_by = accepted_by
    db.flush()
    return q


def decline_quotation(db: Session, quotation_id: UUID) -> Quotation:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise QuotingError(f"quotation {quotation_id} not found")
    if q.status not in ("DRAFT", "SENT"):
        raise QuotingError(f"cannot decline from status {q.status}")
    q.status = "DECLINED"
    db.flush()
    return q


def void_quotation(db: Session, quotation_id: UUID) -> Quotation:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise QuotingError(f"quotation {quotation_id} not found")
    if q.status == "INVOICED":
        raise QuotingError("cannot void an already-invoiced quotation")
    q.status = "VOID"
    db.flush()
    return q


def delete_quotation(db: Session, quotation_id: UUID) -> None:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise QuotingError(f"quotation {quotation_id} not found")
    if q.status != "DRAFT":
        raise QuotingError(
            f"only DRAFT quotations can be deleted (got {q.status})"
        )
    db.delete(q)
    db.flush()


def convert_to_invoice(
    db: Session,
    quotation_id: UUID,
    *,
    issue_date: date | None = None,
    due_in_days: int = 14,
) -> Invoice:
    """Copy the quotation into a new DRAFT invoice and mark the quote INVOICED."""
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise QuotingError(f"quotation {quotation_id} not found")
    if q.status in ("INVOICED", "VOID", "DECLINED"):
        raise QuotingError(
            f"cannot convert a {q.status} quotation to invoice"
        )
    if not q.line_items:
        raise QuotingError("quotation has no line items to convert")

    today = issue_date or date.today()
    days = due_in_days
    if q.payment_terms:
        days = _parse_payment_terms_days(q.payment_terms)

    invoice = Invoice(
        customer_id=q.customer_id,
        project_id=q.project_id,
        source_quote_id=q.quotation_id,
        invoice_type="MILESTONE",
        invoice_number=generate_invoice_number(db),
        po_so_number=q.po_so_number,
        currency=q.currency,
        subtotal=q.subtotal,
        discount_type=q.discount_type,
        discount_value=q.discount_value,
        amount=q.amount,
        balance_due=q.amount,
        status="DRAFT",
        issue_date=today,
        due_date=today + timedelta(days=days),
        payment_terms=q.payment_terms,
        notes=q.notes,
        footer=q.footer,
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
        for ln in q.line_items
    ]
    db.add(invoice)
    db.flush()

    q.status = "INVOICED"
    q.converted_invoice_id = invoice.invoice_id
    db.flush()
    return invoice
