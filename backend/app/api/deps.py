"""FastAPI dependencies: DB session, session-cookie auth, webhook verification."""

from __future__ import annotations

import hmac
from hashlib import sha256
from uuid import UUID

from fastapi import Depends, Header, HTTPException, Request, status
from sqlalchemy.orm import Session

from app.config import get_settings
from app.db.session import get_db
from app.models.user import User


def current_admin(request: Request, db: Session = Depends(get_db)) -> User:
    user_id = request.session.get("user_id")
    if user_id is None:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="not authenticated")
    user = db.get(User, UUID(user_id))
    if user is None or user.role != "admin":
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="admin required")
    return user


async def verify_webhook_hmac(
    request: Request,
    x_signature: str | None = Header(default=None, alias="X-Signature"),
) -> bytes:
    """HMAC-SHA256 of the raw body against `webhook_hmac_secret`. Returns raw body."""
    if x_signature is None:
        raise HTTPException(status_code=400, detail="missing X-Signature header")
    body = await request.body()
    secret = get_settings().webhook_hmac_secret.encode()
    expected = hmac.new(secret, body, sha256).hexdigest()
    if not hmac.compare_digest(expected, x_signature.strip()):
        raise HTTPException(status_code=401, detail="invalid webhook signature")
    return body


def verify_email_webhook_token(
    x_email_token: str | None = Header(default=None, alias="X-Email-Token"),
) -> None:
    settings = get_settings()
    if x_email_token is None or not hmac.compare_digest(
        x_email_token, settings.email_webhook_token
    ):
        raise HTTPException(status_code=401, detail="invalid email webhook token")
