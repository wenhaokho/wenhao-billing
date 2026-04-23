from decimal import Decimal
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.bill import Bill
from app.models.user import User
from app.schemas.bill import BillCreate, BillOut, BillUpdate
from app.schemas.payment import RecordPaymentRequest
from app.services import billing_ap

router = APIRouter(prefix="/bills", tags=["bills"])


@router.post("", response_model=BillOut, status_code=201)
def create_bill(
    payload: BillCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Bill:
    try:
        bill = billing_ap.create_bill(db, payload)
    except billing_ap.BillError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(bill)
    return bill


@router.get("", response_model=list[BillOut])
def list_bills(
    status: str | None = Query(default=None),
    vendor_id: UUID | None = Query(default=None),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Bill]:
    stmt = select(Bill).order_by(Bill.created_at.desc())
    if status:
        stmt = stmt.where(Bill.status == status)
    if vendor_id:
        stmt = stmt.where(Bill.vendor_id == vendor_id)
    return list(db.scalars(stmt))


@router.get("/{bill_id}", response_model=BillOut)
def get_bill(
    bill_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Bill:
    bill = db.get(Bill, bill_id)
    if bill is None:
        raise HTTPException(status_code=404, detail="bill not found")
    return bill


@router.patch("/{bill_id}", response_model=BillOut)
def update_bill(
    bill_id: UUID,
    payload: BillUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Bill:
    bill = db.get(Bill, bill_id)
    if bill is None:
        raise HTTPException(status_code=404, detail="bill not found")
    try:
        bill = billing_ap.update_bill(db, bill, payload)
    except billing_ap.BillError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(bill)
    return bill


@router.post("/{bill_id}/record-payment", response_model=BillOut)
def record_bill_payment(
    bill_id: UUID,
    payload: RecordPaymentRequest,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Bill:
    """Manually record an outgoing payment against this bill.

    Decrements balance_due and flips status to PARTIAL or PAID. Refuses on
    DRAFT or VOID bills.
    """
    bill = db.get(Bill, bill_id)
    if bill is None:
        raise HTTPException(status_code=404, detail="bill not found")
    if bill.status in ("DRAFT", "VOID", "PAID"):
        raise HTTPException(
            status_code=400, detail=f"cannot record payment on {bill.status} bill",
        )
    amount = Decimal(payload.amount)
    if amount > Decimal(bill.balance_due):
        raise HTTPException(
            status_code=400,
            detail=f"amount {amount} exceeds balance due {bill.balance_due}",
        )
    bill.balance_due = Decimal(bill.balance_due) - amount
    if bill.balance_due <= 0:
        bill.balance_due = Decimal("0")
        bill.status = "PAID"
    else:
        bill.status = "PARTIAL"
    db.commit()
    db.refresh(bill)
    return bill


@router.post("/{bill_id}/void", response_model=BillOut)
def void_bill(
    bill_id: UUID,
    payload: dict,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Bill:
    bill = db.get(Bill, bill_id)
    if bill is None:
        raise HTTPException(status_code=404, detail="bill not found")
    reason = (payload or {}).get("reason") or "no reason given"
    try:
        bill = billing_ap.void_bill(db, bill, reason)
    except billing_ap.BillError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(bill)
    return bill
