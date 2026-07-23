"""OAuth 2.1 authorization layer for the remote MCP endpoint.

Covers the JWT token primitives (app.mcp.tokens) and the authorization-code +
PKCE (S256) flow exposed by build_oauth_router() (app.mcp.oauth).
"""
from __future__ import annotations

import base64
import hashlib
import re
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


_REDIRECT_URI = "http://localhost/cb"


def _register(client, *, redirect_uris=None, client_name="cli") -> str:
    """Dynamically register a client, return its client_id."""
    reg = client.post(
        "/oauth/register",
        json={"client_name": client_name, "redirect_uris": redirect_uris or [_REDIRECT_URI]},
    )
    assert reg.status_code == 201, reg.text
    return reg.json()["client_id"]


def test_authorize_redirects_to_login_when_anonymous(client):
    cid = _register(client)
    r = client.get(
        "/oauth/authorize",
        params={
            "response_type": "code",
            "client_id": cid,
            "redirect_uri": _REDIRECT_URI,
            "code_challenge": _challenge_for(_VERIFIER),
            "code_challenge_method": "S256",
        },
        follow_redirects=False,
    )
    assert r.status_code in (302, 307)
    # Not an auth code — bounced to the frontend login.
    assert "code=" not in r.headers["location"]


def _authorize_params(cid, *, redirect_uri=_REDIRECT_URI, state=None):
    params = {
        "response_type": "code",
        "client_id": cid,
        "redirect_uri": redirect_uri,
        "code_challenge": _challenge_for(_VERIFIER),
        "code_challenge_method": "S256",
    }
    if state is not None:
        params["state"] = state
    return params


def _csrf_from(page_html: str) -> str:
    m = re.search(r'name="csrf_token" value="([^"]+)"', page_html)
    assert m, f"no csrf_token in consent page: {page_html[:400]}"
    return m.group(1)


def _consent_form_data(cid, csrf, *, redirect_uri=_REDIRECT_URI, state=None, decision="approve"):
    data = {
        "response_type": "code",
        "client_id": cid,
        "redirect_uri": redirect_uri,
        "code_challenge": _challenge_for(_VERIFIER),
        "code_challenge_method": "S256",
        "csrf_token": csrf,
        "decision": decision,
    }
    if state is not None:
        data["state"] = state
    return data


def test_authorize_get_renders_consent_and_mints_no_code(client, admin_session):
    """A GET on /oauth/authorize (even with a live admin session) must only
    render the consent page — it must NOT mint a code or redirect. This is the
    phishing defense: a cross-site GET link cannot silently obtain a code."""
    cid = _register(client)
    r = client.get("/oauth/authorize", params=_authorize_params(cid), follow_redirects=False)
    assert r.status_code == 200
    assert "location" not in {k.lower() for k in r.headers}
    assert "code=" not in r.text
    assert "csrf_token" in r.text
    assert _csrf_from(r.text)  # a token is present


def test_consent_post_without_csrf_mints_nothing(client, admin_session):
    cid = _register(client)
    client.get("/oauth/authorize", params=_authorize_params(cid))  # sets session csrf
    data = _consent_form_data(cid, csrf="")  # empty CSRF
    r = client.post("/oauth/authorize/consent", data=data, follow_redirects=False)
    assert r.status_code == 400
    assert "location" not in {k.lower() for k in r.headers}


def test_consent_post_with_wrong_csrf_mints_nothing(client, admin_session):
    cid = _register(client)
    client.get("/oauth/authorize", params=_authorize_params(cid))
    data = _consent_form_data(cid, csrf="not-the-real-token")
    r = client.post("/oauth/authorize/consent", data=data, follow_redirects=False)
    assert r.status_code == 400
    assert "location" not in {k.lower() for k in r.headers}


def test_consent_post_deny_mints_nothing(client, admin_session):
    cid = _register(client)
    page = client.get("/oauth/authorize", params=_authorize_params(cid))
    csrf = _csrf_from(page.text)
    data = _consent_form_data(cid, csrf, decision="deny")
    r = client.post("/oauth/authorize/consent", data=data, follow_redirects=False)
    assert r.status_code == 400
    assert "location" not in {k.lower() for k in r.headers}


def test_full_consent_flow_mints_code(client, admin_session):
    """GET consent -> POST approve with the session CSRF token -> 302 with code."""
    cid = _register(client)
    page = client.get("/oauth/authorize", params=_authorize_params(cid, state="xyz"))
    csrf = _csrf_from(page.text)
    r = client.post(
        "/oauth/authorize/consent",
        data=_consent_form_data(cid, csrf, state="xyz"),
        follow_redirects=False,
    )
    assert r.status_code in (302, 307)
    qs = parse_qs(urlparse(r.headers["location"]).query)
    assert qs["state"] == ["xyz"]
    assert "code" in qs


def test_authorize_refuses_non_admin_session(client, db):
    """A logged-in but non-admin user must not be able to mint an MCP token."""
    from passlib.context import CryptContext

    from app.models.user import User

    pwd = CryptContext(schemes=["bcrypt"], deprecated="auto")
    db.add(
        User(
            email="staff-authorize@example.com",
            password_hash=pwd.hash("test-password"),
            role="staff",
        )
    )
    db.flush()
    login = client.post(
        "/api/v1/auth/login",
        json={"email": "staff-authorize@example.com", "password": "test-password"},
    )
    assert login.status_code == 200, login.text

    cid = _register(client)
    r = client.get("/oauth/authorize", params=_authorize_params(cid), follow_redirects=False)
    assert r.status_code == 403
    assert "location" not in {k.lower() for k in r.headers}
    assert r.json()["error"] == "access_denied"


def test_authorize_rejects_unknown_client(client, admin_session):
    r = client.get(
        "/oauth/authorize",
        params={
            "response_type": "code",
            "client_id": "definitely-not-registered",
            "redirect_uri": _REDIRECT_URI,
            "code_challenge": _challenge_for(_VERIFIER),
            "code_challenge_method": "S256",
        },
        follow_redirects=False,
    )
    # Rejected outright — never a redirect (no open-redirect / code exfil).
    assert r.status_code == 401
    assert r.json()["error"] == "invalid_client"


def test_authorize_rejects_unregistered_redirect_uri(client, admin_session):
    cid = _register(client, redirect_uris=[_REDIRECT_URI])
    r = client.get(
        "/oauth/authorize",
        params={
            "response_type": "code",
            "client_id": cid,
            "redirect_uri": "http://attacker.example/steal",  # not registered
            "code_challenge": _challenge_for(_VERIFIER),
            "code_challenge_method": "S256",
        },
        follow_redirects=False,
    )
    # Must reject with a 4xx and NOT 302 to the attacker URI, and issue no code.
    assert r.status_code == 400
    assert r.json()["error"] == "invalid_request"
    assert "location" not in {k.lower() for k in r.headers}


# ---------------------------------------------------------------------------
# Full flow: register -> authorize -> token -> refresh -> revoke
# ---------------------------------------------------------------------------


def _authorize_and_get_code(client, *, client_id: str, redirect_uri: str) -> str:
    """Two-step consent flow: GET the consent page, extract the session-bound
    CSRF token, POST approval, and return the minted code from the redirect."""
    page = client.get(
        "/oauth/authorize",
        params=_authorize_params(client_id, redirect_uri=redirect_uri, state="xyz"),
    )
    assert page.status_code == 200, page.text
    csrf = _csrf_from(page.text)
    r = client.post(
        "/oauth/authorize/consent",
        data=_consent_form_data(client_id, csrf, redirect_uri=redirect_uri, state="xyz"),
        follow_redirects=False,
    )
    assert r.status_code in (302, 307), r.text
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


def test_token_verifier_accepts_valid_access_token_for_admin(db, mcp_tool_db, seed_admin):
    import asyncio

    from app.mcp.verifier import BillingTokenVerifier

    tok = mint_access_token(seed_admin, now=int(time.time()))
    at = asyncio.run(BillingTokenVerifier().verify_token(tok))
    assert at is not None
    assert at.subject == str(seed_admin)
    assert at.claims["user_id"] == str(seed_admin)


def test_token_verifier_rejects_non_admin_subject(db, mcp_tool_db):
    """A validly-signed token whose subject is a non-admin user is rejected
    (returns None -> 401): a signature is necessary but not sufficient."""
    import asyncio

    from passlib.context import CryptContext

    from app.models.user import User
    from app.mcp.verifier import BillingTokenVerifier

    pwd = CryptContext(schemes=["bcrypt"], deprecated="auto")
    staff = User(
        email="staff-verifier@example.com",
        password_hash=pwd.hash("x"),
        role="staff",
    )
    db.add(staff)
    db.flush()
    tok = mint_access_token(staff.user_id, now=int(time.time()))
    assert asyncio.run(BillingTokenVerifier().verify_token(tok)) is None


def test_token_verifier_rejects_deleted_subject(db, mcp_tool_db):
    """A validly-signed token whose subject no longer exists is rejected."""
    import asyncio

    from app.mcp.verifier import BillingTokenVerifier

    tok = mint_access_token(uuid4(), now=int(time.time()))  # no such user
    assert asyncio.run(BillingTokenVerifier().verify_token(tok)) is None


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
    cid = _register(client)
    code = _authorize_and_get_code(client, client_id=cid, redirect_uri=_REDIRECT_URI)
    tok = client.post(
        "/oauth/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": _REDIRECT_URI,
            "client_id": cid,
            "code_verifier": "wrong-verifier-value-that-does-not-match-challenge",
        },
    )
    assert tok.status_code == 400


def test_token_requires_client_id(client, admin_session):
    cid = _register(client)
    code = _authorize_and_get_code(client, client_id=cid, redirect_uri=_REDIRECT_URI)
    # Public client: omitting client_id must be rejected, not silently accepted.
    tok = client.post(
        "/oauth/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": _REDIRECT_URI,
            "code_verifier": _VERIFIER,
        },
    )
    assert tok.status_code == 400
    assert tok.json()["error"] == "invalid_request"


def test_refresh_reuse_revokes_successor_family(client, admin_session):
    """RFC 9700 reuse detection: replaying a rotated-out refresh token must
    also invalidate the still-valid successor issued during rotation."""
    cid = _register(client)
    code = _authorize_and_get_code(client, client_id=cid, redirect_uri=_REDIRECT_URI)
    tok = client.post(
        "/oauth/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": _REDIRECT_URI,
            "client_id": cid,
            "code_verifier": _VERIFIER,
        },
    )
    assert tok.status_code == 200, tok.text
    r1 = tok.json()["refresh_token"]

    # Rotate: r1 -> r2 (r1 becomes revoked).
    rot = client.post(
        "/oauth/token",
        data={"grant_type": "refresh_token", "refresh_token": r1, "client_id": cid},
    )
    assert rot.status_code == 200, rot.text
    r2 = rot.json()["refresh_token"]

    # Replay the rotated-out r1 -> reuse detected.
    reuse = client.post(
        "/oauth/token",
        data={"grant_type": "refresh_token", "refresh_token": r1, "client_id": cid},
    )
    assert reuse.status_code == 400

    # The successor r2 must now also be dead (whole family revoked).
    successor = client.post(
        "/oauth/token",
        data={"grant_type": "refresh_token", "refresh_token": r2, "client_id": cid},
    )
    assert successor.status_code == 400
