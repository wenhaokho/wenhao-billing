"""Gated send_invoice_email: two-phase confirm-token flow.

FastMCP 3.4.3's `@mcp.tool` returns the plain function (no `.fn`) — the tool
is called directly. Every test requests `mcp_tool_db` to bind
app.mcp.db.tool_session to the rollback-safe test connection.

Covers:
- first call (no confirm_token) returns a preview + confirm_token and sends
  nothing (send_email not called, no status change)
- second call with the token from the preview actually sends (send_email
  called once) and reports sent=True; render_invoice_pdf is patched so no
  real PDF rendering runs
- a tampered payload (different to_email than what was previewed) with the
  original token is rejected with {"error": ...} and send_email is not
  called
"""
from __future__ import annotations

from unittest.mock import patch

from app.mcp.tools.gated_email import send_invoice_email, send_payment_reminder


def test_first_call_returns_preview_and_sends_nothing(mcp_tool_db, seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_invoice_email(invoice_id=str(inv_id), to_email="a@b.com")
    assert out["requires_confirmation"] is True
    assert "confirm_token" in out
    assert out["recipient"] == "a@b.com"
    mock_send.assert_not_called()


def test_second_call_with_token_sends(mcp_tool_db, seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    preview = send_invoice_email(invoice_id=str(inv_id), to_email="a@b.com")
    with patch("app.mcp.tools.gated_email.send_email") as mock_send, patch(
        "app.mcp.tools.gated_email.render_invoice_pdf", return_value=b"%PDF"
    ):
        out = send_invoice_email(
            invoice_id=str(inv_id),
            to_email="a@b.com",
            confirm_token=preview["confirm_token"],
        )
    mock_send.assert_called_once()
    assert out["sent"] is True


def test_tampered_token_refuses(mcp_tool_db, seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    preview = send_invoice_email(invoice_id=str(inv_id), to_email="a@b.com")
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_invoice_email(
            invoice_id=str(inv_id),
            to_email="DIFFERENT@x.com",
            confirm_token=preview["confirm_token"],
        )
    assert "error" in out
    mock_send.assert_not_called()


def test_draft_invoice_promoted_to_sent_after_send(mcp_tool_db, seed_draft_invoice, db):
    """DRAFT invoice: once the confirmed send goes out, the invoice status is
    promoted to SENT."""
    from app.models.invoice import Invoice

    inv_id = seed_draft_invoice
    preview = send_invoice_email(invoice_id=str(inv_id), to_email="a@b.com")
    with patch("app.mcp.tools.gated_email.send_email") as mock_send, patch(
        "app.mcp.tools.gated_email.render_invoice_pdf", return_value=b"%PDF"
    ):
        out = send_invoice_email(
            invoice_id=str(inv_id),
            to_email="a@b.com",
            confirm_token=preview["confirm_token"],
        )
    mock_send.assert_called_once()
    assert out["sent"] is True

    db.expire_all()
    assert db.get(Invoice, inv_id).status == "SENT"


def test_void_invoice_refuses(mcp_tool_db, seed_customer, db):
    """VOID invoice: refused with {"error": ...} and nothing is sent."""
    from decimal import Decimal

    from app.models.invoice import Invoice

    inv = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-VOID-EMAIL",
        currency="USD",
        amount=Decimal("100.0000"),
        balance_due=Decimal("100.0000"),
        status="VOID",
    )
    db.add(inv)
    db.flush()
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_invoice_email(invoice_id=str(inv.invoice_id), to_email="a@b.com")
    assert "error" in out
    mock_send.assert_not_called()


def test_no_recipient_refuses(mcp_tool_db, seed_sent_invoice):
    """No to_email and the customer has no contact_email: refused, nothing
    sent."""
    inv_id, _ = seed_sent_invoice  # seed_customer has no contact_email
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_invoice_email(invoice_id=str(inv_id))
    assert "error" in out
    mock_send.assert_not_called()


# ---------------------------------------------------------------------------
# send_payment_reminder — same two-phase shape as send_invoice_email, but
# composes a reminder body (bound to inv.balance_due) instead of attaching
# a PDF.
# ---------------------------------------------------------------------------


def test_reminder_first_call_returns_preview_and_sends_nothing(mcp_tool_db, seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_payment_reminder(invoice_id=str(inv_id), to_email="a@b.com")
    assert out["requires_confirmation"] is True
    assert "confirm_token" in out
    assert out["recipient"] == "a@b.com"
    mock_send.assert_not_called()


def test_reminder_second_call_with_token_sends(mcp_tool_db, seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    preview = send_payment_reminder(invoice_id=str(inv_id), to_email="a@b.com")
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_payment_reminder(
            invoice_id=str(inv_id),
            to_email="a@b.com",
            confirm_token=preview["confirm_token"],
        )
    mock_send.assert_called_once()
    assert out["sent"] is True


def test_reminder_tampered_token_refuses(mcp_tool_db, seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    preview = send_payment_reminder(invoice_id=str(inv_id), to_email="a@b.com")
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_payment_reminder(
            invoice_id=str(inv_id),
            to_email="DIFFERENT@x.com",
            confirm_token=preview["confirm_token"],
        )
    assert "error" in out
    mock_send.assert_not_called()
