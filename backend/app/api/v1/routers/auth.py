"""Session auth + self-service password flows.

Endpoints:
  POST /auth/login
  POST /auth/logout
  GET  /auth/me
  POST /auth/change-password       (authed — requires current password)
  POST /auth/forgot-password       (public — issues reset token)
  POST /auth/reset-password        (public — consumes reset token)
"""

from __future__ import annotations

import secrets
from datetime import datetime, timedelta

from fastapi import APIRouter, Depends, HTTPException, Request, status
from passlib.context import CryptContext
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.config import get_settings
from app.db.session import get_db
from app.models.user import User
from app.schemas.auth import (
    ChangePasswordRequest,
    ForgotPasswordRequest,
    ForgotPasswordResponse,
    LoginRequest,
    ResetPasswordRequest,
    UserOut,
)
from app.services.email import send_password_reset_email

router = APIRouter(prefix="/auth", tags=["auth"])
_pwd = CryptContext(schemes=["bcrypt"], deprecated="auto")

# Reset tokens live 1 hour. Plenty for dev single-tenant usage.
_RESET_TOKEN_TTL = timedelta(hours=1)


@router.post("/login", response_model=UserOut)
def login(payload: LoginRequest, request: Request, db: Session = Depends(get_db)) -> User:
    user = db.scalar(select(User).where(User.email == payload.email))
    if user is None or not _pwd.verify(payload.password, user.password_hash):
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="invalid credentials")
    request.session["user_id"] = str(user.user_id)
    return user


@router.post("/logout", status_code=status.HTTP_204_NO_CONTENT)
def logout(request: Request) -> None:
    request.session.clear()


@router.get("/me", response_model=UserOut)
def me(user: User = Depends(current_admin)) -> User:
    return user


@router.post("/change-password", response_model=UserOut)
def change_password(
    payload: ChangePasswordRequest,
    db: Session = Depends(get_db),
    user: User = Depends(current_admin),
) -> User:
    if not _pwd.verify(payload.current_password, user.password_hash):
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="current password is incorrect",
        )
    if payload.current_password == payload.new_password:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="new password must differ from current password",
        )
    user.password_hash = _pwd.hash(payload.new_password)
    # Invalidate any outstanding reset token as a safety measure.
    user.password_reset_token = None
    user.password_reset_expires = None
    db.commit()
    db.refresh(user)
    return user


@router.post("/forgot-password", response_model=ForgotPasswordResponse)
def forgot_password(
    payload: ForgotPasswordRequest,
    db: Session = Depends(get_db),
) -> ForgotPasswordResponse:
    """Issue a single-use password reset token and email the link.

    Always responds with the same generic message so callers can't probe for
    valid emails. If SMTP isn't configured the email service logs the link
    (see app.services.email) — sufficient for single-tenant dev use.
    """
    settings = get_settings()
    generic = ForgotPasswordResponse(
        message="If an account exists for that email, a reset link has been sent.",
        reset_token=None,
        reset_link=None,
    )

    user = db.scalar(select(User).where(User.email == payload.email))
    if user is None:
        return generic

    token = secrets.token_urlsafe(32)
    user.password_reset_token = token
    user.password_reset_expires = datetime.utcnow() + _RESET_TOKEN_TTL
    db.commit()

    reset_link = f"{settings.app_base_url.rstrip('/')}/reset-password?token={token}"
    try:
        send_password_reset_email(to_email=user.email, reset_link=reset_link)
    except Exception:
        # Don't leak the failure to the caller; the token is still valid and
        # an admin can retrieve it from logs / DB if SMTP is transiently down.
        pass

    return generic


@router.post("/reset-password", response_model=UserOut)
def reset_password(
    payload: ResetPasswordRequest,
    db: Session = Depends(get_db),
) -> User:
    user = db.scalar(
        select(User).where(User.password_reset_token == payload.token)
    )
    if user is None:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="invalid or expired reset token",
        )
    if user.password_reset_expires is None or user.password_reset_expires < datetime.utcnow():
        # Expired — clear it defensively.
        user.password_reset_token = None
        user.password_reset_expires = None
        db.commit()
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="invalid or expired reset token",
        )

    user.password_hash = _pwd.hash(payload.new_password)
    user.password_reset_token = None
    user.password_reset_expires = None
    db.commit()
    db.refresh(user)
    return user
