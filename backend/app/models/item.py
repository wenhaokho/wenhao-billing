import uuid
from datetime import datetime
from decimal import Decimal

from sqlalchemy import Boolean, DateTime, ForeignKey, Integer, Numeric, String, Text, func
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base


class Item(Base):
    __tablename__ = "items"

    item_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    sku: Mapped[str | None] = mapped_column(String(50), nullable=True, unique=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    item_type: Mapped[str] = mapped_column(String(20), nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    default_currency: Mapped[str] = mapped_column(String(3), nullable=False, default="IDR")
    default_unit_price: Mapped[Decimal | None] = mapped_column(Numeric(19, 4), nullable=True)
    default_purchase_price: Mapped[Decimal | None] = mapped_column(
        Numeric(19, 4), nullable=True
    )
    revenue_account_id: Mapped[int | None] = mapped_column(
        Integer, ForeignKey("chart_of_accounts.account_id"), nullable=True
    )
    expense_account_id: Mapped[int | None] = mapped_column(
        Integer, ForeignKey("chart_of_accounts.account_id"), nullable=True
    )
    is_sold: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    is_purchased: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    active: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=False), server_default=func.current_timestamp()
    )
