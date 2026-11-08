"""add active flag to customers and chart_of_accounts for soft-delete

Revision ID: 0005_active_flag_customers_coa
Revises: 0004_user_profile_fields
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0005_active_flag_customers_coa"
down_revision = "0004_user_profile_fields"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        "customers",
        sa.Column(
            "active", sa.Boolean(), nullable=False, server_default=sa.text("true")
        ),
    )
    op.add_column(
        "chart_of_accounts",
        sa.Column(
            "active", sa.Boolean(), nullable=False, server_default=sa.text("true")
        ),
    )


def downgrade() -> None:
    op.drop_column("chart_of_accounts", "active")
    op.drop_column("customers", "active")
