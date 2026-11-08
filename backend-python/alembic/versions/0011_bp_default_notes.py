"""business profile default notes / terms

Revision ID: 0011_bp_default_notes
Revises: 0010_business_profile
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0011_bp_default_notes"
down_revision = "0010_business_profile"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        "business_profile",
        sa.Column("default_notes", sa.Text(), nullable=True),
    )


def downgrade() -> None:
    op.drop_column("business_profile", "default_notes")
