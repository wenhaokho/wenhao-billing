"""per-currency payment accounts

Revision ID: 0022_payment_accounts
Revises: mcp_oauth
Create Date: 2026-07-24
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0022_payment_accounts"
down_revision = "mcp_oauth"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "payment_account",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("business_profile_id", sa.Integer(), nullable=False),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("instructions", sa.Text(), nullable=False),
        sa.Column(
            "is_default",
            sa.Boolean(),
            nullable=False,
            server_default=sa.text("false"),
        ),
        sa.Column(
            "updated_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
        sa.ForeignKeyConstraint(
            ["business_profile_id"], ["business_profile.id"], ondelete="CASCADE"
        ),
        sa.UniqueConstraint(
            "business_profile_id", "currency", name="uq_payment_account_currency"
        ),
    )
    op.create_index(
        "uq_payment_account_one_default",
        "payment_account",
        ["business_profile_id"],
        unique=True,
        postgresql_where=sa.text("is_default"),
    )
    # Preserve the existing single field as the IDR default account.
    op.execute(
        """
        INSERT INTO payment_account
            (business_profile_id, currency, instructions, is_default)
        SELECT 1, 'IDR', payment_instructions, true
        FROM business_profile
        WHERE id = 1
          AND payment_instructions IS NOT NULL
          AND btrim(payment_instructions) <> ''
        """
    )
    op.drop_column("business_profile", "payment_instructions")


def downgrade() -> None:
    op.add_column(
        "business_profile",
        sa.Column("payment_instructions", sa.Text(), nullable=True),
    )
    op.execute(
        """
        UPDATE business_profile b
        SET payment_instructions = pa.instructions
        FROM payment_account pa
        WHERE b.id = pa.business_profile_id AND pa.is_default
        """
    )
    op.drop_index("uq_payment_account_one_default", table_name="payment_account")
    op.drop_table("payment_account")
