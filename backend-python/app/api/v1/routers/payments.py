import json

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.api.deps import verify_email_webhook_token, verify_webhook_hmac
from app.db.session import get_db
from app.models.payment import Payment
from app.schemas.payment import PaymentOut
from app.services.intake.email_ocr import parse_inbound_email
from app.services.intake.webhook import parse_generic_webhook
from app.services.reconciliation import process_incoming_payment

router = APIRouter(prefix="/payments", tags=["payments"])


@router.post("/intake/webhook", response_model=PaymentOut, status_code=201)
def intake_webhook(
    body: bytes = Depends(verify_webhook_hmac),
    db: Session = Depends(get_db),
) -> Payment:
    try:
        parsed = json.loads(body)
    except json.JSONDecodeError as e:
        raise HTTPException(status_code=400, detail=f"invalid JSON: {e}")

    try:
        payload = parse_generic_webhook(parsed)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))

    try:
        payment = process_incoming_payment(db, payload, actor_user_id=None)
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(status_code=409, detail="duplicate (intake_source, external_ref)")
    return payment


@router.post("/intake/email", response_model=PaymentOut, status_code=201)
def intake_email(
    body: dict,
    db: Session = Depends(get_db),
    _: None = Depends(verify_email_webhook_token),
) -> Payment:
    try:
        payload = parse_inbound_email(body)
    except (ValueError, NotImplementedError) as e:
        raise HTTPException(status_code=400, detail=str(e))

    try:
        payment = process_incoming_payment(db, payload, actor_user_id=None)
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(status_code=409, detail="duplicate email payment")
    return payment
