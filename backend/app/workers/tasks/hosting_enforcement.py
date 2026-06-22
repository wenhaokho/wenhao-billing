from __future__ import annotations

from datetime import date

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.db.session import SessionLocal
from app.models.invoice import Invoice
from app.services.hosting_subscriptions import (
    resolve_overdue_invoice,
    restore_subscription_if_eligible,
    suspend_subscription,
)
from app.workers.celery_app import celery_app


def run_hosting_enforcement(db: Session, today: date | None = None) -> dict[str, int]:
    day = today or date.today()
    counts = {"suspended": 0, "restored": 0, "errors": 0}
    templates = list(
        db.scalars(
            select(Invoice)
            .where(Invoice.is_hosting.is_(True))
            .where(Invoice.is_template.is_(True))
            .where(Invoice.hosting_suspension_enabled.is_(True))
            .where(Invoice.hosting_status.in_(("ACTIVE", "SUSPEND_PENDING", "SUSPENDED")))
            .order_by(Invoice.created_at.asc())
        )
    )
    for template in templates:
        try:
            overdue_invoice = resolve_overdue_invoice(db, template, day)
            if overdue_invoice is not None:
                if suspend_subscription(db, template, overdue_invoice):
                    counts["suspended"] += 1
                continue
            if restore_subscription_if_eligible(db, template):
                counts["restored"] += 1
        except Exception:
            counts["errors"] += 1
    return counts


@celery_app.task(name="app.workers.tasks.hosting_enforcement.daily_hosting_enforcement")
def daily_hosting_enforcement() -> dict[str, int]:
    with SessionLocal() as db:
        counts = run_hosting_enforcement(db)
        db.commit()
    return counts
