"""OAuth 2.1 authorization server for the remote MCP endpoint.

A hand-rolled authorization-code + PKCE (S256) flow, deliberately *not* using
FastMCP's bundled OAuth proxy (security advisory). It is backed by the
operator's existing browser session: ``/oauth/authorize`` trusts
``request.session["user_id"]`` (the same cookie ``current_admin`` reads), so no
new credential surface is introduced — the operator logs in once via the normal
frontend login and consents by being redirected back.

Tokens are JWTs keyed to the app ``User`` (see app.mcp.tokens). Access tokens
are stateless; refresh tokens carry a ``jti`` persisted in
``oauth_refresh_tokens`` so they can be rotated and revoked.

Endpoints:
  GET  /.well-known/oauth-authorization-server
  GET  /.well-known/oauth-protected-resource
  POST /oauth/register     (RFC 7591 dynamic client registration)
  GET  /oauth/authorize    (PKCE S256)
  POST /oauth/token        (authorization_code + refresh_token grants)
  POST /oauth/revoke       (RFC 7009)
"""
from __future__ import annotations

import base64
import hashlib
import hmac
import html
import secrets
import time
from urllib.parse import urlencode
from uuid import UUID

import jwt
from fastapi import APIRouter, Depends, Form, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse
from pydantic import BaseModel
from sqlalchemy.orm import Session

from app.config import get_settings
from app.db.session import get_db
from app.mcp.tokens import (
    TokenError,
    mint_access_token,
    mint_refresh_token,
    verify_refresh_token,
)
from app.models.oauth_client import OAuthClient, OAuthRefreshToken
from app.models.user import User

# Session key under which the per-authorize CSRF token is stashed. The consent
# POST must echo it back; an attacker's cross-site form cannot read it (it lives
# in the operator's session cookie, SameSite=Lax) so it cannot forge the POST.
_CSRF_SESSION_KEY = "oauth_authorize_csrf"

_ALG = "HS256"
# Authorization codes are single-use and short-lived; PKCE is what actually
# binds the code to the client, so a tight TTL is the main defense here.
_CODE_TTL_SECONDS = 120


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------


def _base() -> str:
    return get_settings().mcp_public_base_url.rstrip("/")


def _oauth_error(error: str, description: str, status_code: int = 400) -> JSONResponse:
    return JSONResponse(
        status_code=status_code,
        content={"error": error, "error_description": description},
    )


def _pkce_s256_matches(verifier: str, challenge: str) -> bool:
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    expected = base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")
    return hmac.compare_digest(expected, challenge)


def _validate_authorize_request(
    db: Session,
    *,
    response_type: str | None,
    client_id: str | None,
    redirect_uri: str | None,
    code_challenge: str | None,
    code_challenge_method: str,
) -> tuple[JSONResponse | None, OAuthClient | None]:
    """Validate the shared authorize parameters (used by both the GET consent
    render and the POST approval). Returns ``(error_response, None)`` on any
    failure, else ``(None, client)``. Never redirects to an unvalidated URI —
    that is the open-redirect / code-exfiltration sink.
    """
    if response_type != "code":
        return _oauth_error("unsupported_response_type", "response_type must be 'code'"), None
    if not client_id or not redirect_uri:
        return _oauth_error("invalid_request", "client_id and redirect_uri are required"), None
    # PKCE is mandatory for OAuth 2.1 public clients — and S256 only.
    if not code_challenge:
        return _oauth_error("invalid_request", "code_challenge is required (PKCE)"), None
    if code_challenge_method != "S256":
        return _oauth_error("invalid_request", "code_challenge_method must be 'S256'"), None

    # SECURITY (OAuth 2.1 §4.1.2.1 / RFC 9700): the client_id MUST be a
    # registered client and the redirect_uri MUST exactly match one of its
    # registered URIs. On failure we reject WITHOUT redirecting.
    client = db.get(OAuthClient, client_id)
    if client is None:
        return _oauth_error("invalid_client", "unknown client_id", status_code=401), None
    if redirect_uri not in client.redirect_uris.split():
        return (
            _oauth_error(
                "invalid_request", "redirect_uri is not registered for this client"
            ),
            None,
        )
    return None, client


def _admin_session_user(request: Request, db: Session) -> User | None:
    """Return the logged-in User iff the session belongs to an admin, else None.

    A non-admin (or absent) session must never mint an MCP token — the MCP
    surface is admin-only, mirroring app.mcp.context.current_tool_user.
    """
    user_id = request.session.get("user_id")
    if not user_id:
        return None
    user = db.get(User, UUID(str(user_id)))
    if user is None or user.role != "admin":
        return None
    return user


def _hidden(name: str, value: str | None) -> str:
    if value is None:
        return ""
    return f'<input type="hidden" name="{html.escape(name, quote=True)}" value="{html.escape(value, quote=True)}">'


def _render_consent_page(
    *,
    client: OAuthClient,
    csrf_token: str,
    response_type: str,
    client_id: str,
    redirect_uri: str,
    code_challenge: str,
    code_challenge_method: str,
    state: str | None,
) -> str:
    """Minimal, self-contained consent page. The form POSTs same-origin to
    /oauth/authorize/consent carrying the session-bound CSRF token; no code is
    minted on this GET."""
    client_label = html.escape(client.client_name or client_id, quote=True)
    hidden_fields = "".join(
        _hidden(n, v)
        for n, v in (
            ("response_type", response_type),
            ("client_id", client_id),
            ("redirect_uri", redirect_uri),
            ("code_challenge", code_challenge),
            ("code_challenge_method", code_challenge_method),
            ("state", state),
            ("csrf_token", csrf_token),
        )
    )
    return f"""<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Authorize MCP access</title>
<style>
  body {{ font-family: system-ui, -apple-system, sans-serif; max-width: 30rem; margin: 3rem auto; padding: 0 1rem; }}
  .card {{ border: 1px solid #d0d0d0; border-radius: 8px; padding: 1.5rem; }}
  .client {{ font-weight: 600; }}
  .scope {{ background: #f5f5f5; border-radius: 6px; padding: .75rem 1rem; margin: 1rem 0; }}
  button {{ font-size: 1rem; padding: .6rem 1.2rem; border-radius: 6px; border: 1px solid #888; cursor: pointer; }}
  button.approve {{ background: #1a7f37; color: #fff; border-color: #1a7f37; }}
  .actions {{ display: flex; gap: .75rem; margin-top: 1rem; }}
</style>
</head>
<body>
  <div class="card">
    <h1>Authorize access</h1>
    <p><span class="client">{client_label}</span> is requesting access to your wenhao-billing account via the AI assistant (MCP).</p>
    <div class="scope">
      <strong>Scope:</strong> read and write access to your billing data
      (customers, invoices, bills, reconciliation) on your behalf.
    </div>
    <p>Approve only if you initiated this connection.</p>
    <form method="post" action="/oauth/authorize/consent">
      {hidden_fields}
      <div class="actions">
        <button type="submit" name="decision" value="approve" class="approve">Approve</button>
        <button type="submit" name="decision" value="deny">Deny</button>
      </div>
    </form>
  </div>
</body>
</html>"""


def _mint_code(
    *,
    user_id: str,
    client_id: str,
    redirect_uri: str,
    code_challenge: str,
    now: int | None = None,
) -> str:
    now = int(time.time()) if now is None else now
    payload = {
        "typ": "code",
        "sub": user_id,
        "client_id": client_id,
        "redirect_uri": redirect_uri,
        "code_challenge": code_challenge,
        "iat": now,
        "exp": now + _CODE_TTL_SECONDS,
    }
    return jwt.encode(payload, get_settings().mcp_token_secret, algorithm=_ALG)


def _decode_code(code: str) -> dict:
    try:
        payload = jwt.decode(
            code, get_settings().mcp_token_secret, algorithms=[_ALG]
        )
    except jwt.PyJWTError as e:
        raise TokenError(str(e)) from e
    if payload.get("typ") != "code":
        raise TokenError("not an authorization code")
    return payload


def _revoke_user_family(db: Session, user_id: str) -> None:
    """Revoke every live refresh token for a user (reuse-detection blast radius).

    The model has no explicit family/chain column, so the family is scoped to
    the user: on reuse of a rotated-out token we invalidate all of that user's
    still-valid refresh tokens, forcing a fresh authorization.
    """
    db.query(OAuthRefreshToken).filter(
        OAuthRefreshToken.user_id == user_id,
        OAuthRefreshToken.revoked.is_(False),
    ).update({OAuthRefreshToken.revoked: True}, synchronize_session=False)
    db.commit()


def _token_response(user_id: UUID, db: Session) -> dict:
    """Mint an access + (persisted) refresh token pair for ``user_id``."""
    s = get_settings()
    access = mint_access_token(user_id)
    refresh = mint_refresh_token(user_id)
    _, jti = verify_refresh_token(refresh)
    db.add(OAuthRefreshToken(jti=jti, user_id=str(user_id), revoked=False))
    db.commit()
    return {
        "access_token": access,
        "token_type": "Bearer",
        "expires_in": s.mcp_access_token_ttl_seconds,
        "refresh_token": refresh,
    }


# ---------------------------------------------------------------------------
# router
# ---------------------------------------------------------------------------


class RegisterRequest(BaseModel):
    redirect_uris: list[str]
    client_name: str | None = None


def build_oauth_router() -> APIRouter:
    router = APIRouter(tags=["mcp-oauth"])

    @router.get("/.well-known/oauth-authorization-server")
    def authorization_server_metadata() -> dict:
        base = _base()
        return {
            "issuer": base,
            "authorization_endpoint": f"{base}/oauth/authorize",
            "token_endpoint": f"{base}/oauth/token",
            "registration_endpoint": f"{base}/oauth/register",
            "revocation_endpoint": f"{base}/oauth/revoke",
            "response_types_supported": ["code"],
            "grant_types_supported": ["authorization_code", "refresh_token"],
            "code_challenge_methods_supported": ["S256"],
            "token_endpoint_auth_methods_supported": ["none"],
        }

    @router.get("/.well-known/oauth-protected-resource")
    def protected_resource_metadata() -> dict:
        base = _base()
        return {
            "resource": base,
            "authorization_servers": [base],
        }

    @router.post("/oauth/register", status_code=201)
    def register(payload: RegisterRequest, db: Session = Depends(get_db)):
        if not payload.redirect_uris:
            return _oauth_error("invalid_redirect_uri", "redirect_uris is required")
        client = OAuthClient(
            client_name=payload.client_name,
            redirect_uris=" ".join(payload.redirect_uris),
        )
        db.add(client)
        db.commit()
        db.refresh(client)
        return {
            "client_id": client.client_id,
            "client_name": client.client_name,
            "redirect_uris": payload.redirect_uris,
            "token_endpoint_auth_method": "none",
            "grant_types": ["authorization_code", "refresh_token"],
            "response_types": ["code"],
        }

    @router.get("/oauth/authorize")
    def authorize(request: Request, db: Session = Depends(get_db)):
        # SECURITY (phishing defense): a cross-site GET on a live operator
        # session must NOT silently mint a code. This endpoint only ever
        # *renders* a consent page; the actual code is minted by the
        # same-origin, CSRF-protected POST to /oauth/authorize/consent.
        params = request.query_params
        response_type = params.get("response_type")
        client_id = params.get("client_id")
        redirect_uri = params.get("redirect_uri")
        code_challenge = params.get("code_challenge")
        code_challenge_method = params.get("code_challenge_method", "S256")
        state = params.get("state")

        error, client = _validate_authorize_request(
            db,
            response_type=response_type,
            client_id=client_id,
            redirect_uri=redirect_uri,
            code_challenge=code_challenge,
            code_challenge_method=code_challenge_method,
        )
        if error is not None:
            return error

        if not request.session.get("user_id"):
            # Not logged in: bounce to the frontend login, asking it to return
            # the browser to this exact authorize URL afterwards (unchanged).
            settings = get_settings()
            return_to = str(request.url)
            login_url = (
                f"{settings.app_base_url.rstrip('/')}/login?"
                + urlencode({"return_to": return_to})
            )
            return RedirectResponse(url=login_url, status_code=302)

        # Logged in — but the MCP surface is admin-only. A non-admin session
        # must not be able to mint an MCP token.
        if _admin_session_user(request, db) is None:
            return _oauth_error("access_denied", "admin role required", status_code=403)

        # Bind a fresh CSRF token to the session and render the consent page.
        # No code is minted here.
        csrf_token = secrets.token_urlsafe(32)
        request.session[_CSRF_SESSION_KEY] = csrf_token
        page = _render_consent_page(
            client=client,
            csrf_token=csrf_token,
            response_type="code",
            client_id=client_id,
            redirect_uri=redirect_uri,
            code_challenge=code_challenge,
            code_challenge_method=code_challenge_method,
            state=state,
        )
        return HTMLResponse(page)

    @router.post("/oauth/authorize/consent")
    def authorize_consent(
        request: Request,
        db: Session = Depends(get_db),
        response_type: str = Form(...),
        client_id: str = Form(...),
        redirect_uri: str = Form(...),
        code_challenge: str = Form(...),
        code_challenge_method: str = Form("S256"),
        state: str | None = Form(default=None),
        csrf_token: str | None = Form(default=None),
        decision: str = Form(...),
    ):
        # Must still be a logged-in admin (session could have expired between
        # the GET render and this POST).
        user = _admin_session_user(request, db)
        if user is None:
            return _oauth_error("access_denied", "admin session required", status_code=403)

        # CSRF: the posted token must match the one bound to this session on the
        # authorize GET. A cross-site POST cannot carry it. Mismatch mints
        # nothing. Consume it either way (single use).
        session_csrf = request.session.pop(_CSRF_SESSION_KEY, None)
        if (
            not session_csrf
            or not csrf_token
            or not hmac.compare_digest(str(session_csrf), str(csrf_token))
        ):
            return _oauth_error("invalid_request", "invalid or missing CSRF token")

        # A denial mints nothing.
        if decision != "approve":
            return _oauth_error("access_denied", "authorization denied by operator", status_code=400)

        # Re-validate the request params (client, redirect_uri, PKCE) before
        # minting — never trust the POST body alone.
        error, _client = _validate_authorize_request(
            db,
            response_type=response_type,
            client_id=client_id,
            redirect_uri=redirect_uri,
            code_challenge=code_challenge,
            code_challenge_method=code_challenge_method,
        )
        if error is not None:
            return error

        code = _mint_code(
            user_id=str(user.user_id),
            client_id=client_id,
            redirect_uri=redirect_uri,
            code_challenge=code_challenge,
        )
        q = {"code": code}
        if state:
            q["state"] = state
        sep = "&" if "?" in redirect_uri else "?"
        return RedirectResponse(url=f"{redirect_uri}{sep}{urlencode(q)}", status_code=302)

    @router.post("/oauth/token")
    def token(
        grant_type: str = Form(...),
        db: Session = Depends(get_db),
        code: str | None = Form(default=None),
        redirect_uri: str | None = Form(default=None),
        client_id: str | None = Form(default=None),
        code_verifier: str | None = Form(default=None),
        refresh_token: str | None = Form(default=None),
    ):
        if grant_type == "authorization_code":
            if not code or not code_verifier:
                return _oauth_error(
                    "invalid_request", "code and code_verifier are required"
                )
            # Public client (token_endpoint_auth_method=none): client_id is
            # REQUIRED and must match the code, since there is no client secret.
            if not client_id:
                return _oauth_error("invalid_request", "client_id is required")
            try:
                payload = _decode_code(code)
            except TokenError as e:
                return _oauth_error("invalid_grant", f"invalid authorization code: {e}")

            if redirect_uri != payload["redirect_uri"]:
                return _oauth_error("invalid_grant", "redirect_uri mismatch")
            if client_id != payload["client_id"]:
                return _oauth_error("invalid_grant", "client_id mismatch")
            if not _pkce_s256_matches(code_verifier, payload["code_challenge"]):
                return _oauth_error("invalid_grant", "PKCE verification failed")

            return _token_response(UUID(payload["sub"]), db)

        if grant_type == "refresh_token":
            if not refresh_token:
                return _oauth_error("invalid_request", "refresh_token is required")
            try:
                user_id, jti = verify_refresh_token(refresh_token)
            except TokenError as e:
                return _oauth_error("invalid_grant", f"invalid refresh token: {e}")

            row = db.get(OAuthRefreshToken, jti)
            if row is None:
                return _oauth_error("invalid_grant", "unknown refresh token")
            if row.revoked:
                # Reuse detection (RFC 9700 §4.14.2): a revoked (rotated-out)
                # token was replayed — the chain may be compromised. Revoke the
                # entire family (all this user's live refresh tokens) so any
                # in-flight successor is killed too.
                _revoke_user_family(db, str(user_id))
                return _oauth_error("invalid_grant", "refresh token reuse detected")

            # Rotate: revoke the presented token, issue a fresh pair.
            row.revoked = True
            db.flush()
            return _token_response(user_id, db)

        return _oauth_error("unsupported_grant_type", f"unsupported grant_type: {grant_type}")

    @router.post("/oauth/revoke")
    def revoke(
        token: str = Form(...),
        db: Session = Depends(get_db),
    ):
        # RFC 7009: always 200, even for unknown/invalid tokens.
        try:
            _, jti = verify_refresh_token(token)
        except TokenError:
            return JSONResponse(status_code=200, content={})
        row = db.get(OAuthRefreshToken, jti)
        if row is not None and not row.revoked:
            row.revoked = True
            db.commit()
        return JSONResponse(status_code=200, content={})

    return router
