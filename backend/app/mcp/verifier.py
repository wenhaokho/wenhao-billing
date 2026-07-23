"""FastMCP ``TokenVerifier`` for the billing MCP endpoint.

The MCP transport calls ``verify_token`` on every request with the raw bearer
string. We validate it as an app access token (app.mcp.tokens) and surface the
``user_id`` as the ``AccessToken.subject`` so tool handlers can resolve the
principal via ``fastmcp.server.dependencies.get_access_token()`` (Task 3).

Import contract confirmed against FastMCP 3.4.3 (see
``.superpowers/sdd/fastmcp-auth-findings.md``): ``verify_token`` is async,
returns ``AccessToken`` on success or ``None`` (→ 401) on failure.
"""
from __future__ import annotations

from fastmcp.server.auth import AccessToken, TokenVerifier

from app.config import get_settings
from app.mcp.tokens import TokenError, verify_access_token

# Stable synthetic client id reported for every MCP session — the real
# principal is the app user carried in ``subject``.
_MCP_CLIENT_ID = "billing-mcp"


class BillingTokenVerifier(TokenVerifier):
    """Verify MCP bearer tokens against the app's OAuth access tokens."""

    def __init__(self) -> None:
        super().__init__(resource_base_url=get_settings().mcp_public_base_url)

    async def verify_token(self, token: str) -> AccessToken | None:
        try:
            user_id = verify_access_token(token)
        except TokenError:
            return None  # -> 401
        return AccessToken(
            token=token,
            client_id=_MCP_CLIENT_ID,
            scopes=[],
            subject=str(user_id),
            claims={"user_id": str(user_id)},
        )
