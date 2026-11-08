"""FastMCP server: auth verifier + tool registry. Tools are registered by
importing the module in app.mcp.tools (added in later phases)."""
from __future__ import annotations

from urllib.parse import urlsplit

from fastmcp import FastMCP

from app.config import get_settings

# Auth-provider wiring: BillingTokenVerifier (Task 2) validates the bearer
# token via app.mcp.tokens.verify_access_token and surfaces user_id as
# AccessToken.subject; returning None on TokenError yields a 401.
from app.mcp.verifier import BillingTokenVerifier

mcp = FastMCP("wenhao-billing", auth=BillingTokenVerifier())


@mcp.tool
def ping() -> str:
    """Health check — returns 'pong'."""
    return "pong"


import app.mcp.tools  # noqa: F401  (registers all tool modules)


def _transport_allowlist(base_url: str) -> tuple[list[str], list[str]]:
    """Derive (allowed_hosts, allowed_origins) for FastMCP's DNS-rebinding guard
    from the public base URL.

    FastMCP's HostOriginGuardMiddleware rejects any Host header not in
    DEFAULT_HOSTS (127.0.0.1/localhost/::1) plus the ASGI scope["server"] host.
    Behind a reverse proxy (Coolify → uvicorn) that server host is the
    container's internal bind, NOT the public domain, so requests to the real
    domain get a 421 "Misdirected Request". Allow-listing the public host keeps
    the guard ON while trusting the deployed origin.
    """
    parsed = urlsplit(base_url)
    hosts = [h for h in {parsed.hostname, parsed.netloc} if h]
    origins = (
        [f"{parsed.scheme}://{parsed.netloc}"]
        if parsed.scheme and parsed.netloc
        else []
    )
    return hosts, origins


_allowed_hosts, _allowed_origins = _transport_allowlist(
    get_settings().mcp_public_base_url
)

# Build the ASGI app for streamable-HTTP transport. path="/" so that once
# app.main mounts this sub-app at "/mcp", the effective route is "/mcp"
# (not "/mcp/mcp" — FastMCP's default internal path is itself "/mcp").
# This sub-app carries its own lifespan (session-manager startup/shutdown);
# app.main must pass mcp_http_app.lifespan through to the outer FastAPI app
# for it to run.
mcp_http_app = mcp.http_app(
    path="/",
    transport="streamable-http",
    allowed_hosts=_allowed_hosts,
    allowed_origins=_allowed_origins,
)
