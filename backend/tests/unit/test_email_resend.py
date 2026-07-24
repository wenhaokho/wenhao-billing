from app.config import Settings, get_settings


def test_resend_settings_defaults():
    s = Settings()
    assert s.resend_api_key is None
    assert s.resend_from_email == "noreply@wenhao.id"
    assert s.resend_from_name == "Wenhao Dev Billing"
    assert s.resend_base_url == "https://api.resend.com"
    assert s.resend_timeout_seconds == 15.0


def test_resend_api_key_from_env(monkeypatch):
    monkeypatch.setenv("RESEND_API_KEY", "re_test_123")
    get_settings.cache_clear()
    try:
        assert get_settings().resend_api_key == "re_test_123"
    finally:
        get_settings.cache_clear()


import base64
from typing import ClassVar

import pytest

from app.services import email as email_service


class _FakeResponse:
    def __init__(self, status_code: int = 200, text: str = '{"message": "The domain is not verified."}'):
        self.status_code = status_code
        self.text = text

    def raise_for_status(self):
        if self.status_code >= 400:
            raise RuntimeError(f"resend returned {self.status_code}")


class _FakeClient:
    """Captures the single POST and returns a canned response."""

    captured: ClassVar[dict] = {}

    def __init__(self, *args, status_code: int = 200, **kwargs):
        self._status_code = status_code

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def post(self, url, headers=None, json=None):
        _FakeClient.captured = {"url": url, "headers": headers, "json": json}
        return _FakeResponse(self._status_code)


def _use_resend(monkeypatch, status_code: int = 200):
    monkeypatch.setenv("RESEND_API_KEY", "re_test_123")
    get_settings.cache_clear()

    def _factory(*args, **kwargs):
        return _FakeClient(*args, status_code=status_code, **kwargs)

    monkeypatch.setattr(email_service.httpx, "Client", _factory)
    _FakeClient.captured = {}


def test_resend_used_when_key_set_and_smtp_untouched(monkeypatch):
    _use_resend(monkeypatch)

    def _boom(*a, **k):
        raise AssertionError("SMTP must not be used when Resend is configured")

    monkeypatch.setattr(email_service.smtplib, "SMTP", _boom)
    try:
        email_service.send_email(
            to_email="client@example.com",
            subject="Hi",
            body_text="hello",
        )
    finally:
        get_settings.cache_clear()

    body = _FakeClient.captured["json"]
    assert _FakeClient.captured["url"] == "https://api.resend.com/emails"
    assert _FakeClient.captured["headers"]["Authorization"] == "Bearer re_test_123"
    assert body["from"] == "Wenhao Dev Billing <noreply@wenhao.id>"
    assert body["to"] == ["client@example.com"]
    assert body["subject"] == "Hi"
    assert body["text"] == "hello"
    # Reuses the existing smtp_reply_to default.
    assert body["reply_to"] == "wenhao.kho@gmail.com"
    # Omitted when absent.
    assert "html" not in body
    assert "cc" not in body
    assert "attachments" not in body


def test_resend_payload_includes_html_cc_and_attachments(monkeypatch):
    _use_resend(monkeypatch)
    try:
        email_service.send_email(
            to_email="client@example.com",
            subject="Invoice",
            body_text="see attached",
            body_html="<p>see attached</p>",
            cc_email="cc@example.com",
            attachments=[("invoice.pdf", b"%PDF-1.4", "application/pdf")],
        )
    finally:
        get_settings.cache_clear()

    body = _FakeClient.captured["json"]
    assert body["html"] == "<p>see attached</p>"
    assert body["cc"] == ["cc@example.com"]
    assert body["attachments"] == [
        {
            "filename": "invoice.pdf",
            "content": base64.b64encode(b"%PDF-1.4").decode("ascii"),
        }
    ]


def test_resend_raises_on_error_response(monkeypatch):
    _use_resend(monkeypatch, status_code=422)
    try:
        with pytest.raises(RuntimeError):
            email_service.send_email(
                to_email="client@example.com",
                subject="Hi",
                body_text="hello",
            )
    finally:
        get_settings.cache_clear()


def test_logging_fallback_when_nothing_configured(monkeypatch):
    monkeypatch.delenv("RESEND_API_KEY", raising=False)
    monkeypatch.delenv("SMTP_HOST", raising=False)
    get_settings.cache_clear()
    try:
        # Must not raise and must not attempt any network call.
        email_service.send_email(
            to_email="client@example.com",
            subject="Hi",
            body_text="hello",
        )
    finally:
        get_settings.cache_clear()


def test_resend_error_body_is_logged(monkeypatch, caplog):
    import logging

    _use_resend(monkeypatch, status_code=422)
    try:
        with caplog.at_level(logging.ERROR, logger="app.services.email"), pytest.raises(
            RuntimeError
        ):
            email_service.send_email(
                to_email="client@example.com",
                subject="Hi",
                body_text="hello",
            )
    finally:
        get_settings.cache_clear()

    assert "Resend rejected email" in caplog.text
    assert "not verified" in caplog.text
