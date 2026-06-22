from __future__ import annotations

from datetime import date

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.db.session import SessionLocal
from app.models.item import Item
from app.services.hosting import (
    resolve_overdue_invoice,
    restore_hosting_if_eligible,
    suspend_hosting,
)
from app.workers.celery_app import celery_app


def run_hosting_enforcement(db: Session, today: date | None = None) -> dict[str, int]:
    day = today or date.today()
    counts = {"suspended": 0, "restored": 0, "errors": 0}
    items = list(
        db.scalars(
            select(Item)
            .where(Item.is_hosting.is_(True))
            .where(Item.hosting_suspension_enabled.is_(True))
            .where(Item.hosting_status.in_(("ACTIVE", "SUSPEND_PENDING", "SUSPENDED")))
            .order_by(Item.created_at.asc())
        )
    )
    for item in items:
        try:
            overdue_invoice = resolve_overdue_invoice(db, item, day)
            if overdue_invoice is not None:
                if suspend_hosting(db, item, overdue_invoice):
                    counts["suspended"] += 1
                continue
            if restore_hosting_if_eligible(db, item):
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
