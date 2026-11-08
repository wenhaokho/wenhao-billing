"""Notify app users when recurring DRAFT invoices are generated.

Shared by the beat scanner, the manual UI trigger, and the MCP tool so a
newly created draft always produces the same approval digest. Sending is
best-effort: failures are logged, never raised — the Awaiting Finalization
queue remains the source of truth.
"""

from __future__ import annotations

import logging

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.config import get_settings
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.user import User
from app.services.email import send_recurring_drafts_email

log = logging.getLogger(__name__)


def draft_summary(db: Session, invoice: Invoice, cycle_key: str) -> dict:
    customer = db.get(Customer, invoice.customer_id)
    base = get_settings().app_base_url.rstrip("/")
    return {
        "invoice_number": invoice.invoice_number,
        "customer_name": customer.name if customer else "(unknown customer)",
        "amount": f"{invoice.amount:,.2f}",
        "currency": invoice.currency,
        "cycle_date": cycle_key,
        "link": f"{base}/invoices/{invoice.invoice_id}/edit",
    }


def user_emails(db: Session) -> list[str]:
    return list(db.scalars(select(User.email)))


def send_draft_notifications(emails: list[str], drafts: list[dict]) -> None:
    """Send the digest to each recipient; log-and-continue on failure."""
    if not drafts:
        return
    for email in emails:
        try:
            send_recurring_drafts_email(to_email=email, drafts=drafts)
        except Exception:
            log.exception(
                "failed to notify %s about %d new drafts", email, len(drafts)
            )


def notify_users_of_new_drafts(db: Session, drafts: list[dict]) -> None:
    if not drafts:
        return
    send_draft_notifications(user_emails(db), drafts)
