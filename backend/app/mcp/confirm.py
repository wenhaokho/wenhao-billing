"""Stateless HMAC confirm-tokens for gated MCP actions (email / delete).

A token binds (action, canonical-payload, expiry). It cannot be replayed for a
different action or a mutated payload, and expires quickly. No DB storage.
"""
from __future__ import annotations

import hashlib
import hmac
import json
import time

from app.config import get_settings

_TTL_SECONDS = 300  # 5 minutes


class ConfirmError(Exception):
    pass


def _canonical(action: str, payload: dict, expiry: int) -> bytes:
    body = {"action": action, "payload": payload, "expiry": expiry}
    return json.dumps(body, sort_keys=True, separators=(",", ":")).encode()


def _sign(msg: bytes) -> str:
    secret = get_settings().mcp_confirm_secret.encode()
    return hmac.new(secret, msg, hashlib.sha256).hexdigest()


def make_confirm_token(action: str, payload: dict, *, now: int | None = None) -> str:
    now = int(time.time()) if now is None else now
    expiry = now + _TTL_SECONDS
    sig = _sign(_canonical(action, payload, expiry))
    return f"{expiry}.{sig}"


def verify_confirm_token(token: str, action: str, payload: dict, *, now: int | None = None) -> None:
    now = int(time.time()) if now is None else now
    try:
        expiry_str, sig = token.split(".", 1)
        expiry = int(expiry_str)
    except (ValueError, AttributeError) as e:
        raise ConfirmError("malformed confirm_token") from e
    if now > expiry:
        raise ConfirmError("confirm_token expired — re-request the action")
    expected = _sign(_canonical(action, payload, expiry))
    if not hmac.compare_digest(expected, sig):
        raise ConfirmError("confirm_token does not match this action/payload")
