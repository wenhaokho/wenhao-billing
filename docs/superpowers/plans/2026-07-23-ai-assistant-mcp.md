# AI Assistant (MCP Server) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a remote MCP server, in-process with the FastAPI backend, that lets the operator drive every billing module in natural language from Claude Desktop/Code — reusing existing services, gated only on customer emails and hard deletes.

**Architecture:** A FastMCP server registers tools that call the existing `app.services.*` functions and `app.models.*` queries against a shared DB session. It is mounted as a sub-app on the existing FastAPI instance and exposed over streamable-HTTP at `/mcp`. An OAuth 2.1 authorization-code layer (backed by the existing `User` + password login, built on `authlib`) protects the endpoint; a bearer token is validated on every MCP request. Two action classes (send-email, hard-delete) require a stateless HMAC confirm-token.

**Tech Stack:** Python 3.12, FastAPI, SQLAlchemy 2.0, `fastmcp==3.4.3`, `authlib` (OAuth AS), `pyjwt`, Postgres 16, pytest (real Postgres per `tests/conftest.py`).

## Global Constraints

- **`fastmcp==3.4.3`** — pin exactly; its bundled OAuth *proxy* has open advisory GHSA-5h2m-4q8j-pqpj and MUST NOT be used. We build the AS ourselves.
- **All money is `DECIMAL(19,4)`.** Never float. Use `Decimal`.
- **`business_id` scoping** — this is single-tenant single-`User`; there is no `business_id` column in these models. "Scoping" here = auth resolves to the existing `admin` `User` and every tool runs as that user. Do not add a `business_id` filter that doesn't exist; do enforce auth before dispatch.
- **Currency is never auto-converted.** Surface mismatches; never resolve them.
- **Writes go through `app.services.*`** — never hand-roll invoice/ledger logic in the tool layer.
- **Autonomous** for all internal/reversible actions; **confirm-token gated** ONLY for `send_*_email` and `delete_*`.
- **Ambiguity stops** — a lookup matching >1 record returns candidates and performs no action.
- Alembic revision ids **< 32 chars**.
- Tests require a real Postgres and skip when `TEST_DATABASE_URL` is unset.

---

## Phase 0 — Dependencies & config

### Task 0: Add dependencies and settings

**Files:**
- Modify: `backend/pyproject.toml` (dependencies)
- Modify: `backend/app/config.py:42-44` (add settings after cloudflare block)
- Test: `backend/tests/unit/test_mcp_config.py`

**Interfaces:**
- Produces: `Settings.mcp_confirm_secret: str`, `Settings.mcp_token_secret: str`, `Settings.mcp_access_token_ttl_seconds: int`, `Settings.mcp_enabled: bool`.

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/unit/test_mcp_config.py
from app.config import Settings


def test_mcp_settings_have_secure_defaults_shape():
    s = Settings(
        mcp_confirm_secret="x" * 32,
        mcp_token_secret="y" * 32,
    )
    assert s.mcp_enabled is True
    assert s.mcp_access_token_ttl_seconds == 3600
    assert len(s.mcp_confirm_secret) >= 32
    assert len(s.mcp_token_secret) >= 32
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_mcp_config.py -v`
Expected: FAIL — `Settings` has no `mcp_confirm_secret`.

- [ ] **Step 3: Add dependencies to `backend/pyproject.toml`**

In the `dependencies = [...]` array add:

```toml
    "fastmcp==3.4.3",
    "authlib>=1.3,<2",
    "pyjwt>=2.8,<3",
```

- [ ] **Step 4: Add settings to `backend/app/config.py`**

Insert after line 44 (the `cloudflare_timeout_seconds` field), still inside `Settings`:

```python
    # --- MCP assistant ---
    mcp_enabled: bool = Field(default=True)
    # HMAC secret for stateless confirm-tokens (email / delete gating).
    mcp_confirm_secret: str = Field(default="dev-mcp-confirm-secret-change-me", min_length=16)
    # Signing secret for OAuth access/refresh tokens.
    mcp_token_secret: str = Field(default="dev-mcp-token-secret-change-me", min_length=16)
    mcp_access_token_ttl_seconds: int = Field(default=3600)
    mcp_refresh_token_ttl_seconds: int = Field(default=60 * 60 * 24 * 30)
    # Public origin the MCP endpoint is served from (for OAuth metadata URLs).
    mcp_public_base_url: str = Field(default="http://localhost:8000")
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_mcp_config.py -v`
Expected: PASS.

- [ ] **Step 6: Install deps and commit**

```bash
cd backend && pip install -e .[dev]
git add pyproject.toml app/config.py tests/unit/test_mcp_config.py
git commit -m "feat(mcp): add fastmcp/authlib deps and MCP settings"
```

---

## Phase 1 — Walking skeleton (auth-gated, one read tool, end-to-end)

Goal of this phase: a running MCP endpoint at `/mcp` that requires a valid bearer token and exposes exactly one working tool (`ping`) plus one real read tool (`get_business_profile`). This de-risks transport + auth + in-process DB before any breadth.

### Task 1: Confirm-token utility (stateless HMAC)

**Files:**
- Create: `backend/app/mcp/__init__.py` (empty)
- Create: `backend/app/mcp/confirm.py`
- Test: `backend/tests/unit/test_mcp_confirm.py`

**Interfaces:**
- Produces:
  - `make_confirm_token(action: str, payload: dict, *, now: int | None = None) -> str`
  - `verify_confirm_token(token: str, action: str, payload: dict, *, now: int | None = None) -> None` — raises `ConfirmError` on mismatch/expiry.
  - `class ConfirmError(Exception)`

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/unit/test_mcp_confirm.py
import pytest

from app.mcp.confirm import ConfirmError, make_confirm_token, verify_confirm_token

PAYLOAD = {"target_id": "abc", "amount": "100.0000", "currency": "USD", "recipient": "a@b.com"}


def test_roundtrip_ok():
    t = make_confirm_token("send_invoice_email", PAYLOAD, now=1000)
    verify_confirm_token(t, "send_invoice_email", PAYLOAD, now=1100)  # within TTL


def test_rejects_wrong_action():
    t = make_confirm_token("send_invoice_email", PAYLOAD, now=1000)
    with pytest.raises(ConfirmError):
        verify_confirm_token(t, "delete_invoice", PAYLOAD, now=1100)


def test_rejects_mutated_payload():
    t = make_confirm_token("send_invoice_email", PAYLOAD, now=1000)
    mutated = {**PAYLOAD, "amount": "999.0000"}
    with pytest.raises(ConfirmError):
        verify_confirm_token(t, "send_invoice_email", mutated, now=1100)


def test_rejects_expired():
    t = make_confirm_token("send_invoice_email", PAYLOAD, now=1000)
    with pytest.raises(ConfirmError):
        verify_confirm_token(t, "send_invoice_email", PAYLOAD, now=1000 + 10_000)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_mcp_confirm.py -v`
Expected: FAIL — module `app.mcp.confirm` not found.

- [ ] **Step 3: Implement `backend/app/mcp/confirm.py`**

```python
"""Stateless HMAC confirm-tokens for gated MCP actions (email / delete).

A token binds (action, canonical-payload, expiry). It cannot be replayed for a
different action or a mutated payload, and expires quickly. No DB storage.
"""
from __future__ import annotations

import hashlib
import hmac
import json
import time

from app.config import get_settings

_TTL_SECONDS = 300  # 5 minutes


class ConfirmError(Exception):
    pass


def _canonical(action: str, payload: dict, expiry: int) -> bytes:
    body = {"action": action, "payload": payload, "expiry": expiry}
    return json.dumps(body, sort_keys=True, separators=(",", ":")).encode()


def _sign(msg: bytes) -> str:
    secret = get_settings().mcp_confirm_secret.encode()
    return hmac.new(secret, msg, hashlib.sha256).hexdigest()


def make_confirm_token(action: str, payload: dict, *, now: int | None = None) -> str:
    now = int(time.time()) if now is None else now
    expiry = now + _TTL_SECONDS
    sig = _sign(_canonical(action, payload, expiry))
    return f"{expiry}.{sig}"


def verify_confirm_token(token: str, action: str, payload: dict, *, now: int | None = None) -> None:
    now = int(time.time()) if now is None else now
    try:
        expiry_str, sig = token.split(".", 1)
        expiry = int(expiry_str)
    except (ValueError, AttributeError) as e:
        raise ConfirmError("malformed confirm_token") from e
    if now > expiry:
        raise ConfirmError("confirm_token expired — re-request the action")
    expected = _sign(_canonical(action, payload, expiry))
    if not hmac.compare_digest(expected, sig):
        raise ConfirmError("confirm_token does not match this action/payload")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_mcp_confirm.py -v`
Expected: PASS (all 4).

- [ ] **Step 5: Commit**

```bash
git add app/mcp/__init__.py app/mcp/confirm.py tests/unit/test_mcp_confirm.py
git commit -m "feat(mcp): stateless HMAC confirm-token utility"
```

### Task 2: OAuth authorization layer (SPIKE + build)

> This task carries a **verification spike** because the exact FastMCP 3.4.3 auth-provider hook and `authlib` AS wiring must be confirmed against the installed versions before committing code. The spike's deliverable is a throwaway `spike_mcp_auth.py` that proves one authenticated request; the real code is then written against the verified API. This is a task, not a placeholder.

**Files:**
- Create (throwaway, deleted at end): `backend/spike_mcp_auth.py`
- Create: `backend/app/mcp/oauth.py` (authlib-based AS: metadata, register, authorize, token)
- Create: `backend/app/mcp/tokens.py` (JWT mint/verify against `User`)
- Create: `backend/app/models/oauth_client.py` (dynamic client registrations + issued refresh tokens)
- Create: `backend/alembic/versions/<rev>_mcp_oauth.py` (migration; revision id `mcp_oauth`, < 32 chars)
- Test: `backend/tests/integration/test_mcp_oauth.py`

**Interfaces:**
- Produces:
  - `mint_access_token(user_id: UUID, *, now: int | None = None) -> str` and `mint_refresh_token(...)` in `tokens.py`
  - `verify_access_token(token: str) -> UUID` — returns `user_id`, raises `TokenError`
  - `build_oauth_router() -> APIRouter` in `oauth.py` mounting: `/.well-known/oauth-authorization-server`, `/.well-known/oauth-protected-resource`, `/oauth/register`, `/oauth/authorize`, `/oauth/token`, `/oauth/revoke`
  - `class TokenError(Exception)`

- [ ] **Step 1: Spike — verify FastMCP auth hook + token flow**

Write `backend/spike_mcp_auth.py` that:
1. Builds a `fastmcp.FastMCP("spike")` with one `@mcp.tool` returning `"pong"`.
2. Uses FastMCP 3.4.3's token-verifier/auth-provider hook (confirm the exact class name in the installed package: run `python -c "import fastmcp, pkgutil; print(fastmcp.__version__); import fastmcp.server.auth as a; print(dir(a))"`).
3. Gets `mcp.http_app(transport="streamable-http")`.
4. Mounts it on a throwaway FastAPI app.
Run it with uvicorn and hit it with an MCP client (or `curl` the initialize handshake) once **without** a token (expect 401) and once **with** a hand-minted JWT (expect the tool list).
Record the confirmed import paths/class names as comments at the top of `spike_mcp_auth.py`.

Run: `cd backend && python -c "import fastmcp; print(fastmcp.__version__)"`
Expected: `3.4.3`.

- [ ] **Step 2: Write the failing integration test**

```python
# backend/tests/integration/test_mcp_oauth.py
import time
import pytest
from uuid import uuid4

from app.mcp.tokens import TokenError, mint_access_token, verify_access_token


def test_access_token_roundtrip():
    uid = uuid4()
    tok = mint_access_token(uid, now=1000)
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/integration/test_mcp_oauth.py -v`
Expected: FAIL — `app.mcp.tokens` not found.

- [ ] **Step 4: Implement `backend/app/mcp/tokens.py`**

```python
"""JWT access/refresh tokens for the MCP OAuth layer, keyed to the app User."""
from __future__ import annotations

import time
from uuid import UUID

import jwt

from app.config import get_settings

_ALG = "HS256"


class TokenError(Exception):
    pass


def _secret() -> str:
    return get_settings().mcp_token_secret


def mint_access_token(user_id: UUID, *, now: int | None = None) -> str:
    now = int(time.time()) if now is None else now
    s = get_settings()
    payload = {"sub": str(user_id), "typ": "access", "iat": now,
               "exp": now + s.mcp_access_token_ttl_seconds}
    return jwt.encode(payload, _secret(), algorithm=_ALG)


def mint_refresh_token(user_id: UUID, *, now: int | None = None) -> str:
    now = int(time.time()) if now is None else now
    s = get_settings()
    payload = {"sub": str(user_id), "typ": "refresh", "iat": now,
               "exp": now + s.mcp_refresh_token_ttl_seconds}
    return jwt.encode(payload, _secret(), algorithm=_ALG)


def verify_access_token(token: str) -> UUID:
    try:
        payload = jwt.decode(token, _secret(), algorithms=[_ALG])
    except jwt.PyJWTError as e:
        raise TokenError(str(e)) from e
    if payload.get("typ") != "access":
        raise TokenError("not an access token")
    return UUID(payload["sub"])
```

- [ ] **Step 5: Run token test to verify it passes**

Run: `cd backend && python -m pytest tests/integration/test_mcp_oauth.py -v`
Expected: PASS (3).

- [ ] **Step 6: Implement OAuth model + migration**

Create `backend/app/models/oauth_client.py`:

```python
"""Dynamically-registered OAuth clients + issued refresh tokens (revocation)."""
from __future__ import annotations

from datetime import datetime
from uuid import uuid4

from sqlalchemy import DateTime, String, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base  # NOTE: confirm the declarative Base import path in Step 6a


class OAuthClient(Base):
    __tablename__ = "oauth_clients"

    client_id: Mapped[str] = mapped_column(String, primary_key=True,
                                            default=lambda: uuid4().hex)
    client_name: Mapped[str | None] = mapped_column(String, nullable=True)
    redirect_uris: Mapped[str] = mapped_column(String, nullable=False)  # space-delimited
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True),
                                                 server_default=func.now())


class OAuthRefreshToken(Base):
    __tablename__ = "oauth_refresh_tokens"

    jti: Mapped[str] = mapped_column(String, primary_key=True)
    user_id: Mapped[str] = mapped_column(String, nullable=False)
    revoked: Mapped[bool] = mapped_column(default=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True),
                                                 server_default=func.now())
```

- [ ] **Step 6a: Confirm the declarative `Base` import path**

Run: `cd backend && python -c "import app.db.base as b; print([x for x in dir(b) if 'Base' in x])"`
If `app.db.base` doesn't expose `Base`, find it: `grep -rn "declarative_base\|class Base" app/models app/db`
Fix the import in `oauth_client.py` to match an existing model's Base import (open any file in `app/models/` and copy its Base import).

- [ ] **Step 6b: Generate and apply migration**

```bash
cd backend && alembic revision --autogenerate -m "mcp_oauth" && alembic upgrade head
```
Open the generated file, confirm revision id is `mcp_oauth` (rename if autogen made it longer than 32 chars), confirm it creates both tables.

- [ ] **Step 7: Implement `backend/app/mcp/oauth.py`**

Build an `APIRouter` (prefix `""`) using `authlib.integrations.starlette_client` / `authlib.oauth2` for the AS endpoints. Wire the confirmed API surface from the Step 1 spike. Endpoints:
- `GET /.well-known/oauth-protected-resource` and `GET /.well-known/oauth-authorization-server` → JSON metadata (issuer = `settings.mcp_public_base_url`, authorization/token/registration endpoints, `code_challenge_methods_supported: ["S256"]`).
- `POST /oauth/register` → dynamic client registration: persist `OAuthClient`, return `client_id` + registered `redirect_uris`.
- `GET /oauth/authorize` → if `request.session.get("user_id")` is set, issue an auth code (signed, short-TTL, PKCE `code_challenge` bound) and 302 back to `redirect_uri`; else 302 to the existing frontend login (`settings.app_base_url`) with a return URL back to `/oauth/authorize`.
- `POST /oauth/token` → validate PKCE `code_verifier`, exchange auth code → `mint_access_token` + `mint_refresh_token` (persist refresh `jti`); support `grant_type=refresh_token` (reject if `jti` revoked).
- `POST /oauth/revoke` → mark refresh `jti` revoked.

Reuse `current_admin`'s session mechanism — the operator's existing browser login satisfies `authorize`.

- [ ] **Step 8: Add an authorize→token integration test**

```python
# append to backend/tests/integration/test_mcp_oauth.py
def test_authorize_requires_login_then_issues_code(client, admin_session):
    # admin_session: fixture that logs in via POST /api/v1/auth/login and keeps the cookie
    r = client.get("/oauth/authorize", params={
        "response_type": "code", "client_id": "test", "redirect_uri": "http://localhost/cb",
        "code_challenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", "code_challenge_method": "S256",
    }, follow_redirects=False)
    assert r.status_code in (302, 307)
    assert "code=" in r.headers["location"]
```

(If `admin_session`/`client` fixtures don't exist, add them to `tests/conftest.py` following the existing app/session pattern — log in through `/api/v1/auth/login`.)

- [ ] **Step 9: Run OAuth tests**

Run: `cd backend && python -m pytest tests/integration/test_mcp_oauth.py -v`
Expected: PASS.

- [ ] **Step 10: Delete the spike and commit**

```bash
rm backend/spike_mcp_auth.py
git add app/mcp/oauth.py app/mcp/tokens.py app/models/oauth_client.py alembic/versions/*mcp_oauth* tests/integration/test_mcp_oauth.py tests/conftest.py
git commit -m "feat(mcp): OAuth 2.1 authorization layer backed by existing login"
```

### Task 3: MCP server, DB-session helper, auth binding, and mount

**Files:**
- Create: `backend/app/mcp/server.py` (FastMCP instance + auth verifier + `ping`)
- Create: `backend/app/mcp/db.py` (session helper for tools)
- Create: `backend/app/mcp/context.py` (resolve authed `User` from request)
- Modify: `backend/app/main.py:24-60` (mount MCP sub-app + OAuth router when `mcp_enabled`)
- Test: `backend/tests/integration/test_mcp_server.py`

**Interfaces:**
- Consumes: `verify_access_token` (Task 2), `SessionLocal` (`app/db/session.py`).
- Produces:
  - `tool_session() -> ContextManager[Session]` in `db.py` — commits on success, rolls back on exception, always closes.
  - `mcp` (the `FastMCP` instance) and `mcp_http_app` in `server.py`.
  - `current_tool_user(db: Session) -> User` in `context.py`.

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/integration/test_mcp_server.py
from app.mcp.db import tool_session
from app.models.user import User


def test_tool_session_commits_and_closes(seed_admin):
    # seed_admin: fixture inserting one admin User; returns its id
    with tool_session() as db:
        u = db.get(User, seed_admin)
        assert u is not None


def test_mcp_app_importable():
    from app.mcp.server import mcp_http_app
    assert mcp_http_app is not None
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/integration/test_mcp_server.py -v`
Expected: FAIL — `app.mcp.db` not found.

- [ ] **Step 3: Implement `backend/app/mcp/db.py`**

```python
"""Per-tool DB session: commit on success, rollback on error, always close.

Tools run outside FastAPI's request scope, so they manage their own session."""
from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager

from sqlalchemy.orm import Session

from app.db.session import SessionLocal


@contextmanager
def tool_session() -> Iterator[Session]:
    db = SessionLocal()
    try:
        yield db
        db.commit()
    except Exception:
        db.rollback()
        raise
    finally:
        db.close()
```

- [ ] **Step 4: Implement `backend/app/mcp/context.py`**

```python
"""Resolve the authenticated admin User for a tool call."""
from __future__ import annotations

from uuid import UUID

from sqlalchemy.orm import Session

from app.models.user import User


class ToolAuthError(Exception):
    pass


def current_tool_user(db: Session, user_id: UUID) -> User:
    user = db.get(User, user_id)
    if user is None or user.role != "admin":
        raise ToolAuthError("authenticated principal is not an admin user")
    return user
```

- [ ] **Step 5: Implement `backend/app/mcp/server.py`**

```python
"""FastMCP server: auth verifier + tool registry. Tools are registered by
importing the module in app.mcp.tools (added in later phases)."""
from __future__ import annotations

from fastmcp import FastMCP

# Auth-provider wiring: use the class/hook CONFIRMED in the Task 2 spike.
# It must call app.mcp.tokens.verify_access_token(bearer) and reject on TokenError.
from app.mcp.tokens import TokenError, verify_access_token  # noqa: F401

mcp = FastMCP("wenhao-billing")


@mcp.tool
def ping() -> str:
    """Health check — returns 'pong'."""
    return "pong"


# Build the ASGI app for streamable-HTTP transport, attaching the verified
# auth provider (exact constructor confirmed in the spike).
mcp_http_app = mcp.http_app(transport="streamable-http")
```

> Wire the auth provider exactly as the spike confirmed. The verifier extracts the `Authorization: Bearer` token and calls `verify_access_token`; on `TokenError` it returns 401. The resolved `user_id` must be reachable from tool context (FastMCP request state) so tools can call `current_tool_user`.

- [ ] **Step 6: Mount on FastAPI in `backend/app/main.py`**

After the routers are included (after line 58), before `return app`:

```python
    if settings.mcp_enabled:
        from app.mcp.oauth import build_oauth_router
        from app.mcp.server import mcp_http_app

        app.include_router(build_oauth_router())  # /.well-known/*, /oauth/*
        app.mount("/mcp", mcp_http_app)
```

Also confirm the FastMCP app's lifespan is honored — if `http_app()` needs a lifespan, pass it to `FastAPI(lifespan=...)` per the spike findings.

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd backend && python -m pytest tests/integration/test_mcp_server.py -v`
Expected: PASS (2).

- [ ] **Step 8: Manual smoke test**

```bash
cd backend && uvicorn app.main:app --port 8000
```
In another shell, confirm `GET /.well-known/oauth-protected-resource` returns JSON and `/mcp` without a token returns 401. Stop the server.

- [ ] **Step 9: Commit**

```bash
git add app/mcp/server.py app/mcp/db.py app/mcp/context.py app/main.py tests/integration/test_mcp_server.py
git commit -m "feat(mcp): mount auth-gated FastMCP server with ping tool"
```

### Task 4: First real read tool — `get_business_profile` + serialization helper

**Files:**
- Create: `backend/app/mcp/serialize.py` (model → plain dict for tool returns)
- Create: `backend/app/mcp/tools/__init__.py` (imports each tool module so registration runs)
- Create: `backend/app/mcp/tools/read_misc.py` (`get_business_profile`, `get_stats`)
- Modify: `backend/app/mcp/server.py` (import `app.mcp.tools` so tools register)
- Test: `backend/tests/integration/test_mcp_read_misc.py`

**Interfaces:**
- Consumes: `tool_session` (Task 3), models `BusinessProfile`, stats service.
- Produces: `to_dict(obj, fields: list[str]) -> dict` in `serialize.py`; registered tools `get_business_profile()`, `get_stats()`.

- [ ] **Step 1: Inspect the real shapes**

Run: `grep -n "class BusinessProfile" -r backend/app/models` and open `backend/app/api/v1/routers/business_profile.py` and `backend/app/services/stats.py` to copy the exact field names and the stats function signature. Use those exact names below (replace the illustrative field list if it differs).

- [ ] **Step 2: Write the failing test**

```python
# backend/tests/integration/test_mcp_read_misc.py
from app.mcp.tools.read_misc import get_business_profile


def test_get_business_profile_returns_dict(seed_business_profile):
    # seed_business_profile: fixture inserting one BusinessProfile row
    result = get_business_profile.fn()  # .fn accesses the undecorated callable
    assert isinstance(result, dict)
    assert "legal_name" in result or "name" in result
```

> Confirm in the spike/Task 3 how FastMCP exposes the raw function (`.fn`). If different, adjust the test to call the underlying function directly.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/integration/test_mcp_read_misc.py -v`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement `backend/app/mcp/serialize.py`**

```python
"""Serialize SQLAlchemy rows to JSON-safe dicts for MCP tool returns."""
from __future__ import annotations

from datetime import date, datetime
from decimal import Decimal
from typing import Any
from uuid import UUID


def _coerce(v: Any) -> Any:
    if isinstance(v, Decimal):
        return str(v)          # preserve exact money
    if isinstance(v, (datetime, date)):
        return v.isoformat()
    if isinstance(v, UUID):
        return str(v)
    return v


def to_dict(obj: Any, fields: list[str]) -> dict:
    return {f: _coerce(getattr(obj, f)) for f in fields}
```

- [ ] **Step 5: Implement `backend/app/mcp/tools/read_misc.py`**

```python
"""Misc read tools: business profile, stats."""
from __future__ import annotations

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.business_profile import BusinessProfile  # confirm module/class name

# Replace with the ACTUAL column names confirmed in Step 1:
_PROFILE_FIELDS = ["legal_name", "contact_email", "address", "base_currency"]


@mcp.tool
def get_business_profile() -> dict:
    """Return the agency's business profile (name, contact, address, currency)."""
    with tool_session() as db:
        row = db.scalar(select(BusinessProfile))
        if row is None:
            return {}
        return to_dict(row, _PROFILE_FIELDS)


@mcp.tool
def get_stats() -> dict:
    """Return dashboard/summary figures (revenue, outstanding, counts)."""
    from app.services import stats as stats_service  # confirm function name in Step 1
    with tool_session() as db:
        data = stats_service.get_dashboard_stats(db)  # replace with real fn
        return data if isinstance(data, dict) else dict(data)
```

- [ ] **Step 6: Create `backend/app/mcp/tools/__init__.py`**

```python
"""Importing this package registers all tools on the FastMCP instance."""
from app.mcp.tools import read_misc  # noqa: F401
```

- [ ] **Step 7: Register tools by importing the package in `server.py`**

Add at the bottom of `backend/app/mcp/server.py`, AFTER `mcp = FastMCP(...)` and the `ping` tool, and BEFORE `mcp_http_app = ...`:

```python
import app.mcp.tools  # noqa: E402,F401  (registers all tool modules)
```

- [ ] **Step 8: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/integration/test_mcp_read_misc.py -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add app/mcp/serialize.py app/mcp/tools/ app/mcp/server.py tests/integration/test_mcp_read_misc.py
git commit -m "feat(mcp): serialization helper + business-profile/stats read tools"
```

**Phase 1 exit criteria:** `uvicorn` serves `/mcp`; unauthenticated calls 401; a client with a minted token can call `ping`, `get_business_profile`, `get_stats`.

---

## Phase 2 — Read tools across all modules

Each read tool follows the Task 4 pattern exactly: `@mcp.tool` → `with tool_session() as db:` → `select(...)` (copy the query from the module's router) → `to_dict`/list-comprehension. Lookups that can match multiple rows return `{"candidates": [...]}` and take no further action (ambiguity-stop).

### Task 5: Invoice + customer read tools

**Files:**
- Create: `backend/app/mcp/tools/read_invoices.py`
- Create: `backend/app/mcp/tools/read_customers.py`
- Modify: `backend/app/mcp/tools/__init__.py` (add imports)
- Test: `backend/tests/integration/test_mcp_read_invoices.py`, `test_mcp_read_customers.py`

**Interfaces:**
- Produces registered tools with these EXACT signatures (later write tools depend on the resolver):
  - `list_invoices(status: list[str] | None = None, customer_id: str | None = None, invoice_type: str | None = None, limit: int = 200) -> list[dict]`
  - `get_invoice(invoice_id: str) -> dict`
  - `list_customers(query: str | None = None, limit: int = 200) -> list[dict]`
  - `get_customer(customer_id: str) -> dict`
  - `resolve_customer(query: str) -> dict` — returns `{"customer_id": "..."}` on unique match, `{"candidates": [{customer_id, name}, ...]}` on 0 or many. **Ambiguity-stop primitive reused by write tools.**

- [ ] **Step 1: Write the failing test (resolver is the important one)**

```python
# backend/tests/integration/test_mcp_read_customers.py
from app.mcp.tools.read_customers import resolve_customer, list_customers


def test_resolve_unique_match(seed_customer):
    # seed_customer: inserts customer name="Acme Pte Ltd"; returns its id
    out = resolve_customer.fn(query="Acme")
    assert out["customer_id"] == str(seed_customer)


def test_resolve_ambiguous_returns_candidates(seed_two_acme):
    out = resolve_customer.fn(query="Acme")
    assert "candidates" in out
    assert len(out["candidates"]) == 2
    assert "customer_id" not in out  # took NO action


def test_resolve_no_match_returns_empty_candidates(seed_customer):
    out = resolve_customer.fn(query="Nonexistent Corp")
    assert out.get("candidates") == []
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/integration/test_mcp_read_customers.py -v`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `backend/app/mcp/tools/read_customers.py`**

```python
"""Customer read tools + the ambiguity-stop resolver."""
from __future__ import annotations

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.customer import Customer

_CUSTOMER_FIELDS = ["customer_id", "name", "contact_email"]  # confirm real columns


@mcp.tool
def list_customers(query: str | None = None, limit: int = 200) -> list[dict]:
    """List customers, optionally filtered by a name substring."""
    with tool_session() as db:
        stmt = select(Customer).order_by(Customer.name).limit(limit)
        if query:
            stmt = stmt.where(Customer.name.ilike(f"%{query}%"))
        return [to_dict(c, _CUSTOMER_FIELDS) for c in db.scalars(stmt)]


@mcp.tool
def get_customer(customer_id: str) -> dict:
    """Fetch one customer by id."""
    from uuid import UUID
    with tool_session() as db:
        c = db.get(Customer, UUID(customer_id))
        return to_dict(c, _CUSTOMER_FIELDS) if c else {}


@mcp.tool
def resolve_customer(query: str) -> dict:
    """Resolve a customer name to a single id. On 0 or >1 matches, returns
    {"candidates": [...]} and takes no action (ambiguity-stop)."""
    with tool_session() as db:
        rows = list(db.scalars(
            select(Customer).where(Customer.name.ilike(f"%{query}%")).limit(10)
        ))
        if len(rows) == 1:
            return {"customer_id": str(rows[0].customer_id)}
        return {"candidates": [
            {"customer_id": str(c.customer_id), "name": c.name} for c in rows
        ]}
```

- [ ] **Step 4: Implement `backend/app/mcp/tools/read_invoices.py`**

Mirror the invoice router's `list_invoices` (lines 319-343) and `get_invoice` (375-384) queries:

```python
"""Invoice read tools."""
from __future__ import annotations

from uuid import UUID

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.invoice import Invoice

_INVOICE_FIELDS = [
    "invoice_id", "invoice_number", "customer_id", "invoice_type", "currency",
    "amount", "balance_due", "status", "issue_date", "due_date",
]


@mcp.tool
def list_invoices(status: list[str] | None = None, customer_id: str | None = None,
                  invoice_type: str | None = None, limit: int = 200) -> list[dict]:
    """List non-template invoices with optional status/customer/type filters."""
    with tool_session() as db:
        stmt = (select(Invoice).where(Invoice.is_template.is_(False))
                .order_by(Invoice.created_at.desc()).limit(min(limit, 500)))
        if status:
            stmt = stmt.where(Invoice.status.in_(status))
        if customer_id:
            stmt = stmt.where(Invoice.customer_id == UUID(customer_id))
        if invoice_type:
            stmt = stmt.where(Invoice.invoice_type == invoice_type)
        return [to_dict(i, _INVOICE_FIELDS) for i in db.scalars(stmt)]


@mcp.tool
def get_invoice(invoice_id: str) -> dict:
    """Fetch one invoice by id."""
    with tool_session() as db:
        inv = db.get(Invoice, UUID(invoice_id))
        return to_dict(inv, _INVOICE_FIELDS) if inv else {}
```

- [ ] **Step 5: Register both modules in `__init__.py`**

```python
from app.mcp.tools import read_misc, read_invoices, read_customers  # noqa: F401
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd backend && python -m pytest tests/integration/test_mcp_read_customers.py tests/integration/test_mcp_read_invoices.py -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add app/mcp/tools/read_invoices.py app/mcp/tools/read_customers.py app/mcp/tools/__init__.py tests/integration/test_mcp_read_invoices.py tests/integration/test_mcp_read_customers.py
git commit -m "feat(mcp): invoice + customer read tools with ambiguity-stop resolver"
```

### Task 6: Remaining read tools (bills, quotations, projects, items, vendors, reconciliation)

**Files:**
- Create: `backend/app/mcp/tools/read_bills.py`, `read_quotations.py`, `read_projects.py`, `read_catalog.py` (items + vendors), `read_recon.py`
- Modify: `backend/app/mcp/tools/__init__.py`
- Test: `backend/tests/integration/test_mcp_reads_all.py`

**Interfaces — one tool per row, each following the Task 5 pattern. Copy the exact `select(...)` from the named router and the exact column list from the named model:**

| Tool | Model | Copy query from |
|------|-------|-----------------|
| `list_bills(status=None, vendor_id=None, limit=200)` | `Bill` | `routers/bills.py` list endpoint |
| `get_bill(bill_id)` | `Bill` | `routers/bills.py` get endpoint |
| `list_quotations(status=None, customer_id=None, limit=200)` | `Quotation` | `routers/quotations.py` |
| `get_quotation(quotation_id)` | `Quotation` | `routers/quotations.py` |
| `list_projects(customer_id=None, limit=200)` | `Project` | `routers/projects.py` |
| `get_project(project_id)` | `Project` | `routers/projects.py` |
| `list_items(query=None, limit=200)` | `Item` | `routers/items.py` |
| `list_vendors(query=None, limit=200)` | `Vendor` | `routers/vendors.py` |
| `resolve_vendor(query)` | `Vendor` | mirror `resolve_customer` exactly |
| `get_reconciliation_queue(limit=200)` | `Payment` where `status='PENDING_MANUAL_REVIEW'` | `routers/recon.py` |

- [ ] **Step 1: For EACH tool above, write a test** asserting it returns a list/dict and that filters work, using existing seed fixtures (or add per-model fixtures to `conftest.py`). One test file `test_mcp_reads_all.py`; one test function per tool. Example for bills:

```python
def test_list_bills_returns_list(seed_bill):
    from app.mcp.tools.read_bills import list_bills
    rows = list_bills.fn(limit=10)
    assert isinstance(rows, list)
```

- [ ] **Step 2: Run to verify they fail.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_reads_all.py -v` → FAIL (modules missing).

- [ ] **Step 3: Implement each module** following the Task 5 pattern precisely — `@mcp.tool` + `tool_session` + the query copied from the router in the table + `to_dict` with the model's real columns. `resolve_vendor` is a verbatim copy of `resolve_customer` with `Vendor`/`vendor_id`.

- [ ] **Step 4: Add all modules to `__init__.py`.**

- [ ] **Step 5: Run to verify they pass.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_reads_all.py -v` → PASS.

- [ ] **Step 6: Commit**

```bash
git add app/mcp/tools/read_*.py app/mcp/tools/__init__.py tests/integration/test_mcp_reads_all.py
git commit -m "feat(mcp): read tools for bills, quotations, projects, catalog, recon"
```

---

## Phase 3 — Autonomous write tools

Write tools call the existing `app.services.*` functions (never re-implement logic). They build the service's Pydantic payload from tool args, call the service, and return the serialized result. `tool_session` commits automatically. Ambiguous name inputs are resolved via the resolvers from Phase 2 and stop on ambiguity.

### Task 7: Invoice write tools (create / update / finalize / void / recurring)

**Files:**
- Create: `backend/app/mcp/tools/write_invoices.py`
- Modify: `backend/app/mcp/tools/__init__.py`
- Test: `backend/tests/integration/test_mcp_write_invoices.py`

**Interfaces:**
- Consumes: `invoicing.create_invoice/update_invoice/finalize_invoice/void_invoice/create_recurring_template/trigger_recurring_cycle` (all confirmed in `services/invoicing.py`), `InvoiceCreate/InvoiceUpdate/RecurringTemplateCreate` schemas, `resolve_customer`.
- Produces:
  - `create_invoice(customer_id: str, currency: str, line_items: list[dict], invoice_type: str = "MILESTONE", discount_type: str | None = None, discount_value: str | None = None, payment_terms: str | None = None, notes: str | None = None) -> dict`
  - `update_invoice(invoice_id: str, changes: dict) -> dict`
  - `finalize_invoice(invoice_id: str) -> dict`
  - `void_invoice(invoice_id: str) -> dict`
  - `create_recurring_template(...) -> dict`, `trigger_recurring(template_id: str, cycle_key: str) -> dict`

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/integration/test_mcp_write_invoices.py
from decimal import Decimal
from app.mcp.tools.write_invoices import create_invoice, finalize_invoice


def test_create_invoice_makes_draft(seed_customer):
    out = create_invoice.fn(
        customer_id=str(seed_customer), currency="USD",
        line_items=[{"description": "Design", "quantity": "40", "unit_price": "100"}],
    )
    assert out["status"] == "DRAFT"
    assert out["amount"] == "4000.0000"


def test_finalize_moves_draft_to_sent(seed_customer):
    created = create_invoice.fn(
        customer_id=str(seed_customer), currency="USD",
        line_items=[{"description": "x", "quantity": "1", "unit_price": "10"}],
    )
    out = finalize_invoice.fn(invoice_id=created["invoice_id"])
    assert out["status"] == "SENT"
```

- [ ] **Step 2: Run to verify it fails.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_write_invoices.py -v` → FAIL.

- [ ] **Step 3: Implement `backend/app/mcp/tools/write_invoices.py`**

```python
"""Invoice write tools — thin wrappers over app.services.invoicing."""
from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_invoices import _INVOICE_FIELDS
from app.schemas.invoice import InvoiceCreate, InvoiceLineItemIn, InvoiceUpdate
from app.services import invoicing


def _lines(raw: list[dict]) -> list[InvoiceLineItemIn]:
    return [InvoiceLineItemIn(
        description=ln["description"],
        quantity=Decimal(str(ln["quantity"])),
        unit_price=Decimal(str(ln["unit_price"])),
        item_id=UUID(ln["item_id"]) if ln.get("item_id") else None,
    ) for ln in raw]


@mcp.tool
def create_invoice(customer_id: str, currency: str, line_items: list[dict],
                   invoice_type: str = "MILESTONE", discount_type: str | None = None,
                   discount_value: str | None = None, payment_terms: str | None = None,
                   notes: str | None = None) -> dict:
    """Create a DRAFT invoice (lands in Awaiting Finalization). Autonomous."""
    payload = InvoiceCreate(
        customer_id=UUID(customer_id), currency=currency,
        invoice_type=invoice_type, line_items=_lines(line_items),
        discount_type=discount_type,
        discount_value=Decimal(discount_value) if discount_value else None,
        payment_terms=payment_terms, notes=notes,
    )
    with tool_session() as db:
        inv = invoicing.create_invoice(db, payload)
        db.flush()
        return to_dict(inv, _INVOICE_FIELDS)


@mcp.tool
def update_invoice(invoice_id: str, changes: dict) -> dict:
    """Edit a DRAFT invoice. `changes` matches InvoiceUpdate fields. Autonomous."""
    with tool_session() as db:
        inv = invoicing.update_invoice(db, UUID(invoice_id), InvoiceUpdate(**changes))
        db.flush()
        return to_dict(inv, _INVOICE_FIELDS)


@mcp.tool
def finalize_invoice(invoice_id: str) -> dict:
    """Promote a DRAFT invoice to SENT. Autonomous (internal, reversible via void)."""
    with tool_session() as db:
        inv = invoicing.finalize_invoice(db, UUID(invoice_id))
        db.flush()
        return to_dict(inv, _INVOICE_FIELDS)


@mcp.tool
def void_invoice(invoice_id: str) -> dict:
    """Void an invoice (reversible state, not a delete). Autonomous."""
    with tool_session() as db:
        inv = invoicing.void_invoice(db, UUID(invoice_id))
        db.flush()
        return to_dict(inv, _INVOICE_FIELDS)
```

> Add `create_recurring_template` and `trigger_recurring` wrappers over `invoicing.create_recurring_template` / `invoicing.trigger_recurring_cycle` following the same shape (build `RecurringTemplateCreate` from args). Surface `invoicing.InvoicingError` as a returned `{"error": str(e)}` rather than a raw exception, so the model can relay it.

- [ ] **Step 4: Wrap service errors cleanly** — add near the top of each tool body a `try/except invoicing.InvoicingError as e: return {"error": str(e)}` (the `tool_session` will roll back because we re-raise? No — return instead). Pattern:

```python
    try:
        with tool_session() as db:
            inv = invoicing.finalize_invoice(db, UUID(invoice_id))
            db.flush()
            return to_dict(inv, _INVOICE_FIELDS)
    except invoicing.InvoicingError as e:
        return {"error": str(e)}
```

Apply this wrapper to all four tools. (The `tool_session` context rolls back on the raised error before we catch it, since the `with` block exits via exception — verify: the exception propagates out of `with`, triggering rollback, then our `except` catches it. Correct.)

- [ ] **Step 5: Register in `__init__.py`, run tests.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_write_invoices.py -v` → PASS.

- [ ] **Step 6: Commit**

```bash
git add app/mcp/tools/write_invoices.py app/mcp/tools/__init__.py tests/integration/test_mcp_write_invoices.py
git commit -m "feat(mcp): autonomous invoice write tools over invoicing service"
```

### Task 8: Remaining autonomous write tools (customers, bills, quotations, projects, items, vendors, reconciliation)

**Files:**
- Create: `backend/app/mcp/tools/write_customers.py`, `write_bills.py`, `write_quotations.py`, `write_projects.py`, `write_catalog.py`, `write_recon.py`
- Modify: `backend/app/mcp/tools/__init__.py`
- Test: `backend/tests/integration/test_mcp_writes_all.py`

**Interfaces — each tool wraps the named service function / router logic, following the Task 7 pattern (build schema → call service → `db.flush()` → `to_dict`, wrap `*Error` as `{"error": ...}`):**

| Tool | Service / source |
|------|------------------|
| `create_customer(name, contact_email=None, ...)` / `update_customer(customer_id, changes)` | customer create/update in `routers/customers.py` (no service layer → replicate the router's model write) |
| `create_bill(...)` / `update_bill(bill_id, changes)` | `services/billing_ap.py` |
| `create_quotation(...)` / `update_quotation(id, changes)` | `services/quoting.py` |
| `create_project(...)` / `update_project(id, changes)` | `routers/projects.py` |
| `create_item(...)` / `update_item(id, changes)` | `routers/items.py` |
| `create_vendor(...)` / `update_vendor(id, changes)` | `routers/vendors.py` |
| `resolve_reconciliation_match(payment_id, invoice_id)` | `services/reconciliation.py` / `routers/recon.py` — MUST honor safe-stop: never auto-resolve on currency/amount mismatch; return `{"error": ...}` if the service refuses |

- [ ] **Step 1: For each tool, write a test** in `test_mcp_writes_all.py` asserting the created/updated row has expected values, and (for `resolve_reconciliation_match`) a test that a currency mismatch returns `{"error": ...}` and does NOT change payment status.

- [ ] **Step 2: Run → FAIL.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_writes_all.py -v`

- [ ] **Step 3: Implement each module** per the table, following Task 7's pattern. Where a module has no service layer (customers, projects, items, vendors), copy the exact model-write logic from its router into the tool (do not call the HTTP endpoint).

- [ ] **Step 4: Register in `__init__.py`, run → PASS.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_writes_all.py -v`

- [ ] **Step 5: Commit**

```bash
git add app/mcp/tools/write_*.py app/mcp/tools/__init__.py tests/integration/test_mcp_writes_all.py
git commit -m "feat(mcp): autonomous write tools for remaining modules"
```

---

## Phase 4 — Gated actions (confirm-token: email + hard delete)

Gated tools take an optional `confirm_token`. Without it, they compute the canonical payload, return a preview `{requires_confirmation: true, action, ..., confirm_token}`, and perform NO side effect. With a matching token, they verify it and execute.

### Task 9: `send_invoice_email` (gated)

**Files:**
- Create: `backend/app/mcp/tools/gated_email.py`
- Modify: `backend/app/mcp/tools/__init__.py`
- Test: `backend/tests/integration/test_mcp_gated_email.py`

**Interfaces:**
- Consumes: `make_confirm_token`/`verify_confirm_token`/`ConfirmError` (Task 1); `render_invoice_pdf`, `send_email` (already imported in `routers/invoices.py`); `Invoice`, `Customer` models.
- Produces: `send_invoice_email(invoice_id: str, to_email: str | None = None, subject: str | None = None, message: str | None = None, confirm_token: str | None = None) -> dict`

- [ ] **Step 1: Write the failing test (two-phase: preview then execute)**

```python
# backend/tests/integration/test_mcp_gated_email.py
from unittest.mock import patch
from app.mcp.tools.gated_email import send_invoice_email


def test_first_call_returns_preview_and_sends_nothing(seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_invoice_email.fn(invoice_id=inv_id, to_email="a@b.com")
    assert out["requires_confirmation"] is True
    assert "confirm_token" in out
    mock_send.assert_not_called()


def test_second_call_with_token_sends(seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    preview = send_invoice_email.fn(invoice_id=inv_id, to_email="a@b.com")
    with patch("app.mcp.tools.gated_email.send_email") as mock_send, \
         patch("app.mcp.tools.gated_email.render_invoice_pdf", return_value=b"%PDF"):
        out = send_invoice_email.fn(invoice_id=inv_id, to_email="a@b.com",
                                    confirm_token=preview["confirm_token"])
    mock_send.assert_called_once()
    assert out["sent"] is True


def test_tampered_token_refuses(seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    preview = send_invoice_email.fn(invoice_id=inv_id, to_email="a@b.com")
    with patch("app.mcp.tools.gated_email.send_email") as mock_send:
        out = send_invoice_email.fn(invoice_id=inv_id, to_email="DIFFERENT@x.com",
                                    confirm_token=preview["confirm_token"])
    assert "error" in out
    mock_send.assert_not_called()
```

- [ ] **Step 2: Run → FAIL.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_gated_email.py -v`

- [ ] **Step 3: Implement `backend/app/mcp/tools/gated_email.py`**

```python
"""Gated: emailing an invoice to a customer. Requires confirm_token."""
from __future__ import annotations

from uuid import UUID

from app.mcp.confirm import ConfirmError, make_confirm_token, verify_confirm_token
from app.mcp.db import tool_session
from app.mcp.server import mcp
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services.email import send_email
from app.services.invoice_pdf import render_invoice_pdf

_ACTION = "send_invoice_email"


def _payload(invoice: Invoice, to_email: str) -> dict:
    return {
        "target_id": str(invoice.invoice_id),
        "amount": str(invoice.amount),
        "currency": invoice.currency,
        "recipient": to_email,
    }


@mcp.tool
def send_invoice_email(invoice_id: str, to_email: str | None = None,
                       subject: str | None = None, message: str | None = None,
                       confirm_token: str | None = None) -> dict:
    """Email an invoice PDF to a customer. GATED: first call previews and returns
    a confirm_token; call again with that token to actually send."""
    with tool_session() as db:
        inv = db.get(Invoice, UUID(invoice_id))
        if inv is None:
            return {"error": "invoice not found"}
        if inv.status == "VOID":
            return {"error": "cannot send a voided invoice"}
        cust = db.get(Customer, inv.customer_id) if inv.customer_id else None
        recipient = to_email or (cust.contact_email if cust else None)
        if not recipient:
            return {"error": "no recipient: pass to_email or set customer contact_email"}

        payload = _payload(inv, recipient)
        if confirm_token is None:
            return {
                "requires_confirmation": True,
                "action": _ACTION,
                "invoice_number": inv.invoice_number,
                "recipient": recipient,
                "amount": str(inv.amount),
                "currency": inv.currency,
                "confirm_token": make_confirm_token(_ACTION, payload),
            }
        try:
            verify_confirm_token(confirm_token, _ACTION, payload)
        except ConfirmError as e:
            return {"error": str(e)}

        pdf = render_invoice_pdf(inv, cust)
        fname = f"invoice-{inv.invoice_number or str(inv.invoice_id)[:8]}.pdf"
        send_email(
            to_email=recipient, cc_email=None,
            subject=subject or (f"Invoice {inv.invoice_number}" if inv.invoice_number else "Invoice"),
            body_text=message or f"Please find attached invoice {inv.invoice_number or ''}.",
            attachments=[(fname, pdf, "application/pdf")],
        )
        if inv.status == "DRAFT":
            inv.status = "SENT"
        db.flush()
        return {"sent": True, "invoice_id": str(inv.invoice_id), "recipient": recipient}
```

- [ ] **Step 4: Register in `__init__.py`, run → PASS.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_gated_email.py -v`

- [ ] **Step 5: Commit**

```bash
git add app/mcp/tools/gated_email.py app/mcp/tools/__init__.py tests/integration/test_mcp_gated_email.py
git commit -m "feat(mcp): gated send_invoice_email with confirm-token"
```

### Task 10: Gated deletes + `send_payment_reminder`

**Files:**
- Create: `backend/app/mcp/tools/gated_delete.py` (`delete_invoice`, `delete_bill`, `delete_customer`)
- Modify: `backend/app/mcp/tools/gated_email.py` (add `send_payment_reminder`)
- Modify: `backend/app/mcp/tools/__init__.py`
- Test: `backend/tests/integration/test_mcp_gated_delete.py`

**Interfaces:**
- Produces: `delete_invoice(invoice_id, confirm_token=None)`, `delete_bill(bill_id, confirm_token=None)`, `delete_customer(customer_id, confirm_token=None)`, `send_payment_reminder(invoice_id, to_email=None, confirm_token=None)` — all two-phase gated, same shape as Task 9.

- [ ] **Step 1: Write the failing test** — for `delete_invoice`: first call returns `requires_confirmation` and the row still exists; second call with token deletes it; a DRAFT-only guard (`invoicing.delete_invoice` refuses non-DRAFT) returns `{"error": ...}`.

```python
# backend/tests/integration/test_mcp_gated_delete.py
from app.mcp.tools.gated_delete import delete_invoice


def test_delete_previews_then_deletes(seed_draft_invoice):
    inv_id = seed_draft_invoice
    preview = delete_invoice.fn(invoice_id=inv_id)
    assert preview["requires_confirmation"] is True
    out = delete_invoice.fn(invoice_id=inv_id, confirm_token=preview["confirm_token"])
    assert out["deleted"] is True


def test_delete_non_draft_errors(seed_sent_invoice):
    inv_id, _ = seed_sent_invoice
    preview = delete_invoice.fn(invoice_id=inv_id)
    out = delete_invoice.fn(invoice_id=inv_id, confirm_token=preview["confirm_token"])
    assert "error" in out  # invoicing.delete_invoice refuses non-DRAFT
```

- [ ] **Step 2: Run → FAIL.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_gated_delete.py -v`

- [ ] **Step 3: Implement `gated_delete.py`** following the Task 9 two-phase pattern. For `delete_invoice`, the executing branch calls `invoicing.delete_invoice(db, UUID(invoice_id))` inside `try/except invoicing.InvoicingError`. Payload for the token = `{"target_id": invoice_id}` (deletes have no amount/recipient). `delete_bill`/`delete_customer` follow identically against their models/services (confirm whether a service delete exists; if not, replicate the router's delete).

- [ ] **Step 4: Add `send_payment_reminder`** to `gated_email.py` — same structure as `send_invoice_email` but composes a reminder body and does not attach a PDF (or attaches it — match how reminders are sent elsewhere; if no existing reminder path, send a plain `send_email`). Token payload = `{"target_id": invoice_id, "amount": str(inv.balance_due), "currency": inv.currency, "recipient": recipient}`.

- [ ] **Step 5: Register, run → PASS.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_gated_delete.py -v`

- [ ] **Step 6: Commit**

```bash
git add app/mcp/tools/gated_delete.py app/mcp/tools/gated_email.py app/mcp/tools/__init__.py tests/integration/test_mcp_gated_delete.py
git commit -m "feat(mcp): gated deletes and payment-reminder tools"
```

---

## Phase 5 — Deployment & end-to-end verification

### Task 11: docker-compose + reverse-proxy wiring

**Files:**
- Modify: `docker-compose.yml` (ensure backend exposes `/mcp` and `/oauth`; add MCP env vars)
- Modify: `.env.example` (add `MCP_CONFIRM_SECRET`, `MCP_TOKEN_SECRET`, `MCP_PUBLIC_BASE_URL`)
- Create: `docs/mcp-setup.md` (how to connect Claude Desktop to `billing.wenhao.id/mcp`)

- [ ] **Step 1:** Add the three MCP env vars to the `backend` (and `worker` if it imports the app) service environment in `docker-compose.yml`, sourced from `.env`. Since the MCP server is in-process, no new container is needed — it rides on the existing `backend` uvicorn.

- [ ] **Step 2:** Set `MCP_PUBLIC_BASE_URL=https://billing.wenhao.id` in the production `.env`. Ensure the reverse proxy in front of `billing.wenhao.id` forwards `/mcp`, `/oauth/*`, and `/.well-known/oauth-*` to the backend (streamable-HTTP needs response streaming — disable proxy buffering for `/mcp`).

- [ ] **Step 3:** Write `docs/mcp-setup.md`: the Claude Desktop custom-connector URL (`https://billing.wenhao.id/mcp`), the OAuth login flow the user will see, and how to revoke tokens.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml .env.example docs/mcp-setup.md
git commit -m "chore(mcp): deployment wiring and setup docs"
```

### Task 12: End-to-end OAuth + tool smoke test

**Files:**
- Test: `backend/tests/integration/test_mcp_e2e.py`

- [ ] **Step 1: Write an end-to-end test** driving the real ASGI app in-process (`httpx.ASGITransport` against `app.main.app`): (1) log in via `/api/v1/auth/login`; (2) walk `/oauth/authorize` → `/oauth/token` to obtain an access token; (3) open an MCP session against `/mcp` with `Authorization: Bearer <token>`; (4) call `ping` and `list_invoices`; (5) confirm a call with a bad token 401s; (6) call `send_invoice_email` without a token → assert `requires_confirmation`. Use an MCP client library or hand-craft the JSON-RPC `initialize` + `tools/call` per the spike findings.

- [ ] **Step 2: Run → PASS.**
Run: `cd backend && python -m pytest tests/integration/test_mcp_e2e.py -v`

- [ ] **Step 3: Full suite + lint.**
Run: `cd backend && ruff check app && python -m pytest`
Expected: all pass (tests skip only if `TEST_DATABASE_URL` unset).

- [ ] **Step 4: Commit**

```bash
git add tests/integration/test_mcp_e2e.py
git commit -m "test(mcp): end-to-end OAuth + tool-call smoke test"
```

---

## Self-Review (completed by plan author)

- **Spec coverage:** architecture (Task 3), all-module read tools (Tasks 4-6), autonomous writes (Tasks 7-8), gated email+delete (Tasks 9-10), OAuth-via-login (Task 2), in-process DB (Task 3), testing (throughout + Task 12), deployment (Task 11). ✅
- **Placeholder scan:** the two "confirm the real column names / service fn" steps are explicit inspection steps with a command to run, not hand-waves; the spike (Task 2 Step 1) is a real deliverable. Module-expansion tasks (6, 8) use explicit mapping tables naming each tool's exact source — not "similar to Task N". ✅
- **Type consistency:** `_INVOICE_FIELDS` defined in Task 5, reused in Task 7; `resolve_customer` return shape defined Task 5, consumed by writes; confirm-token `make/verify` signatures consistent across Tasks 1/9/10; `tool_session`/`to_dict` signatures stable. ✅

## Known verification points (resolve during execution, not blockers)

- FastMCP 3.4.3 exact auth-provider class + how tools read the authed principal — **pinned by the Task 2 spike**.
- `@mcp.tool` raw-callable access in tests (`.fn`) — confirm in Task 3; adjust tests if the accessor differs.
- Declarative `Base` import path for new model — Task 2 Step 6a.
- Real column names per model and exact service-function names — the inspection step at the head of Tasks 4/6/8.
