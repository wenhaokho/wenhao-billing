"""add vendors and items (services catalog) tables

Revision ID: 0003_vendors_items
Revises: 0002_seed_coa
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0003_vendors_items"
down_revision = "0002_seed_coa"
branch_labels = None
depends_on = None


def upgrade() -> None:
    # ---------- vendors ----------
    op.create_table(
        "vendors",
        sa.Column("vendor_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("name", sa.String(255), nullable=False),
        sa.Column("contact_email", sa.String(255), nullable=True),
        sa.Column("contact_name", sa.String(255), nullable=True),
        sa.Column("default_currency", sa.String(3), nullable=False, server_default="IDR"),
        sa.Column("payment_terms_days", sa.Integer(), nullable=False, server_default="30"),
        sa.Column("tax_id", sa.String(100), nullable=True),
        sa.Column("notes", sa.Text(), nullable=True),
        sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.text("true")),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
    )
    op.create_index("ix_vendors_name", "vendors", ["name"])

    # ---------- items (services / products catalog) ----------
    op.create_table(
        "items",
        sa.Column("item_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("sku", sa.String(50), nullable=True, unique=True),
        sa.Column("name", sa.String(255), nullable=False),
        sa.Column("item_type", sa.String(20), nullable=False),  # SERVICE / USAGE / FIXED_FEE
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("default_currency", sa.String(3), nullable=False, server_default="IDR"),
        sa.Column("default_unit_price", sa.Numeric(19, 4), nullable=True),
        sa.Column(
            "revenue_account_id",
            sa.Integer(),
            sa.ForeignKey("chart_of_accounts.account_id"),
            nullable=True,
        ),
        sa.Column("active", sa.Boolean(), nullable=False, server_default=sa.text("true")),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
        sa.CheckConstraint(
            "item_type IN ('SERVICE','USAGE','FIXED_FEE')",
            name="ck_items_type",
        ),
    )
    op.create_index("ix_items_name", "items", ["name"])


def downgrade() -> None:
    op.drop_index("ix_items_name", table_name="items")
    op.drop_table("items")
    op.drop_index("ix_vendors_name", table_name="vendors")
    op.drop_table("vendors")
