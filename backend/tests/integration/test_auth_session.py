"""Session lifetime / "Keep me logged in" behavior.

The login cookie is always signed with the long (30d) remember TTL, but the
*effective* expiry is enforced server-side via ``session["exp"]``:
  - without ``remember`` → 8h (``session_max_age_seconds``)
  - with ``remember``    → 30d (``session_remember_max_age_seconds``)
``deps.current_admin`` rejects a request once ``time.time() >= exp``.
"""

from __future__ import annotations

import pytest
from passlib.context import CryptContext

from app.config import get_settings
from app.models.user import User

_PWD = CryptContext(schemes=["bcrypt"], deprecated="auto")


@pytest.fixture()
def admin(db):
    user = User(
        email="session-admin@example.com",
        password_hash=_PWD.hash("test-password"),
        role="admin",
    )
    db.add(user)
    db.flush()
    return user


def _login(client, *, remember: bool):
    r = client.post(
        "/api/v1/auth/login",
        json={
            "email": "session-admin@example.com",
            "password": "test-password",
            "remember": remember,
        },
    )
    assert r.status_code == 200, r.text
    return r


def _advance_time(monkeypatch, seconds: float):
    """Make ``deps.current_admin`` believe ``seconds`` have elapsed."""
    import app.api.deps as deps

    base = deps.time.time()
    monkeypatch.setattr(deps.time, "time", lambda: base + seconds)


def test_login_defaults_to_no_remember(client, admin):
    """A bare login (no ``remember``) still authenticates — field is optional."""
    r = client.post(
        "/api/v1/auth/login",
        json={"email": "session-admin@example.com", "password": "test-password"},
    )
    assert r.status_code == 200, r.text
    assert client.get("/api/v1/auth/me").status_code == 200


def test_short_session_expires_after_default_ttl(client, admin, monkeypatch):
    _login(client, remember=False)
    assert client.get("/api/v1/auth/me").status_code == 200

    # Jump just past the 8h short TTL — the session must be rejected.
    _advance_time(monkeypatch, get_settings().session_max_age_seconds + 60)
    assert client.get("/api/v1/auth/me").status_code == 401


def test_remember_session_survives_short_ttl(client, admin, monkeypatch):
    _login(client, remember=True)

    # Well past 8h but far short of 30d — a remembered session stays valid.
    _advance_time(monkeypatch, get_settings().session_max_age_seconds + 3600)
    assert client.get("/api/v1/auth/me").status_code == 200


def test_remember_session_expires_after_remember_ttl(client, admin, monkeypatch):
    _login(client, remember=True)

    _advance_time(monkeypatch, get_settings().session_remember_max_age_seconds + 60)
    assert client.get("/api/v1/auth/me").status_code == 401
