from datetime import date, datetime
from decimal import Decimal
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field


ProjectStatus = Literal["ACTIVE", "ON_HOLD", "COMPLETED", "CANCELLED"]


class ProjectCreate(BaseModel):
    customer_id: UUID
    code: str = Field(min_length=1, max_length=40)
    name: str = Field(min_length=1, max_length=255)
    currency: str = Field(min_length=3, max_length=3)
    contract_value: Decimal | None = Field(default=None, ge=0, max_digits=19, decimal_places=4)
    status: ProjectStatus = "ACTIVE"
    start_date: date | None = None
    end_date: date | None = None
    notes: str | None = None


class ProjectUpdate(BaseModel):
    customer_id: UUID | None = None
    code: str | None = Field(default=None, min_length=1, max_length=40)
    name: str | None = Field(default=None, min_length=1, max_length=255)
    currency: str | None = Field(default=None, min_length=3, max_length=3)
    contract_value: Decimal | None = Field(default=None, ge=0, max_digits=19, decimal_places=4)
    status: ProjectStatus | None = None
    start_date: date | None = None
    end_date: date | None = None
    notes: str | None = None
    active: bool | None = None


class ProjectOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    project_id: UUID
    customer_id: UUID
    code: str
    name: str
    currency: str
    contract_value: Decimal | None
    status: str
    start_date: date | None
    end_date: date | None
    notes: str | None
    active: bool
    created_at: datetime


class ProjectSummary(BaseModel):
    """Derived stats for a single project — computed from invoices + bills."""

    project_id: UUID
    invoiced_total: Decimal
    paid_total: Decimal
    outstanding_total: Decimal
    contract_value: Decimal | None
    contract_remaining: Decimal | None  # contract_value - invoiced_total, if set
    invoice_count: int
    bill_total: Decimal
