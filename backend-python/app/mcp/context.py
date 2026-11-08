"""Resolve the authenticated admin User for a tool call.

The MCP transport verifies the bearer token via ``BillingTokenVerifier``
(app.mcp.verifier) and stashes the resulting ``AccessToken`` on the request
context. Inside a tool body, ``fastmcp.server.dependencies.get_access_token``
returns that ``AccessToken``; its ``subject`` carries the ``user_id`` string
(see ``.superpowers/sdd/fastmcp-auth-findings.md``).
"""
from __future__ import annotations

from uuid import UUID

from fastmcp.server.dependencies import get_access_token
from sqlalchemy.orm import Session

from app.models.user import User


class ToolAuthError(Exception):
    pass


def authed_user_id() -> UUID:
    """Return the user_id of the currently-authenticated MCP principal.

    Must be called from within a tool invocation (i.e. inside an active
    FastMCP request context) — that's where ``get_access_token()`` resolves.
    """
    access_token = get_access_token()
    return UUID(access_token.subject)


def current_tool_user(db: Session, user_id: UUID) -> User:
    user = db.get(User, user_id)
    if user is None or user.role != "admin":
        raise ToolAuthError("authenticated principal is not an admin user")
    return user
