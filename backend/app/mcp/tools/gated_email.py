"""Gated: emailing an invoice to a customer. Requires confirm_token.

Two-phase flow: the first call (no confirm_token) previews the action and
returns a confirm_token bound to (invoice_id, amount, currency, recipient) —
no email is sent and no state changes. A second call with that token
verifies it against the *current* payload before sending; a tampered
recipient/amount fails verification and nothing is sent.
"""
from __future__ import annotations

from uuid import UUID

from app.mcp.confirm import ConfirmError, make_confirm_token, verify_confirm_token
from app.mcp.db import tool_session
from app.mcp.server import mcp
from app.models.business_profile import BusinessProfile
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services.email import send_email
from app.services.invoice_pdf import render_invoice_pdf

_ACTION = "send_invoice_email"


def _payload(invoice: Invoice, to_email: str) -> dict:
    return {
        "target_id": str(invoice.invoice_id),
        "amount": str(invoice.amount),
        "currency": invoice.currency,
        "recipient": to_email,
    }


@mcp.tool
def send_invoice_email(
    invoice_id: str,
    to_email: str | None = None,
    subject: str | None = None,
    message: str | None = None,
    confirm_token: str | None = None,
) -> dict:
    """Email an invoice PDF to a customer. GATED: first call (no
    confirm_token) previews the send and returns a confirm_token; call again
    passing that confirm_token to actually send. If invoice status is DRAFT
    it is promoted to SENT once the email is sent."""
    with tool_session() as db:
        inv = db.get(Invoice, UUID(invoice_id))
        if inv is None:
            return {"error": "invoice not found"}
        if inv.status == "VOID":
            return {"error": "cannot send a voided invoice"}
        cust = db.get(Customer, inv.customer_id) if inv.customer_id else None
        recipient = to_email or (cust.contact_email if cust else None)
        if not recipient:
            return {"error": "no recipient: pass to_email or set customer contact_email"}

        payload = _payload(inv, recipient)
        if confirm_token is None:
            return {
                "requires_confirmation": True,
                "action": _ACTION,
                "invoice_id": str(inv.invoice_id),
                "invoice_number": inv.invoice_number,
                "recipient": recipient,
                "amount": str(inv.amount),
                "currency": inv.currency,
                "confirm_token": make_confirm_token(_ACTION, payload),
            }

        try:
            verify_confirm_token(confirm_token, _ACTION, payload)
        except ConfirmError as e:
            return {"error": str(e)}

        business = db.get(BusinessProfile, 1)
        pdf = render_invoice_pdf(inv, cust, business)
        fname = f"invoice-{inv.invoice_number or str(inv.invoice_id)[:8]}.pdf"
        send_email(
            to_email=recipient,
            cc_email=None,
            subject=subject or (f"Invoice {inv.invoice_number}" if inv.invoice_number else "Invoice"),
            body_text=message or f"Please find attached invoice {inv.invoice_number or ''}.",
            attachments=[(fname, pdf, "application/pdf")],
        )

        if inv.status == "DRAFT":
            inv.status = "SENT"
        db.flush()
        return {"sent": True, "invoice_id": str(inv.invoice_id), "recipient": recipient}
