"""Beat task: scan RECURRING templates and generate DRAFT invoices for today's cycles.

Idempotency is delegated to `invoicing.trigger_recurring_cycle` — if a DRAFT
for the same (template, cycle_key) already exists, it's returned unchanged.

Cycle decisions are delegated to `app.services.recurring_schedule` so the
same logic serves the scanner and the UI's "next invoice" column.
"""

from __future__ import annotations

import logging
from datetime import date

from sqlalchemy import select

from app.config import get_settings
from app.db.session import SessionLocal
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.user import User
from app.services import invoicing
from app.services.email import send_recurring_drafts_email
from app.services.recurring_schedule import (
    ScheduleError,
    current_cycle,
    cycle_key,
    is_paused,
    parse_schedule,
)
from app.workers.celery_app import celery_app

log = logging.getLogger(__name__)


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


def _draft_summary(db, invoice: Invoice, key: str) -> dict:
    customer = db.get(Customer, invoice.customer_id)
    base = get_settings().app_base_url.rstrip("/")
    return {
        "invoice_number": invoice.invoice_number,
        "customer_name": customer.name if customer else "(unknown customer)",
        "amount": f"{invoice.amount:,.2f}",
        "currency": invoice.currency,
        "cycle_date": key,
        "link": f"{base}/invoices/{invoice.invoice_id}/edit",
    }


def _notify_users(db, drafts: list[dict]) -> None:
    """Email every app user a digest of newly generated drafts.

    Mail failures are logged, never raised — the drafts are already committed
    and the Awaiting Finalization queue remains the source of truth.
    """
    if not drafts:
        return
    for email in db.scalars(select(User.email)):
        try:
            send_recurring_drafts_email(to_email=email, drafts=drafts)
        except Exception:
            log.exception("failed to notify %s about %d new drafts", email, len(drafts))


@celery_app.task(name="app.workers.tasks.recurring.scan_and_generate")
def scan_and_generate() -> int:
    today = date.today()
    generated = 0
    new_drafts: list[dict] = []
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
            existing = invoicing.find_cycle_invoice(
                db, template_invoice_id=template.invoice_id, cycle_key=key
            )
            invoice = invoicing.trigger_recurring_cycle(
                db, template_invoice_id=template.invoice_id, cycle_key=key
            )
            if existing is None:
                new_drafts.append(_draft_summary(db, invoice, key))
            generated += 1
        db.commit()
        _notify_users(db, new_drafts)
    return generated
