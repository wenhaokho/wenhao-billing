"""Beat task: fire Usage Lock for invoices whose cut_off_day fell yesterday.

Phase 1 stub: iterates USAGE invoices whose `billing_cycle_ref.cut_off_day`
matches yesterday's day-of-month and are still DRAFT. Accrued amount must be
computed externally (e.g., from the AI-token usage counter) — here we call a
`compute_accrued` hook. Replace the placeholder when the usage-metering
subsystem lands.
"""

from __future__ import annotations

from datetime import date, timedelta
from decimal import Decimal
from typing import Callable

from sqlalchemy import select

from app.db.session import SessionLocal
from app.models.invoice import Invoice
from app.services import invoicing
from app.workers.celery_app import celery_app


def _stub_accrued(_invoice_id) -> Decimal:
    raise NotImplementedError("wire usage-metering source before enabling usage-lock beat")


@celery_app.task(name="app.workers.tasks.usage_lock.cutoff_scan")
def cutoff_scan(compute_accrued: Callable[[object], Decimal] = _stub_accrued) -> int:
    yesterday = date.today() - timedelta(days=1)
    locked = 0
    with SessionLocal() as db:
        rows = db.scalars(
            select(Invoice)
            .where(Invoice.invoice_type == "USAGE")
            .where(Invoice.status == "DRAFT")
        )
        for invoice in rows:
            cfg = invoice.billing_cycle_ref or {}
            if cfg.get("locked"):
                continue
            cut_off_day = cfg.get("cut_off_day")
            if cut_off_day is None or int(cut_off_day) != yesterday.day:
                continue
            amount = compute_accrued(invoice.invoice_id)
            invoicing.lock_usage_invoice(
                db,
                invoice_id=invoice.invoice_id,
                accrued_amount=amount,
                cutoff_date=yesterday,
            )
            locked += 1
        db.commit()
    return locked
