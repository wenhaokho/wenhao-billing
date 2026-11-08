"""Gated: hard-deleting invoices/bills, and deactivating customers.
Requires confirm_token.

Two-phase flow identical to gated_email.py: the first call (no
confirm_token) previews the action and returns a confirm_token bound to
``{"target_id": <id>}`` — deletes have no amount/recipient, unlike the send
tools — and no row is touched. A second call with that token verifies it
against the *current* target_id before acting; a tampered target_id fails
verification and nothing changes.

Delete-path notes (confirmed by reading the service/router layer, not
guessed — see task-10-report.md for the full trail):

- invoice: ``app.services.invoicing.delete_invoice(db, invoice_id)``
  already exists and enforces DRAFT-only. We call it directly and only
  catch ``invoicing.InvoicingError`` — the domain guard fires at *execute*
  time, not at preview time, exactly like ``send_invoice_email``'s
  DRAFT->SENT transition happens at execute time.
- bill: ``app.services.billing_ap`` has no delete function, and
  ``routers/bills.py`` exposes no DELETE endpoint at all — there is no
  existing "delete a bill" behavior to mirror. This tool applies the same
  DRAFT-only guard invoices use (a bill that has left DRAFT has started
  affecting AP; ``void_bill`` is the correct tool past that point) and
  hard-deletes the row itself once confirmed.
- customer: ``app.services.customers`` has no delete function either, but
  ``routers/customers.py``'s ``DELETE /{customer_id}`` *does* exist — and
  it is a soft delete (sets ``active=False``; it never refuses). This tool
  replicates that exact behavior rather than inventing a new hard-delete
  semantic with no precedent anywhere in the app (customers are
  referenced by invoices/payments, so a real hard delete would also risk
  FK violations).
"""
from __future__ import annotations

from uuid import UUID

from app.mcp.confirm import ConfirmError, make_confirm_token, verify_confirm_token
from app.mcp.db import tool_session
from app.mcp.server import mcp
from app.models.bill import Bill
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services import invoicing

_DELETE_INVOICE = "delete_invoice"
_DELETE_BILL = "delete_bill"
_DELETE_CUSTOMER = "delete_customer"


class _BillDeleteError(Exception):
    """Local mirror of invoicing.InvoicingError's DRAFT-only guard — no
    equivalent function exists in app.services.billing_ap to call."""


def _payload(target_id: str) -> dict:
    return {"target_id": target_id}


def _hard_delete_draft_bill(db, bill: Bill) -> None:
    if bill.status != "DRAFT":
        raise _BillDeleteError(f"only DRAFT bills can be deleted (got {bill.status})")
    db.delete(bill)
    db.flush()


@mcp.tool
def delete_invoice(invoice_id: str, confirm_token: str | None = None) -> dict:
    """Permanently delete a DRAFT invoice. GATED: first call (no
    confirm_token) previews the delete and returns a confirm_token; call
    again passing that confirm_token to actually delete. Only DRAFT
    invoices can be deleted — invoicing.delete_invoice refuses anything
    else."""
    with tool_session() as db:
        inv = db.get(Invoice, UUID(invoice_id))
        if inv is None:
            return {"error": "invoice not found"}

        payload = _payload(str(inv.invoice_id))
        if confirm_token is None:
            return {
                "requires_confirmation": True,
                "action": _DELETE_INVOICE,
                "invoice_id": str(inv.invoice_id),
                "invoice_number": inv.invoice_number,
                "status": inv.status,
                "confirm_token": make_confirm_token(_DELETE_INVOICE, payload),
            }

        try:
            verify_confirm_token(confirm_token, _DELETE_INVOICE, payload)
        except ConfirmError as e:
            return {"error": str(e)}

        try:
            invoicing.delete_invoice(db, inv.invoice_id)
        except invoicing.InvoicingError as e:
            return {"error": str(e)}
        return {"deleted": True, "invoice_id": str(inv.invoice_id)}


@mcp.tool
def delete_bill(bill_id: str, confirm_token: str | None = None) -> dict:
    """Permanently delete a DRAFT bill. GATED: first call (no
    confirm_token) previews the delete and returns a confirm_token; call
    again passing that confirm_token to actually delete. Only DRAFT bills
    can be deleted — a bill that has moved past DRAFT already affects AP
    and should be voided instead (see void_bill)."""
    with tool_session() as db:
        bill = db.get(Bill, UUID(bill_id))
        if bill is None:
            return {"error": "bill not found"}

        payload = _payload(str(bill.bill_id))
        if confirm_token is None:
            return {
                "requires_confirmation": True,
                "action": _DELETE_BILL,
                "bill_id": str(bill.bill_id),
                "bill_number": bill.bill_number,
                "status": bill.status,
                "confirm_token": make_confirm_token(_DELETE_BILL, payload),
            }

        try:
            verify_confirm_token(confirm_token, _DELETE_BILL, payload)
        except ConfirmError as e:
            return {"error": str(e)}

        try:
            _hard_delete_draft_bill(db, bill)
        except _BillDeleteError as e:
            return {"error": str(e)}
        return {"deleted": True, "bill_id": str(bill.bill_id)}


@mcp.tool
def delete_customer(customer_id: str, confirm_token: str | None = None) -> dict:
    """Deactivate a customer (soft delete — sets active=False). GATED:
    first call (no confirm_token) previews the action and returns a
    confirm_token; call again passing that confirm_token to actually
    deactivate. Matches the existing DELETE /customers/{id} endpoint:
    customers are never hard-deleted anywhere in this app."""
    with tool_session() as db:
        customer = db.get(Customer, UUID(customer_id))
        if customer is None:
            return {"error": "customer not found"}

        payload = _payload(str(customer.customer_id))
        if confirm_token is None:
            return {
                "requires_confirmation": True,
                "action": _DELETE_CUSTOMER,
                "customer_id": str(customer.customer_id),
                "name": customer.name,
                "confirm_token": make_confirm_token(_DELETE_CUSTOMER, payload),
            }

        try:
            verify_confirm_token(confirm_token, _DELETE_CUSTOMER, payload)
        except ConfirmError as e:
            return {"error": str(e)}

        customer.active = False
        db.flush()
        return {"deleted": True, "customer_id": str(customer.customer_id)}
