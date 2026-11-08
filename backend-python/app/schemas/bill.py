from datetime import date, datetime
from decimal import Decimal
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field


class BillLineItemIn(BaseModel):
    item_id: UUID | None = None
    expense_account_id: int | None = None
    description: str
    quantity: Decimal = Field(ge=0, max_digits=19, decimal_places=4)
    unit_price: Decimal = Field(ge=0, max_digits=19, decimal_places=4)
    position: int = 0


class BillLineItemOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    line_item_id: UUID
    item_id: UUID | None
    expense_account_id: int | None
    description: str
    quantity: Decimal
    unit_price: Decimal
    amount: Decimal
    position: int


class BillCreate(BaseModel):
    vendor_id: UUID
    project_id: UUID | None = None
    bill_number: str | None = None
    po_number: str | None = None
    currency: str = Field(min_length=3, max_length=3)
    issue_date: date | None = None
    due_date: date | None = None
    payment_terms: str | None = None
    notes: str | None = None
    discount_type: Literal["PERCENT", "AMOUNT"] | None = None
    discount_value: Decimal | None = Field(
        default=None, ge=0, max_digits=19, decimal_places=4
    )
    line_items: list[BillLineItemIn] = Field(default_factory=list)


class BillUpdate(BaseModel):
    vendor_id: UUID | None = None
    project_id: UUID | None = None
    bill_number: str | None = None
    po_number: str | None = None
    currency: str | None = Field(default=None, min_length=3, max_length=3)
    issue_date: date | None = None
    due_date: date | None = None
    payment_terms: str | None = None
    notes: str | None = None
    discount_type: Literal["PERCENT", "AMOUNT"] | None = None
    discount_value: Decimal | None = Field(
        default=None, ge=0, max_digits=19, decimal_places=4
    )
    line_items: list[BillLineItemIn] | None = None
    status: Literal["DRAFT", "OPEN", "PARTIAL", "PAID", "VOID"] | None = None


class BillOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    bill_id: UUID
    vendor_id: UUID
    project_id: UUID | None
    bill_number: str | None
    po_number: str | None
    currency: str
    subtotal: Decimal | None
    discount_type: str | None
    discount_value: Decimal | None
    amount: Decimal
    balance_due: Decimal
    status: str
    issue_date: date | None
    due_date: date | None
    payment_terms: str | None
    notes: str | None
    created_at: datetime
    line_items: list[BillLineItemOut] = Field(default_factory=list)
