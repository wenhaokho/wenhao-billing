"""AP bill service — mirror of invoicing.py on the purchase side.

Scope kept deliberately small: create / update / list / void. Payment
application and journal posting are handled by their own subsystems later.
"""

from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from sqlalchemy.orm import Session

from app.models.bill import Bill
from app.models.bill_line_item import BillLineItem
from app.schemas.bill import BillCreate, BillLineItemIn, BillUpdate


class BillError(Exception):
    """Raised on domain validation errors in the AP layer."""


def _compute_line_amount(line: BillLineItemIn) -> Decimal:
    return (line.quantity * line.unit_price).quantize(Decimal("0.0001"))


def _apply_totals(bill: Bill, lines: list[BillLineItemIn]) -> None:
    subtotal = sum((_compute_line_amount(ln) for ln in lines), Decimal("0"))
    total = subtotal
    if bill.discount_type and bill.discount_value is not None:
        if bill.discount_type == "PERCENT":
            total = subtotal * (Decimal("1") - (bill.discount_value / Decimal("100")))
        elif bill.discount_type == "AMOUNT":
            total = subtotal - bill.discount_value
        if total < 0:
            total = Decimal("0")
    bill.subtotal = subtotal.quantize(Decimal("0.0001"))
    bill.amount = total.quantize(Decimal("0.0001"))
    # On create / edit-in-DRAFT we re-baseline balance_due. Payment
    # application owns this column after the bill goes OPEN.
    if bill.status in (None, "DRAFT"):
        bill.balance_due = bill.amount


def create_bill(db: Session, payload: BillCreate) -> Bill:
    bill = Bill(
        vendor_id=payload.vendor_id,
        project_id=payload.project_id,
        bill_number=payload.bill_number,
        po_number=payload.po_number,
        currency=payload.currency.upper(),
        discount_type=payload.discount_type,
        discount_value=payload.discount_value,
        issue_date=payload.issue_date,
        due_date=payload.due_date,
        payment_terms=payload.payment_terms,
        notes=payload.notes,
        status="DRAFT",
        amount=Decimal("0"),
        balance_due=Decimal("0"),
    )
    db.add(bill)
    db.flush()

    for idx, ln in enumerate(payload.line_items):
        db.add(
            BillLineItem(
                bill_id=bill.bill_id,
                item_id=ln.item_id,
                expense_account_id=ln.expense_account_id,
                position=ln.position or idx,
                description=ln.description,
                quantity=ln.quantity,
                unit_price=ln.unit_price,
                amount=_compute_line_amount(ln),
            )
        )

    _apply_totals(bill, payload.line_items)
    db.flush()
    return bill


def update_bill(db: Session, bill: Bill, payload: BillUpdate) -> Bill:
    if bill.status not in ("DRAFT", "OPEN"):
        raise BillError(f"bill is {bill.status} and cannot be edited")

    data = payload.model_dump(exclude_unset=True, exclude={"line_items"})
    for field, value in data.items():
        if field == "currency" and value is not None:
            value = value.upper()
        setattr(bill, field, value)

    if payload.line_items is not None:
        # Replace lines wholesale. Simpler than diffing and matches the
        # invoice-edit UX (bill form posts the whole list on save).
        bill.line_items.clear()
        db.flush()
        for idx, ln in enumerate(payload.line_items):
            db.add(
                BillLineItem(
                    bill_id=bill.bill_id,
                    item_id=ln.item_id,
                    expense_account_id=ln.expense_account_id,
                    position=ln.position or idx,
                    description=ln.description,
                    quantity=ln.quantity,
                    unit_price=ln.unit_price,
                    amount=_compute_line_amount(ln),
                )
            )
        _apply_totals(bill, payload.line_items)

    db.flush()
    return bill


def void_bill(db: Session, bill: Bill, reason: str) -> Bill:
    if bill.status in ("PAID", "VOID"):
        raise BillError(f"bill is {bill.status} and cannot be voided")
    bill.status = "VOID"
    bill.balance_due = Decimal("0")
    bill.notes = (bill.notes or "") + f"\n[voided] {reason}"
    db.flush()
    return bill
