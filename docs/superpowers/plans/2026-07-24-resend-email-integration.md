# Resend Email Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Resend's native HTTP API as an email backend that takes precedence over SMTP when configured, without changing any of the four existing `send_email()` call sites.

**Architecture:** Keep the single `send_email()` seam in `backend/app/services/email.py`. Add a new `_send_via_resend()` helper (sync `httpx.Client`, matching the `cloudflare.py`/`fx_provider.py` pattern) and a branch at the top of `send_email()`: if `resend_api_key` is set, delegate to Resend and return; otherwise fall through to the unchanged SMTP / logging-fallback logic. Selection is gated on a new default-unset key, so existing deployments see no behavior change.

**Tech Stack:** Python 3, FastAPI app, pydantic-settings, `httpx` (sync client, already a dependency), `pytest` (real Postgres not required for these unit tests — they are pure unit tests with mocked HTTP).

## Global Constraints

- Resend precedence: when `resend_api_key` is set it is used; SMTP is only used when `resend_api_key` is unset and `smtp_host` is set; otherwise log-and-return.
- Do NOT modify the four call sites (`api/v1/routers/auth.py`, `api/v1/routers/invoices.py`, `api/v1/routers/quotations.py`, `mcp/tools/gated_email.py`) or the MCP gated-email preview/confirm flow.
- Sender default name is exactly `Wenhao Dev Billing`; sender default email is exactly `noreply@wenhao.id`.
- Reply-To reuses the existing `smtp_reply_to` setting — do NOT add a Resend-specific reply-to field.
- Preserve the raise-on-failure contract: a failed send raises.
- Follow the established sync-HTTP pattern: `with httpx.Client(timeout=...) as client: ...`.
- Alembic/migrations: none — no model or schema changes.

---

### Task 1: Add Resend config settings

**Files:**
- Modify: `backend/app/config.py` (add fields to the `Settings` class, after the FX block ending at line ~89, before the closing of the class)
- Test: `backend/tests/unit/test_email_resend.py` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `Settings.resend_api_key: str | None`, `Settings.resend_from_email: str`, `Settings.resend_from_name: str`, `Settings.resend_base_url: str`, `Settings.resend_timeout_seconds: float`. Env var names (pydantic-settings, case-insensitive): `RESEND_API_KEY`, `RESEND_FROM_EMAIL`, `RESEND_FROM_NAME`, `RESEND_BASE_URL`, `RESEND_TIMEOUT_SECONDS`.

- [ ] **Step 1: Write the failing test**

Create `backend/tests/unit/test_email_resend.py`:

```python
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose exec backend pytest tests/unit/test_email_resend.py -v`
Expected: FAIL — `AttributeError: 'Settings' object has no attribute 'resend_api_key'`.

- [ ] **Step 3: Add the config fields**

In `backend/app/config.py`, after the FX provider block (the `fx_sync_currencies` field, ~line 89) and before the closing of the `Settings` class / the `@lru_cache` decorator, add:

```python
    # --- Resend (native HTTP API email) ---
    # When resend_api_key is set it takes precedence over the SMTP_* settings.
    # The sender domain must be verified in Resend.
    resend_api_key: str | None = Field(default=None)
    resend_from_email: str = Field(default="noreply@wenhao.id")
    resend_from_name: str = Field(default="Wenhao Dev Billing")
    resend_base_url: str = Field(default="https://api.resend.com")
    resend_timeout_seconds: float = Field(default=15.0)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker compose exec backend pytest tests/unit/test_email_resend.py -v`
Expected: PASS (both tests).

- [ ] **Step 5: Lint**

Run: `docker compose exec backend ruff check app`
Expected: no new errors.

- [ ] **Step 6: Commit**

```bash
git add backend/app/config.py backend/tests/unit/test_email_resend.py
git commit -m "Add Resend config settings"
```

---

### Task 2: Implement the Resend backend and wire it into send_email()

**Files:**
- Modify: `backend/app/services/email.py` (add imports `base64` and `httpx`; add `_send_via_resend()`; add a branch at the top of `send_email()`)
- Test: `backend/tests/unit/test_email_resend.py` (append tests to the file created in Task 1)

**Interfaces:**
- Consumes: `Settings.resend_*` fields from Task 1; existing `Settings.smtp_reply_to`.
- Produces: `_send_via_resend(*, to_email: str, subject: str, body_text: str, body_html: str | None, cc_email: str | None, attachments: list[tuple[str, bytes, str]] | None) -> None`. The public `send_email(...)` signature is unchanged.

- [ ] **Step 1: Write the failing tests**

Append to `backend/tests/unit/test_email_resend.py`:

```python
import base64

import httpx
import pytest

from app.services import email as email_service


class _FakeResponse:
    def __init__(self, status_code: int = 200):
        self.status_code = status_code

    def raise_for_status(self):
        if self.status_code >= 400:
            raise RuntimeError(f"resend returned {self.status_code}")


class _FakeClient:
    """Captures the single POST and returns a canned response."""

    captured: dict = {}

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `docker compose exec backend pytest tests/unit/test_email_resend.py -v`
Expected: the four new tests FAIL (e.g. `AttributeError: module 'app.services.email' has no attribute 'httpx'`, and Resend branch not taken). The two Task-1 config tests still PASS.

- [ ] **Step 3: Add imports to `email.py`**

At the top of `backend/app/services/email.py`, add `base64` and `httpx` to the imports. After the change the import block reads:

```python
from __future__ import annotations

import base64
import logging
import smtplib
from email.message import EmailMessage

import httpx

from app.config import get_settings
```

- [ ] **Step 4: Add the `_send_via_resend()` helper**

In `backend/app/services/email.py`, add this function immediately before `send_email()`:

```python
def _send_via_resend(
    *,
    to_email: str,
    subject: str,
    body_text: str,
    body_html: str | None,
    cc_email: str | None,
    attachments: list[tuple[str, bytes, str]] | None,
) -> None:
    """Send an email via the Resend HTTP API (https://resend.com/docs)."""
    settings = get_settings()

    payload: dict = {
        "from": f"{settings.resend_from_name} <{settings.resend_from_email}>",
        "to": [to_email],
        "subject": subject,
        "text": body_text,
    }
    if body_html:
        payload["html"] = body_html
    if cc_email:
        payload["cc"] = [cc_email]
    if settings.smtp_reply_to:
        payload["reply_to"] = settings.smtp_reply_to
    if attachments:
        # Resend infers the content type from the filename, so the tuple's mime
        # element is unused in the payload.
        payload["attachments"] = [
            {
                "filename": filename,
                "content": base64.b64encode(content).decode("ascii"),
            }
            for filename, content, _mime in attachments
        ]

    try:
        with httpx.Client(timeout=settings.resend_timeout_seconds) as client:
            resp = client.post(
                f"{settings.resend_base_url}/emails",
                headers={"Authorization": f"Bearer {settings.resend_api_key}"},
                json=payload,
            )
            resp.raise_for_status()
    except Exception:
        log.exception("failed to send email via Resend to %s", to_email)
        raise

    log.info("sent email via Resend to=%s subject=%r", to_email, subject)
```

- [ ] **Step 5: Add the Resend branch to `send_email()`**

In `backend/app/services/email.py`, in `send_email()`, immediately after `settings = get_settings()` and before the `if not settings.smtp_host:` block, add:

```python
    if settings.resend_api_key:
        _send_via_resend(
            to_email=to_email,
            subject=subject,
            body_text=body_text,
            body_html=body_html,
            cc_email=cc_email,
            attachments=attachments,
        )
        return
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `docker compose exec backend pytest tests/unit/test_email_resend.py -v`
Expected: PASS (all six tests — two config + four backend).

- [ ] **Step 7: Lint**

Run: `docker compose exec backend ruff check app`
Expected: no new errors.

- [ ] **Step 8: Commit**

```bash
git add backend/app/services/email.py backend/tests/unit/test_email_resend.py
git commit -m "Add Resend native-API email backend"
```

---

### Task 3: Document the Resend env vars

**Files:**
- Modify: `backend/.env.example`
- Modify: `.env.coolify.example`

**Interfaces:**
- Consumes: env var names produced by Task 1.
- Produces: nothing (documentation only).

- [ ] **Step 1: Append Resend vars to `backend/.env.example`**

Add these lines to the end of `backend/.env.example`:

```
# Resend (native API). When RESEND_API_KEY is set it takes precedence over SMTP.
# The sender domain must be verified in Resend.
RESEND_API_KEY=
RESEND_FROM_EMAIL=noreply@wenhao.id
RESEND_FROM_NAME=Wenhao Dev Billing
```

- [ ] **Step 2: Append Resend vars to `.env.coolify.example`**

Add these lines to the end of `.env.coolify.example`:

```
# --- Resend email (native API; recommended over SMTP on Coolify where outbound
# SMTP ports are often blocked). When RESEND_API_KEY is set it takes precedence
# over any SMTP_* settings. The sender domain must be verified in Resend. ---
RESEND_API_KEY=
RESEND_FROM_EMAIL=noreply@wenhao.id
RESEND_FROM_NAME=Wenhao Dev Billing
```

- [ ] **Step 3: Commit**

```bash
git add backend/.env.example .env.coolify.example
git commit -m "Document Resend email env vars"
```

---

## Notes for the implementer

- These are pure unit tests — they mock `httpx.Client` and never hit the network, so `TEST_DATABASE_URL` is not required for `tests/unit/test_email_resend.py` specifically. If your `conftest.py` session fixtures still require Postgres, run the full stack via `docker compose up` first, or run just this file which should not need DB fixtures.
- `_FakeClient` is monkeypatched onto `email_service.httpx.Client`, so the real `httpx` is never called. The `smtplib.SMTP` "boom" guard in the first backend test proves the SMTP path is not taken when Resend is configured.
- Do not touch `send_password_reset_email()` — it calls `send_email()` and automatically benefits from the Resend branch.
