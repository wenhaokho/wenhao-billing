"""add payment_instructions to business_profile

Revision ID: business_pay_instr
Revises: mcp_oauth
Create Date: 2026-07-24
"""
from alembic import op
import sqlalchemy as sa

revision = "business_pay_instr"
down_revision = "mcp_oauth"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        "business_profile",
        sa.Column("payment_instructions", sa.Text(), nullable=True),
    )


def downgrade() -> None:
    op.drop_column("business_profile", "payment_instructions")
