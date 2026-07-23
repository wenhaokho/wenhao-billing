"""FastMCP server: auth verifier + tool registry. Tools are registered by
importing the module in app.mcp.tools (added in later phases)."""
from __future__ import annotations

from fastmcp import FastMCP

# Auth-provider wiring: BillingTokenVerifier (Task 2) validates the bearer
# token via app.mcp.tokens.verify_access_token and surfaces user_id as
# AccessToken.subject; returning None on TokenError yields a 401.
from app.mcp.verifier import BillingTokenVerifier

mcp = FastMCP("wenhao-billing", auth=BillingTokenVerifier())


@mcp.tool
def ping() -> str:
    """Health check — returns 'pong'."""
    return "pong"


# Build the ASGI app for streamable-HTTP transport. path="/" so that once
# app.main mounts this sub-app at "/mcp", the effective route is "/mcp"
# (not "/mcp/mcp" — FastMCP's default internal path is itself "/mcp").
# This sub-app carries its own lifespan (session-manager startup/shutdown);
# app.main must pass mcp_http_app.lifespan through to the outer FastAPI app
# for it to run.
mcp_http_app = mcp.http_app(path="/", transport="streamable-http")
