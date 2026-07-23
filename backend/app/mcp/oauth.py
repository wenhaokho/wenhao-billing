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
import time
from urllib.parse import urlencode
from uuid import UUID

import jwt
from fastapi import APIRouter, Depends, Form, Request
from fastapi.responses import JSONResponse, RedirectResponse
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
    def authorize(request: Request):
        params = request.query_params
        response_type = params.get("response_type")
        client_id = params.get("client_id")
        redirect_uri = params.get("redirect_uri")
        code_challenge = params.get("code_challenge")
        code_challenge_method = params.get("code_challenge_method", "S256")
        state = params.get("state")

        if response_type != "code":
            return _oauth_error("unsupported_response_type", "response_type must be 'code'")
        if not client_id or not redirect_uri:
            return _oauth_error("invalid_request", "client_id and redirect_uri are required")
        # PKCE is mandatory for OAuth 2.1 public clients — and S256 only.
        if not code_challenge:
            return _oauth_error("invalid_request", "code_challenge is required (PKCE)")
        if code_challenge_method != "S256":
            return _oauth_error(
                "invalid_request", "code_challenge_method must be 'S256'"
            )

        user_id = request.session.get("user_id")
        if not user_id:
            # Not logged in: bounce to the frontend login, asking it to return
            # the browser to this exact authorize URL afterwards.
            settings = get_settings()
            return_to = str(request.url)
            login_url = (
                f"{settings.app_base_url.rstrip('/')}/login?"
                + urlencode({"return_to": return_to})
            )
            return RedirectResponse(url=login_url, status_code=302)

        code = _mint_code(
            user_id=str(user_id),
            client_id=client_id,
            redirect_uri=redirect_uri,
            code_challenge=code_challenge,
        )
        q = {"code": code}
        if state is not None:
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
            try:
                payload = _decode_code(code)
            except TokenError as e:
                return _oauth_error("invalid_grant", f"invalid authorization code: {e}")

            if redirect_uri != payload["redirect_uri"]:
                return _oauth_error("invalid_grant", "redirect_uri mismatch")
            if client_id is not None and client_id != payload["client_id"]:
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
            if row is None or row.revoked:
                return _oauth_error("invalid_grant", "refresh token revoked or unknown")

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
