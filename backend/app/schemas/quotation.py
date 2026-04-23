from datetime import date, datetime
from decimal import Decimal
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field


QuotationStatus = Literal[
    "DRAFT", "SENT", "ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"
]


class QuotationLineItemIn(BaseModel):
    item_id: UUID | None = None
    description: str
    quantity: Decimal = Field(ge=0, max_digits=19, decimal_places=4)
    unit_price: Decimal = Field(ge=0, max_digits=19, decimal_places=4)
    position: int = 0


class QuotationLineItemOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    line_item_id: UUID
    item_id: UUID | None
    description: str
    quantity: Decimal
    unit_price: Decimal
    amount: Decimal
    position: int


class QuotationCreate(BaseModel):
    customer_id: UUID
    project_id: UUID | None = None
    currency: str = Field(min_length=3, max_length=3)
    quotation_number: str | None = None
    po_so_number: str | None = None
    issue_date: date | None = None
    valid_until: date | None = None
    payment_terms: str | None = None
    notes: str | None = None
    footer: str | None = None
    discount_type: Literal["PERCENT", "AMOUNT"] | None = None
    discount_value: Decimal | None = Field(
        default=None, ge=0, max_digits=19, decimal_places=4
    )
    line_items: list[QuotationLineItemIn] = Field(default_factory=list)


class QuotationUpdate(BaseModel):
    customer_id: UUID | None = None
    project_id: UUID | None = None
    currency: str | None = Field(default=None, min_length=3, max_length=3)
    quotation_number: str | None = None
    po_so_number: str | None = None
    issue_date: date | None = None
    valid_until: date | None = None
    payment_terms: str | None = None
    notes: str | None = None
    footer: str | None = None
    discount_type: Literal["PERCENT", "AMOUNT"] | None = None
    discount_value: Decimal | None = Field(
        default=None, ge=0, max_digits=19, decimal_places=4
    )
    line_items: list[QuotationLineItemIn] | None = None


class SendQuotationRequest(BaseModel):
    to_email: str | None = None
    cc_email: str | None = None
    subject: str | None = None
    message: str | None = None


class AcceptQuotationRequest(BaseModel):
    accepted_by: str | None = None


class ConvertToInvoiceRequest(BaseModel):
    issue_date: date | None = None
    due_in_days: int = 14


class QuotationOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    quotation_id: UUID
    customer_id: UUID
    project_id: UUID | None
    quotation_number: str | None
    po_so_number: str | None
    currency: str
    subtotal: Decimal | None
    discount_type: str | None
    discount_value: Decimal | None
    amount: Decimal
    status: str
    issue_date: date | None
    valid_until: date | None
    payment_terms: str | None
    notes: str | None
    footer: str | None
    last_sent_at: datetime | None
    accepted_at: datetime | None
    accepted_by: str | None
    converted_invoice_id: UUID | None
    created_at: datetime
    line_items: list[QuotationLineItemOut] = Field(default_factory=list)
