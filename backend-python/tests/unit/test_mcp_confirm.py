import pytest

from app.mcp.confirm import ConfirmError, make_confirm_token, verify_confirm_token

PAYLOAD = {"target_id": "abc", "amount": "100.0000", "currency": "USD", "recipient": "a@b.com"}


def test_roundtrip_ok():
    t = make_confirm_token("send_invoice_email", PAYLOAD, now=1000)
    verify_confirm_token(t, "send_invoice_email", PAYLOAD, now=1100)  # within TTL


def test_rejects_wrong_action():
    t = make_confirm_token("send_invoice_email", PAYLOAD, now=1000)
    with pytest.raises(ConfirmError):
        verify_confirm_token(t, "delete_invoice", PAYLOAD, now=1100)


def test_rejects_mutated_payload():
    t = make_confirm_token("send_invoice_email", PAYLOAD, now=1000)
    mutated = {**PAYLOAD, "amount": "999.0000"}
    with pytest.raises(ConfirmError):
        verify_confirm_token(t, "send_invoice_email", mutated, now=1100)


def test_rejects_expired():
    t = make_confirm_token("send_invoice_email", PAYLOAD, now=1000)
    with pytest.raises(ConfirmError):
        verify_confirm_token(t, "send_invoice_email", PAYLOAD, now=1000 + 10_000)
