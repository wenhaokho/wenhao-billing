"""Minimal email sender with a no-op (logging) fallback for dev.

If SMTP is not configured (smtp_host unset), send_email logs the message
instead of raising — this keeps the password-reset flow functional in
single-tenant dev deployments without a real SMTP relay.
"""

from __future__ import annotations

import base64
import logging
import smtplib
from email.message import EmailMessage

import httpx

from app.config import get_settings

log = logging.getLogger(__name__)


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
            if resp.status_code >= 400:
                # Surface Resend's JSON error detail (e.g. domain-not-verified)
                # before raise_for_status() reduces it to an opaque status error.
                log.error(
                    "Resend rejected email to %s: %s %s",
                    to_email,
                    resp.status_code,
                    resp.text,
                )
            resp.raise_for_status()
    except Exception:
        log.exception("failed to send email via Resend to %s", to_email)
        raise

    log.info("sent email via Resend to=%s subject=%r", to_email, subject)


def send_email(
    *,
    to_email: str,
    subject: str,
    body_text: str,
    body_html: str | None = None,
    cc_email: str | None = None,
    attachments: list[tuple[str, bytes, str]] | None = None,
) -> None:
    """Send an email.

    attachments: list of (filename, content_bytes, mime_type) tuples, e.g.
                 ("invoice.pdf", b"...", "application/pdf").
    """
    settings = get_settings()

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

    if not settings.smtp_host:
        log.info(
            "[email disabled] would send to=%s cc=%s subject=%r attachments=%s body=%s",
            to_email,
            cc_email,
            subject,
            [a[0] for a in (attachments or [])],
            body_text,
        )
        return

    msg = EmailMessage()
    msg["From"] = f"{settings.smtp_from_name} <{settings.smtp_from_email}>"
    msg["To"] = to_email
    if settings.smtp_reply_to:
        msg["Reply-To"] = settings.smtp_reply_to
    if cc_email:
        msg["Cc"] = cc_email
    msg["Subject"] = subject
    msg.set_content(body_text)
    if body_html:
        msg.add_alternative(body_html, subtype="html")
    for filename, content, mime in attachments or []:
        maintype, _, subtype = mime.partition("/")
        msg.add_attachment(
            content, maintype=maintype or "application", subtype=subtype or "octet-stream",
            filename=filename,
        )

    try:
        with smtplib.SMTP(settings.smtp_host, settings.smtp_port, timeout=10) as smtp:
            if settings.smtp_use_tls:
                smtp.starttls()
            if settings.smtp_username and settings.smtp_password:
                smtp.login(settings.smtp_username, settings.smtp_password)
            smtp.send_message(msg)
    except Exception:
        log.exception("failed to send email to %s", to_email)
        raise


def send_password_reset_email(*, to_email: str, reset_link: str) -> None:
    subject = "Reset your wenhao-billing password"
    text = (
        "Hi,\n\n"
        "A password reset was requested for your wenhao-billing account.\n"
        "Open the link below within the next hour to choose a new password:\n\n"
        f"{reset_link}\n\n"
        "If you didn't request this, you can ignore this email — your current "
        "password will stay active.\n"
    )
    html = (
        "<p>Hi,</p>"
        "<p>A password reset was requested for your wenhao-billing account.</p>"
        f"<p><a href=\"{reset_link}\">Reset your password</a> "
        "(link expires in 1 hour).</p>"
        "<p>If you didn't request this, you can ignore this email.</p>"
    )
    send_email(to_email=to_email, subject=subject, body_text=text, body_html=html)
