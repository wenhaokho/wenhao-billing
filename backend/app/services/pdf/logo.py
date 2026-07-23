"""Resolve the document logo to an embeddable data URI.

The Account page stores uploaded logos directly as base64 data URIs in
BusinessProfile.logo_url, so the common case is a passthrough. An http(s)
URL is fetched and encoded; anything else falls back to the bundled logo.
"""
from __future__ import annotations

import logging
from base64 import b64encode

import httpx

from app.services.pdf.assets import default_logo_data_uri

log = logging.getLogger(__name__)


def resolve_logo(business) -> str:
    url = getattr(business, "logo_url", None) if business is not None else None
    if not url:
        return default_logo_data_uri()
    if url.startswith("data:"):
        return url
    if url.startswith(("http://", "https://")):
        try:
            resp = httpx.get(url, timeout=5.0)
            resp.raise_for_status()
            mime = resp.headers.get("content-type", "image/png").split(";")[0]
            return f"data:{mime};base64," + b64encode(resp.content).decode("ascii")
        except Exception:
            log.warning("failed to fetch logo %s; using default", url, exc_info=True)
    return default_logo_data_uri()
