from __future__ import annotations

from typing import Any

import httpx

from app.config import get_settings
from app.models.cloudflare_target import CloudflareTarget


class CloudflareError(RuntimeError):
    pass


def _headers() -> dict[str, str]:
    settings = get_settings()
    if not settings.cloudflare_api_token:
        raise CloudflareError("cloudflare_api_token is not configured")
    return {
        "Authorization": f"Bearer {settings.cloudflare_api_token}",
        "Content-Type": "application/json",
    }


def _parse_error(payload: dict[str, Any]) -> str:
    errors = payload.get("errors") or []
    if not errors:
        return "unknown Cloudflare error"
    first = errors[0]
    if isinstance(first, dict):
        code = first.get("code")
        message = first.get("message") or "Cloudflare request failed"
        return f"{code}: {message}" if code else str(message)
    return str(first)


def _update_target(target: CloudflareTarget, content: str) -> None:
    settings = get_settings()
    payload = {
        "type": target.record_type,
        "name": target.record_name,
        "content": content,
        "proxied": target.proxied,
    }
    url = (
        f"{settings.cloudflare_api_base_url}/zones/{target.zone_id}"
        f"/dns_records/{target.record_id}"
    )
    with httpx.Client(timeout=settings.cloudflare_timeout_seconds) as client:
        response = client.patch(url, headers=_headers(), json=payload)
    response.raise_for_status()
    body = response.json()
    if not body.get("success", False):
        raise CloudflareError(_parse_error(body))


def suspend_target(target: CloudflareTarget) -> None:
    _update_target(target, target.maintenance_content)


def restore_target(target: CloudflareTarget) -> None:
    _update_target(target, target.live_content)
