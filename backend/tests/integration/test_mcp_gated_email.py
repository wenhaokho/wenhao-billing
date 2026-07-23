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

from app.mcp.tools.gated_email import send_invoice_email


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
