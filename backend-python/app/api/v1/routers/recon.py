from datetime import date, datetime, time
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.payment import Payment
from app.models.recon_log import ReconciliationLog
from app.models.user import User
from app.schemas.payment import ManualApproveRequest, PaymentOut, ReverseRequest
from app.services import reconciliation
from app.services.matching_engine import AdjustmentType

router = APIRouter(prefix="/payments", tags=["recon"])


@router.get("/audit-log")
def audit_log(
    action: str | None = Query(default=None, description="Filter by action"),
    payer: str | None = Query(default=None, description="Substring match on payer_name"),
    from_date: date | None = Query(default=None),
    to_date: date | None = Query(default=None),
    limit: int = Query(default=200, le=500),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[dict]:
    """List reconciliation log entries, joined to the payment for context."""
    stmt = (
        select(
            ReconciliationLog.log_id,
            ReconciliationLog.payment_id,
            ReconciliationLog.action,
            ReconciliationLog.reasons,
            ReconciliationLog.actor_user_id,
            ReconciliationLog.created_at,
            Payment.payer_name,
            Payment.amount,
            Payment.currency,
            Payment.invoice_id,
            Payment.intake_source,
            User.email.label("actor_email"),
        )
        .join(Payment, Payment.payment_id == ReconciliationLog.payment_id, isouter=True)
        .join(User, User.user_id == ReconciliationLog.actor_user_id, isouter=True)
        .order_by(ReconciliationLog.created_at.desc())
        .limit(limit)
    )
    if action:
        stmt = stmt.where(ReconciliationLog.action == action)
    if payer:
        stmt = stmt.where(Payment.payer_name.ilike(f"%{payer}%"))
    if from_date:
        stmt = stmt.where(ReconciliationLog.created_at >= datetime.combine(from_date, time.min))
    if to_date:
        stmt = stmt.where(ReconciliationLog.created_at <= datetime.combine(to_date, time.max))
    rows = db.execute(stmt).all()
    return [
        {
            "log_id": str(r.log_id),
            "payment_id": str(r.payment_id),
            "action": r.action,
            "reasons": r.reasons or [],
            "actor_user_id": str(r.actor_user_id) if r.actor_user_id else None,
            "actor_email": r.actor_email,
            "created_at": r.created_at,
            "payer_name": r.payer_name,
            "amount": str(r.amount) if r.amount is not None else None,
            "currency": r.currency,
            "invoice_id": str(r.invoice_id) if r.invoice_id else None,
            "intake_source": r.intake_source,
        }
        for r in rows
    ]


@router.get("/awaiting-review", response_model=list[PaymentOut])
def awaiting_review(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Payment]:
    return list(
        db.scalars(
            select(Payment)
            .where(Payment.status == "PENDING_MANUAL_REVIEW")
            .order_by(Payment.created_at.desc())
        )
    )


@router.get("/{payment_id}", response_model=PaymentOut)
def get_payment(
    payment_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Payment:
    payment = db.get(Payment, payment_id)
    if payment is None:
        raise HTTPException(status_code=404, detail="payment not found")
    return payment


@router.get("/{payment_id}/log")
def get_payment_log(
    payment_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[dict]:
    rows = db.scalars(
        select(ReconciliationLog)
        .where(ReconciliationLog.payment_id == payment_id)
        .order_by(ReconciliationLog.created_at.asc())
    )
    return [
        {
            "log_id": r.log_id,
            "action": r.action,
            "reasons": r.reasons,
            "actor_user_id": r.actor_user_id,
            "created_at": r.created_at,
        }
        for r in rows
    ]


@router.post("/{payment_id}/manual-review", response_model=PaymentOut)
def manual_approve(
    payment_id: UUID,
    payload: ManualApproveRequest,
    db: Session = Depends(get_db),
    user: User = Depends(current_admin),
) -> Payment:
    try:
        adj = AdjustmentType(payload.adjustment_type)
    except ValueError:
        raise HTTPException(status_code=400, detail=f"unknown adjustment_type {payload.adjustment_type!r}")
    try:
        payment = reconciliation.approve_manual_match(
            db, payment_id, payload.invoice_id, user.user_id, adj
        )
        db.commit()
    except ValueError as e:
        db.rollback()
        raise HTTPException(status_code=400, detail=str(e))
    return payment


@router.post("/{payment_id}/reverse", response_model=PaymentOut)
def reverse(
    payment_id: UUID,
    payload: ReverseRequest,
    db: Session = Depends(get_db),
    user: User = Depends(current_admin),
) -> Payment:
    try:
        payment = reconciliation.reverse_payment(db, payment_id, user.user_id, payload.reason)
        db.commit()
    except ValueError as e:
        db.rollback()
        raise HTTPException(status_code=400, detail=str(e))
    return payment
