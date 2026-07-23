"""OAuth 2.1 authorization layer for the remote MCP endpoint.

Covers the JWT token primitives (app.mcp.tokens) and the authorization-code +
PKCE (S256) flow exposed by build_oauth_router() (app.mcp.oauth).
"""
from __future__ import annotations

import base64
import hashlib
import time
from urllib.parse import parse_qs, urlparse
from uuid import uuid4

import pytest

from app.mcp.tokens import (
    TokenError,
    mint_access_token,
    verify_access_token,
)


# ---------------------------------------------------------------------------
# Token primitives
# ---------------------------------------------------------------------------


def test_access_token_roundtrip():
    uid = uuid4()
    # Mint "now" so the token is within its TTL — the brief's literal now=1000
    # produces a token that expired in 1970 and can never verify.
    tok = mint_access_token(uid, now=int(time.time()))
    assert verify_access_token(tok) == uid


def test_expired_access_token_rejected():
    uid = uuid4()
    tok = mint_access_token(uid, now=1)  # long past
    with pytest.raises(TokenError):
        verify_access_token(tok)


def test_tampered_token_rejected():
    uid = uuid4()
    tok = mint_access_token(uid, now=int(time.time()))
    with pytest.raises(TokenError):
        verify_access_token(tok + "x")


def test_refresh_token_not_accepted_as_access_token():
    from app.mcp.tokens import mint_refresh_token

    uid = uuid4()
    tok = mint_refresh_token(uid, now=int(time.time()))
    with pytest.raises(TokenError):
        verify_access_token(tok)


# ---------------------------------------------------------------------------
# PKCE helper (test-side, mirrors the server's S256 verification)
# ---------------------------------------------------------------------------

_VERIFIER = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"


def _challenge_for(verifier: str) -> str:
    digest = hashlib.sha256(verifier.encode()).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode()


# ---------------------------------------------------------------------------
# Metadata
# ---------------------------------------------------------------------------


def test_authorization_server_metadata(client):
    r = client.get("/.well-known/oauth-authorization-server")
    assert r.status_code == 200
    body = r.json()
    assert body["code_challenge_methods_supported"] == ["S256"]
    assert body["authorization_endpoint"].endswith("/oauth/authorize")
    assert body["token_endpoint"].endswith("/oauth/token")
    assert body["registration_endpoint"].endswith("/oauth/register")


def test_protected_resource_metadata(client):
    r = client.get("/.well-known/oauth-protected-resource")
    assert r.status_code == 200
    body = r.json()
    assert "authorization_servers" in body


# ---------------------------------------------------------------------------
# Authorize
# ---------------------------------------------------------------------------


def test_authorize_redirects_to_login_when_anonymous(client):
    r = client.get(
        "/oauth/authorize",
        params={
            "response_type": "code",
            "client_id": "test",
            "redirect_uri": "http://localhost/cb",
            "code_challenge": _challenge_for(_VERIFIER),
            "code_challenge_method": "S256",
        },
        follow_redirects=False,
    )
    assert r.status_code in (302, 307)
    # Not an auth code — bounced to the frontend login.
    assert "code=" not in r.headers["location"]


def test_authorize_requires_login_then_issues_code(client, admin_session):
    r = client.get(
        "/oauth/authorize",
        params={
            "response_type": "code",
            "client_id": "test",
            "redirect_uri": "http://localhost/cb",
            "code_challenge": _challenge_for(_VERIFIER),
            "code_challenge_method": "S256",
        },
        follow_redirects=False,
    )
    assert r.status_code in (302, 307)
    assert "code=" in r.headers["location"]


# ---------------------------------------------------------------------------
# Full flow: register -> authorize -> token -> refresh -> revoke
# ---------------------------------------------------------------------------


def _authorize_and_get_code(client, *, client_id: str, redirect_uri: str) -> str:
    r = client.get(
        "/oauth/authorize",
        params={
            "response_type": "code",
            "client_id": client_id,
            "redirect_uri": redirect_uri,
            "code_challenge": _challenge_for(_VERIFIER),
            "code_challenge_method": "S256",
            "state": "xyz",
        },
        follow_redirects=False,
    )
    assert r.status_code in (302, 307)
    qs = parse_qs(urlparse(r.headers["location"]).query)
    assert qs["state"] == ["xyz"]
    return qs["code"][0]


def test_register_authorize_token_refresh_revoke(client, admin_session):
    # Dynamic client registration
    reg = client.post(
        "/oauth/register",
        json={"client_name": "cli", "redirect_uris": ["http://localhost/cb"]},
    )
    assert reg.status_code == 201
    cid = reg.json()["client_id"]
    assert reg.json()["redirect_uris"] == ["http://localhost/cb"]

    code = _authorize_and_get_code(client, client_id=cid, redirect_uri="http://localhost/cb")

    # Exchange code for tokens (PKCE verifier must match)
    tok = client.post(
        "/oauth/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": "http://localhost/cb",
            "client_id": cid,
            "code_verifier": _VERIFIER,
        },
    )
    assert tok.status_code == 200, tok.text
    body = tok.json()
    assert body["token_type"].lower() == "bearer"
    access = body["access_token"]
    refresh = body["refresh_token"]
    assert verify_access_token(access)  # a valid app user_id

    # Refresh grant rotates tokens
    ref = client.post(
        "/oauth/token",
        data={"grant_type": "refresh_token", "refresh_token": refresh, "client_id": cid},
    )
    assert ref.status_code == 200, ref.text
    new_refresh = ref.json()["refresh_token"]

    # The rotated-out refresh token is now revoked
    reused = client.post(
        "/oauth/token",
        data={"grant_type": "refresh_token", "refresh_token": refresh, "client_id": cid},
    )
    assert reused.status_code == 400

    # Revoke the current refresh token
    rev = client.post("/oauth/revoke", data={"token": new_refresh})
    assert rev.status_code == 200
    after = client.post(
        "/oauth/token",
        data={"grant_type": "refresh_token", "refresh_token": new_refresh, "client_id": cid},
    )
    assert after.status_code == 400


# ---------------------------------------------------------------------------
# FastMCP TokenVerifier hook
# ---------------------------------------------------------------------------


def test_token_verifier_accepts_valid_access_token():
    import asyncio

    from app.mcp.verifier import BillingTokenVerifier

    uid = uuid4()
    tok = mint_access_token(uid, now=int(time.time()))
    at = asyncio.run(BillingTokenVerifier().verify_token(tok))
    assert at is not None
    assert at.subject == str(uid)
    assert at.claims["user_id"] == str(uid)


def test_token_verifier_rejects_bad_token():
    import asyncio

    from app.mcp.verifier import BillingTokenVerifier

    at = asyncio.run(BillingTokenVerifier().verify_token("not-a-jwt"))
    assert at is None


def test_token_verifier_rejects_refresh_token():
    import asyncio

    from app.mcp.tokens import mint_refresh_token
    from app.mcp.verifier import BillingTokenVerifier

    tok = mint_refresh_token(uuid4(), now=int(time.time()))
    at = asyncio.run(BillingTokenVerifier().verify_token(tok))
    assert at is None


def test_token_rejects_wrong_pkce_verifier(client, admin_session):
    code = _authorize_and_get_code(
        client, client_id="test", redirect_uri="http://localhost/cb"
    )
    tok = client.post(
        "/oauth/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": "http://localhost/cb",
            "client_id": "test",
            "code_verifier": "wrong-verifier-value-that-does-not-match-challenge",
        },
    )
    assert tok.status_code == 400
