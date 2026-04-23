"""add contact and billing address fields to customers

Revision ID: 0007_customer_contact_fields
Revises: 0006_ledger_base_currency_and_fx
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0007_customer_contact_fields"
down_revision = "0006_ledger_base_currency_and_fx"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("customers", sa.Column("contact_name", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("contact_email", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("contact_phone", sa.String(length=64), nullable=True))
    op.add_column("customers", sa.Column("billing_address", sa.Text(), nullable=True))


def downgrade() -> None:
    op.drop_column("customers", "billing_address")
    op.drop_column("customers", "contact_phone")
    op.drop_column("customers", "contact_email")
    op.drop_column("customers", "contact_name")
