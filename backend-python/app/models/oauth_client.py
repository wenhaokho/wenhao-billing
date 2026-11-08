"""Dynamically-registered OAuth clients + issued refresh tokens (revocation).

The MCP OAuth layer (app.mcp.oauth) persists two things:
  * ``OAuthClient`` — one row per dynamic client registration (RFC 7591).
  * ``OAuthRefreshToken`` — one row per issued refresh-token ``jti`` so a token
    can be revoked (rotation on refresh, explicit /oauth/revoke).
Access tokens stay stateless (verified by signature + expiry only).
"""
from __future__ import annotations

from datetime import datetime
from uuid import uuid4

from sqlalchemy import Boolean, DateTime, String, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base


class OAuthClient(Base):
    __tablename__ = "oauth_clients"

    client_id: Mapped[str] = mapped_column(
        String, primary_key=True, default=lambda: uuid4().hex
    )
    client_name: Mapped[str | None] = mapped_column(String, nullable=True)
    # Space-delimited list of registered redirect URIs.
    redirect_uris: Mapped[str] = mapped_column(String, nullable=False)
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now()
    )


class OAuthRefreshToken(Base):
    __tablename__ = "oauth_refresh_tokens"

    jti: Mapped[str] = mapped_column(String, primary_key=True)
    user_id: Mapped[str] = mapped_column(String, nullable=False)
    revoked: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now()
    )
