"""JWT access/refresh tokens for the MCP OAuth layer, keyed to the app User.

Access tokens are short-lived bearer JWTs the MCP transport verifies on every
request (see the ``TokenVerifier`` below / Task 3). Refresh tokens carry a
``jti`` so a matching row in ``oauth_refresh_tokens`` can revoke them.
All tokens are HMAC-signed with ``settings.mcp_token_secret``; the secret is
read at call time (never cached at import) so tests that swap settings work.
"""
from __future__ import annotations

import time
from uuid import UUID, uuid4

import jwt

from app.config import get_settings

_ALG = "HS256"


class TokenError(Exception):
    """Raised when a token is malformed, expired, tampered, or the wrong type."""


def _secret() -> str:
    return get_settings().mcp_token_secret


def _decode(token: str) -> dict:
    try:
        return jwt.decode(token, _secret(), algorithms=[_ALG])
    except jwt.PyJWTError as e:  # expired, bad signature, malformed, ...
        raise TokenError(str(e)) from e


def mint_access_token(user_id: UUID, *, now: int | None = None) -> str:
    now = int(time.time()) if now is None else now
    s = get_settings()
    payload = {
        "sub": str(user_id),
        "typ": "access",
        "iat": now,
        "exp": now + s.mcp_access_token_ttl_seconds,
    }
    return jwt.encode(payload, _secret(), algorithm=_ALG)


def mint_refresh_token(
    user_id: UUID, *, now: int | None = None, jti: str | None = None
) -> str:
    """Mint a refresh token. A ``jti`` is embedded (and generated if not given)
    so the issuer can persist it in ``oauth_refresh_tokens`` for revocation."""
    now = int(time.time()) if now is None else now
    s = get_settings()
    payload = {
        "sub": str(user_id),
        "typ": "refresh",
        "iat": now,
        "exp": now + s.mcp_refresh_token_ttl_seconds,
        "jti": jti or uuid4().hex,
    }
    return jwt.encode(payload, _secret(), algorithm=_ALG)


def verify_access_token(token: str) -> UUID:
    """Return the ``user_id`` carried by a valid access token, else ``TokenError``."""
    payload = _decode(token)
    if payload.get("typ") != "access":
        raise TokenError("not an access token")
    return UUID(payload["sub"])


def verify_refresh_token(token: str) -> tuple[UUID, str]:
    """Return ``(user_id, jti)`` for a valid refresh token, else ``TokenError``.

    Signature/expiry are checked here; revocation (jti present + not revoked)
    is the caller's responsibility since it requires a DB lookup.
    """
    payload = _decode(token)
    if payload.get("typ") != "refresh":
        raise TokenError("not a refresh token")
    jti = payload.get("jti")
    if not jti:
        raise TokenError("refresh token missing jti")
    return UUID(payload["sub"]), jti
