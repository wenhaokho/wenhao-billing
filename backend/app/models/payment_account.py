from datetime import datetime

from sqlalchemy import (
    Boolean,
    DateTime,
    ForeignKey,
    Integer,
    String,
    Text,
    UniqueConstraint,
    func,
    text,
)
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base


class PaymentAccount(Base):
    __tablename__ = "payment_account"
    __table_args__ = (
        UniqueConstraint(
            "business_profile_id", "currency", name="uq_payment_account_currency"
        ),
    )

    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    business_profile_id: Mapped[int] = mapped_column(
        ForeignKey("business_profile.id", ondelete="CASCADE"),
        nullable=False,
        default=1,
    )
    currency: Mapped[str] = mapped_column(String(3), nullable=False)
    instructions: Mapped[str] = mapped_column(Text, nullable=False)
    is_default: Mapped[bool] = mapped_column(
        Boolean, nullable=False, server_default=text("false")
    )
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=False),
        server_default=func.current_timestamp(),
        onupdate=func.current_timestamp(),
    )
