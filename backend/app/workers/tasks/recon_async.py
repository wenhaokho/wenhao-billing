"""Async wrappers around the reconciliation orchestrator.

These tasks are thin — the safe-stop gate lives in `services/reconciliation.py`,
not here. Idempotency is handled at the DB layer by the unique constraint on
(intake_source, external_ref).
"""

from __future__ import annotations

from typing import Any

from sqlalchemy.exc import IntegrityError

from app.db.session import SessionLocal
from app.services.intake.email_ocr import parse_inbound_email
from app.services.intake.webhook import parse_generic_webhook
from app.services.reconciliation import process_incoming_payment
from app.workers.celery_app import celery_app


@celery_app.task(name="app.workers.tasks.recon_async.ingest_webhook_payment", bind=True)
def ingest_webhook_payment(self, raw_body: dict[str, Any]) -> str:
    payload = parse_generic_webhook(raw_body)
    with SessionLocal() as db:
        try:
            payment = process_incoming_payment(db, payload)
            db.commit()
        except IntegrityError:
            db.rollback()
            return "duplicate"
    return str(payment.payment_id)


@celery_app.task(name="app.workers.tasks.recon_async.ingest_email_payment", bind=True)
def ingest_email_payment(self, body: dict[str, Any]) -> str:
    payload = parse_inbound_email(body)
    with SessionLocal() as db:
        try:
            payment = process_incoming_payment(db, payload)
            db.commit()
        except IntegrityError:
            db.rollback()
            return "duplicate"
    return str(payment.payment_id)
