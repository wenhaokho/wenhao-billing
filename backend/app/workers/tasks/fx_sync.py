"""Sync reference FX rates from the provider into the fx_rates table.

The core ``run_fx_sync`` is shared by the weekly Celery beat job and the manual
"Sync now" endpoint, so both paths behave identically. Upserts are keyed on the
``(from_currency, to_currency, as_of_date)`` unique constraint: a rate already
stored for that provider date is updated in place (idempotent re-runs), never
duplicated. Existing MANUAL rows for a different date are left untouched.
"""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.config import get_settings
from app.models.fx import FxRate
from app.services.fx_provider import SOURCE, fetch_latest_rates
from app.workers.celery_app import celery_app


def run_fx_sync(db: Session) -> dict[str, object]:
    settings = get_settings()
    target = settings.base_currency.upper()
    fetched = fetch_latest_rates(settings.fx_sync_currencies, target)

    counts = {"created": 0, "updated": 0}
    pairs: list[str] = []
    for item in fetched:
        pairs.append(f"{item.from_currency}->{item.to_currency}")
        existing = db.scalar(
            select(FxRate)
            .where(FxRate.from_currency == item.from_currency)
            .where(FxRate.to_currency == item.to_currency)
            .where(FxRate.as_of_date == item.as_of_date)
        )
        if existing is None:
            db.add(
                FxRate(
                    from_currency=item.from_currency,
                    to_currency=item.to_currency,
                    rate=item.rate,
                    as_of_date=item.as_of_date,
                    source=SOURCE,
                )
            )
            counts["created"] += 1
        elif existing.rate != item.rate or existing.source != SOURCE:
            existing.rate = item.rate
            existing.source = SOURCE
            counts["updated"] += 1

    return {
        "created": counts["created"],
        "updated": counts["updated"],
        "fetched": len(fetched),
        "pairs": pairs,
        "source": SOURCE,
    }


@celery_app.task(name="app.workers.tasks.fx_sync.sync_fx_rates")
def sync_fx_rates() -> dict[str, object]:
    from app.db.session import SessionLocal

    with SessionLocal() as db:
        result = run_fx_sync(db)
        db.commit()
    return result
