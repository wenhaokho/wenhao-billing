from datetime import datetime
from decimal import Decimal
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field

ItemType = Literal["SERVICE", "USAGE", "FIXED_FEE"]


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


class ItemCreate(ItemBase):
    pass


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


class ItemOut(ItemBase):
    model_config = ConfigDict(from_attributes=True)

    item_id: UUID
    created_at: datetime


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
