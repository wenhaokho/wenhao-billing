from uuid import UUID

from pydantic import BaseModel, ConfigDict, EmailStr, Field


class CustomerBase(BaseModel):
    name: str | None = Field(default=None, min_length=1, max_length=255)
    matching_aliases: list[str] | None = None
    active: bool | None = None

    # Primary contact
    contact_name: str | None = Field(default=None, max_length=255)
    contact_first_name: str | None = Field(default=None, max_length=120)
    contact_last_name: str | None = Field(default=None, max_length=120)
    contact_email: EmailStr | None = None
    contact_phone: str | None = Field(default=None, max_length=64)
    contact_phone_2: str | None = Field(default=None, max_length=64)

    # Misc profile
    account_number: str | None = Field(default=None, max_length=255)
    website: str | None = Field(default=None, max_length=255)
    notes: str | None = None

    # Billing
    default_currency: str | None = Field(default=None, min_length=3, max_length=3)
    billing_address: str | None = None
    billing_address1: str | None = Field(default=None, max_length=255)
    billing_address2: str | None = Field(default=None, max_length=255)
    billing_country: str | None = Field(default=None, max_length=2)
    billing_state: str | None = Field(default=None, max_length=255)
    billing_city: str | None = Field(default=None, max_length=255)
    billing_postal_code: str | None = Field(default=None, max_length=32)

    # Shipping
    ship_to_name: str | None = Field(default=None, max_length=255)
    shipping_address1: str | None = Field(default=None, max_length=255)
    shipping_address2: str | None = Field(default=None, max_length=255)
    shipping_country: str | None = Field(default=None, max_length=2)
    shipping_state: str | None = Field(default=None, max_length=255)
    shipping_city: str | None = Field(default=None, max_length=255)
    shipping_postal_code: str | None = Field(default=None, max_length=32)
    shipping_phone: str | None = Field(default=None, max_length=64)
    shipping_delivery_instructions: str | None = None


class CustomerCreate(CustomerBase):
    name: str = Field(min_length=1, max_length=255)
    matching_aliases: list[str] = Field(default_factory=list)


class CustomerUpdate(CustomerBase):
    pass


class CustomerOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    customer_id: UUID
    name: str
    matching_aliases: list[str]
    active: bool

    contact_name: str | None = None
    contact_first_name: str | None = None
    contact_last_name: str | None = None
    contact_email: str | None = None
    contact_phone: str | None = None
    contact_phone_2: str | None = None

    account_number: str | None = None
    website: str | None = None
    notes: str | None = None

    default_currency: str | None = None
    billing_address: str | None = None
    billing_address1: str | None = None
    billing_address2: str | None = None
    billing_country: str | None = None
    billing_state: str | None = None
    billing_city: str | None = None
    billing_postal_code: str | None = None

    ship_to_name: str | None = None
    shipping_address1: str | None = None
    shipping_address2: str | None = None
    shipping_country: str | None = None
    shipping_state: str | None = None
    shipping_city: str | None = None
    shipping_postal_code: str | None = None
    shipping_phone: str | None = None
    shipping_delivery_instructions: str | None = None
