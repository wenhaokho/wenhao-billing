from datetime import datetime
from decimal import Decimal
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, model_validator

ItemType = Literal["SERVICE", "USAGE", "FIXED_FEE"]
HostingStatus = Literal["ACTIVE", "SUSPEND_PENDING", "SUSPENDED", "CANCELLED"]


class CloudflareTargetIn(BaseModel):
    zone_id: str = Field(min_length=1, max_length=100)
    record_id: str = Field(min_length=1, max_length=100)
    record_name: str = Field(min_length=1, max_length=255)
    record_type: str = Field(min_length=1, max_length=20)
    live_content: str = Field(min_length=1)
    maintenance_content: str = Field(min_length=1)
    proxied: bool = True


class CloudflareTargetOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    target_id: UUID
    item_id: UUID
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


class ItemBase(BaseModel):
    sku: str | None = Field(default=None, max_length=50)
    name: str = Field(min_length=1, max_length=255)
    item_type: ItemType
    description: str | None = None
    default_currency: str = Field(default="IDR", min_length=3, max_length=3)
    default_unit_price: Decimal | None = Field(default=None, ge=0, max_digits=19, decimal_places=4)
    default_purchase_price: Decimal | None = Field(
        default=None, ge=0, max_digits=19, decimal_places=4
    )
    revenue_account_id: int | None = None
    expense_account_id: int | None = None
    is_sold: bool = True
    is_purchased: bool = False
    active: bool = True
    is_hosting: bool = False
    hosting_domain: str | None = Field(default=None, max_length=255)
    hosting_grace_days: int | None = Field(default=None, ge=0, le=365)
    hosting_suspension_enabled: bool | None = None


class ItemCreate(ItemBase):
    cloudflare_target: CloudflareTargetIn | None = None

    @model_validator(mode="after")
    def _hosting_fields_when_hosting(self) -> "ItemCreate":
        if self.is_hosting:
            missing = []
            if not self.hosting_domain:
                missing.append("hosting_domain")
            if self.hosting_grace_days is None:
                missing.append("hosting_grace_days")
            if self.hosting_suspension_enabled is None:
                missing.append("hosting_suspension_enabled")
            if self.cloudflare_target is None:
                missing.append("cloudflare_target")
            if missing:
                raise ValueError(f"hosting item requires: {', '.join(missing)}")
        return self


class ItemUpdate(BaseModel):
    sku: str | None = None
    name: str | None = None
    item_type: ItemType | None = None
    description: str | None = None
    default_currency: str | None = Field(default=None, min_length=3, max_length=3)
    default_unit_price: Decimal | None = Field(default=None, ge=0, max_digits=19, decimal_places=4)
    default_purchase_price: Decimal | None = Field(
        default=None, ge=0, max_digits=19, decimal_places=4
    )
    revenue_account_id: int | None = None
    expense_account_id: int | None = None
    is_sold: bool | None = None
    is_purchased: bool | None = None
    active: bool | None = None
    is_hosting: bool | None = None
    hosting_domain: str | None = Field(default=None, max_length=255)
    hosting_grace_days: int | None = Field(default=None, ge=0, le=365)
    hosting_suspension_enabled: bool | None = None
    hosting_status: HostingStatus | None = None
    cloudflare_target: CloudflareTargetIn | None = None


class ItemOut(ItemBase):
    model_config = ConfigDict(from_attributes=True)

    item_id: UUID
    created_at: datetime
    hosting_status: HostingStatus | None = None
    hosting_last_paid_at: datetime | None = None
    hosting_last_action_at: datetime | None = None
    hosting_last_error: str | None = None
    cloudflare_target: CloudflareTargetOut | None = None


AccountType = Literal["ASSET", "LIABILITY", "EQUITY", "INCOME", "EXPENSE", "COGS"]


class AccountBase(BaseModel):
    code: str = Field(min_length=1, max_length=10)
    name: str = Field(min_length=1, max_length=100)
    type: AccountType
    active: bool = True


class AccountCreate(AccountBase):
    pass


class AccountUpdate(BaseModel):
    code: str | None = Field(default=None, min_length=1, max_length=10)
    name: str | None = Field(default=None, min_length=1, max_length=100)
    type: AccountType | None = None
    active: bool | None = None


class AccountOut(AccountBase):
    model_config = ConfigDict(from_attributes=True)

    account_id: int
