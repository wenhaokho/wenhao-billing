"""End-to-end smoke test: real OAuth 2.1 + PKCE flow, then a real
authenticated MCP tool call, both driven against the real `app.main` ASGI
app in-process.

Speaks MCP with fastmcp's own ``Client`` (``StreamableHttpTransport``) over
an ``httpx.ASGITransport`` bound to the *exact* app object the ``client``
fixture already has ``get_db`` overridden onto the rollback-safe test
connection (``client.app``) — so the MCP calls and the HTTP calls made
through ``client``/``admin_session`` see the same seeded data, and the
FastMCP sub-app's session manager (started by ``client``'s own
``with TestClient(app):`` lifespan) is already running.

Chain asserted at the TRANSPORT level (real bearer token, real streamable-
HTTP JSON-RPC over the mounted ``/mcp`` route — nothing here calls a tool
function directly):

1. log in via ``/api/v1/auth/login`` (``admin_session``)
2. ``/oauth/register`` -> ``/oauth/authorize`` -> ``/oauth/token`` (PKCE) to
   obtain a real access token, mirroring ``test_mcp_oauth.py``
3. MCP ``initialize`` + ``tools/call`` for ``ping`` and ``list_invoices``
   with ``Authorization: Bearer <token>`` -> success, real data visible
4. the same ``/mcp`` route with a bad bearer token -> 401
5. ``tools/call`` for ``send_invoice_email`` with no ``confirm_token`` ->
   ``{"requires_confirmation": True, ...}`` and no email sent
"""
from __future__ import annotations

import asyncio
import base64
import hashlib
from urllib.parse import parse_qs, urlparse

import httpx
from fastmcp import Client
from fastmcp.client.transports import StreamableHttpTransport

# Mirrors test_mcp_oauth.py's PKCE fixture verifier/challenge pair.
_VERIFIER = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
_REDIRECT_URI = "http://localhost/cb"


def _challenge_for(verifier: str) -> str:
    digest = hashlib.sha256(verifier.encode()).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode()


def _obtain_access_token(client) -> str:
    """Dynamic client registration -> authorize (using the already-logged-in
    session on `client`) -> code exchange. Same shape as
    test_mcp_oauth.py::test_register_authorize_token_refresh_revoke."""
    reg = client.post(
        "/oauth/register",
        json={"client_name": "e2e-smoke", "redirect_uris": [_REDIRECT_URI]},
    )
    assert reg.status_code == 201, reg.text
    cid = reg.json()["client_id"]

    auth = client.get(
        "/oauth/authorize",
        params={
            "response_type": "code",
            "client_id": cid,
            "redirect_uri": _REDIRECT_URI,
            "code_challenge": _challenge_for(_VERIFIER),
            "code_challenge_method": "S256",
            "state": "e2e",
        },
        follow_redirects=False,
    )
    assert auth.status_code in (302, 307), auth.text
    qs = parse_qs(urlparse(auth.headers["location"]).query)
    assert qs["state"] == ["e2e"]
    code = qs["code"][0]

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
    return tok.json()["access_token"]


def _mcp_client(app, token: str) -> Client:
    """A real fastmcp Client speaking streamable-HTTP JSON-RPC to the real
    app in-process (ASGITransport, no socket) carrying a bearer token —
    exactly what a real remote MCP client does against the deployed server,
    minus the network hop."""

    def factory(headers=None, timeout=None, auth=None, **kwargs):
        return httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app),
            base_url="http://testserver",
            headers=headers,
            auth=auth,
            timeout=timeout or httpx.Timeout(30.0),
        )

    transport = StreamableHttpTransport(
        url="http://testserver/mcp/",
        auth=token,  # bare string -> fastmcp.client.auth.bearer.BearerAuth
        httpx_client_factory=factory,
    )
    return Client(transport)


# ---------------------------------------------------------------------------
# The chain
# ---------------------------------------------------------------------------


def test_e2e_oauth_then_authenticated_tool_calls(
    client, admin_session, mcp_tool_db, seed_invoice
):
    token = _obtain_access_token(client)

    async def _run():
        async with _mcp_client(client.app, token) as mcp:
            ping = await mcp.call_tool("ping")
            assert ping.data == "pong"

            invoices = await mcp.call_tool("list_invoices")
            ids = {inv["invoice_id"] for inv in invoices.data}
            assert str(seed_invoice) in ids

    asyncio.run(_run())


def test_e2e_bad_token_rejected(client):
    """A request against the real /mcp route carrying a bogus bearer token
    must 401 — mirrors test_mcp_server.py::test_mcp_requires_auth but with a
    (invalid) token present rather than absent, exercising the reject path
    of BillingTokenVerifier.verify_token rather than the auth=None guard."""
    r = client.post(
        "/mcp/",
        json={"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}},
        headers={
            "Accept": "application/json, text/event-stream",
            "Authorization": "Bearer not-a-real-token",
        },
    )
    assert r.status_code == 401


def test_e2e_send_invoice_email_requires_confirmation_over_transport(
    client, admin_session, mcp_tool_db, seed_sent_invoice
):
    """send_invoice_email called through the real MCP transport, with no
    confirm_token, must preview-and-refuse rather than send anything."""
    inv_id, _customer_id = seed_sent_invoice
    token = _obtain_access_token(client)

    async def _run():
        async with _mcp_client(client.app, token) as mcp:
            result = await mcp.call_tool(
                "send_invoice_email",
                {"invoice_id": str(inv_id), "to_email": "a@b.com"},
            )
            return result.data

    data = asyncio.run(_run())
    assert data["requires_confirmation"] is True
    assert data["action"] == "send_invoice_email"
    assert "confirm_token" in data
    assert data["recipient"] == "a@b.com"
