"""business profile singleton

Revision ID: 0010_business_profile
Revises: 0009_invoice_line_items
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0010_business_profile"
down_revision = "0009_invoice_line_items"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "business_profile",
        sa.Column("id", sa.Integer(), primary_key=True),
        sa.Column("name", sa.String(255), nullable=True),
        sa.Column("address", sa.Text(), nullable=True),
        sa.Column("contact_email", sa.String(255), nullable=True),
        sa.Column("contact_phone", sa.String(50), nullable=True),
        sa.Column("invoice_title", sa.String(255), nullable=True),
        sa.Column("invoice_summary", sa.Text(), nullable=True),
        sa.Column("logo_url", sa.Text(), nullable=True),
        sa.Column(
            "updated_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
        sa.CheckConstraint("id = 1", name="ck_business_profile_singleton"),
    )
    op.execute("INSERT INTO business_profile (id) VALUES (1)")


def downgrade() -> None:
    op.drop_table("business_profile")
