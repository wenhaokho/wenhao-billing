from datetime import date, datetime
from decimal import Decimal
from typing import Any
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field


class PaymentOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    payment_id: UUID
    invoice_id: UUID | None
    customer_id: UUID | None
    amount: Decimal
    currency: str
    payer_name: str
    payer_reference: str | None
    payment_date: date
    intake_source: str
    external_ref: str | None
    status: str
    adjustment_type: str
    confidence_score: Decimal | None
    created_at: datetime


class ManualApproveRequest(BaseModel):
    invoice_id: UUID
    adjustment_type: str = Field(default="NONE")


class ReverseRequest(BaseModel):
    reason: str


class WebhookPayload(BaseModel):
    amount: Decimal = Field(gt=0, max_digits=19, decimal_places=4)
    currency: str = Field(min_length=3, max_length=3)
    payer_name: str
    payer_reference: str | None = None
    payment_date: date
    external_ref: str
    extra: dict[str, Any] | None = None


class RecordPaymentRequest(BaseModel):
    """Manually recorded payment against an invoice or bill."""
    amount: Decimal = Field(gt=0, max_digits=19, decimal_places=4)
    payment_date: date
    payer_name: str | None = None
    payer_reference: str | None = None
    notes: str | None = None


class EmailWebhookPayload(BaseModel):
    # Minimal SendGrid/Postmark-compatible shape; adapters may fill in more.
    model_config = ConfigDict(extra="allow")

    from_: str | None = Field(default=None, alias="from")
    subject: str | None = None
    text: str | None = None
    html: str | None = None
    message_id: str | None = None
    payment_date: date | None = None
    attachments: list[dict[str, Any]] | None = None
