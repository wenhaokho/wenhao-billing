import uuid
from datetime import datetime
from decimal import Decimal

from sqlalchemy import CheckConstraint, DateTime, ForeignKey, Integer, Numeric, String, func
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.db.base import Base


class JournalEntry(Base):
    __tablename__ = "journal_entries"

    entry_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    posted_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=False), nullable=False, server_default=func.current_timestamp()
    )
    source_type: Mapped[str] = mapped_column(String(20), nullable=False)
    source_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    memo: Mapped[str | None] = mapped_column(String(500), nullable=True)
    reversed_by_entry_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True), ForeignKey("journal_entries.entry_id"), nullable=True
    )

    lines: Mapped[list["JournalLine"]] = relationship(
        back_populates="entry", cascade="all, delete-orphan"
    )


class JournalLine(Base):
    __tablename__ = "journal_lines"
    __table_args__ = (
        CheckConstraint(
            "(debit = 0 AND credit > 0) OR (credit = 0 AND debit > 0)",
            name="ck_journal_lines_one_sided",
        ),
    )

    line_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    entry_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("journal_entries.entry_id"), nullable=False
    )
    account_id: Mapped[int] = mapped_column(
        Integer, ForeignKey("chart_of_accounts.account_id"), nullable=False
    )
    debit: Mapped[Decimal] = mapped_column(Numeric(19, 4), nullable=False, default=0)
    credit: Mapped[Decimal] = mapped_column(Numeric(19, 4), nullable=False, default=0)
    currency: Mapped[str] = mapped_column(String(3), nullable=False)
    # Line amount restated into Settings.base_currency using fx_rate at post time.
    # For base-currency lines, base_amount_* == debit/credit and fx_rate == 1.
    base_amount_debit: Mapped[Decimal] = mapped_column(Numeric(19, 4), nullable=False, default=0)
    base_amount_credit: Mapped[Decimal] = mapped_column(Numeric(19, 4), nullable=False, default=0)
    fx_rate: Mapped[Decimal] = mapped_column(Numeric(19, 8), nullable=False, default=1)

    entry: Mapped[JournalEntry] = relationship(back_populates="lines")
