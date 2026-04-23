"""items: sell/buy flags, expense account, default purchase price

Revision ID: 0016_items_sell_buy
Revises: 0015_quotations
Create Date: 2026-04-23

Adds Wave-parity sell/buy distinction to the Products & Services catalog:
  * ``is_sold``          — appears in invoice line-item picker
  * ``is_purchased``     — appears in bill line-item picker
  * ``expense_account_id`` — FK to chart_of_accounts for purchased items
  * ``default_purchase_price`` — default cost for purchased items

Defaults match today's behaviour: catalog is used on invoices only.
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0016_items_sell_buy"
down_revision = "0015_quotations"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        "items",
        sa.Column("is_sold", sa.Boolean(), nullable=False, server_default=sa.true()),
    )
    op.add_column(
        "items",
        sa.Column("is_purchased", sa.Boolean(), nullable=False, server_default=sa.false()),
    )
    op.add_column(
        "items",
        sa.Column("expense_account_id", sa.Integer(), nullable=True),
    )
    op.add_column(
        "items",
        sa.Column("default_purchase_price", sa.Numeric(19, 4), nullable=True),
    )
    op.create_foreign_key(
        "fk_items_expense_account",
        source_table="items",
        referent_table="chart_of_accounts",
        local_cols=["expense_account_id"],
        remote_cols=["account_id"],
    )


def downgrade() -> None:
    op.drop_constraint("fk_items_expense_account", "items", type_="foreignkey")
    op.drop_column("items", "default_purchase_price")
    op.drop_column("items", "expense_account_id")
    op.drop_column("items", "is_purchased")
    op.drop_column("items", "is_sold")
