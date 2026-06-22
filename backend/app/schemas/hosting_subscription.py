from datetime import date, datetime
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field


class CloudflareTargetIn(BaseModel):
    zone_id: str = Field(min_length=1, max_length=100)
    record_id: str = Field(min_length=1, max_length=100)
    record_name: str = Field(min_length=1, max_length=255)
    record_type: str = Field(min_length=1, max_length=20)
    live_content: str = Field(min_length=1)
    maintenance_content: str = Field(min_length=1)
    proxied: bool = True


class CloudflareTargetUpdate(BaseModel):
    zone_id: str | None = Field(default=None, min_length=1, max_length=100)
    record_id: str | None = Field(default=None, min_length=1, max_length=100)
    record_name: str | None = Field(default=None, min_length=1, max_length=255)
    record_type: str | None = Field(default=None, min_length=1, max_length=20)
    live_content: str | None = Field(default=None, min_length=1)
    maintenance_content: str | None = Field(default=None, min_length=1)
    proxied: bool | None = None


class CloudflareTargetOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    target_id: UUID
    invoice_id: UUID
    zone_id: str
    record_id: str
    record_name: str
    record_type: str
    live_content: str
    maintenance_content: str
    proxied: bool
    provider_status: str
    last_action_at: datetime | None
    last_error: str | None
    created_at: datetime
    updated_at: datetime


class HostingSubscriptionCreate(BaseModel):
    customer_id: UUID
    project_id: UUID | None = None
    item_id: UUID
    service_name: str = Field(min_length=1, max_length=255)
    domain_name: str = Field(min_length=1, max_length=255)
    currency: str = Field(min_length=3, max_length=3)
    bundle_months: int = Field(ge=1, le=60)
    payment_terms: str = Field(min_length=1, max_length=30)
    billing_anchor_date: date
    grace_days: int = Field(default=3, ge=0, le=365)
    suspension_enabled: bool = True
    cloudflare_target: CloudflareTargetIn


class HostingSubscriptionUpdate(BaseModel):
    customer_id: UUID | None = None
    project_id: UUID | None = None
    item_id: UUID | None = None
    service_name: str | None = Field(default=None, min_length=1, max_length=255)
    domain_name: str | None = Field(default=None, min_length=1, max_length=255)
    currency: str | None = Field(default=None, min_length=3, max_length=3)
    bundle_months: int | None = Field(default=None, ge=1, le=60)
    payment_terms: str | None = Field(default=None, min_length=1, max_length=30)
    billing_anchor_date: date | None = None
    grace_days: int | None = Field(default=None, ge=0, le=365)
    suspension_enabled: bool | None = None
    status: str | None = Field(default=None, min_length=1, max_length=20)
    cloudflare_target: CloudflareTargetUpdate | None = None


class HostingSubscriptionOut(BaseModel):
    """Projection of a hosting recurring-invoice template.

    `subscription_id` is the template invoice's id — it's the stable identity
    of the hosting deal as far as API consumers are concerned.
    """

    model_config = ConfigDict(from_attributes=True)

    subscription_id: UUID
    template_invoice_id: UUID
    customer_id: UUID | None
    project_id: UUID | None
    item_id: UUID | None
    service_name: str
    domain_name: str
    currency: str
    bundle_months: int
    payment_terms: str
    billing_anchor_date: date
    grace_days: int
    suspension_enabled: bool
    status: str
    last_invoice_id: UUID | None
    last_paid_at: datetime | None
    created_at: datetime
    cloudflare_target: CloudflareTargetOut | None = None
