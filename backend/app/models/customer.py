import uuid

from sqlalchemy import Boolean, String, Text
from sqlalchemy.dialects.postgresql import JSONB, UUID
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base


class Customer(Base):
    __tablename__ = "customers"

    customer_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    matching_aliases: Mapped[list[str]] = mapped_column(
        JSONB, nullable=False, default=list, server_default="[]"
    )
    active: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)

    # Primary contact (legacy single-field kept for backward compat; prefer first/last)
    contact_name: Mapped[str | None] = mapped_column(String(255), nullable=True)
    contact_first_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    contact_last_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    contact_email: Mapped[str | None] = mapped_column(String(255), nullable=True)
    contact_phone: Mapped[str | None] = mapped_column(String(64), nullable=True)
    contact_phone_2: Mapped[str | None] = mapped_column(String(64), nullable=True)

    # Misc profile fields
    account_number: Mapped[str | None] = mapped_column(String(255), nullable=True)
    website: Mapped[str | None] = mapped_column(String(255), nullable=True)
    notes: Mapped[str | None] = mapped_column(Text, nullable=True)

    # Billing
    default_currency: Mapped[str] = mapped_column(
        String(3), nullable=False, default="IDR", server_default="IDR"
    )
    # Legacy unstructured billing address (kept read-only for backward compat)
    billing_address: Mapped[str | None] = mapped_column(Text, nullable=True)
    billing_address1: Mapped[str | None] = mapped_column(String(255), nullable=True)
    billing_address2: Mapped[str | None] = mapped_column(String(255), nullable=True)
    billing_country: Mapped[str | None] = mapped_column(String(2), nullable=True)
    billing_state: Mapped[str | None] = mapped_column(String(255), nullable=True)
    billing_city: Mapped[str | None] = mapped_column(String(255), nullable=True)
    billing_postal_code: Mapped[str | None] = mapped_column(String(32), nullable=True)

    # Shipping
    ship_to_name: Mapped[str | None] = mapped_column(String(255), nullable=True)
    shipping_address1: Mapped[str | None] = mapped_column(String(255), nullable=True)
    shipping_address2: Mapped[str | None] = mapped_column(String(255), nullable=True)
    shipping_country: Mapped[str | None] = mapped_column(String(2), nullable=True)
    shipping_state: Mapped[str | None] = mapped_column(String(255), nullable=True)
    shipping_city: Mapped[str | None] = mapped_column(String(255), nullable=True)
    shipping_postal_code: Mapped[str | None] = mapped_column(String(32), nullable=True)
    shipping_phone: Mapped[str | None] = mapped_column(String(64), nullable=True)
    shipping_delivery_instructions: Mapped[str | None] = mapped_column(Text, nullable=True)
