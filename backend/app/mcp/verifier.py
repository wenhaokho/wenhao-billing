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

from collections.abc import Callable

from fastmcp.server.auth import AccessToken, TokenVerifier
from sqlalchemy.orm import Session

from app.config import get_settings
from app.db.session import SessionLocal
from app.mcp.context import ToolAuthError, current_tool_user
from app.mcp.tokens import TokenError, verify_access_token

# Stable synthetic client id reported for every MCP session — the real
# principal is the app user carried in ``subject``.
_MCP_CLIENT_ID = "billing-mcp"

# Short-lived DB session used only to re-check that the token subject is still a
# live admin. Production uses SessionLocal (a fresh, quickly-closed session in
# the request path — deliberately NOT a tool_session, which is for tool bodies).
# Tests override this to bind the check to their rollback-safe connection.
_session_factory: Callable[[], Session] = SessionLocal


def set_verifier_session_factory(factory: Callable[[], Session]) -> None:
    """Override the admin-check session factory (tests bind it to the test
    connection so the subject user seeded in the per-test transaction is
    visible)."""
    global _session_factory
    _session_factory = factory


def reset_verifier_session_factory() -> None:
    global _session_factory
    _session_factory = SessionLocal


class BillingTokenVerifier(TokenVerifier):
    """Verify MCP bearer tokens against the app's OAuth access tokens."""

    def __init__(self) -> None:
        super().__init__(resource_base_url=get_settings().mcp_public_base_url)

    async def verify_token(self, token: str) -> AccessToken | None:
        try:
            user_id = verify_access_token(token)
        except TokenError:
            return None  # -> 401

        # A valid signature is not enough: the subject must still exist AND
        # still be an admin (revoked/demoted/deleted users lose MCP access
        # immediately, without waiting for the short token TTL to lapse). The
        # admin predicate lives in one place — app.mcp.context.current_tool_user.
        db = _session_factory()
        try:
            current_tool_user(db, user_id)
        except ToolAuthError:
            return None  # -> 401
        finally:
            db.close()

        return AccessToken(
            token=token,
            client_id=_MCP_CLIENT_ID,
            scopes=[],
            subject=str(user_id),
            claims={"user_id": str(user_id)},
        )
