from datetime import datetime
from uuid import UUID

from pydantic import BaseModel, ConfigDict, EmailStr, Field


class VendorBase(BaseModel):
    name: str = Field(min_length=1, max_length=255)
    contact_email: EmailStr | None = None
    contact_name: str | None = None
    default_currency: str = Field(default="IDR", min_length=3, max_length=3)
    payment_terms_days: int = Field(default=30, ge=0, le=365)
    tax_id: str | None = None
    notes: str | None = None
    active: bool = True


class VendorCreate(VendorBase):
    pass


class VendorUpdate(BaseModel):
    name: str | None = None
    contact_email: EmailStr | None = None
    contact_name: str | None = None
    default_currency: str | None = Field(default=None, min_length=3, max_length=3)
    payment_terms_days: int | None = Field(default=None, ge=0, le=365)
    tax_id: str | None = None
    notes: str | None = None
    active: bool | None = None


class VendorOut(VendorBase):
    model_config = ConfigDict(from_attributes=True)

    vendor_id: UUID
    created_at: datetime
