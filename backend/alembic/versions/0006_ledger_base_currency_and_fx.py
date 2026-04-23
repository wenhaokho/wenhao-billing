"""ledger base-currency columns and fx_rates table

Adds base_amount_debit / base_amount_credit / fx_rate to journal_lines so
consolidated reports can SUM a single column. Creates fx_rates for storing
per-date conversion rates and seeds an identity row for the base currency.

Revision ID: 0006_ledger_base_currency_and_fx
Revises: 0005_active_flag_customers_coa
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0006_ledger_base_currency_and_fx"
down_revision = "0005_active_flag_customers_coa"
branch_labels = None
depends_on = None


BASE_CURRENCY = "IDR"


def upgrade() -> None:
    # ---------- fx_rates ----------
    op.create_table(
        "fx_rates",
        sa.Column("rate_id", sa.BigInteger(), primary_key=True, autoincrement=True),
        sa.Column("from_currency", sa.String(3), nullable=False),
        sa.Column("to_currency", sa.String(3), nullable=False),
        sa.Column("rate", sa.Numeric(19, 8), nullable=False),
        sa.Column("as_of_date", sa.Date(), nullable=False),
        sa.Column("source", sa.String(50), nullable=False, server_default="MANUAL"),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
        sa.UniqueConstraint(
            "from_currency", "to_currency", "as_of_date", name="uq_fx_rates_triple"
        ),
    )
    op.create_index(
        "ix_fx_rates_from_to_date",
        "fx_rates",
        ["from_currency", "to_currency", "as_of_date"],
    )

    # Identity rate for base currency. Keeps fx.get_rate() trivially true.
    op.execute(
        sa.text(
            "INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) "
            "VALUES (:c, :c, 1, CURRENT_DATE, 'SEED')"
        ).bindparams(c=BASE_CURRENCY)
    )

    # ---------- journal_lines: base-currency columns ----------
    # Add nullable first so we can backfill before tightening.
    op.add_column(
        "journal_lines",
        sa.Column("base_amount_debit", sa.Numeric(19, 4), nullable=True),
    )
    op.add_column(
        "journal_lines",
        sa.Column("base_amount_credit", sa.Numeric(19, 4), nullable=True),
    )
    op.add_column(
        "journal_lines",
        sa.Column("fx_rate", sa.Numeric(19, 8), nullable=True),
    )

    # Backfill: lines already in base currency convert 1:1. Non-base lines get
    # rate=1 as a stopgap — ops must revalue historical non-base journals
    # before turning on cross-currency settlement in Phase 4.
    op.execute(
        sa.text(
            "UPDATE journal_lines SET "
            "base_amount_debit = debit, "
            "base_amount_credit = credit, "
            "fx_rate = 1"
        )
    )

    op.alter_column("journal_lines", "base_amount_debit", nullable=False, server_default="0")
    op.alter_column("journal_lines", "base_amount_credit", nullable=False, server_default="0")
    op.alter_column("journal_lines", "fx_rate", nullable=False, server_default="1")


def downgrade() -> None:
    op.drop_column("journal_lines", "fx_rate")
    op.drop_column("journal_lines", "base_amount_credit")
    op.drop_column("journal_lines", "base_amount_debit")
    op.drop_index("ix_fx_rates_from_to_date", table_name="fx_rates")
    op.drop_table("fx_rates")
