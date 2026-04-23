from datetime import datetime

from pydantic import BaseModel, ConfigDict


class BusinessProfileUpdate(BaseModel):
    name: str | None = None
    address: str | None = None
    contact_email: str | None = None
    contact_phone: str | None = None
    invoice_title: str | None = None
    invoice_summary: str | None = None
    logo_url: str | None = None
    default_notes: str | None = None


class BusinessProfileOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    name: str | None
    address: str | None
    contact_email: str | None
    contact_phone: str | None
    invoice_title: str | None
    invoice_summary: str | None
    logo_url: str | None
    default_notes: str | None
    updated_at: datetime
