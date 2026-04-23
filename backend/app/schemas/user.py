from datetime import datetime
from uuid import UUID

from pydantic import BaseModel, ConfigDict, EmailStr, Field


class UserOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    user_id: UUID
    email: EmailStr
    role: str
    display_name: str | None = None
    created_at: datetime


class UserCreate(BaseModel):
    email: EmailStr
    password: str = Field(min_length=6, max_length=128)
    role: str = Field(default="admin")
    display_name: str | None = Field(default=None, max_length=255)


class UserPasswordReset(BaseModel):
    password: str = Field(min_length=6, max_length=128)
