"""business profile NPWP and taxpayer name

Revision ID: 0028_business_npwp
Revises: 0027_withholding_tax
Create Date: 2026-10-26

Adds ``business_profile.npwp`` and ``business_profile.npwp_name`` so the
invoice PDF can show the taxpayer identity customers need for PPh bukti
potong.
"""
from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = "0028_business_npwp"
down_revision = "0027_withholding_tax"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("business_profile", sa.Column("npwp", sa.String(32), nullable=True))
    op.add_column(
        "business_profile", sa.Column("npwp_name", sa.String(255), nullable=True)
    )


def downgrade() -> None:
    op.drop_column("business_profile", "npwp_name")
    op.drop_column("business_profile", "npwp")
