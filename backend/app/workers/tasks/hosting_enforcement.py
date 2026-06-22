from __future__ import annotations

from datetime import date

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.db.session import SessionLocal
from app.models.hosting_subscription import HostingSubscription
from app.services.hosting_subscriptions import (
    resolve_overdue_invoice,
    restore_subscription_if_eligible,
    suspend_subscription,
)
from app.workers.celery_app import celery_app


def run_hosting_enforcement(db: Session, today: date | None = None) -> dict[str, int]:
    day = today or date.today()
    counts = {"suspended": 0, "restored": 0, "errors": 0}
    subscriptions = list(
        db.scalars(
            select(HostingSubscription)
            .where(HostingSubscription.suspension_enabled.is_(True))
            .where(HostingSubscription.status.in_(("ACTIVE", "SUSPEND_PENDING", "SUSPENDED")))
            .order_by(HostingSubscription.created_at.asc())
        )
    )
    for subscription in subscriptions:
        try:
            overdue_invoice = resolve_overdue_invoice(db, subscription, day)
            if overdue_invoice is not None:
                if suspend_subscription(db, subscription, overdue_invoice):
                    counts["suspended"] += 1
                continue
            if restore_subscription_if_eligible(db, subscription):
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
