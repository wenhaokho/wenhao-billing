"""customer extended profile fields

Revision ID: 0012_customer_extended_fields
Revises: 0011_business_profile_default_notes
Create Date: 2026-04-21

Adds structured primary-contact (first/last), secondary phone, account
number, website, notes, default currency, structured billing and
shipping addresses, and shipping instructions to the ``customers``
table.

Legacy handling:
  * ``billing_address`` (free-text) is kept for backward compatibility
    as a read-only fallback. Its contents are best-effort copied into
    ``billing_address1`` on upgrade, but the UI and schemas no longer
    write to it.
  * ``contact_name`` is kept for backward compatibility. Its contents
    are best-effort split into ``contact_first_name`` /
    ``contact_last_name`` via ``split_part`` on upgrade.
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0012_customer_extended_fields"
down_revision = "0011_bp_default_notes"
branch_labels = None
depends_on = None


def upgrade() -> None:
    # Primary contact split + secondary phone
    op.add_column("customers", sa.Column("contact_first_name", sa.String(length=120), nullable=True))
    op.add_column("customers", sa.Column("contact_last_name", sa.String(length=120), nullable=True))
    op.add_column("customers", sa.Column("contact_phone_2", sa.String(length=64), nullable=True))

    # Misc profile
    op.add_column("customers", sa.Column("account_number", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("website", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("notes", sa.Text(), nullable=True))

    # Billing
    op.add_column(
        "customers",
        sa.Column(
            "default_currency",
            sa.String(length=3),
            nullable=False,
            server_default="IDR",
        ),
    )
    op.add_column("customers", sa.Column("billing_address1", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("billing_address2", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("billing_country", sa.String(length=2), nullable=True))
    op.add_column("customers", sa.Column("billing_state", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("billing_city", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("billing_postal_code", sa.String(length=32), nullable=True))

    # Shipping
    op.add_column("customers", sa.Column("ship_to_name", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("shipping_address1", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("shipping_address2", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("shipping_country", sa.String(length=2), nullable=True))
    op.add_column("customers", sa.Column("shipping_state", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("shipping_city", sa.String(length=255), nullable=True))
    op.add_column("customers", sa.Column("shipping_postal_code", sa.String(length=32), nullable=True))
    op.add_column("customers", sa.Column("shipping_phone", sa.String(length=64), nullable=True))
    op.add_column("customers", sa.Column("shipping_delivery_instructions", sa.Text(), nullable=True))

    # Best-effort migration of legacy single-text billing address.
    op.execute(
        "UPDATE customers SET billing_address1 = billing_address "
        "WHERE billing_address1 IS NULL AND billing_address IS NOT NULL"
    )

    # Best-effort split of legacy contact_name into first/last.
    op.execute(
        "UPDATE customers "
        "SET contact_first_name = split_part(contact_name, ' ', 1), "
        "    contact_last_name  = NULLIF(regexp_replace(contact_name, '^[^ ]+\\s*', ''), '') "
        "WHERE contact_name IS NOT NULL"
    )


def downgrade() -> None:
    op.drop_column("customers", "shipping_delivery_instructions")
    op.drop_column("customers", "shipping_phone")
    op.drop_column("customers", "shipping_postal_code")
    op.drop_column("customers", "shipping_city")
    op.drop_column("customers", "shipping_state")
    op.drop_column("customers", "shipping_country")
    op.drop_column("customers", "shipping_address2")
    op.drop_column("customers", "shipping_address1")
    op.drop_column("customers", "ship_to_name")

    op.drop_column("customers", "billing_postal_code")
    op.drop_column("customers", "billing_city")
    op.drop_column("customers", "billing_state")
    op.drop_column("customers", "billing_country")
    op.drop_column("customers", "billing_address2")
    op.drop_column("customers", "billing_address1")
    op.drop_column("customers", "default_currency")

    op.drop_column("customers", "notes")
    op.drop_column("customers", "website")
    op.drop_column("customers", "account_number")

    op.drop_column("customers", "contact_phone_2")
    op.drop_column("customers", "contact_last_name")
    op.drop_column("customers", "contact_first_name")
