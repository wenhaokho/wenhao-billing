from datetime import datetime
from uuid import UUID

from pydantic import BaseModel, ConfigDict, EmailStr, Field


class LoginRequest(BaseModel):
    email: EmailStr
    password: str


class UserOut(BaseModel):
    """Session-user view. Used by /auth/me, /auth/login, and profile PATCH."""

    model_config = ConfigDict(from_attributes=True)

    user_id: UUID
    email: EmailStr
    role: str
    display_name: str | None = None
    created_at: datetime


class ProfileUpdate(BaseModel):
    """PATCH /users/me — edit your own profile (not password)."""

    display_name: str | None = Field(default=None, max_length=255)
    email: EmailStr | None = None


class ChangePasswordRequest(BaseModel):
    """POST /auth/change-password — requires current password."""

    current_password: str
    new_password: str = Field(min_length=6, max_length=128)


class ForgotPasswordRequest(BaseModel):
    email: EmailStr


class ForgotPasswordResponse(BaseModel):
    """Dev-mode response: returns the token inline since no SMTP is wired up."""

    message: str
    # Phase 1 single-tenant: no email provider, so we expose the token + link.
    # In multi-tenant / prod, remove these fields and send via email.
    reset_token: str | None = None
    reset_link: str | None = None


class ResetPasswordRequest(BaseModel):
    token: str
    new_password: str = Field(min_length=6, max_length=128)
