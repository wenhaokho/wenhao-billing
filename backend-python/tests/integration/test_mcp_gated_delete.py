"""Gated hard-deletes: two-phase confirm-token flow for invoices, bills,
and customers.

FastMCP 3.4.3's `@mcp.tool` returns the plain function (no `.fn`) — tools
are called directly. Every test requests `mcp_tool_db` to bind
app.mcp.db.tool_session to the rollback-safe test connection.

Covers, per target:
- first call (no confirm_token) returns a preview + confirm_token and
  deletes nothing (row still exists)
- second call with the token from the preview actually deletes
- a tampered token (confirm_token from a *different* target_id) is
  rejected with {"error": ...} and nothing is deleted
- deleting a non-DRAFT invoice/bill returns {"error": ...} (the domain
  guard fires only at execute time, matching invoicing.delete_invoice)
"""
from __future__ import annotations

from uuid import UUID

from app.mcp.tools.gated_delete import delete_bill, delete_customer, delete_invoice
from app.models.bill import Bill
from app.models.customer import Customer
from app.models.invoice import Invoice


# ---------------------------------------------------------------------------
# delete_invoice
# ---------------------------------------------------------------------------


def test_delete_invoice_preview_does_not_delete(mcp_tool_db, db, seed_draft_invoice):
    inv_id = seed_draft_invoice
    preview = delete_invoice(invoice_id=str(inv_id))
    assert preview["requires_confirmation"] is True
    assert "confirm_token" in preview
    assert db.get(Invoice, inv_id) is not None


def test_delete_invoice_previews_then_deletes(mcp_tool_db, db, seed_draft_invoice):
    inv_id = seed_draft_invoice
    preview = delete_invoice(invoice_id=str(inv_id))
    out = delete_invoice(invoice_id=str(inv_id), confirm_token=preview["confirm_token"])
    assert out["deleted"] is True
    db.expire_all()
    assert db.get(Invoice, inv_id) is None


def test_delete_non_draft_invoice_errors(mcp_tool_db, db, seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    preview = delete_invoice(invoice_id=str(inv_id))
    out = delete_invoice(invoice_id=str(inv_id), confirm_token=preview["confirm_token"])
    assert "error" in out
    db.expire_all()
    assert db.get(Invoice, inv_id) is not None


def test_delete_invoice_tampered_token_rejected(
    mcp_tool_db, db, seed_draft_invoice, seed_sent_invoice
):
    inv_id = seed_draft_invoice
    other_id, _ = seed_sent_invoice
    preview = delete_invoice(invoice_id=str(other_id))
    out = delete_invoice(invoice_id=str(inv_id), confirm_token=preview["confirm_token"])
    assert "error" in out
    db.expire_all()
    assert db.get(Invoice, inv_id) is not None


# ---------------------------------------------------------------------------
# delete_bill
# ---------------------------------------------------------------------------


def test_delete_bill_preview_does_not_delete(mcp_tool_db, db, seed_draft_bill):
    bill_id = seed_draft_bill
    preview = delete_bill(bill_id=str(bill_id))
    assert preview["requires_confirmation"] is True
    assert db.get(Bill, bill_id) is not None


def test_delete_bill_previews_then_deletes(mcp_tool_db, db, seed_draft_bill):
    bill_id = seed_draft_bill
    preview = delete_bill(bill_id=str(bill_id))
    out = delete_bill(bill_id=str(bill_id), confirm_token=preview["confirm_token"])
    assert out["deleted"] is True
    db.expire_all()
    assert db.get(Bill, bill_id) is None


def test_delete_non_draft_bill_errors(mcp_tool_db, db, seed_bill):
    bill_id = seed_bill  # seed_bill fixture is OPEN, not DRAFT
    preview = delete_bill(bill_id=str(bill_id))
    out = delete_bill(bill_id=str(bill_id), confirm_token=preview["confirm_token"])
    assert "error" in out
    db.expire_all()
    assert db.get(Bill, bill_id) is not None


def test_delete_bill_tampered_token_rejected(mcp_tool_db, db, seed_draft_bill, seed_bill):
    preview = delete_bill(bill_id=str(seed_bill))
    out = delete_bill(bill_id=str(seed_draft_bill), confirm_token=preview["confirm_token"])
    assert "error" in out
    db.expire_all()
    assert db.get(Bill, seed_draft_bill) is not None


# ---------------------------------------------------------------------------
# delete_customer (soft-deactivate, matching routers/customers.py's
# existing DELETE endpoint — there is no hard-delete precedent for
# customers anywhere in the app)
# ---------------------------------------------------------------------------


def test_delete_customer_preview_does_not_deactivate(mcp_tool_db, db, seed_customer):
    preview = delete_customer(customer_id=str(seed_customer))
    assert preview["requires_confirmation"] is True
    db.expire_all()
    assert db.get(Customer, seed_customer).active is True


def test_delete_customer_previews_then_deactivates(mcp_tool_db, db, seed_customer):
    preview = delete_customer(customer_id=str(seed_customer))
    out = delete_customer(customer_id=str(seed_customer), confirm_token=preview["confirm_token"])
    assert out["deleted"] is True
    db.expire_all()
    customer = db.get(Customer, seed_customer)
    assert customer is not None
    assert customer.active is False


def test_delete_customer_tampered_token_rejected(mcp_tool_db, db, seed_customer, seed_two_acme):
    other_id = seed_two_acme[0]
    preview = delete_customer(customer_id=str(other_id))
    out = delete_customer(customer_id=str(seed_customer), confirm_token=preview["confirm_token"])
    assert "error" in out
    db.expire_all()
    assert db.get(Customer, seed_customer).active is True
