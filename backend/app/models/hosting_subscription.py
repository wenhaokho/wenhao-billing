import uuid
from datetime import date, datetime
from typing import TYPE_CHECKING

from sqlalchemy import Boolean, Date, DateTime, ForeignKey, Integer, String, func
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.db.base import Base

if TYPE_CHECKING:
    from app.models.cloudflare_target import CloudflareTarget


class HostingSubscription(Base):
    __tablename__ = "hosting_subscriptions"

    subscription_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    customer_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("customers.customer_id"),
        nullable=False,
        index=True,
    )
    project_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("projects.project_id"),
        nullable=True,
        index=True,
    )
    template_invoice_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("invoices.invoice_id"),
        nullable=True,
        index=True,
    )
    item_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("items.item_id"),
        nullable=True,
        index=True,
    )
    service_name: Mapped[str] = mapped_column(String(255), nullable=False)
    domain_name: Mapped[str] = mapped_column(String(255), nullable=False)
    currency: Mapped[str] = mapped_column(String(3), nullable=False)
    bundle_months: Mapped[int] = mapped_column(Integer, nullable=False)
    payment_terms: Mapped[str] = mapped_column(String(30), nullable=False)
    billing_anchor_date: Mapped[date] = mapped_column(Date, nullable=False)
    grace_days: Mapped[int] = mapped_column(Integer, nullable=False, default=3)
    suspension_enabled: Mapped[bool] = mapped_column(
        Boolean, nullable=False, default=True
    )
    status: Mapped[str] = mapped_column(
        String(20), nullable=False, default="ACTIVE", server_default="ACTIVE"
    )
    last_invoice_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("invoices.invoice_id"),
        nullable=True,
        index=True,
    )
    last_paid_at: Mapped[datetime | None] = mapped_column(
        DateTime(timezone=False), nullable=True
    )
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=False), server_default=func.current_timestamp()
    )
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=False),
        server_default=func.current_timestamp(),
        onupdate=func.current_timestamp(),
    )

    cloudflare_target: Mapped["CloudflareTarget | None"] = relationship(
        "CloudflareTarget",
        back_populates="subscription",
        cascade="all, delete-orphan",
        uselist=False,
        lazy="selectin",
    )
