"""FastMCP server plumbing: injectable tool session + importable http app."""
from __future__ import annotations

from app.mcp.db import tool_session
from app.models.user import User


def test_tool_session_uses_injected_factory(db, mcp_tool_db, seed_admin):
    # mcp_tool_db binds tool_session to the test connection.
    # seed_admin: fixture inserting one admin User into `db`; returns its id.
    with tool_session() as s:
        u = s.get(User, seed_admin)
        assert u is not None  # tool sees fixture-seeded data via shared connection


def test_mcp_app_importable():
    from app.mcp.server import mcp_http_app

    assert mcp_http_app is not None
