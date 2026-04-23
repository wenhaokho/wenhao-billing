from datetime import date, datetime
from decimal import Decimal
from typing import Any, Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field


class MilestoneInvoiceCreate(BaseModel):
    customer_id: UUID
    project_id: UUID | None = None
    currency: str = Field(min_length=3, max_length=3)
    amount: Decimal = Field(gt=0, max_digits=19, decimal_places=4)
    milestone_ref: str
    issue_date: date | None = None
    due_in_days: int = 14


class RecurringTrigger(BaseModel):
    cycle_key: str = Field(
        description="Stable identifier for this cycle (e.g. '2026-04'); used for idempotency."
    )


class UsageLockRequest(BaseModel):
    accrued_amount: Decimal = Field(ge=0, max_digits=19, decimal_places=4)
    cutoff_date: date


class VoidRequest(BaseModel):
    reason: str


class InvoiceLineItemIn(BaseModel):
    item_id: UUID | None = None
    description: str
    quantity: Decimal = Field(ge=0, max_digits=19, decimal_places=4)
    unit_price: Decimal = Field(ge=0, max_digits=19, decimal_places=4)
    position: int = 0


class InvoiceLineItemOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    line_item_id: UUID
    item_id: UUID | None
    description: str
    quantity: Decimal
    unit_price: Decimal
    amount: Decimal
    position: int


class InvoiceCreate(BaseModel):
    customer_id: UUID | None = None
    project_id: UUID | None = None
    invoice_type: str = "MILESTONE"
    currency: str = Field(min_length=3, max_length=3)
    invoice_number: str | None = None
    po_so_number: str | None = None
    issue_date: date | None = None
    due_date: date | None = None
    payment_terms: str | None = None
    notes: str | None = None
    footer: str | None = None
    discount_type: Literal["PERCENT", "AMOUNT"] | None = None
    discount_value: Decimal | None = Field(default=None, ge=0, max_digits=19, decimal_places=4)
    line_items: list[InvoiceLineItemIn] = Field(default_factory=list)


class InvoiceUpdate(BaseModel):
    customer_id: UUID | None = None
    project_id: UUID | None = None
    invoice_type: str | None = None
    currency: str | None = Field(default=None, min_length=3, max_length=3)
    invoice_number: str | None = None
    po_so_number: str | None = None
    issue_date: date | None = None
    due_date: date | None = None
    payment_terms: str | None = None
    notes: str | None = None
    footer: str | None = None
    discount_type: Literal["PERCENT", "AMOUNT"] | None = None
    discount_value: Decimal | None = Field(default=None, ge=0, max_digits=19, decimal_places=4)
    line_items: list[InvoiceLineItemIn] | None = None


class RecurringSchedule(BaseModel):
    frequency: Literal["DAILY", "WEEKLY", "MONTHLY", "YEARLY"]
    interval: int = Field(ge=1, default=1)
    start_date: date
    end_mode: Literal["NEVER", "ON_DATE", "AFTER_N"] = "NEVER"
    end_date: date | None = None
    end_after_cycles: int | None = Field(default=None, ge=1)


class RecurringTemplateCreate(BaseModel):
    customer_id: UUID | None = None
    currency: str = Field(min_length=3, max_length=3)
    po_so_number: str | None = None
    payment_terms: str
    notes: str | None = None
    footer: str | None = None
    discount_type: Literal["PERCENT", "AMOUNT"] | None = None
    discount_value: Decimal | None = Field(default=None, ge=0, max_digits=19, decimal_places=4)
    line_items: list[InvoiceLineItemIn] = Field(default_factory=list)
    schedule: RecurringSchedule


class SendInvoiceRequest(BaseModel):
    to_email: str | None = Field(
        default=None, description="Override recipient (defaults to customer contact_email)"
    )
    cc_email: str | None = None
    subject: str | None = None
    message: str | None = None


class InvoiceOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    invoice_id: UUID
    customer_id: UUID | None
    project_id: UUID | None
    source_quote_id: UUID | None = None
    invoice_type: str
    invoice_number: str | None
    po_so_number: str | None
    currency: str
    subtotal: Decimal | None
    discount_type: str | None
    discount_value: Decimal | None
    amount: Decimal
    balance_due: Decimal
    status: str
    billing_cycle_ref: dict[str, Any] | None
    issue_date: date | None
    due_date: date | None
    payment_terms: str | None
    notes: str | None
    footer: str | None
    is_template: bool
    created_at: datetime
    line_items: list[InvoiceLineItemOut] = Field(default_factory=list)
