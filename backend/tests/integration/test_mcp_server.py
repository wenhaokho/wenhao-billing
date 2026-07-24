"""FastMCP server plumbing: injectable tool session + importable http app.

Covers:
- the happy path (tool_session sees fixture-seeded data through mcp_tool_db)
- the fail-loud guard rail (tool_session without mcp_tool_db must never
  silently touch the real database)
- composability of mcp_tool_db with HTTP calls through `client`/`admin_session`
  (no SAWarning, writes visible both ways, nothing leaks to the real DB)
- rollback-on-error inside tool_session()
- unauthenticated /mcp/ requests are rejected (401), guarding against an
  accidental auth=None on the FastMCP instance
"""
from __future__ import annotations

import warnings

import pytest
from sqlalchemy import text
from sqlalchemy.exc import SAWarning

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


def test_tool_session_without_mcp_tool_db_fails_loud():
    """A test (or, later, real tool code) that calls tool_session() without
    opting into `mcp_tool_db` must fail immediately and clearly rather than
    silently opening a session against the real database."""
    with pytest.raises(RuntimeError, match="mcp_tool_db"):
        with tool_session():
            pass


def test_tool_session_rolls_back_on_error(db, mcp_tool_db):
    """A partial write inside `with tool_session():` must not persist if the
    block raises — the context manager rolls back on exception."""

    class _Boom(Exception):
        pass

    with pytest.raises(_Boom):
        with tool_session() as s:
            s.add(
                User(
                    email="rollback-on-error@example.com",
                    password_hash="x",
                    role="admin",
                )
            )
            s.flush()  # assign a PK / hit the DB, but don't commit
            raise _Boom("simulated failure mid-tool")

    with tool_session() as s2:
        found = (
            s2.query(User).filter_by(email="rollback-on-error@example.com").first()
        )
        assert found is None


def test_mcp_tool_db_composes_with_client_http_commits(client, db, mcp_tool_db, admin_session):
    """mcp_tool_db must compose safely with `client`/`admin_session`, whose
    get_db override commits directly on `db` — the same connection
    mcp_tool_db binds tool_session to. This exercises Task 5+'s shape:
    write via a tool, read back over the HTTP API, with no SAWarning from
    either side's commits, and nothing ever leaks to the real database.
    """
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")

        # 1) Write directly via a tool (tool_session commits on success).
        with tool_session() as s:
            s.add(
                User(
                    email="tool-written@example.com",
                    password_hash="x",
                    role="admin",
                )
            )

        # 2) Write via the real HTTP API on the same connection — exercises
        #    db.commit() (inside create_user) running right after a tool's
        #    commit on the same shared connection.
        r = admin_session.post(
            "/api/v1/users",
            json={"email": "http-written@example.com", "password": "test-password"},
        )
        assert r.status_code == 201, r.text

    sa_warnings = [w for w in caught if issubclass(w.category, SAWarning)]
    assert not sa_warnings, [str(w.message) for w in sa_warnings]

    # 3) Both writes are visible through the HTTP API (shared connection).
    r = admin_session.get("/api/v1/users")
    assert r.status_code == 200
    emails = {u["email"] for u in r.json()}
    assert "tool-written@example.com" in emails
    assert "http-written@example.com" in emails

    # 4) Neither write ever actually committed to the real database — a
    #    fresh connection outside this test's (uncommitted) outer
    #    transaction can't see them.
    from app.db.session import engine as real_engine

    with real_engine.connect() as raw:
        rows = raw.execute(
            text("SELECT email FROM users WHERE email IN (:a, :b)"),
            {"a": "tool-written@example.com", "b": "http-written@example.com"},
        ).all()
        assert rows == []


def test_mcp_requires_auth(client):
    """Guards against an accidental `auth=None` on FastMCP(...): the /mcp
    endpoint must reject unauthenticated requests with 401."""
    r = client.post(
        "/mcp/",
        json={"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}},
        headers={"Accept": "application/json, text/event-stream"},
    )
    assert r.status_code == 401


@pytest.mark.parametrize("method", ["GET", "POST", "DELETE"])
def test_mcp_bare_path_redirects_to_trailing_slash(client, method):
    """A client that connects to `/mcp` (no trailing slash) must be routed to
    the mounted FastMCP app, not fall through to the SPA catch-all (405/404).
    The redirect is a 307 so the method and JSON-RPC body are preserved."""
    r = client.request(
        method,
        "/mcp",
        headers={"Accept": "application/json, text/event-stream"},
        follow_redirects=False,
    )
    assert r.status_code == 307
    assert r.headers["location"] == "/mcp/"
