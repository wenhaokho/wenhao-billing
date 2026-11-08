import uuid
from datetime import date, datetime
from decimal import Decimal
from typing import TYPE_CHECKING

from sqlalchemy import DateTime, ForeignKey, Numeric, String, Text, func
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.db.base import Base

if TYPE_CHECKING:
    from app.models.bill_line_item import BillLineItem


class Bill(Base):
    """AP bill — the purchase-side counterpart to Invoice.

    Status lifecycle (Phase 1):
      DRAFT  → OPEN  → PARTIAL → PAID
                    → VOID
    """

    __tablename__ = "bills"

    bill_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    vendor_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("vendors.vendor_id"), nullable=False, index=True
    )
    project_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True), ForeignKey("projects.project_id"), nullable=True, index=True
    )
    # Vendor's own bill / invoice number on the paper they sent us.
    bill_number: Mapped[str | None] = mapped_column(String(100), nullable=True)
    # Our internal PO reference, if any.
    po_number: Mapped[str | None] = mapped_column(String(100), nullable=True)
    currency: Mapped[str] = mapped_column(String(3), nullable=False)
    subtotal: Mapped[Decimal | None] = mapped_column(Numeric(19, 4), nullable=True)
    discount_type: Mapped[str | None] = mapped_column(String(10), nullable=True)
    discount_value: Mapped[Decimal | None] = mapped_column(Numeric(19, 4), nullable=True)
    amount: Mapped[Decimal] = mapped_column(Numeric(19, 4), nullable=False)
    balance_due: Mapped[Decimal] = mapped_column(Numeric(19, 4), nullable=False)
    status: Mapped[str] = mapped_column(String(20), nullable=False, default="DRAFT")
    issue_date: Mapped[date | None] = mapped_column(nullable=True)
    due_date: Mapped[date | None] = mapped_column(nullable=True)
    payment_terms: Mapped[str | None] = mapped_column(String(30), nullable=True)
    notes: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=False), server_default=func.current_timestamp()
    )

    line_items: Mapped[list["BillLineItem"]] = relationship(
        "BillLineItem",
        cascade="all, delete-orphan",
        order_by="BillLineItem.position",
        lazy="selectin",
    )
