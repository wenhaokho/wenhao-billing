"""Beat task: scan RECURRING templates and generate DRAFT invoices for today's cycles.

Idempotency is delegated to `invoicing.trigger_recurring_cycle` — if a DRAFT
for the same (template, cycle_key) already exists, it's returned unchanged.

Cycle decisions are delegated to `app.services.recurring_schedule` so the
same logic serves the scanner and the UI's "next invoice" column.
"""

from __future__ import annotations

from datetime import date

from sqlalchemy import select

from app.db.session import SessionLocal
from app.models.invoice import Invoice
from app.services import invoicing
from app.services.recurring_schedule import (
    ScheduleError,
    current_cycle,
    cycle_key,
    is_paused,
    parse_schedule,
)
from app.workers.celery_app import celery_app


def _cycle_key_for(today: date, template: Invoice) -> str | None:
    """Return the ISO cycle-start date if a cycle is due today, else None.

    Malformed / missing configs return None so a single bad template can't
    crash the whole beat loop.
    """
    if is_paused(template.billing_cycle_ref):
        return None
    try:
        schedule = parse_schedule(template.billing_cycle_ref)
    except ScheduleError:
        return None
    cycle = current_cycle(schedule, today)
    if cycle is None:
        return None
    return cycle_key(cycle)


@celery_app.task(name="app.workers.tasks.recurring.scan_and_generate")
def scan_and_generate() -> int:
    today = date.today()
    generated = 0
    with SessionLocal() as db:
        templates = db.scalars(
            select(Invoice)
            .where(Invoice.invoice_type == "RECURRING")
            .where(Invoice.is_template.is_(True))
        )
        for template in templates:
            key = _cycle_key_for(today, template)
            if key is None:
                continue
            invoicing.trigger_recurring_cycle(
                db, template_invoice_id=template.invoice_id, cycle_key=key
            )
            generated += 1
        db.commit()
    return generated
